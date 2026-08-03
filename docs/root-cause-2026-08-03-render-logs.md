# Root cause, part 2 — the Render logs, and what they change

**Date:** 2026-08-03 · **Commit investigated:** `c5de812` (main) · **Service:** `srv-d953nmcvikkc73d8aq60`

Follow-up to [`root-cause-2026-08-02-concurrent-sandbox.md`](root-cause-2026-08-02-concurrent-sandbox.md).
That investigation could not pull the Render logs — `RENDER_API_KEY` inside the sandbox
was the 14-char literal string `RENDER_API_KEY`, so every call 401'd, and §5 was written
as a labelled hypothesis set. The credential now resolves, so §5 can be settled with data
instead of inference.

**Some of it does not survive.** This document records what the logs confirm, what they
refute, and one root cause the first pass did not find.

---

## 0. What changed

| Prior finding | Verdict from the logs |
| --- | --- |
| §4 — Render credential broken (H1 vs H2 unresolved) | **H1 confirmed, H2 refuted** (§1) |
| F3 — unpinned image inputs cause intermittent build failures | **Refuted as an incident source** — 1 build failure in 291 deploys, four weeks ago (§2) |
| F1 — the gate never builds the production image | True, but **has not caused a production incident** in the retained window; de-prioritized (§2) |
| F5 — deploy-induced restarts | **Confirmed, and root-caused** to a specific code defect the first pass did not find (§3) |
| §1 — the concurrent-create race is the root cause | Real and reproducible, but a **minor** contributor in production: 6 events vs 72 from a different path (§5) |
| §1 — "generation numbers are the fingerprint this bug leaves behind" | **Refuted.** The two paths log different lines and the ratio is 12:1 against the race (§5) |
| A3.1 — "nothing ages a turn out" | True of the turn machine; **the steward covers the ticket-level symptom** (§6) |
| A3.5 — an Amika list endpoint could change shape and break the client | **Already happened**, on `/sandbox-snapshots`, 2026-08-01 (§4) |

---

## 1. The credential: H1 confirmed, H2 refuted

`RENDER_API_KEY` inside a freshly created sandbox is now a real 32-char `rnd_…` key, and
`GET /v1/services` returns 200. It arrives by the same `.amika/config.toml`
`[env] { secret = "RENDER_API_KEY" }` path that was suspect.

That discriminates the two hypotheses for free — the probe secret proposed as rec #8 is no
longer needed:

- **H2 (Amika's `{ secret = … }` resolver passes the reference name through) — refuted.**
  The resolver works; the same declaration now yields the real value.
- **H1 (the stored value was the placeholder) — confirmed by elimination.** The secret was
  registered with its own name as its value.

Recs #9 (assert at boot when an injected secret's value equals its own name) and #10 (fix
the `.amika/config.toml` comment, which anticipates *unset* rather than *value == name*)
still stand, and are now worth more: this failure mode cost two investigations.

---

## 2. Build failures are not the story

Render retains **291 deploys** for this service, 2026-07-05 → 2026-08-03:

| Status | Count |
| --- | --- |
| `deactivated` | 289 |
| `live` | 1 |
| `build_failed` | **1** |

The single failure is `dep-d95pdie1355s73ajc4f0`, **2026-07-06T11:51:37Z**, commit
`9cda02ad` ("Update system prompt"). It is four weeks old and nothing has failed to build
since — 278 distinct commits, one bad build.

**F3 is refuted as an explanation for recurring failures.** The reasoning was sound —
mutable base tags and a network `corepack` fetch really can make the same commit build
differently — but the predicted signature (retries of the same commit) does not appear.
There are no retries because there are no failures. F3 stays on the list as hygiene
(§P5 of the prior doc), not as an incident cause.

**F1 is downgraded, not withdrawn.** It remains true that no gate step builds
`backend/Dockerfile`, and that this is a real hole. But the empirical failure rate through
that hole is 1-in-291 over a month, so P3 ("add `docker build` to `check.yml`") is
insurance, not urgent remediation.

**F2 is confirmed and matters — through a different mechanism.** `autoDeploy: true` with
no CI gate does not hurt via bad builds; it hurts via *deploy frequency*, which is the
input to §3. 16 deploys on 2026-08-01, 13 on 2026-08-02.

---

## 3. The actual recurring Render failure: every deploy kills the process

This is the finding the first pass missed, and it is the direct answer to "recurring
render failures".

```
{"level":"ERROR","msg":"kiln exited with error","err":"kiln: http shutdown: context deadline exceeded"}
```

**18 occurrences**, 2026-08-01T11:47:07 → 2026-08-03T11:35:32. Correlating each against
the deploy list:

| Time since the preceding deploy started | Exits |
| --- | --- |
| 44–170s | **17** |
| no deploy within 12 min | 1 (2026-08-03T11:20:22) |

So this fires on essentially every deploy. Not an occasional unlucky drain — a
deterministic one.

### Why it always times out

`run()` (`backend/cmd/kiln/wiring.go:784-791`) does the textbook graceful shutdown:

```go
<-ctx.Done()
shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
defer cancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    return fmt.Errorf("kiln: http shutdown: %w", err)
}
```

with `shutdownTimeout = 15 * time.Second` (`wiring.go:56`).

`http.Server.Shutdown` stops accepting new connections and then **waits for active
connections to become idle. It does not cancel in-flight request contexts.**

The SSE board stream never becomes idle. `Hub.ServeStream`
(`backend/internal/api/hub.go:107-145`) is an unbounded loop that returns only on
`r.Context().Done()` (the *client* disconnecting) or a write error:

```go
for {
    select {
    case <-r.Context().Done():
        return
    case f := <-c.ch:
        ...
    case <-keepalive.C:
        ...
    }
}
```

Nothing in the shutdown path signals that loop. So if **one** browser holds
`/api/stream` open — the normal state whenever anyone has the app in front of them —
`Shutdown` burns the full 15 seconds and returns `DeadlineExceeded`. The process then
exits hard with in-flight work still running.

### The confirmed consequence cascade

Every one of these is `context canceled` from that hard exit, and every one is in the logs:

| Message | Count |
| --- | --- |
| `agent: persist turn` — `agent/postgres: update: context canceled` | 47 |
| `runtime.event.failed` — `brain: anthropic messages.new: context canceled` | 6 |
| `runtime: mark retry` — `runtime/postgres: mark retry: context canceled` | 5 |
| `api: record push presence` — `context canceled` | 2 |
| (plus `sql: database is closed` variants once the pool closes) | 5 |

**This is not data loss.** `runtime/worker.go:109` documents the design: a crash between
claim and mark leaves the row pending with the claim's pushed-out due time, and restart
re-runs it. Confirmed — zero `mark dead` / dead-letter events in the window.

**But each kill burns one attempt of `MaxAttempts = 8`** (`runtime/queue.go:56`), because
`ClaimNextDue` increments on claim. An event unlucky enough to be mid-brain-pass across
eight deploys dead-letters. At 16 deploys/day that margin is thinner than it looks, and it
is invisible until it happens.

It also means a brain LLM call is being killed mid-flight several times a day — paid for,
then discarded.

### The fix

Make the SSE streams shut down with the server. Either:

1. `srv.RegisterOnShutdown(func() { close(h.done) })` and add `case <-h.done: return` to
   `ServeStream`'s select; or
2. give the server a `BaseContext` derived from a cancellable context, and cancel it just
   before `Shutdown` so every request context — including the streams — fires.

Either turns a guaranteed 15-second timeout-and-hard-exit into a sub-second clean drain,
and removes the whole cascade above. This is a small, well-contained change and it is the
highest-value fix in either document.

---

## 4. Orchestration failures, ranked by what actually happened

The prior doc reasoned from code. Here is the same module ranked by production frequency,
2026-07-27 → 2026-08-03 (Render's retention window).

**1. `claude_credential_reauth_required` — 15 events, 2026-08-03 11:17–11:20.**

```
amika: 400 claude_credential_reauth_required: Your stored Claude OAuth credential is no
longer valid: Anthropic rejected its refresh token. This usually means the same Claude
login was refreshed elsewhere (for example by Claude Code on your machine).
```

All three slots, retrying about every 60s. **Total loss of sandbox creation** while it
lasted — no worker could be provisioned for any project. Now resolved: a new sandbox was
created at 11:33:38 and Amika currently lists three `started` workers.

Note the shape: this is an *Amika-side* credential (the Claude OAuth token Amika holds to
run the agent), not a Kiln one, and it can be invalidated by an unrelated action elsewhere
— a local `claude` login refreshing the same account. Kiln has no way to detect it in
advance and no fallback. Worth a health check and a distinct user-facing message, because
the current failure is "every ticket stops moving" with the reason buried in an ERROR log.

**2. Amika name `validation_failed` (DNS label) — 25 events, 2026-08-01 14:41–15:51.**

Self-inflicted, and instructive. `462f9e7` ("auto-rotate worker names") deployed at
14:27 UTC and built rotation names from the **full** slot UUID:
`<prefix><fullUUID>-g<gen>` ≈ 65 chars, over the 63-char DNS label limit. Amika 400'd every
one, so the rotation intended to heal a wedge could not itself be created. `212ab3b`
("DNS-label-safe generational worker names") deployed at 15:48 UTC and fixed it with the
12-hex fragment. **~70 minutes in which no slot could rotate at all.**

Both commits landed the same afternoon, straight to production, with no gate that could
have caught a name-length violation — `names_internal_test.go` now guards it, added by the
fix rather than before it.

**3. Amika `403 Forbidden` — 28 events, 2026-08-02 15:02–19:09.**

```
amika: 403 : <!doctype html><meta charset="utf-8">…<title>403</title>403 Forbidden (trace )
```

An HTML edge/WAF response, not a JSON API error — note the empty `code` and empty `trace`,
which is how you can tell it never reached the application. Spread across
`api: list dev boxes` (19), `agent: liveness refresh; skipping project` (6), and
`steward: sweep project` (3).

Kiln polls `GET /sandboxes` from three independent loops — liveness every 10s per project,
reconcile every 60s, plus dashboard reads — so a rate limiter is a plausible trigger. This
shape is unhandled: it is neither retried as transient nor surfaced as a distinct
condition, it just fails the sweep.

**4. `GET /sandbox-snapshots` decode — 10 events, 2026-08-01 13:20–13:37.**

```
amika: decode GET /sandbox-snapshots: json: cannot unmarshal object into Go value of
type []amika.snapshotObject
```

Amika wrapped a list response in `{"items":[…]}`. Fixed by `8af36c3`.

**This is exactly the failure A3.5 predicted**, one endpoint over. The prior doc flagged
`ListWorkers` reading `GET /sandboxes` as an unpaginated array and called it "[Low,
latent] — harmless today, but if Amika ever introduces a default page cap…". Amika changed
the shape of a *sibling* list endpoint within the same week. A3.5 should be re-rated:
the vendor demonstrably does this, and `GET /sandboxes` is the one where a truncated or
misread list causes mass duplication rather than a dashboard error.

**5. The §1 concurrent-create race — 6 events, all 2026-08-01, none since.** See §5.

Also seen once each: `401 github_token_required` (2026-08-02T15:36), a
`500 internal_error` on snapshots, a `connection reset by peer`, and three
`brain: malformed model output repeated after re-prompt`.

---

## 5. Correcting §1's production evidence

The prior doc's headline root cause — two goroutines racing to create one slot's sandbox,
and `createWorkerRotating` responding to the 409 by creating a *second* sandbox — is
**confirmed in code and deterministically reproducible**. Nothing here disputes the
mechanism.

What the logs refute is the claim about its production footprint. §1 argued:

> The signal is in the **generation numbers**: g6, g5, g4, g4, g2, g1 … a slot sitting at
> g5/g6 has rotated five or six times and had its predecessors swept. That is the
> fingerprint this bug leaves behind.

The two ways a generation can advance log **different lines**, so they can be counted
separately:

| Path | Log line | Events |
| --- | --- | --- |
| 409 on create — **the race** (`createWorkerRotating`, `service.go:928`) | `agent: worker name conflict; rotating to next generation` | **6** |
| Nothing adoptable (`adoptOrCreateSlot`, `service.go:860`) | `agent: rotating slot to next generation past unadoptable record` | **72** |

**12:1 against the race.** Generation numbers are overwhelmingly the fingerprint of the
*unadoptable-record* path — `newGen = maxGen + 1` when `pickAdoptable` returns nothing —
not of concurrent creates. The g4→g10 climb on slot `405f746a804d` between the two
investigations happened alongside **six** name conflicts in total, across all slots, in
the entire window.

Two further constraints on the race's footprint:

- All 6 conflicts are on **2026-08-01**, between 14:31 and 21:20. None on 08-02 or 08-03.
- Rotation-on-conflict only *existed* from `462f9e7` (deployed 2026-08-01 14:27), and the
  first conflict WARN is at 14:31 — four minutes later. Before that commit a 409 was a
  hard wedge, not a duplicate sandbox.

So the P0 recommendations from §6 remain correct as engineering (adopt-on-conflict, pin
the turn to its sandbox, per-slot single-flight, don't destroy a sandbox with a live
turn) — the race is a real correctness bug and the pin is genuinely missing. But it should
be sequenced **after** §3, and the doc should not claim the generation numbers as its
evidence.

### The churn that is actually there: a rebuild loop

72 rebuild rotations in ~42 hours is its own finding, and a larger one. It runs tight —
slot `bb268f76` went gen1 → gen2 in **10 seconds** (2026-08-03 11:35:06 → 11:35:16),
which no sandbox could have provisioned and failed within.

Deploys amplify it but do not explain it:

| Time since last deploy | Rotations |
| --- | --- |
| < 5 min | 15 |
| 5–15 min | 21 |
| 15–60 min | 5 |
| > 60 min | **31** |

Half cluster within 15 minutes of a deploy — consistent with F5's interaction (a restart
drops the in-memory worker cache, so every slot takes the cache-miss path at once). The
other 31 do not, so there is an independent driver.

**Why a slot reads unadoptable is not determinable from the current logs**, and that is
itself the finding: `adoptOrCreateSlot` logs the rotation and the new generation but
**not the candidate set or the statuses that made it unadoptable**, and `destroyUnkept`
(`service.go:997`) logs only failures — never which sandboxes it destroyed. Between them
the pool's actual lifecycle is invisible.

The leading hypothesis, unconfirmed: `pickAdoptable` (`service.go:887`) skips only
`RunErrored`, and `classifyState` (`amika/states.go:54-56`) maps `deleted` and
`terminated` into the errored set. A tombstoned record left in Amika's list would read as
permanently unadoptable and force a fresh generation on *every* sweep — which matches both
the volume and the 10-second spacing. Confirming it needs one log line, not another
investigation.

---

## 6. Two corrections to §3's severity ratings

**A3.1 ("a turn has no wall-clock timeout") — half true.** The turn machine really has no
deadline: `stepCheckTurn` returns silently when `st.Running`, `recordFailure` is never
reached, and `maxAttempts` only counts errors. But the *ticket* is not stranded — the
steward (`backend/internal/steward`) sweeps every 60s and pokes any Working ticket whose
agent has been idle/stopped for 5 minutes, escalating to Blocked if the poke doesn't take
(`config.go:11-13`, `service.go:269-292`). It fired in production: **3 `poked stalled
agent` + 1 `blocked stalled ticket`** on 2026-08-02.

The prior doc's "the ticket sits in Working indefinitely" is therefore wrong; ~5–10 minutes
is the real bound. Two caveats keep this from being a full mitigation:

- The steward deliberately never touches a `building`/`starting` agent ("no safe way to
  tell a slow-but-fine turn from a hang" — `steward.go:14-18`), so a wedge that keeps the
  sandbox looking busy is still invisible to it.
- The poke starts a *new* turn but does not terminate the wedged one. `pollOnce`
  (`service.go:505-514`) advances every non-terminal turn independently with no per-slot
  serialization, so the wedged row stays non-terminal forever and keeps polling Amika every
  2 seconds — across restarts, since turns are persisted. That is an unbounded leak of poll
  traffic and `ListNonTerminal` rows, and it is **completely silent**: the `Running: true`
  branch logs nothing.

**A3.5 ("[Low, latent]") — should be re-rated.** See §4 item 4: the vendor changed a list
endpoint's response shape during the window under investigation.

---

## 7. Revised priorities

Ordered by evidence, not by discovery order.

### P0 — Stop killing the process on every deploy (§3)

1. Close the SSE streams on shutdown (`RegisterOnShutdown` + a done channel in
   `ServeStream`, or a cancellable `BaseContext`). One deterministic failure, several times
   a day, with a small fix. Removes the whole `context canceled` cascade and the
   `MaxAttempts` erosion.

### P1 — Make the pool's lifecycle visible (§5)

2. Log the candidate set and each candidate's status when `adoptOrCreateSlot` decides
   nothing is adoptable, and log what `destroyUnkept` destroys. Without this the 72
   rotations cannot be explained, and any fix cannot be verified.
3. Then confirm or kill the tombstone hypothesis — if `deleted`/`terminated` records
   persist in `GET /sandboxes`, exclude them from candidates rather than classifying them
   `RunErrored`.

### P2 — The race and the missing pin (prior §6 P0)

4. Adopt-on-conflict in `createWorkerRotating`; set and read `Turn.ProviderWorker`;
   per-slot single-flight; don't destroy a sandbox with a live turn. Still correct, still
   worth doing — 6 production events, so not an emergency.
5. Bound the silent wedge (prior rec #5) — a turn that has polled `Running: true` for N
   minutes should fail, not poll forever. This is the leak in §6 above, and the steward
   does not cover it.

### P3 — Harden the Amika client (§4)

6. Handle the HTML `403` shape distinctly (retry/backoff rather than failing the sweep),
   and reconsider the polling rate that may be provoking it.
7. Re-rate A3.5 and paginate / envelope-tolerate `GET /sandboxes` before it bites the way
   `/sandbox-snapshots` did.
8. Surface `claude_credential_reauth_required` as a distinct, user-actionable condition —
   it is a total outage whose cause is currently only visible in an ERROR log.

### P4 — Deploy hygiene (§2, revised)

9. Recs #9/#10 from the prior doc (assert at boot on `value == name`; fix the
   `.amika/config.toml` comment). Cheap, and this bug cost two investigations.
10. F1/P3 (`docker build` in CI) and F3/P5 (pin image inputs) — keep, but as hygiene. The
    measured failure rate is 1 build in 291 deploys.
11. F2 / prior P4 (branch protection, `autoDeploy: false`) — unchanged as an owner's call,
    but note the argument has shifted: the reason to slow deploys down is no longer "a bad
    build could ship", it is that **each deploy currently kills in-flight work** (§3). Fix
    P0 first and this becomes much less urgent.

---

## 8. Method note

Everything in §2–§5 is derived from the Render API: the deploy list
(`/v1/services/{id}/deploys`, fully paginated — 291 records) and the runtime logs
(`/v1/logs`, filtered by `text` and `level`, walked backwards via `nextEndTime`). The log
retention window is **7 days** (2026-07-27 → 2026-08-03), which bounds every count here —
frequencies before 07-27 are unknown, and the agent module changed substantially on 08-01.

Counts are of log lines, not of incidents; one incident can emit several lines. Where a
claim rests on code rather than logs it says so, and §5's tombstone hypothesis is
explicitly labelled unconfirmed.
