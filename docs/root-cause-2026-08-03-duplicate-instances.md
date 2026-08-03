# Root cause, part 3 — two backend instances, and the duplicate agent launches

**Date:** 2026-08-03 · **Service:** `srv-d953nmcvikkc73d8aq60` · **Code cited at:** `f22b040`
(the running service was `225f2b8e`, which touches neither `internal/agent` nor `internal/runtime`)
**Trigger:** live incident on ticket `0ecad1a2-0084-4f9f-932d-5bf96a9d893d`
("Integrations page: layout cleanup…"), corroborated by three further live captures
(§1.3–§1.5), the last of which was recorded **while this paragraph was being written**.

Third in the series, after
[`root-cause-2026-08-02-concurrent-sandbox.md`](root-cause-2026-08-02-concurrent-sandbox.md)
and [`root-cause-2026-08-03-render-logs.md`](root-cause-2026-08-03-render-logs.md).

A worker reported, mid-turn, that a **second Claude Code process was editing the same files
in the same working directory on the identical prompt**. The dispatch logs settle how. It is
not the in-process goroutine race the first document blamed, and no in-process lock would
have prevented it.

> **The headline.** Every Render deploy runs **two full Kiln backends against one database for
> ~68 seconds**. Both run the agent poller, both run the queue dispatchers, and **nothing in
> the codebase excludes a second process** — not the agent turn machine (which has no claim at
> all) and not the events queue (whose only per-project serialization is an in-memory map).
> Measured: **12 turns started twice** (100 % cross-instance) and **31 events processed twice**
> (30 of 31 cross-instance), ≈0.9 duplicate brain passes per deploy across 35 deploys — and
> those turn counts are a **lower bound**, because a duplicate whose instance dies mid-send
> spawns a real agent and never logs the line we counted (§4.1).

---

## 0. Status of each deliverable, and what changes

| Ticket item | Status |
| --- | --- |
| Pull the dispatch/instruction logs for ticket `0ecad1a2` | **Done** (§1) — full timeline recovered |
| Explain how a second process was launched on the same sandbox | **Root-caused** (§2–§3). Not an auto-retry, and not the orchestrator deciding to send two instructions: two backend *instances* independently advanced the same `agent_turns` row |
| Fold in as a confirmed real-world instance | **Four** folded in (§1): the reported one (`idem_key` 8871); a second on **this investigation's own dispatch** (`8914`); a third on ticket `2f9bc4cf` where the two agents ran 26 minutes side by side (`8904`, §1.4); and a fourth (`8968`, §1.5) captured **while this document was being written**, in which one agent-completion event produced **three** `claude` processes in one sandbox — one of them unrecorded |
| Fix recommendation | §6, re-prioritized across all three documents |

| Prior finding | Verdict |
| --- | --- |
| 08-02 §1 — concurrent sandbox creation is *the* root cause of duplicated work | **Demoted.** Real, but a distinct and much rarer bug. The duplicate-*instruction* mechanism is cross-process (§2) |
| 08-02 rec #3 — "per-slot mutex / single-flight around `adoptOrCreateSlot`" | **Insufficient.** An in-process mutex cannot exclude another process. Any real fix must be in Postgres (§6) |
| 08-02 F5 / 08-03 §3 — deploys restart the backend mid-pass | **Confirmed and extended.** The problem is not only that the old process dies, it is that the **new one starts working before the old one stops** (§2) |
| 08-03 §5 — "why a slot reads unadoptable is not determinable" | **Partly answered.** At least some of the 72 rotations are two instances rotating the same slot (§5) |
| 08-03 §3 — the 15s SSE shutdown timeout | Still true; it **lengthens** the overlap by 15s but is not its main cause (§2) |
| The report's framing: "two instances launched ~7 min apart" | **Corrected.** They launched **1.47 s apart**; the agent's wording was "both started ~7 min ago" (§1) |

---

## 1. What happened on ticket `0ecad1a2`

All times UTC, 2026-08-03, from the Render log API. Instance ids are the Render `instance`
label suffix.

### 1.1 The reported incident — `idem_key` 8871

| Time | Instance | Event |
| --- | --- | --- |
| 12:23:58 | — | deploy `dep-d9o8gnnlk1mc739o4upg` starts |
| 12:24:31.594 | dlnvl | `board.transition` **pull** — ticket → working, worker `bb268f76` |
| 12:24:32.600 | dlnvl | `agent.delivery.recorded` idem_key **8871**, hash `5e7a35fa9ec0` — one delivery |
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

Note the dispatch side is clean: **one** `pull`, **one** `agent.delivery.recorded`, **one**
instruction hash. The orchestrator did not decide to send two instructions and nothing
auto-retried. The duplication is entirely below the delivery, in the turn machine's poll loop.

**Correction to the incident report.** The processes launched **1.47 seconds apart**, not ~7
minutes. The agent's own wording was "both started ~7 min ago" — i.e. both were ~7 minutes
*old* when it noticed them at 12:33. The distinction matters for diagnosis: 7 minutes apart
would implicate a retry or a steward poke; 1.5 seconds apart implicates a concurrent claim,
which is what the logs show. The PIDs corroborate it — 1544 and 1587 are 43 apart, seconds of
process creation, not minutes.

Both lines carry **`fresh: true`**, and that is the aggravating detail. `fresh` ⇒
`Client.StartTurn` calls `createSession` (`amika/client.go:307`). The two instances did not
merely double-send into one conversation — they minted **two independent Amika sessions** on
one sandbox, each with its own `claude` process, each editing `Integrations.tsx`,
`Dashboard.css` and the tests with no knowledge of the other. Whichever instance's `s.update`
committed last owns `agent_turns.provider_turn`; the other session is **orphaned** — no turn
row references it, `CheckTurn` will never read it, its output is never collected, and nothing
will ever stop it. That is why both processes were still alive seven minutes later.

### 1.2 Then it happened three more times on the same ticket

The completion event was itself double-processed, and each resulting instruction was
double-started:

| Time | Instance | Event |
| --- | --- | --- |
| 12:33:22.620 | pxl2k | `runtime.event.received` evt **1870**, attempts **1** |
| **12:33:23.513** | **b2xvb** | **`kiln serving`** — next deploy's instance boots |
| **12:33:24.611** | **b2xvb** | `runtime.event.received` evt **1870**, attempts **2** — 1.1 s after its own boot |
| 12:33:30 | — | deploy `dep-d9o8klgae00c73fqp3cg` marked live — 6 s *after* b2xvb claimed the event |
| 12:33:41.329 / 12:33:44.897 | pxl2k / b2xvb | `agent.turn.started` idem **8904** (`fresh:true`) — twice |
| 12:33:49.075 | pxl2k | brain pass A → `send_to_agent` → delivery **8912** ("Status check: has the watcher…") |
| 12:33:51.757 | b2xvb | brain pass B → `send_to_agent` → delivery **8910** ("The tree should be settled now…") |
| 12:33:54 / 12:33:57 | both | two different `post_update`s to the feed — the duplicated "Heads up:" messages |
| 12:34:12.453 / 12:34:14.558 | b2xvb / pxl2k | `agent.turn.started` idem **8910** — twice |
| 12:34:24.889 / 12:34:26.939 | b2xvb / pxl2k | `agent.turn.started` idem **8912** — twice |
| 12:34:30.076 | pxl2k | `agent: persist turn` err=`context canceled`, turn_id `delivery-8914` |
| 12:34:31.153 | pxl2k | `kiln shutting down` |
| 12:34:43.783 | b2xvb | `agent.turn.started` idem **8914** |

**One agent completion produced two brain passes, two different instructions, and four
`StartTurn` calls into the same sandbox inside 40 seconds.** The 2× at the queue and the 2× at
the turn machine multiply.

This is also the direct answer to the incident report's question "did the orchestrator send
two instructions in quick succession?" — it did, and it was *made* to, by the double claim.
Two independent LLM passes over one event reached two different conclusions and issued two
different instructions to one ticket.

### 1.3 The second confirmed instance — this investigation's own dispatch, `idem_key` 8914

`idem_key` 8914 is the instruction that commissioned this document, and it was
double-launched too. Verified at the OS level from inside the sandbox rather than inferred:

```
$ ps -o pid,ppid,lstart,cmd
 891    1  Mon Aug  3 12:24:17 2026  /opt/daytona/daytona
1561  891  Mon Aug  3 12:34:27 2026  /usr/bin/zsh
1565 1561  Mon Aug  3 12:34:27 2026  claude --dangerously-skip-permissions --output-format json -p Live confirmation of…
1620  891  Mon Aug  3 12:34:32 2026  /usr/bin/zsh
1624 1620  Mon Aug  3 12:34:32 2026  claude --dangerously-skip-permissions --output-format json -p Live confirmation of…

$ tr '\0' '\n' < /proc/1565/cmdline | sha256sum   → 671baf863ea7976a…
$ tr '\0' '\n' < /proc/1624/cmdline | sha256sum   → 671baf863ea7976a…   (identical argv)
$ ls -l /proc/{1565,1624}/cwd  → both /home/amika/workspace/kiln
```

Byte-identical argv, same cwd, 5 seconds apart, both parented to the Daytona agent (PID 891).

The timings reconcile exactly with the two instances. `agentSendTimeout` is 12 s
(`amika/client.go:63`) and a real coding turn is *expected* to trip it — `StartTurn` is
fire-and-forget — so `agent.turn.started` is logged ~12 s *after* the process is spawned.
`b2xvb` logged at 12:34:43 ⇒ POSTed ~12:34:31 ⇒ PID 1624. `pxl2k` POSTed ~12:34:27 ⇒ PID 1565,
then was cancelled 3 s in, which is precisely the `agent: persist turn … context canceled` on
`delivery-8914` at 12:34:30.

**So `pxl2k` spawned a coding agent whose existence was never recorded anywhere.** Its
`StartTurn` reached Amika, Amika spawned the process, and the instance died before it could
write the row. There is no turn row, no session handle, and no sweep that knows about it.
This is the worst shape the bug takes, and a leader lock alone does not prevent it (§6 item 2).

Both processes then wrote this write-up concurrently: PID 1624 produced its own copy of this
document and edited the banners in the two prior documents, while PID 1565 did the same. The
merge you are reading was done by hand afterwards. That is the failure mode, reproduced on
the investigation into it.

---

## 2. The mechanism: a ~68-second two-headed window on every deploy

`render.yaml` says `plan: starter`, and the live service reports `numInstances: 1`. That is
the **steady-state** count. Render's zero-downtime deploy boots the replacement, waits for
`healthCheckPath: /healthz`, cuts traffic over, and only then drains the old one — so
`numInstances: 1` never means *one process exists*.

Measured from `kiln serving` (new instance's first log) to the old instance's last log line:

| New instance boots | Old instance's last line | Overlap |
| --- | --- | --- |
| pxl2k 12:25:17 | dlnvl 12:26:25 | **68 s** |
| b2xvb 12:33:23 | pxl2k 12:34:31 | **68 s** |
| dlnvl 12:03:39 | bmznn 12:05:01 | ≥ 82 s |
| 6fjfm 11:53:21 | q9l6p 11:54:44 | ≥ 83 s |

Roughly: ~45 s of Render grace before `SIGTERM`, plus the 15 s the process then burns failing
`srv.Shutdown` on the SSE streams (08-03 §3), plus boot. The SSE bug lengthens the window; the
grace period creates it.

Two properties of `run()` (`backend/cmd/kiln/wiring.go:750-765`) make the window fully
*active* rather than merely overlapping:

```go
go g.runWorker(ctx, "events", g.events, log)
go g.runWorker(ctx, "outbox", g.outbox, log)
go g.runAgent(ctx, log)      // poller 2s, reconciler 60s, liveness 10s
go g.runSteward(ctx, log)
```

1. **The new instance starts every background loop at boot**, before `/healthz` passes and
   before Render routes traffic to it. `b2xvb` claimed a production event **1.1 s after boot
   and 6 s before its deploy was marked live**; `pxl2k` was orchestrating **8 s** before its
   own deploy went live.
2. **The old instance keeps all of them running until `SIGTERM`**, ~45 s after the new one is
   already working. Nothing hands off; nothing quiesces.

So for ~68 seconds per deploy there are two pollers, two reconcilers, two stewards and four
queue dispatchers on one database. At 13–16 deploys/day that is ~15–18 minutes/day of
two-headed operation — and the deploys cluster exactly when tickets are moving.

There is **no leader election, no advisory lock, and no instance fencing anywhere in the
codebase** (`grep -rn "advisory\|pg_try_advisory\|leader" --include=*.go` → nothing). Every
concurrency control Kiln has is process-local.

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

and `ListNonTerminal` (`agent/postgres/store.go:62`) is a bare
`SELECT … FROM agent_turns WHERE phase <> 'done'` — no lock, no lease, no owner column.

`stepStartTurn` (`service.go:611-636`) then does the read-decide-act in the fatal order:

```go
ref, err := provider.StartTurn(ctx, w, conversation, t.Message, fresh)  // ← side effect FIRST
…
t.Phase = PhaseTurnStarted
s.update(ctx, t)                                                        // ← state change AFTER
```

The row sits at `worker_ready` for the entire duration of the provider call — up to the full
12 s `agentSendTimeout` — and `StartTurn` is not idempotent, since `fresh` mints a brand-new
session on every call. Any other poller that lists during that window sees `worker_ready`,
calls `StartTurn` again, and launches a second agent. With `PollInterval = 2s`, a second
instance is essentially *guaranteed* to hit the window: every observed pair is 0.6–3.8 s apart.

Within one process this is safe only because `loop` runs `pollOnce` serially. That is an
accident of the loop's shape, not a designed invariant, and nothing in the code says so.
`Service.mu` guards the in-memory `workers` map only, and is irrelevant across processes.

### 3.2 The events queue's serialization is in-memory, behind a 1-second visibility timeout

The queue claim (`backend/internal/runtime/postgres/store.go:107-120`) is a correct
`FOR UPDATE SKIP LOCKED` claim — but the lock lasts only the microseconds of the `UPDATE`.
After it commits the row is still `status = 'pending'`; the only thing keeping another
dispatcher off it is the lease:

```sql
next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
```

`attempts` is the **pre-update** value, so **a fresh row's visibility timeout is 1 second**
(`power(2,0) = 1`). A brain pass takes 10–60 s (event 1870: claimed 12:33:22, still calling
tools at 12:33:57). So from ~1 s after the claim until `MarkDone`, the row is openly claimable.
The only other guard is:

```go
e, ok, err := w.store.ClaimNextDue(ctx, w.queue, busyProjects(busy))
```

where `busy` is a `map[string]struct{}` **local to one `Worker.Run` goroutine** in one process
(`runtime/worker.go:97-150`). The type comment is explicit that the events worker "*is* the
single-writer-per-project constraint realized **in-process**". Process B's busy set is empty
by construction, so B re-claims the row one second after A claimed it, and both run a full
brain pass on the same event — each emitting its own tool calls, feed posts, and instructions.

This is a latent design bug even single-instance (any pass longer than its lease is one
crash-recovery edge from double execution); two instances make it fire on essentially every
deploy. Spec 04 §4's single-writer-per-project guarantee, which everything downstream assumes,
is silently void for the duration of every deploy.

### 3.3 Why the existing idempotency did not catch it

`0008_events_idempotency.sql` closed the adjacent gap — the same completion being *inserted*
twice — with a partial unique index on `events.idempotency_key`. That is **insert-side** dedup.
It does nothing about one already-inserted row being **claimed** twice, which is this bug.
Likewise `agent_turns.Record` is `ON CONFLICT DO NOTHING` on `idempotency_key`, which makes
*recording* a delivery idempotent but says nothing about how many times the row is advanced.

### 3.4 What *did* hold

`agent.delivery.recorded`: **333 lines, 333 distinct idempotency keys, zero duplicates.** The
board outbox → delivery path is properly idempotent. The duplication is entirely downstream of
a correctly-recorded single delivery, and the `pull` transition also fired once.

That is worth stating plainly: **the board's transactional-outbox design is not at fault.**
The two places without a durable claim are the two places that duplicated.

The outbox worker is a useful control: it uses **byte-identical claim SQL** to the events
queue, yet duplicated 0 of 333 times. The difference is handler duration — outbox executors
finish well inside the 1-second lease and `MarkDone` before the window opens. The exposure is
identical; only the timing saves it. Any outbox handler that ever gets slow inherits the bug
silently.

### 3.5 Amika does not serialize either

`POST /sandboxes/{ref}/agent-send` spawns a `claude` process per call with no per-sandbox or
per-session mutual exclusion. Two sends 1.5 s apart yield two processes in one working
directory. There is no provider-side backstop, so whatever guard Kiln adds is the only one.

---

## 4. How often, and what it costs

Log window 2026-08-01T11:28 → 2026-08-03T12:34 (the `agent.turn.started` line was added
2026-08-01, so turn-level counts cover ~49 h; 35 deploys finished in the window).

**Turns started more than once** (`agent.turn.started`, grouped by `idem_key`):

| | |
| --- | --- |
| Distinct turns started | 243 |
| Started **twice** | **12 (4.9 %)** |
| …of which cross-instance | **12 (100 %)** |
| …of which same-instance | **0** |
| …with `fresh=true` (two independent sessions) | 2 (`8871`, `8904`) |

```
6268 5b7dee77  gap 3.8s  k8hsk / wt4ln
6271 5b7dee77  gap 0.7s  k8hsk / wt4ln
6273 5b7dee77  gap 0.6s  k8hsk / wt4ln
6752 7d938c99  gap 0.6s  6spnc / 992sc
8103 ccb420d3  gap 2.4s  7mtts / cqvfb
8839 2e833e40  gap 3.7s  bmznn / dlnvl
8844 2e833e40  gap 2.9s  bmznn / dlnvl
8871 0ecad1a2  gap 1.5s  dlnvl / pxl2k   ← this incident (fresh=true)
8888 2e833e40  gap 0.9s  dlnvl / pxl2k
8904 2f9bc4cf  gap 3.6s  pxl2k / b2xvb   (fresh=true)
8910 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
8912 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
```

Every one falls inside a post-deploy overlap window. Every gap is under 4 seconds — under two
poll intervals. **Zero same-instance duplicates**: the in-process goroutine race the 08-02
document made its headline has never produced a duplicate turn start in the retained window.

**Events processed more than once** (`runtime.event.received`, grouped by `event_id`):

| | |
| --- | --- |
| Distinct events | 561 |
| Re-delivered while still in flight | **31 (5.5 %)** |
| …cross-instance | **30** (≈0.9 per deploy) |
| …same-instance | 1 (evt 1624, 8.2 s apart — a genuine `MarkRetry`) |
| Processed **three** times | 2 (evts 1471, 1709) |

Most gaps are 1.0–1.9 s — the 1-second visibility timeout, exactly. Both `human.message` and
`agent.turn_completed` are affected, so a **user message can also produce two brain passes**.

This is **twice the footprint of the concurrent-sandbox race** the first two documents
prioritized (6 events), in a shorter window, with a strictly worse blast radius — that race
produced a spare sandbox; this one produces two coding agents editing one working tree.

### Consequences, by severity

**C1 — [Critical] Two coding agents in one working directory.** No file locking, no awareness
of each other. On this occasion the agent noticed mid-read and backgrounded a watcher rather
than interleaving edits — that was the agent's judgment, and there is no system guard behind
it. The default outcome is interleaved writes, a corrupted tree, and a commit that is neither
agent's intent. This is the "corrupted work" symptom the investigation ticket was opened for.

**C2 — [Critical] Orphaned sessions and orphaned processes.** Two shapes: a `fresh=true`
duplicate mints two sessions and the turn row records one, leaving the other's agent running
unsupervised with nothing to terminate it; and a duplicate whose instance dies mid-`StartTurn`
(8914, §1.3) leaves a spawned process with *no* recorded session at all — `destroyUnkept`
cannot know to spare it and `CheckTurn` cannot know to read it.

**C3 — [High] Duplicate brain passes mutate the board twice.** Two independent LLM decisions
on one event: two different `post_update` texts (the user is told the same thing twice, in
different words) and two different `send_to_agent` instructions to one ticket. Any
non-idempotent brain action — a state transition, a notification, a spend — is exposed the
same way, and each duplicate pass is a paid LLM call.

**C4 — [High] `fresh=false` duplicates corrupt the turn-output baseline.** Both sends land in
the same session, and `CheckTurn` (`amika/client.go:336-352`) resolves a turn by counting
assistant messages past a recorded baseline. Two agents replying into one transcript means the
first reply is attributed to the turn and the second shifts the count, so a *later* turn can
complete instantly against a message that answered an earlier instruction. Ten of the twelve
duplicates are this shape.

**C5 — [Medium] `MaxAttempts` erosion.** Every duplicate claim increments `attempts` against a
budget of 8. Combined with the deploy-kill path of 08-03 §3, two attempts can be consumed per
deploy by an event that never actually failed.

**C6 — [Medium] Every other loop is exposed identically.** The reconciler, the liveness
refresh and the steward all run in both instances with no lease. Two reconcilers sweeping one
project is precisely the read-decide-create interleaving 08-02 §1 described — so **deploy
overlap is also a driver of that race**, not merely a cache-invalidation amplifier. Two
stewards can double-poke a stalled ticket.

---

## 5. Corrections to the two prior documents

**08-02 §1 is demoted, not withdrawn.** Two goroutines racing `adoptOrCreateSlot` inside one
process is a real bug, reproducible, and worth fixing. But it is a *sandbox-provisioning* bug,
and it is not what put two Claude Code processes on one prompt. Of the 6 production
`worker name conflict` events, only one pair is same-instance (`mst45`, slot `4181c8d9`,
14:41:27 and 14:41:28 on 08-01) — that pair *is* the in-process race, and it is the only
sighting of it in seven days.

**08-02 rec #3 would not have helped.** "A per-slot mutex (or single-flight keyed on slot id)"
is process-local. The measured mechanism is cross-process, so the mutex is defence in depth
against the rarer bug only. Any real fix must be in Postgres.

**08-03 §5's open question is partly answered.** That document flagged `bb268f76` going
gen1 → gen2 in 10 seconds as inexplicably fast. It was two instances — verified:

```
11:35:06.699  4qzf8  agent: rotating slot to next generation past unadoptable record  bb268f76-…
11:35:16.382  q9l6p  agent: rotating slot to next generation past unadoptable record  bb268f76-…
```

Some fraction of the 72 "unadoptable" rotations are two reconcilers sweeping the same pool
against each other's half-finished work. The tombstone hypothesis is still live for the rest;
both need the P1 logging that document asked for.

**08-03 §3 stands and gains a second reason to be urgent.** The 15-second failed
`srv.Shutdown` is now not only a source of `context canceled` cascades — it is 15 of the 68
seconds during which two backends are issuing instructions.

---

## 6. Fix recommendations

Ordered by evidence, across all three documents. Items 1–3 are what this incident argues for.

### P0 — Make the background loops single-owner

1. **Gate every background loop behind a Postgres advisory lock.** In `graph.run`, take
   `pg_try_advisory_lock(<fixed key>)` on a dedicated pinned `*sql.Conn` before starting
   `runWorker(events)`, `runWorker(outbox)`, `runAgent` and `runSteward`. An instance that does
   not hold it serves HTTP/SSE only and retries every few seconds; the lock is released on
   clean shutdown and dies with the session on a hard exit, so the new instance picks it up
   within seconds of the old one's death. ~40 lines, no schema change, and it closes **both**
   §3.1 and §3.2 in one move — including the reconciler/steward duplication in C6 and §5.

   It also fixes the ordering bug in §2: today the new instance starts working before it is
   even routed traffic. Under the lock it starts working when the old one stops.

2. **Give the turn machine a durable claim** (defence in depth, and also correct
   single-instance — a leader lock does not prevent the C2 orphan). Make the phase transition a
   compare-and-swap:
   `UPDATE agent_turns SET phase='starting' WHERE idempotency_key=$1 AND phase='worker_ready' RETURNING …`,
   call `StartTurn` only if the CAS won, then move to `turn_started` and record the handle. A
   crash between CAS and `StartTurn` leaves a `starting` row for a wall-clock sweep to
   recover — which is the deadline A3.1 has always wanted anyway. Note `fresh` is currently
   derived from `t.ProviderTurn == nil`, which is stale by construction under this reordering:
   re-read the row inside the claim.

3. **Fix the queue's visibility timeout.** `least(power(2, attempts), 60)` gives a fresh row a
   **1-second** lease, shorter than any brain pass. Separate the two concepts: a `claimed_until`
   (or a floor of ~2× the longest expected pass, e.g. 180 s) for the lease, and the exponential
   value for *retry backoff after a recorded failure*. Stronger still, claim by status — add
   `status='in_flight'` set by the claim and cleared by `MarkDone`/`MarkRetry`, with a reaper
   for stuck rows. Also downgrade the comments in `runtime/worker.go` from "the
   single-writer-per-project constraint" to what `busy` is: an in-process concurrency limiter.

4. **Set and read `Turn.ProviderWorker`** (08-02 rec #2 — still not wired). Pin a turn to the
   sandbox *and* session it started on, so a duplicate is detectable after the fact.

### P1 — Shorten the window, and make it observable

5. **Close the SSE streams on shutdown** — 08-03 §3 rec #1, unchanged. Worth 15 s of the 68.
6. **Stamp an instance id into every log line** (`obs` already stamps `turn_id`; add a
   process-boot uuid). This investigation only worked because Render happens to label log lines
   by instance; the application itself cannot tell you which process did what.
7. **Alert on a duplicate `agent.turn.started` for one `idem_key`**, and on a second claim of
   an event still in flight. Both are one-line SQL invariants, and the first is the direct
   measure of the corrupted-work risk.
8. **Add the concurrency test the gate does not have** (08-02 rec #7, still open), with `-race`
   (rec #6): drive `pollOnce` from two `Service` instances over one store and assert exactly
   one `StartTurn`. Two `Service`s over one store is what production actually does.

### P2 — Provider-side backstop

9. **Ask Amika to reject or serialize a second concurrent `agent-send` on one sandbox.** Kiln
   should not be the only thing standing between a duplicate dispatch and two agents in one
   working tree. Failing that, have `setup.sh` or a wrapper refuse to start a second `claude`
   in the same `AMIKA_AGENT_CWD`.

### P3 — Reduce deploy frequency (owner's call, argument restated)

10. `autoDeploy: false` + branch protection (08-02 P4, 08-03 rec #11). The argument has shifted
    again: it is no longer "a bad build could ship" (refuted, 1 in 291) nor only "deploys kill
    in-flight work" — it is that **each deploy opens a 68-second window in which the
    orchestrator can send an agent two contradictory instructions**, at a measured ~0.9
    duplicate brain passes per deploy. With P0 fixed this reverts to a hygiene question.

### P4 — The prior P0, unchanged in substance

11. Adopt-on-conflict in `createWorkerRotating`; don't destroy a sandbox with a live turn
    (08-02 §6 #1, #4). Still correct. Drop #3 (per-slot mutex) in favour of item 1, or keep it
    only as an in-process optimisation and stop describing it as a fix.

**Sequencing note.** Item 1 alone removes every measured symptom in §4 and is by far the
cheapest. Item 2 is still required — it makes the turn machine correct rather than merely
un-raced, which matters for restarts and for the crash-mid-`StartTurn` orphan (C2) that a
leader lock does not prevent.

---

## 7. What would have been damaged without the agent's judgement

Nothing in the system stopped this. The turn that reported it was one of two writers in the
same working directory with no file locking, no worktree isolation, and no awareness of the
other; the second process had already rewritten `Integrations.tsx`, `Dashboard.css` and the
tests under the first one mid-read. It was caught because the agent noticed the tree changing
beneath it, checked `ps`, and chose to background a watcher rather than interleave edits.

That is not a control. The `end-to-end-development` skill's parallel-agent isolation rule
assumes one agent per working directory; §3.1 means the platform cannot honour it. Item 1 is
what makes the assumption true again.

The point is not hypothetical: as recorded in §1.3, the *investigation into this bug* was
itself dispatched twice, and the two processes independently wrote two versions of this
document and both edited the banners of the two prior ones. That merge was done by hand. A
code change would not have merged so gracefully.

---

## 8. Method note

Two independent evidence sources, both direct:

- **Render API**: `/v1/logs` (walked backwards via `nextEndTime`, text- and time-filtered,
  deduplicated by `(timestamp, message)`), `/v1/services/{id}/deploys` (fully paginated, 296
  records), and `/v1/services/{id}`. Instance attribution comes from each log line's `instance`
  label, which is what makes the cross-process claim checkable rather than inferred.
- **Live `ps` / `/proc` inspection** from inside worker sandbox
  `kiln-prod-worker-e206d8b5-4181c8d97a28-g3`, capturing the second incident as it happened.

Retention is 7 days (2026-07-27 → 2026-08-03), which bounds every count; `agent.turn.started`
only exists from 2026-08-01, which bounds the turn counts to ~49 hours. Counts are of log
lines, not incidents. Code claims cite file and line at `f22b040`.
