# Root cause, part 3 — two backend instances, and the duplicate-instruction bug

**Date:** 2026-08-03 · **Service:** `srv-d953nmcvikkc73d8aq60` · **Trigger:** live incident on
ticket `0ecad1a2-0084-4f9f-932d-5bf96a9d893d` ("Integrations page: layout cleanup…")

Third in the series, after
[`root-cause-2026-08-02-concurrent-sandbox.md`](root-cause-2026-08-02-concurrent-sandbox.md)
and [`root-cause-2026-08-03-render-logs.md`](root-cause-2026-08-03-render-logs.md).

A worker reported, mid-turn, that a **second Claude Code process was editing the same files
in the same working directory on the identical prompt**. The dispatch logs settle how. It is
not the in-process goroutine race the first document blamed, and no in-process lock would have
prevented it.

> **The headline.** Every Render deploy runs **two full Kiln backends against one database for
> ~68 seconds**. Both run the agent poller, both run the queue dispatchers, and **nothing in
> the codebase excludes a second process** — not the agent turn machine (which has no claim at
> all) and not the events queue (whose only per-project serialization is an in-memory map).
> Measured over the 7-day retention window: **12 turns started twice** (100 % cross-instance)
> and **31 events processed twice** (30 of 31 cross-instance).

---

## 0. What changes

| Prior finding | Verdict |
| --- | --- |
| 08-02 §1 — concurrent sandbox creation is *the* root cause of duplicated work | **Demoted.** Real, but a distinct and much rarer bug. The duplicate-*instruction* mechanism is cross-process (§2) |
| 08-02 rec #3 — "per-slot mutex / single-flight around `adoptOrCreateSlot`" | **Insufficient.** An in-process mutex cannot exclude another process. Needs a DB-level guard (§6) |
| 08-02 F5 / 08-03 §3 — deploys restart the backend mid-pass | **Confirmed and extended.** The problem is not only that the old process dies, it is that the **new one starts working before the old one stops** (§2) |
| 08-03 §5 — "why a slot reads unadoptable is not determinable" | **Partly answered.** At least some of the 72 rotations are two instances rotating the same slot (§5) |
| 08-03 §3 — the 15s SSE shutdown timeout | Still true; it **lengthens** the overlap by 15s but is not its main cause (§2) |
| The report's framing: "two instances launched ~7 min apart" | **Corrected.** They launched **1.47 s apart**; the agent's wording was "both started ~7 min ago" (§1) |

---

## 1. What happened on ticket `0ecad1a2`

All times UTC, 2026-08-03, from the Render log API. Instance ids are the Render instance
label suffix.

| Time | Instance | Event |
| --- | --- | --- |
| 12:23:58 | — | deploy `dep-d9o8gnnlk1mc739o4upg` starts |
| 12:24:31.594 | dlnvl | `board.transition` **pull** — ticket → working, worker `bb268f76` |
| 12:24:32.600 | dlnvl | `agent.delivery.recorded` idem_key **8871**, `continuation:false` — one delivery |
| **12:25:17.496** | **pxl2k** | **`kiln serving`** — the new instance boots and starts its loops |
| 12:25:25 | — | deploy marked **live** (8 s *after* pxl2k was already working) |
| **12:25:38.129** | **dlnvl** | **`agent.turn.started`** idem 8871, `fresh:true`, hash `5e7a35fa9ec0` |
| **12:25:39.596** | **pxl2k** | **`agent.turn.started`** idem 8871, `fresh:true`, hash `5e7a35fa9ec0` |
| 12:26:25.125 | dlnvl | last log line — the old instance finally dies |
| ~12:32 | — | the agent notices two PIDs (1544, 1587) and backgrounds a watcher instead of interleaving edits |
| 12:33:18.966 | pxl2k | `agent.turn.completed` idem 8871 — the report |

**Same idempotency key, same `turn_id` (`delivery-8871`), same instruction hash, both
`fresh: true`, 1.47 seconds apart, from two different processes.** One board delivery, two
`StartTurn` calls, two Claude Code conversations in one sandbox on one prompt.

The agent's report said the two PIDs "both started ~7 min ago" — i.e. simultaneously, ~7
minutes before it wrote that at 12:33. Not 7 minutes apart. The log confirms 1.47 s.

### Then it happened three more times on the same ticket

The completion event was itself double-processed, and each resulting instruction was
double-started:

| Time | Instance | Event |
| --- | --- | --- |
| 12:33:22.620 | pxl2k | `runtime.event.received` evt **1870**, attempts **1** |
| **12:33:23.513** | **b2xvb** | **`kiln serving`** — next deploy's instance boots |
| **12:33:24.611** | **b2xvb** | `runtime.event.received` evt **1870**, attempts **2** — 1.1 s after its own boot |
| 12:33:30 | — | deploy `dep-d9o8klgae00c73fqp3cg` marked live — 6 s *after* b2xvb claimed the event |
| 12:33:49.075 | pxl2k | brain pass A → `send_to_agent` → delivery **8910** ("The tree should be settled now…") |
| 12:33:51.755 | b2xvb | brain pass B → `send_to_agent` → delivery **8912** ("Status check: has the watcher…") |
| 12:33:54 / 12:33:57 | both | two different `post_update`s to the feed — the duplicated "Heads up:" messages |
| 12:34:12.453 / 12:34:14.558 | b2xvb / pxl2k | `agent.turn.started` idem **8910** — twice |
| 12:34:24.889 / 12:34:26.939 | b2xvb / pxl2k | `agent.turn.started` idem **8912** — twice |
| 12:34:31.153 | pxl2k | last log line |

**One agent completion produced two brain passes, two different instructions, and four
`StartTurn` calls into the same sandbox inside 40 seconds.** The 2× at the queue and the 2× at
the turn machine multiply.

---

## 2. The mechanism: a ~68-second two-headed window on every deploy

`render.yaml` says `plan: starter`, and the live service reports `numInstances: 1`. That is
the **steady-state** count. Render's zero-downtime deploy boots the replacement, waits for
`/healthz`, cuts traffic over, and only then drains the old one — so `numInstances: 1` never
means *one process exists*.

Measured from `kiln serving` (new instance's first log) to the old instance's last log line:

| New instance boots | Old instance's last line | Overlap |
| --- | --- | --- |
| pxl2k 12:25:17 | dlnvl 12:26:25 | **68 s** |
| b2xvb 12:33:23 | pxl2k 12:34:31 | **68 s** |
| dlnvl 12:03:39 | bmznn 12:05:01 | ≥ 82 s |
| 6fjfm 11:53:21 | q9l6p 11:54:44 | ≥ 83 s |

Roughly: ~45 s of Render grace before `SIGTERM`, plus the 15 s the process then burns failing
`srv.Shutdown` on the SSE streams (08-03 §3), plus boot. The SSE bug lengthens the window;
the grace period creates it.

Two properties of `run()` (`backend/cmd/kiln/wiring.go:750-765`) make the window fully active
rather than merely overlapping:

```go
go g.runWorker(ctx, "events", g.events, log)
go g.runWorker(ctx, "outbox", g.outbox, log)
go g.runAgent(ctx, log)      // poller 2s, reconciler 60s, liveness 10s
go g.runSteward(ctx, log)
```

1. **The new instance starts every background loop at boot**, before `/healthz` passes and
   before Render routes traffic. b2xvb claimed a production event **1.1 s after boot and 6 s
   before its deploy was marked live**.
2. **The old instance keeps all of them running until `SIGTERM`**, ~45 s after the new one is
   already working. Nothing hands off; nothing quiesces.

So for ~68 seconds per deploy there are two pollers, two reconcilers, two stewards and four
queue dispatchers on one database. At 13–16 deploys/day that is ~15–18 minutes/day of
two-headed operation — and the deploys cluster exactly when tickets are moving.

---

## 3. Why nothing stopped it — two independent gaps

### 3.1 The agent turn machine has no claim at all

`pollOnce` (`backend/internal/agent/service.go:504-515`) is a plain read-and-act:

```go
rows, err := s.store.ListNonTerminal(ctx)
for _, t := range rows {
    s.advance(ctx, t)      // → advanceSend → stepStartTurn on PhaseWorkerReady
}
```

`stepStartTurn` (`service.go:611`) calls `provider.StartTurn(...)` and only *afterwards*
writes `t.Phase = PhaseTurnStarted` via `s.update`. There is **no row lock, no lease, no
owner column, no conditional update on phase** — nothing that makes "start this turn" a
mutually exclusive act. Two processes reading the same `worker_ready` row within the same 2 s
poll interval both call `StartTurn`, and both succeed, because Amika happily mints a second
session on the same sandbox.

`Service.mu` guards the in-memory `workers` map only. It is irrelevant across processes.

### 3.2 The events queue's serialization is in-memory, behind a 1-second visibility timeout

The queue claim (`backend/internal/runtime/postgres/store.go:107-120`) is a correct
`FOR UPDATE SKIP LOCKED` claim — but its lease is:

```sql
next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
```

`attempts` is the **pre-update** value, so **a fresh row's visibility timeout is 1 second**
(`power(2,0) = 1`). A brain pass takes 10–60 s. The statement's own comment asserts
"the dispatcher always marks it well before then" — that has never been true of the *time*;
it was true only because of the second guard:

```go
e, ok, err := w.store.ClaimNextDue(ctx, w.queue, busyProjects(busy))
```

`busy` is a `map[string]struct{}` **local to one `Worker.Run` goroutine** in one process
(`runtime/worker.go:100-140`). Process B's busy set is empty by construction. So B re-claims
the row one second after A claimed it, and both run a full brain pass on the same event —
each emitting its own tool calls, feed posts, and instructions.

This is a latent design bug even single-instance (any pass longer than its lease is one
crash-recovery edge from double execution); two instances make it fire on essentially every
deploy.

### 3.3 What *did* hold

`agent.delivery.recorded`: **333 lines, 333 distinct idempotency keys, zero duplicates.** The
board outbox → delivery path is properly idempotent (`InsertEvent`'s partial unique index on
`idempotency_key`, and the delivery row keyed on outbox id). The duplication is entirely
downstream of a correctly-recorded single delivery. The `pull` transition also fired once.

That is worth stating plainly: **the board's transactional-outbox design is not at fault.**
The two places without a durable claim are the two places that duplicated.

---

## 4. How often — the whole retention window

2026-07-27 → 2026-08-03, from the Render log API.

**Turns started more than once** (`agent.turn.started`, grouped by `idem_key`):

| | |
| --- | --- |
| Distinct turns started | 243 |
| Started **twice** | **12 (4.9 %)** |
| …of which cross-instance | **12 (100 %)** |
| …of which same-instance | **0** |

```
6268 5b7dee77  gap 3.8s  k8hsk / wt4ln
6271 5b7dee77  gap 0.7s  k8hsk / wt4ln
6273 5b7dee77  gap 0.6s  k8hsk / wt4ln
6752 7d938c99  gap 0.6s  6spnc / 992sc
8103 ccb420d3  gap 2.4s  7mtts / cqvfb
8839 2e833e40  gap 3.7s  bmznn / dlnvl
8844 2e833e40  gap 2.9s  bmznn / dlnvl
8871 0ecad1a2  gap 1.5s  dlnvl / pxl2k   ← this incident
8888 2e833e40  gap 0.9s  dlnvl / pxl2k
8904 2f9bc4cf  gap 3.6s  pxl2k / b2xvb
8910 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
8912 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
```

Every one falls inside a post-deploy overlap window. Every gap is under 4 seconds — i.e. under
two poll intervals. **Zero same-instance duplicates**: the in-process goroutine race the 08-02
document made its headline has never produced a duplicate turn start in the retained window.

**Events processed more than once** (`runtime.event.received`, grouped by `event_id`):

| | |
| --- | --- |
| Distinct events | 561 |
| Re-delivered while still in flight | **31 (5.5 %)** |
| …cross-instance | **30** |
| …same-instance | 1 (evt 1624, 8.2 s apart) |
| Processed **three** times | 2 (evts 1471, 1709) |

Most gaps are 1.0–1.9 s — the 1-second visibility timeout, exactly. Both `human.message` and
`agent.turn_completed` are affected, so a **user message can also produce two brain passes**.

This is the mechanism behind duplicated feed posts and contradictory instructions, and it
consumes a paid LLM pass every time.

---

## 5. Corrections to the two prior documents

**08-02 §1 is demoted, not withdrawn.** Two goroutines racing `adoptOrCreateSlot` inside one
process is a real bug, reproducible, and worth fixing. But it is a *sandbox-provisioning*
bug, and it is not what put two Claude Code processes on one prompt. Of the 6 production
`worker name conflict` events, only one pair is same-instance (`mst45`, slot `4181c8d9`,
14:41:27 and 14:41:28 on 08-01) — that pair *is* the in-process race, and it is the only
sighting of it in seven days.

**08-02 rec #3 would not have helped.** "A per-slot mutex (or single-flight keyed on slot id)"
is process-local. The measured mechanism is cross-process, so the mutex is defence in depth
against the rarer bug only. Any real fix must be in Postgres.

**08-03 §5's open question is partly answered.** The document flagged
`bb268f76` going gen1 → gen2 in 10 seconds as inexplicably fast. It was two instances:
`4qzf8` rotated it at 11:35:06 and `q9l6p` — a different process — rotated it again at
11:35:16. Some fraction of the 72 "unadoptable" rotations are two reconcilers sweeping the
same pool against each other's half-finished work. The tombstone hypothesis is still live for
the rest; both need the §P1 logging that document asked for.

**08-03 §3 stands and gains a second reason to be urgent.** The 15-second failed
`srv.Shutdown` is now not only a source of `context canceled` cascades — it is 15 of the 68
seconds during which two backends are issuing instructions.

---

## 6. Fix recommendations

Ordered by evidence. Items 1–3 are the ones this incident argues for.

### P0 — Make the background loops single-owner

1. **Gate every background loop behind a Postgres advisory lock.** In `graph.run`, take
   `pg_try_advisory_lock(<fixed key>)` on a dedicated pinned `*sql.Conn` before starting
   `runWorker(events)`, `runWorker(outbox)`, `runAgent` and `runSteward`. An instance that
   does not hold it serves HTTP only and retries every few seconds; the lock is released on
   clean shutdown and dies with the session on a hard exit, so the new instance picks it up
   within seconds of the old one's death. This is ~40 lines, needs no schema change, and it
   closes **both** §3.1 and §3.2 in one move — including the reconciler/steward duplication
   §5 points at.

   It also fixes the ordering bug in §2: today the new instance starts working before it is
   even routed traffic. Under the lock it starts working when the old one stops.

2. **Give the turn machine a durable claim** (defence in depth; also correct single-instance).
   Either make `ListNonTerminal` a claim — `FOR UPDATE SKIP LOCKED` plus a `next_poll_at`
   push, mirroring `ClaimNextDue` — or make the phase transition a compare-and-swap:
   `UPDATE agent_turns SET phase='starting' WHERE id=$1 AND phase='worker_ready' RETURNING …`,
   call `StartTurn` only if the CAS won, then move to `turn_started`. A crash between CAS and
   `StartTurn` leaves a `starting` row for a wall-clock sweep to recover — which is the
   deadline A3.1 has always wanted anyway.

3. **Fix the queue's visibility timeout.** `least(power(2, attempts), 60)` gives a fresh row a
   **1-second** lease, shorter than any brain pass. Separate the two concepts: a `claimed_until`
   (or a floor of ~2× the longest expected pass, e.g. 180 s) for the lease, and the exponential
   value for *retry backoff after a recorded failure*. Correct the statement's comment, which
   currently asserts an invariant the code does not have.

### P1 — Shorten and observe the window

4. **Close the SSE streams on shutdown** — 08-03 §3 rec #1, unchanged. Worth 15 s of the 68.
5. **Stamp an instance id into every log line** (`obs` already stamps `turn_id`; add a
   process-boot uuid). This investigation only worked because Render happens to label log
   lines by instance; the application itself cannot tell you which process did what.
6. **Log `agent.turn.started` with the winning claim**, once (2) lands, so a duplicate is a
   visible error rather than two indistinguishable INFO lines.

### P2 — Reduce deploy frequency (owner's call, argument restated)

7. `autoDeploy: false` + branch protection (08-02 P4, 08-03 rec #11). The argument has shifted
   again: it is no longer "a bad build could ship" (refuted, 1 in 291) nor only "deploys kill
   in-flight work" — it is that **each deploy opens a 68-second window in which the
   orchestrator can send an agent two contradictory instructions**. With P0 fixed this reverts
   to a hygiene question.

### P3 — The prior P0, unchanged in substance

8. Adopt-on-conflict in `createWorkerRotating`; set and read `Turn.ProviderWorker`; don't
   destroy a sandbox with a live turn (08-02 §6 #1, #2, #4). Still correct. Drop #3
   (per-slot mutex) in favour of item 1 above, or keep it only as an in-process optimisation
   and do not describe it as a fix.

---

## 7. What would have been damaged without the agent's judgement

Nothing in the system stopped this. The turn that reported it was one of two writers in the
same working directory with no file locking, no worktree isolation, and no awareness of each
other; the second process had already rewritten `Integrations.tsx`, `Dashboard.css` and the
tests under the first one mid-read. It was caught because the agent noticed the tree changing
beneath it, checked `ps`, and chose to background a watcher rather than interleave edits.

That is not a control. The `end-to-end-development` skill's parallel-agent isolation rule
assumes one agent per working directory; §3.1 means the platform cannot honour it. Item 1 is
what makes the assumption true again.

---

## 8. Method note

Everything here is from the Render API: `/v1/logs` (walked backwards via `nextEndTime`, text-
and time-filtered, deduplicated by `(timestamp, message)`), `/v1/services/{id}/deploys`, and
`/v1/services/{id}`. Instance attribution comes from each log line's `instance` label, which
is what makes the cross-process claim checkable rather than inferred. Retention is 7 days
(2026-07-27 → 2026-08-03), which bounds every count. Code claims cite files at `f22b040`.
