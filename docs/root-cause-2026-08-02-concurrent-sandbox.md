# Root cause — concurrent sandbox creation (the worker 409s), plus the Render/build findings

**Date:** 2026-08-02 · **Commit investigated:** `0a39e78` (main)

> **Partly superseded — see [`root-cause-2026-08-03-render-logs.md`](root-cause-2026-08-03-render-logs.md).**
> The Render credential was fixed and the logs were pulled on 2026-08-03. They confirm §1's
> mechanism but **refute its production-evidence argument** (the generation numbers are not
> the race's fingerprint — 6 conflict events vs 72 from a different path), **refute F3**
> (1 build failure in 291 deploys), and identify the actual recurring failure: every deploy
> times out `srv.Shutdown` on the open SSE streams and kills the process. Read §5 and §1's
> "Corroborating production evidence" only alongside that follow-up.

## 0. Status of each deliverable

| Ticket item | Status |
| --- | --- |
| Confirm the concurrent-sandbox root cause | **Confirmed** — mechanism traced in code and reproduced deterministically (§1, appendix). *Production footprint later measured as minor — see the 08-03 follow-up §5* |
| Pull the Render render/build logs | **Obtained 2026-08-03** — credential fixed; see the follow-up doc. (As written on 08-02: blocked on a broken credential (§4), and nothing in §5 is derived from logs) |
| Check for other failure modes | Done — five in the agent module (§3), six in the deploy/build pipeline (§5) |
| Root-cause write-up + fix recommendations | This document; recommendations in §6 |

Read §5 as a prioritized hypothesis set, not log-confirmed incident attribution. §1–§3
are not hypotheses — they are confirmed against the code and a running reproduction.

---

## 1. The confirmed root cause

**Two goroutines race to create the same board slot's sandbox. The loser gets Amika's
409, and the code's response to a 409 is to create a *second* sandbox.**

### The mechanism

`Service.Run` (`backend/internal/agent/service.go:234`) starts three loops as
independent goroutines:

```go
wg.Go(func() { s.loop(ctx, PollInterval, s.pollOnce) })        // 2s  — turn machine
wg.Go(func() { s.loop(ctx, ReconcileInterval, s.reconcile) })  // 60s — pool sweep
wg.Go(func() { s.loop(ctx, LivenessInterval, s.refreshStatuses) }) // 10s — read-only
```

The first two both reach `adoptOrCreateSlot` (`service.go:846`) for the same slot:

- reconciler → `reconcileProject` → `adoptAndCreate` → `adoptOrCreateSlot`
- poller → `advanceSend` → `stepEnsureReady`/`stepStartTurn`/`stepCheckTurn` → `ensureWorker` → `adoptOrCreateSlot`
- poller → `advanceRelease` → `createWorkerRotating` directly

There is **no mutual exclusion on this path**. `Service.mu` guards only the `workers`
map and is explicitly never held across a provider call, so the read-decide-create
sequence is not atomic — and each step is an HTTP round trip, so the window is on the
order of a second, not nanoseconds.

The sequence:

1. Both goroutines call `ListWorkers` and see the same live set for slot *S*.
2. Both run `pickAdoptable`, find nothing adoptable, and compute the identical
   `newGen = maxGen + 1` — the name is **deterministic**, so they collide by construction.
3. Both `POST /sandboxes` with the same name. Amika 409s the loser.
4. `isNameConflict` (`amika/client.go:547`) maps that to `agent.ErrNameConflict`.
5. `createWorkerRotating` (`service.go:911`) responds by advancing to `gen+1` and
   creating again — **a second sandbox for the same slot**.

Step 5 is the defect. The rotation was designed for one specific cause of a 409: an
orphaned Daytona VM squatting a dead provision's name (05 D6, `auto_delete` off). It
treats *every* 409 as that case. When the conflict is instead caused by a concurrent
create of the same slot, the sandbox holding the conflicting name is **this slot's
correct, brand-new sandbox** — the right response is to re-list and adopt it, not to
route around it. `createWorkerRotating` never re-lists and never adopts on conflict.

### Reproduction

The appendix has the throwaway test. Driving `adoptOrCreateSlot` from two goroutines for
one slot, against a provider that enforces name uniqueness the way Amika does:

```
WARN agent: worker name conflict; rotating to next generation worker_id=4181c8d9-… gen=0 next_gen=1
create attempts: [kiln-prod-worker-e206d8b5-4181c8d9-7a28-…  (x2)   kiln-prod-worker-e206d8b5-4181c8d97a28-g1]
live sandboxes for slot 4181c8d9-…: 2
  kiln-prod-worker-e206d8b5-4181c8d9-7a28-4c11-9f3a-0b6d2e5c8a41
  kiln-prod-worker-e206d8b5-4181c8d97a28-g1
caller A -> "kiln-prod-worker-e206d8b5-4181c8d97a28-g1"
caller B -> "kiln-prod-worker-e206d8b5-4181c8d9-7a28-4c11-9f3a-0b6d2e5c8a41"
RACE CONFIRMED: one board slot now has 2 live sandboxes
RACE CONFIRMED: the two callers disagree on the slot's worker
```

One slot, two sandboxes, and the two callers holding different handles for it.

### Consequence chain — how this kills a turn

**(a) The turn is never pinned to the sandbox it started on.** `stepCheckTurn`
(`service.go:654`) re-resolves the worker via `ensureWorker` on *every* 2-second poll,
and `slotWorker` returns the **highest-generation** cached entry. So once a second
sandbox exists, the turn that `StartTurn` fired on gen *N* starts being polled against
gen *N+1*. `Turn.ProviderTurn` holds a session id that belongs to the *other* sandbox:

```
GET /sandboxes/{gen N+1}/sessions/{session minted on gen N}  →  404
```

and `CheckTurn` maps 404 to "not visible yet" (`amika/client.go:342`):

```go
if statusIs(err, http.StatusNotFound) {
    return agent.TurnStatus{Running: true}, nil // session not visible yet
}
```

**The turn then polls forever.** This is the worst part of the failure: it is silent.
`recordFailure` is never reached, so `Attempts` never increments and the `maxAttempts`
(8) budget never trips — that budget only counts *errors*, and this path returns no
error. No `agent.turn_completed` is ever emitted, so the brain is never woken and the
ticket sits in Working indefinitely. There is no wall-clock timeout on a turn anywhere
in the module.

**(b) The sweep destroys the sandbox the agent is actually working in.**
`destroyUnkept` (`service.go:985`) destroys every prefix-scoped sandbox not adopted in
that sweep, with **no guard for an in-flight turn**. Since adoption prefers the highest
generation, the sandbox the turn started on is exactly the one destroyed. Reproduced:

```
sweep adopted: kiln-prod-worker-e206d8b5-4181c8d97a28-g1
CONFIRMED: the sweep destroyed kiln-prod-worker-e206d8b5-4181c8d9-…  — the sandbox the in-flight turn is running on
```

This is the most likely direct explanation for a worker dying mid-run with no output.

**(c) The pin field exists but is dead.** `Turn.ProviderWorker` (`turn.go:49`) is
declared as an "opaque provider handle as it becomes known", is written and read by the
Postgres store (`postgres/store.go:48,98,140`), and is asserted in a store integration
test. **`service.go` never sets it and never reads it.** The mechanism that would fix
(a) is already in the schema, already persisted, and simply not wired into the machine.

**(d) The board briefly sees a duplicate worker.** `ListAgents` (`service.go:251`)
emits one `AgentInfo` per *sandbox*, deriving the slot id from the name — so a
double-provisioned slot yields two entries with the same `WorkerID`. `statusChanged`
keys its map on `WorkerID`, collapsing them last-wins, which can flap the status and fire
spurious board pushes every 10s. `list_agents` shows the brain a duplicated worker.

**(e) The health gate does not catch any of this.** Health-aware pull
(`docs/superpowers/specs/2026-07-11-health-aware-pull-design.md`) gates on `AgentErrored`
and on provisioning failures. Both sandboxes here are perfectly healthy — the slot reads
`ok` and stays pullable. The hard guarantee that design added is orthogonal to this bug.

### Corroborating production evidence

Suggestive, not proof. The current Amika sandbox list for the `kiln-prod-worker-` prefix
(9 sandboxes, decomposed as `<prefix><project8>-<slotFragment12>-g<gen>`):

```
2026-07-12T21:26:55  proj=3c6244ea slot=dff2774b-6d8a-4c99-be78-8f207ed53442  gen=0  stopped
2026-07-12T23:23:03  proj=3c6244ea slot=77511ed1-bf6e-4f64-b234-0d1e51fb32f0  gen=0  stopped
2026-08-02T15:27:51  proj=043f381f slot=5c20597c469f                          gen=6  stopped
2026-08-02T17:22:17  proj=3c6244ea slot=e691b2b8db97                          gen=1  stopped
2026-08-02T17:23:28  proj=043f381f slot=dcc72a9216fc                          gen=4  stopped
2026-08-02T17:27:57  proj=e206d8b5 slot=bb268f763589                          gen=5  started
2026-08-02T18:43:12  proj=043f381f slot=405f746a804d                          gen=4  stopped
2026-08-02T19:35:49  proj=e206d8b5 slot=d8e608ec6c13                          gen=1  started
2026-08-02T19:53:40  proj=e206d8b5 slot=4181c8d97a28                          gen=2  started
```

Every slot currently holds exactly one sandbox — expected, because the reconciler
converges within 60s, so the duplicate window is invisible in a point-in-time list. The
signal is in the **generation numbers**: g6, g5, g4, g4, g2, g1, with no surviving lower
generation for any of them. A generation only advances on a 409 or an unadoptable
record, so a slot sitting at g5/g6 has rotated five or six times and had its predecessors
swept. That is the fingerprint this bug leaves behind. (The sandbox this investigation
ran in is `…-4181c8d97a28-g2` — created 19:53, one generation past the g1 that the
predecessor run's 409 produced.)

---

## 2. Why the current 409 handling can't be right

`createWorkerRotating` cannot distinguish the two causes of a 409, and they need
opposite responses:

| Cause of the 409 | Correct response | What the code does |
| --- | --- | --- |
| Orphaned VM squatting a dead provision's name (05 D6) | Rotate to gen+1 — route around it | Rotate ✅ |
| A concurrent create of the *same slot* just won the name | Re-list and **adopt** the winner | Rotate ❌ → duplicate sandbox |

Re-listing on conflict distinguishes them for free: if the name is now held by a
sandbox that belongs to this slot and is not `RunErrored`, adopt it; otherwise rotate.
That single change makes the race benign even without a lock.

---

## 3. Other failure modes found in the agent module

**A3.1 — [High] A turn has no wall-clock timeout.** `Attempts`/`maxAttempts` only counts
error returns (`recordFailure`, `service.go:710`). Any path that keeps returning
`Running: true` — including consequence (a) above, but also a genuinely hung agent —
polls forever. Nothing ages a turn out.

**A3.2 — [Medium] A zero-slot sweep destroys the whole pool.** `reconcileProject`
returns early if `slots.WorkerIDs` *errors*, but an empty-and-no-error result flows on:
`adoptAndCreate` produces an empty `kept`, and `destroyUnkept` then destroys every
prefix-matched live sandbox for that project. This is intentional for `ResetProject`
(whose comment relies on it), but it means any transient path that yields zero slots
wipes a project's running workers, including one mid-turn. There is no "refuse to
destroy everything" guard.

**A3.3 — [Medium] `advanceRelease` restarts generation numbering at 0 on a cache miss.**
`service.go:563` seeds `gen := 0` and only raises it if the slot is in the in-memory
cache. On a miss (after a restart, or after `ResetProject`) it recreates at gen 0 while a
gen-N sandbox is live, then burns its 5 `nameRotateAttempts` walking gen 0,1,2,3,4 —
every one of which may 409 against a live or squatted name. If it exhausts, the release
fails and the slot is never recycled; if it succeeds mid-walk it has created a duplicate.
It should seed from the live list, not from the cache.

**A3.4 — [High] Nothing in the gate could have caught this class of bug.**
`make test-backend` is `go test ./...` with **no `-race`** (`Makefile:74-82`), and the
agent module has **no concurrency test at all** — no test starts the reconciler and the
poller against one slot. The three goroutines `Run` launches are only ever exercised
serially through a fake clock.

**A3.5 — [Low, latent] `ListWorkers` does not paginate.** `GET /sandboxes` is read as a
single unpaginated array (`amika/client.go:193`). Amika currently ignores `?limit`
(verified: `?limit=2` still returns all 9), so this is harmless today — but if Amika ever
introduces a default page cap, a truncated list makes the reconciler believe slots have
no sandbox and mass-duplicate the pool, while `destroyUnkept` cannot see the originals.

---

## 4. Why the Render logs could not be pulled — and a correction

`RENDER_API_KEY` inside the agent sandbox is the **14-character literal string
`RENDER_API_KEY`**, not a `rnd_…` key. Every Render API call 401s:

```
GET https://api.render.com/v1/services  →  401 {"message":"Unauthorized"}
GET https://api.render.com/v1/owners    →  401
```

No fallback exists: no `render` CLI on the box, no repo-root `.env`, and Render publishes
nothing to GitHub for this repo (`GET /repos/crabtree-michael/kiln/deployments` → `[]`,
and HEAD carries 0 commit statuses). The credential is the only path.

The secret record itself is present and visible to the org API key Kiln authenticates as:

```
sec_de2f3f24-c658-4b2b-9be6-bca2956de16a  name=RENDER_API_KEY  scope=org  created 2026-08-02T16:11:25Z
```

**Correction to the earlier draft of this investigation.** That draft concluded the
injection mechanism was fine and the *stored value* must be wrong, reasoning that
`GITHUB_TOKEN` "registered the same way and in the same org scope" resolves correctly.
That inference does not hold: `GITHUB_TOKEN` is **not** declared in `.amika/config.toml`,
so it does not travel the `[env] { secret = … }` path at all — per that file's own
`[lifecycle]` comment, credentials arrive via the `.env` baked into the base snapshot.
`RENDER_API_KEY` is the **only** consumer of the `{ secret = … }` resolver, and it is the
only one that failed. Two hypotheses remain live, and they need different fixes:

- **H1 — the stored value is the placeholder** (e.g. `amika secret push RENDER_API_KEY`
  run without a value). Fix: re-push the secret.
- **H2 — Amika's `{ secret = … }` resolver passes the reference name through** instead of
  resolving it. Fix: stop using that path; register the key as a Kiln project secret
  (`amika_secrets`, migration 0003), which the commit message for `a5b4096` already
  documents as the reversal path.

The Amika API exposes no reveal endpoint (`/secrets/{id}` → `invalid_api_route`), so the
two cannot be separated from inside a sandbox. §6 gives a cheap discriminating test.

> **Resolved 2026-08-03: H1.** The key now resolves to a real `rnd_…` value through this
> same `{ secret = … }` declaration, so the resolver works and **H2 is refuted**. The
> probe secret proposed in rec #8 is no longer needed; recs #9 and #10 still stand.

Worth noting: the long comment block in `.amika/config.toml` anticipated the wrong
failure mode. It tells the next agent that a sandbox booting with `RENDER_API_KEY`
**unset** means an empty store lookup. The real failure is worse than unset — the
variable *is* set, so every presence check passes and the only symptom is an unexplained
401.

---

## 5. Deploy/build pipeline findings (config analysis — **not** log-derived)

All independently verified against the repo at `0a39e78`.

> **Settled 2026-08-03 by the logs.** F3 is **refuted** as an incident source (1 build
> failure in 291 deploys, 2026-07-06); F1 is true but has caused no production incident;
> F5 is **confirmed and root-caused** (17 of 18 process exits land 44–170s after a deploy,
> because `srv.Shutdown` waits on SSE streams that never go idle). See the follow-up §2–§3.

**F1 — [High] Nothing in the gate ever builds the production image.** `backend/Dockerfile`
is what Render builds, and no gate step touches it: `make check` is `lint typecheck test`
(`Makefile:50`); `make build` (`Makefile:120`) compiles both surfaces *natively*;
`check.yml` runs `make check`. So the multi-stage image — the corepack/pnpm install, the
`COPY frontend/… → internal/web/dist` handoff that feeds `//go:embed`, the alpine runtime
stage — has its **first automatic execution in production**. A green CI badge says
nothing about it.

**F2 — [High] Render deploys regardless of CI.** `render.yaml:14` sets `autoDeploy: true`
with the inline note "no CI gate — see design doc, delta d". There is no branch
protection on `main`. This is a recorded deliberate deviation from spec 10 §4 (which
mandates auto-deploy off, CI as the only trigger), carried as delta **d** and already
flagged `[High]` as "CI is not a wall" in the 2026-07-08 architecture audit. F1 and F2
compound: the gate cannot see image breakage, and could not block the deploy if it did.

**F3 — [Medium] The image has unpinned inputs** — the most plausible source of
*intermittent, self-healing* build failures, since the same commit can build differently
on different days: mutable base tags (`node:22-alpine`, `golang:1.26-alpine`,
`alpine:3.20`), unpinned `apk add --no-cache git github-cli ripgrep ca-certificates`, and
`corepack enable && pnpm install --frozen-lockfile` (line 29) which *fetches* pnpm from
the network at build time even though `packageManager` is pinned. Also a stray
`frontend/package-lock.json` alongside `pnpm-lock.yaml` — unused by the image, drift
waiting to mislead.

**F4 — [Medium] Two Dockerfile lines turn clean failures into confusing ones.**
`RUN go mod download || true` (line 43) swallows a genuine module-fetch failure; the
build then dies much later at `go build` looking like a code problem.
`COPY backend/go.mod backend/go.sum* ./` (line 42) makes `go.sum` optional via the glob,
so a missing checksum file would silently build unverified rather than fail loudly.

**F5 — [Medium] Deploy-induced restarts.** Already documented with real Render log
evidence in `docs/superpowers/specs/2026-07-08-brain-done-push-gate-design.md:28`
(`kiln exited with error: http shutdown: context deadline exceeded`, `context canceled`
on in-flight turns). Amplified by push frequency — 16 pushes to `main` on 2026-08-01,
each an auto-deploy restarting the backend mid-pass, with no zero-downtime overlap on the
`starter` plan. **Note the interaction with §1:** a restart drops the in-memory worker
cache, so every slot's next `ensureWorker` takes the cache-miss path — the exact path
that races the reconciler. Deploy frequency is a direct multiplier on the sandbox race.

**F6 — [Low] The as-built infra deviates further from spec 10 than delta d records.**
Spec 10 §4 specifies `ci.yml` (gate job + `needs: gate` deploy job) and `sentinel.yml`;
only `check.yml` exists. Spec 10 §8 specifies two Render API keys held as GitHub Actions
secrets; neither exists as a working credential on any agent path — the proximate reason
this ticket could not be completed as written.

CI history for context: 30/30 recent runs green, and `docker build -f backend/Dockerfile .`
succeeds at HEAD. Whatever the Render failures are, they are **intermittent or historical,
not a standing break at `0a39e78`** — which is the shape that points at F3/F5 rather than a
specific bad commit.

---

## 6. Recommendations

### P0 — Fix the sandbox race (the confirmed root cause)

1. **Adopt-on-conflict.** In `createWorkerRotating`, on `ErrNameConflict`, re-`ListWorkers`
   and check who holds the conflicting name. If it belongs to this slot and is not
   `RunErrored`, **adopt it and return** instead of rotating. Only rotate when the name is
   genuinely squatted by something unusable. This alone makes the race benign.
2. **Pin the turn to its sandbox.** Set `t.ProviderWorker` in `stepStartTurn` and have
   `stepCheckTurn` resolve *that* handle rather than calling `ensureWorker`. The field,
   the column, and the store round-trip already exist — only the wiring in `service.go` is
   missing. This is what stops a turn being polled against a sandbox that never ran it.
3. **Serialize per-slot provisioning.** A per-slot mutex (or a single-flight keyed on
   slot id) around `adoptOrCreateSlot` so the reconciler and the poller cannot interleave
   read-decide-create. Defence in depth behind (1).
4. **Do not destroy a sandbox with a live turn.** Have `destroyUnkept` skip any sandbox
   referenced by a non-terminal `agent_turns` row, or defer its destruction one sweep.
5. **Stop the silent wedge.** `CheckTurn`'s 404 → `Running: true` mapping should not be
   unbounded. Either bound it (N consecutive 404s ⇒ conversation lost ⇒ fail or retry
   fresh), or add a wall-clock deadline on `PhaseTurnStarted` (A3.1). Right now a wedged
   turn is indistinguishable from a slow one, forever.

### P1 — Make this class of bug catchable

6. **Add `-race` to `make test-backend`.** Its absence is why none of this was caught.
7. **Add a concurrency test** driving the reconciler and the turn machine against one slot
   and asserting exactly one sandbox results. The appendix test is a starting point.

### P2 — Restore Render observability (unblocks the other half of this ticket)

8. **Discriminate H1 vs H2 cheaply** before re-pushing anything: add a second `[env]`
   entry to `.amika/config.toml` pointing at a *known-value* probe secret
   (`amika secret push KILN_SECRET_PROBE=probe-ok --scope org`) and read it from the next
   freshly-created sandbox. If the probe also comes back as its own name, the resolver is
   at fault (H2) and re-pushing `RENDER_API_KEY` will not help — move it to Kiln project
   secrets instead.
9. **Assert at boot.** Have `scripts/amika/setup.sh` fail loudly when any injected
   secret's value equals its own name. That signature is unambiguous, and catching it at
   boot is far cheaper than the 401 scavenger hunt it caused twice now.
10. **Correct the `.amika/config.toml` comment** — the failure mode to expect is
    *value == name*, not *unset*.

### P3 — Build the production image in CI

11. Add `docker build -f backend/Dockerfile .` to `check.yml`, with a `make check-image`
    target so local and CI stay identical. This moves the whole F1 class off Render and in
    front of the deploy.

### P4 — Owner's call (reverses a deliberate decision)

12. Enable branch protection on `main` requiring `check`, set `autoDeploy: false`, and add
    a `needs: gate` deploy job per spec 10 §4. This is delta **d**'s deferred mitigation
    ("add gate-only `ci.yml` later"). Recommended for ratification, **not** applied
    unilaterally. Note this also reduces F5, which multiplies the §1 race.

### P5 — De-flake the image

13. Drop `|| true` from `go mod download`; change `COPY backend/go.sum*` to
    `COPY backend/go.sum`. Pin base images by digest. Delete `frontend/package-lock.json`.
14. Wire `make schema-verify` into `make check` — same "guard exists but is not wired"
    pattern as F1, flagged `[High]` in the 2026-07-08 audit and still open.

---

## 7. What to do first once the Render key works

Answer the one question the logs settle:

```sh
curl -H "Authorization: Bearer $RENDER_API_KEY" https://api.render.com/v1/services
curl -H "Authorization: Bearer $RENDER_API_KEY" \
  "https://api.render.com/v1/services/<srv-id>/deploys?limit=50"
```

Filter for `status` in `build_failed` / `update_failed` / `canceled`. If failures cluster
on **distinct commits** → F1 (breakage the gate never saw) and P3 is urgent. If they
include **retries of the same commit** → F3 (unpinned inputs) and P5 is urgent.

Separately, grep the service logs for `agent: worker name conflict; rotating to next
generation` — that WARN (`service.go:928`) is emitted on every occurrence of the §1 race.
Its frequency measures the bug directly, and it should drop to zero after P0.

---

## Appendix — the reproduction

Throwaway, deliberately **not** committed (it fails by design and would break the gate).
Recreate as `backend/internal/agent/zz_repro_internal_test.go`, `package agent`, and run
with `go test -race -run TestRepro ./internal/agent/ -v`.

The fake provider is a `map[string]ProviderWorker` implementing `agent.Provider`, whose
`CreateWorker` returns `ErrNameConflict` when the name is taken and parks the first
create from each caller on a shared gate so both sit inside the read-decide-create
window — the same window a real HTTP POST opens.

```go
// Both goroutines model one Run() loop each: the 60s reconciler and the 2s poller.
for i := range 2 {
    go func() {
        live, _ := p.ListWorkers(ctx)
        results[i], errs[i] = s.adoptOrCreateSlot(
            ctx, p, prefix, slot, slotCandidates(prefix, slot, live))
    }()
}
// → 2 live sandboxes for 1 slot; results[0].Name != results[1].Name
```

The second test seeds the post-race state (gen 0 = where the turn is running, gen 1 =
the stray) and runs one sweep:

```go
w, _ := s.adoptOrCreateSlot(ctx, p, prefix, slot, slotCandidates(prefix, slot, live))
s.destroyUnkept(ctx, p, prefix, live, map[string]struct{}{w.Name: {}})
// → gen 0 destroyed: the sandbox the in-flight turn is running on
```
