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
> 68–83 seconds**. Both run the agent poller, both run the queue dispatchers, and **nothing in
> the codebase excludes a second process** — not the agent turn machine (which has no claim at
> all) and not the events queue (whose only per-project serialization is an in-memory map).
> Measured: **12 turns started twice** (100 % cross-instance) and **31 events processed twice**
> (30 of 31 cross-instance), ≈0.9 duplicate brain passes per deploy across 35 deploys — and
> those turn counts are a **lower bound**, because a duplicate whose instance dies mid-send
> spawns a real agent and never logs the line we counted (§4.1).

> **Live capture, 13:00–13:03.** The worst state was caught *in situ*: **three `claude`
> processes in one working directory at once** (§1.5), two with byte-identical argv, plus the
> first confirmed **duplicate `send_to_agent`** — two brain passes over one event issuing two
> differently-worded instructions to one ticket. One of the three was never recorded in any
> turn row.

---

## 0. Status of each deliverable, and what changes

| Ticket item | Status |
| --- | --- |
| Pull the dispatch/instruction logs for ticket `0ecad1a2` | **Done** (§1) — full timeline recovered |
| Explain how a second process was launched on the same sandbox | **Root-caused** (§2–§3). Not an auto-retry, and not the orchestrator deciding to send two instructions: two backend *instances* independently advanced the same `agent_turns` row |
| Fold in as a confirmed real-world instance | **Four** folded in (§1): the reported one (`idem_key` 8871); a second on **this investigation's own dispatch** (`8914`); a third on ticket `2f9bc4cf` where the two agents ran 26 minutes side by side (`8904`, §1.4); and a fourth (`8968`, §1.5) captured **while this document was being written**, in which one agent-completion event produced **three** `claude` processes in one sandbox — one of them unrecorded |
| Whether a stray/retried dispatch, a duplicate `send_to_agent`, or an orphaned process is the trigger | **Settled** (§1.5) — retry **no**, duplicate `send_to_agent` **yes but as a symptom**, orphaned process **yes**. All three reduce to the one mechanism in §2 |
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

## 1. What happened — four confirmed occurrences

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

### 1.4 The third instance — ticket `2f9bc4cf`, `idem_key` 8904, 26 minutes side by side

Reported independently by the worker on `2f9bc4cf-3669-4d29-8a4c-c31d2e1854c4` ("Allow direct
text editing of ticket title/body"): **two `claude` processes, PIDs 1543 and 1618, in one
working tree**, both implementing the same feature and both editing `schema/openapi.yaml`,
`routes.go` and the generated types at the same time. §4 already listed `8904` as a
`fresh=true` double-start; this is what it looked like from inside the sandbox.

| Time | Instance | Event |
| --- | --- | --- |
| 12:32:22 | — | deploy `dep-d9o8klgae00c73fqp3cg` starts |
| 12:32:56.831 | pxl2k | `board.transition` **pull** — ready → working, worker `d8e608ec` |
| 12:32:57.822 | pxl2k | `agent.delivery.recorded` idem **8904** — one delivery |
| **12:33:23.513** | **b2xvb** | **`kiln serving`** — the replacement instance boots |
| 12:33:30 | — | deploy marked **live** |
| **12:33:41.329** | **pxl2k** | `agent.turn.started` idem 8904, `fresh:true`, hash `21b47e087650` |
| **12:33:44.897** | **b2xvb** | `agent.turn.started` idem 8904, `fresh:true`, hash `21b47e087650` |
| 13:00:10.243 | jhhjp | `agent.turn.completed` idem 8904 — **26 min 29 s** after the first start |

**One `pull`, one delivery, one instruction hash, two `StartTurn` calls 3.57 s apart from two
instances** — the same signature as §1.1, on a different ticket, in a different deploy window.
`b2xvb` issued its duplicate **21 s after its own boot** and 15 s after the deploy went live.
The reported PIDs corroborate the gap independently: 1543 and 1618 are 75 apart — seconds of
process creation, consistent with 3.57 s, not a retry minutes later.

Two things this incident adds that §1.1 did not:

- **Duration.** The two agents coexisted for **26 minutes**, not the ~9 of §1.1. Both were
  `fresh:true`, so this was two independent Amika sessions writing one tree for that whole
  span. Exactly **one** `agent.turn.completed` was logged, from whichever session won
  `agent_turns.provider_turn`; the other agent's 26 minutes of work was never collected by
  anything and its completion, if it ever arrived, had no turn row to land on.
- **The only thing that saved the tree was the agent's own judgement, again.** One process
  noticed the collision via `ps` and **isolated itself into a `git worktree`**; the other kept
  going uncommitted in the shared tree. That is now the second time an agent has hand-rolled
  the containment that the platform does not provide (cf. §7) — and the first time we can see
  what the unisolated half was doing: editing the wire schema and its generated output, the one
  place in this repo where two writers cannot be reconciled by hand.

### 1.5 The fourth — `idem_key` 8968: one event, two brain passes, three `claude` processes

The `agent.turn.completed` for 8904 above is event **1877**. Handling it is where the two
mechanisms of §3.1 and §3.2 compounded in full view, ~15 minutes after §1.4, on **this
document's own dispatch**:

| Time | Instance | Event |
| --- | --- | --- |
| 12:58:36 | — | deploy `dep-d9o90v3m8hqs73dgmt30` (`bfabe72c`) starts |
| **12:59:52.493** | **5gwbk** | **`kiln serving`** |
| 12:59:57 | — | deploy marked **live** |
| 13:00:10.243 | jhhjp | `agent.turn.completed` idem 8904 — the §1.4 collision report |
| 13:00:10.310 | jhhjp | `runtime.event.received` evt **1877**, attempts **1** |
| **13:00:11.597** | **5gwbk** | `runtime.event.received` evt **1877**, attempts **2** — **1.29 s later** |
| 13:00:32.133 | 5gwbk | brain pass A → `mark_blocked` + `post_update` ("Found a live instance of the concurrency bug…") |
| 13:00:38.852 | 5gwbk | pass A → `send_to_agent` → delivery **8968** ("New live evidence just surfaced…") |
| 13:00:41.550 | jhhjp | brain pass B → `update_ticket` blocked **again** + a *different* `post_update` ("⚠️ Duplicate-dispatch collision…") |
| 13:00:41.558 | jhhjp | pass B → `send_to_agent` → delivery **8971** ("Heads up, another live occurrence…") |
| **13:00:49** | jhhjp | **PID 2393** spawned — `StartTurn(8968)` |
| **13:00:52** | 5gwbk | **PID 2432** spawned — `StartTurn(8968)` *again* |
| 13:00:55.969 | jhhjp | `runtime.event.handled` evt 1877 (5gwbk had already handled it at 13:00:42.165) |
| 13:00:57.247 | jhhjp | `agent: persist turn` err=`context canceled`, `delivery-8968` **and** `delivery-8971` |
| 13:01:03.592 | 5gwbk | `agent.turn.started` idem **8968** — the **only** line logged for two processes |
| **13:01:10** | 5gwbk | **PID 2498** spawned — `StartTurn(8971)` |
| 13:01:12.249 | jhhjp | `kiln exited with error` — `http shutdown: context deadline exceeded`, 15 s after SIGTERM |
| 13:01:22.089 | 5gwbk | `agent.turn.started` idem **8971** |

Captured at the OS level from inside the sandbox at 13:03, all three still running:

```
$ ps -eo pid,ppid,lstart,cmd
2389   891  Mon Aug  3 13:00:49 2026  /usr/bin/zsh
2393  2389  Mon Aug  3 13:00:49 2026  claude … -p New live evidence just surfaced: ticket 2f9bc4cf…
2428   891  Mon Aug  3 13:00:52 2026  /usr/bin/zsh
2432  2428  Mon Aug  3 13:00:52 2026  claude … -p New live evidence just surfaced: ticket 2f9bc4cf…
2494   891  Mon Aug  3 13:01:10 2026  /usr/bin/zsh
2498  2494  Mon Aug  3 13:01:10 2026  claude … -p Heads up, another live occurrence just happened…

sha256(argv) 2393 = 2432 = d7d3cecb60da0216…      ← byte-identical
sha256(argv) 2498         = 9503df4427ee3f5f…
cwd: all three → /home/amika/workspace/kiln       ← one working directory
```

2393 and 2432 carry the **same instruction text** (delivery 8968); 2498 carries the *other*
brain pass's wording (delivery 8971). All three are parented to the Daytona agent (PID 891) via
their own shell, exactly as in §1.3. "Same instruction, same directory" is measured, not
inferred: the hashes are over `/proc/<pid>/cmdline` and the paths from `/proc/<pid>/cwd` (§8).

**One agent completion became three coding agents in one working directory.** The multiply is
now fully attributed, each factor to its own gap:

1. **×2 at the queue** (§3.2). `jhhjp` claimed evt 1877; `5gwbk` re-claimed it **1.29 s** later
   — the 1-second visibility timeout, to the second. `5gwbk` was 19 s past boot and 14 s past
   its deploy going live.
2. **→ two instructions.** Two independent brain passes over one event reached two different
   conclusions, blocked the ticket twice with two different `blocked_reason`s, posted two
   differently-worded updates to the user, and dispatched **two differently-worded instructions**
   (8968, 8971) to one worker.
3. **×2 again at the turn machine** (§3.1). Both instances then polled `agent_turns`, both saw
   delivery 8968 at `worker_ready`, and both called `StartTurn` — 3 s apart, PIDs 2393 and 2432,
   byte-identical prompts.

#### The three hypotheses in the report, settled

This settles the question the duplicated instruction itself posed — *"a stray/retried dispatch,
a duplicate `send_to_agent`, or an orphaned process?"*

| Hypothesis | Verdict |
| --- | --- |
| A stray or **retried** dispatch | **No.** `attempts` goes 1 → 2 because the *second claim* increments it, not because the first failed. `jhhjp` recorded no failure — it was still mid-pass, 1.29 s in, well inside the 1-second visibility timeout of §3.2 |
| A duplicate **`send_to_agent`** | **Yes — confirmed for the first time, but as a symptom, not the trigger.** Each brain pass called `send_to_agent` exactly once and correctly. There were simply *two passes over one event*, reaching two different conclusions and wording (§3.2, C3) |
| An **orphaned process** from a prior turn | **Yes — PID 2393**, though orphaned *at birth* rather than left over: spawned by an instance that died before recording it, so no turn row references it, `CheckTurn` will never read it, and nothing will ever stop it (C2) |

It is **all three at once**, and all three collapse into the single mechanism of §2. Crucially,
**the board's dispatch remains correct throughout**: the report's own observation that the board
issues one instruction per ticket per worker is exactly right — §3.4's 333/333 clean deliveries
still holds, and both §1.4 and §1.1 show one `pull` and one `agent.delivery.recorded`. The
defect is never that the board sends twice; it is that **there are two of everything above the
board**, and below the delivery there is no claim to stop either one.

**The orphan, made visible.** `PID 2393` is `jhhjp`'s. It POSTed `agent-send` at 13:00:49,
Amika spawned the process, and 8.2 s into the 12 s `agentSendTimeout` the instance's context
was cancelled — the `persist turn … context canceled` on `delivery-8968` at 13:00:57. The row
was never updated and **`agent.turn.started` was never logged**. There is no turn row, no
session handle, and nothing that can stop it. This is C2's worst shape (§1.3) reproduced, and
this time we can watch it: a `claude` process editing this repository that the orchestrator
does not know exists. `jhhjp` was cancelled mid-`StartTurn` on **8971** too, and no fourth
process appeared in `ps` — so whether an orphan survives the cancellation is decided by where
in the ~12-second send window the SIGTERM lands.

`jhhjp`'s own death is 08-03 §3 on schedule: SIGTERM at ~13:00:57, `http shutdown: context
deadline exceeded` at 13:01:12 — **15.0 seconds** of failed `srv.Shutdown` on the SSE streams,
all of it spent as the second head.

---

## 2. The mechanism: a 68–83-second two-headed window on every deploy

`render.yaml` says `plan: starter`, and the live service reports `numInstances: 1`. That is
the **steady-state** count. Render's zero-downtime deploy boots the replacement, waits for
`healthCheckPath: /healthz`, cuts traffic over, and only then drains the old one — so
`numInstances: 1` never means *one process exists*.

Measured from `kiln serving` (new instance's first log) to the old instance's last log line:

| New instance boots | Old instance's last line | Overlap |
| --- | --- | --- |
| pxl2k 12:25:17 | dlnvl 12:26:25 | **68 s** |
| b2xvb 12:33:23 | pxl2k 12:34:31 | **68 s** |
| jhhjp 12:46:38 | b2xvb 12:48:00 | **82 s** |
| 5gwbk 12:59:52 | jhhjp 13:01:12 | **80 s** |
| dlnvl 12:03:39 | bmznn 12:05:01 | ≥ 82 s |
| 6fjfm 11:53:21 | q9l6p 11:54:44 | ≥ 83 s |

Six consecutive deploys, every one of them two-headed for 68–83 seconds. The last two rows of
§1.4 and §1.5 sit inside rows 2 and 4.

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

So for 68–83 seconds per deploy there are two pollers, two reconcilers, two stewards and four
queue dispatchers on one database. At 13–16 deploys/day that is ~16–20 minutes/day of
two-headed operation — and the deploys cluster exactly when tickets are moving. The four
incidents in §1 land in four different deploy windows, three of them consecutive.

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
8904 2f9bc4cf  gap 3.6s  pxl2k / b2xvb   ← §1.4 (fresh=true, 26 min side by side)
8910 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
8912 0ecad1a2  gap 2.1s  b2xvb / pxl2k   ← this incident
```

Every one falls inside a post-deploy overlap window. Every gap is under 4 seconds — under two
poll intervals. **Zero same-instance duplicates**: the in-process goroutine race the 08-02
document made its headline has never produced a duplicate turn start in the retained window.

### 4.1 The turn counts are a lower bound, and blind in the worst direction

`agent.turn.started` is logged **after** `StartTurn` returns. An instance that is cancelled
inside the ~12 s `agentSendTimeout` has already caused Amika to spawn a `claude` process, but
logs nothing. So the detector systematically misses precisely the duplicates that leave an
**orphan** — the C2 shape, the one with no turn row to clean up.

Both duplicates observed directly at the OS level are of this invisible kind:

| `idem_key` | Processes seen in `ps` | `agent.turn.started` lines | Missing line's instance |
| --- | --- | --- | --- |
| 8914 (§1.3) | 2 (PIDs 1565, 1624) | 1 | pxl2k — `persist turn` canceled |
| 8968 (§1.5) | 2 (PIDs 2393, 2432) | **1** | jhhjp — `persist turn` canceled |

Neither appears in the table of 12. **Every duplicate we have caught by looking rather than by
grepping was one the log-based count could not see**, so treat "12 in 49 h" as a floor. The
true rate is bounded above by the events figure (a duplicate brain pass is logged on both
sides), and `agent: persist turn … context canceled` is the usable proxy for the rest — which
is an argument for rec #7 keying its alert on that line as well as on the duplicate. Rec #6
(instance id) is what would let the application itself attribute the two sides, and rec #4
(`Turn.ProviderWorker`) is what would make an orphan detectable at all.

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

**C1 — [Critical] Two (now three) coding agents in one working directory.** No file locking, no
awareness of each other. Now **four** confirmed occurrences (§1.1, §1.3–§1.5), and in each one
the only thing that prevented interleaved writes was the agent noticing: on `0ecad1a2` it
backgrounded a watcher, on `2f9bc4cf` it moved into a `git worktree` — but **only one of that
pair noticed**, and the other kept editing the shared tree unisolated; §1.5 then put **three**
agents in one directory, two on a byte-identical instruction. Those are judgement calls, not
guards, and they degrade as the count rises: the worktree escape protects only the agent that
takes it, and the unisolated process still owns the shared tree. The default outcome is
interleaved writes, a corrupted tree, and a commit that is neither agent's intent. This is the
"corrupted work" symptom the investigation ticket was opened for.

`2f9bc4cf` sharpens it: the unisolated half spent 26 minutes editing `schema/openapi.yaml` and
its generated Go/TS output. The wire-schema regen rule assumes a single writer regenerating
from a single source edit; two concurrent regenerations produce a schema and generated types
that no reviewer can reconcile after the fact, because the generated files carry no record of
which edit they came from.

**C2 — [Critical] Orphaned sessions and orphaned processes.** Two shapes: a `fresh=true`
duplicate mints two sessions and the turn row records one, leaving the other's agent running
unsupervised with nothing to terminate it (8871, and 8904 for 26 minutes); and a duplicate
whose instance dies mid-`StartTurn` (8914 §1.3, 8968 §1.5) leaves a spawned process with *no*
recorded session at all — `destroyUnkept` cannot know to spare it and `CheckTurn` cannot know
to read it. The second shape is both the more damaging and the one §4.1 shows we cannot count.

**C3 — [High] Duplicate brain passes mutate the board twice.** Two independent LLM decisions
on one event: two different `post_update` texts (the user is told the same thing twice, in
different words) and two different `send_to_agent` instructions to one ticket. Any
non-idempotent brain action — a state transition, a notification, a spend — is exposed the
same way, and each duplicate pass is a paid LLM call.

§1.5 shows the state-transition exposure is not hypothetical: both passes over evt 1877 moved
`2f9bc4cf` working → blocked, writing **two different `blocked_reason`s** over each other, and
each pass emitted its own `post_update` — so the user was told about the collision twice, in
two different voices, within 9 seconds. A Blocked transition is also a push-notification
trigger (spec 02 §10), which makes the duplicate user-visible off-app as well. A duplicate
`send_to_agent` is therefore not a separate bug to hunt — it is this one, one level up.

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

5. **Close the SSE streams on shutdown** — 08-03 §3 rec #1, unchanged. Worth 15 s off every
   window; measured again at exactly 15.0 s on `jhhjp` in §1.5.
6. **Stamp an instance id into every log line** (`obs` already stamps `turn_id`; add a
   process-boot uuid). This investigation only worked because Render happens to label log lines
   by instance; the application itself cannot tell you which process did what.
7. **Alert on a second claim of an event still in flight — build this half first.** Both this
   and the duplicate-turn alert below are one-line SQL invariants, but they are not equally
   good detectors and the obvious one is the weaker one.

   - **Primary: the second claim.** evt 1877's `attempts 1 → 2` at 1.29 s (§1.5) is the signal
     that produced 8968 and 8971 in the first place. It fires **one level up**, at the queue,
     *before* the fan-out into two brain passes, two board mutations, two instructions and
     three processes — so it is both the earliest warning available and the only one that
     catches the whole chain rather than one branch of it.
   - **Secondary: a duplicate `agent.turn.started` for one `idem_key`.** Worth having as the
     direct measure of the corrupted-work risk, but it must not be the primary detector: it
     would have **missed both duplicates this investigation caught by looking rather than by
     grepping** — 8914 (§1.3) and 8968 (§1.5). The orphan shape spawns a second real agent and
     logs nothing, so this alert measures the floor, not the rate (§4.1).
   - **Third: `agent: persist turn … context canceled`.** Per §4.1 that line, not the
     duplicate, is the only trace an *orphaned* agent leaves, and the orphan is the shape that
     no Kiln-side fix can clean up after the fact.
8. **Add the concurrency test the gate does not have** (08-02 rec #7, still open), with `-race`
   (rec #6): drive `pollOnce` from two `Service` instances over one store and assert exactly
   one `StartTurn`. Two `Service`s over one store is what production actually does.

### P2 — Provider-side backstop

9. **Ask Amika to reject or serialize a second concurrent `agent-send` on one sandbox.** Kiln
   should not be the only thing standing between a duplicate dispatch and two agents in one
   working tree. Failing that, have `setup.sh` or a wrapper refuse to start a second `claude`
   in the same `AMIKA_AGENT_CWD`.

   This is the only recommendation that also covers the **orphan** (§1.5): PID 2393 was spawned
   by an instance that then died without recording it, so no Kiln-side lock, lease or CAS can
   reach it. Only something at the sandbox boundary can.

10. **Give each turn its own `git worktree`, in the wrapper.** Agents have now hand-rolled
    exactly this containment twice under collision (§1.4, §7) because it is the correct answer.
    Making it the default — the wrapper creates a worktree per turn and the sandbox's shared
    checkout is never written directly — converts C1 from *tree corruption* into *two branches,
    one of which is discarded*. It is strictly weaker than item 1 (it does not stop the
    duplicate agent, the duplicate spend, or the duplicate brain pass) but it is the only
    measure here that degrades gracefully when everything else has already failed, including
    against the orphan.

### P3 — Reduce deploy frequency (owner's call, argument restated)

11. `autoDeploy: false` + branch protection (08-02 P4, 08-03 rec #11). The argument has shifted
    again: it is no longer "a bad build could ship" (refuted, 1 in 291) nor only "deploys kill
    in-flight work" — it is that **each deploy opens a 68–83-second window in which the
    orchestrator can send an agent two contradictory instructions**, at a measured ~0.9
    duplicate brain passes per deploy. With P0 fixed this reverts to a hygiene question.

### P4 — The prior P0, unchanged in substance

12. Adopt-on-conflict in `createWorkerRotating`; don't destroy a sandbox with a live turn
    (08-02 §6 #1, #4). Still correct. Drop #3 (per-slot mutex) in favour of item 1, or keep it
    only as an in-process optimisation and stop describing it as a fix.

**Sequencing note.** Item 1 alone removes every measured symptom in §4 and is by far the
cheapest. All four occurrences in §1 sit inside a deploy overlap window, so all four would have
been prevented by it. Item 2 is still required — it makes the turn machine correct rather than
merely un-raced, which matters for restarts and for the crash-mid-`StartTurn` orphan (C2) that
a leader lock does not prevent.

§1.5 sharpens the ordering of items 1–3. The chain there was **queue duplication first**
(evt 1877 claimed twice, §3.2), *then* turn duplication (8968 started twice, §3.1) — one
duplicated claim fanned out into two brain passes, two board mutations, two instructions and
three processes. Item 3 (the 1-second lease) is therefore not a lesser sibling of item 2: it is
where the multiplication starts, and it is a real bug **even after** item 1 lands, since a
single instance whose pass outruns its own lease is one crash-recovery away from the same
fan-out. This is the same ordering argument that makes the second-claim alert (item 7) the half
to build first.

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

On `2f9bc4cf` (§1.4) the agent reached for the stronger form of the same improvisation — it
moved into a `git worktree` — and that is the only reason there is a coherent tree to inspect.
Its twin ran uncommitted in the shared checkout for 26 minutes. Two agents, two different
improvised containment strategies, zero platform involvement in either.

The point is not hypothetical, and it is not even historical. As recorded in §1.3, the
*investigation into this bug* was dispatched twice, and the two processes independently wrote
two versions of this document and both edited the banners of the two prior ones; that merge was
done by hand. Then it happened again while this revision was being written: §1.5's evt 1877
put **three** `claude` processes into this sandbox — two on a byte-identical prompt, one of them
an unrecorded orphan — and all three were alive, in this working directory, at the moment §1.5
was typed. Two of them (PIDs 2393 and 2432) wrote this section concurrently in the shared tree;
the third (2498) wrote its own copy from a detached `git worktree` (`/tmp/kiln-wt-2498`, branch
`investigate/dup-2f9bc4cf`), exactly the escape §1.4's agent took. **The version you are reading
is the hand-merge of those outputs**, done afterwards by a fourth process once the first three
had exited.

That is worth stating plainly, because it is the whole argument for P0 item 1: **the isolation
rule in the `end-to-end-development` skill is currently enforced by whichever agent happens to
run `ps` first.** Four occurrences in, the failure mode is no longer "an agent might not
notice" — in §1.4 one of the two demonstrably did not.

A documentation merge is the most forgiving possible expression of this bug, and we have now
had to do it twice. The next one will be a code change.

---

## 8. Method note

Two independent evidence sources, both direct:

- **Render API**: `/v1/logs` (walked backwards via `nextEndTime`, text- and time-filtered,
  deduplicated by `(timestamp, message)`), `/v1/services/{id}/deploys` (fully paginated, 296
  records), and `/v1/services/{id}`. Instance attribution comes from each log line's `instance`
  label, which is what makes the cross-process claim checkable rather than inferred.
- **Live `ps` / `/proc` inspection** from inside worker sandbox
  `kiln-prod-worker-e206d8b5-4181c8d97a28-g3`, capturing the second (§1.3) and fourth (§1.5)
  incidents as they happened. The argv comparisons are `sha256` over `/proc/<pid>/cmdline` with
  `NUL` translated to newline, and the working directories from `/proc/<pid>/cwd`, so "same
  instruction, same directory" is measured rather than inferred. Own-PID identification is from
  `/proc/<pid>/stat` ancestry (e.g. 2498 → 2494 `zsh` → 891 `daytona`).

**What is observed vs. reported.** §1.1, §1.3 and §1.5 are corroborated on both sides — an
OS-level observation of the processes *and* a log-level attribution to two named instances.
§1.4's PIDs 1543/1618 and the worktree-isolation detail are **reported**, from the agent on
ticket `2f9bc4cf`, not independently observed here — that sandbox is a different box; its log
half (one pull, one delivery, two cross-instance `fresh:true` starts 3.57 s apart) is directly
verified and is what makes the report consistent, but its spawn times are inferred back through
the 12 s `agentSendTimeout` rather than measured. §1.2's three further occurrences are
log-only. §1.5 is the strongest of the four because the two sides interlock — the `ps`
timestamps (13:00:49, 13:00:52, 13:01:10) predict each `agent.turn.started` to within 0.5 s of
the 12 s `agentSendTimeout`, and the one prediction that has no matching log line is the one
whose instance logged `persist turn … context canceled`.

Retention is 7 days (2026-07-27 → 2026-08-03), which bounds every count; `agent.turn.started`
only exists from 2026-08-01, which bounds the turn counts to ~49 hours. Counts are of log
lines, not incidents. Code claims cite file and line at `f22b040`.
