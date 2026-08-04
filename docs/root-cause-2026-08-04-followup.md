# Root cause, part 4 — the investigation resumed: fresh logs, and the answer to "why is there no lock"

**Date:** 2026-08-04 · **Service:** `srv-d953nmcvikkc73d8aq60`
**Log window:** 2026-08-03T12:34Z → 2026-08-04T04:00Z (~15.4 h, 13 deploys) — i.e. everything
*after* the window part 3 covered.
**Code cited at:** `028bced` (`origin/main`, the commit running in production).

Fourth in the series, after
[`root-cause-2026-08-02-concurrent-sandbox.md`](root-cause-2026-08-02-concurrent-sandbox.md),
[`root-cause-2026-08-03-render-logs.md`](root-cause-2026-08-03-render-logs.md) and
[`root-cause-2026-08-03-duplicate-instances.md`](root-cause-2026-08-03-duplicate-instances.md).

> **The headline.** Part 3's mechanism is confirmed on a fresh, non-overlapping log window and
> **nothing has been fixed** — no advisory lock, no turn claim, no queue lease change, no SSE
> shutdown, on `origin/main` or in production. **7 of 47 turns (14.9 %) were started twice**,
> against 4.9 % in part 3's window, and **3 of the 7 were `fresh:true`** — two independent Claude
> Code sessions in one working tree — against 2 of 12 before. All 7 are cross-instance, all 7
> inside a deploy overlap window, zero same-instance. (Per *turn* that is a 3× rise, but duplicates
> are caused by deploys, not by turns: normalized per deploy it is **1.6×** and the honest reading
> is "not falling" — §1.2.)
>
> **The new finding** is that the race window is **wider than part 3 measured**, and for a reason
> part 3 did not identify: `pollOnce` decides from a snapshot taken at the top of the pass, so the
> cross-process exposure window is the duration of a whole poll pass, not the 2 s poll interval or
> the 12 s send timeout. Two duplicates in this window are 12.4 s and 17.9 s apart, which the
> old model cannot produce (§2).
>
> **And the answer to the ticket's question "why is there no lock":** it was a deliberate,
> documented decision. Spec 04 §4 states the events worker *is* the single-writer constraint
> "realized in-process", justified by spec 10's "single instance of each service". That premise is
> false for 68–83 s of every deploy (§3).

---

## 0. Status of each ticket deliverable

| Ticket item | Status |
| --- | --- |
| (1) Pull/review recent render/build logs for failure patterns | **Done** (§1). Fresh window, no overlap with prior pulls. 0 build failures; the failure pattern is runtime, not build |
| (2) Confirm whether concurrent-instruction races are the recurring root cause, or whether there are distinct modes | **Confirmed as the recurring cause** (§1–§2), and **six** distinct other modes catalogued (§4), three of them new since part 3 |
| (3) Is there a lock/queue around instructions sent to a working ticket's sandbox — and if not, why not | **Answered: there is none, at any of the four layers, and the omission is deliberate and documented** (§3). This is the item the prior three documents left half-answered |
| (4) Root cause + fix recommendation, flagging data-loss risk | §5 (data-loss register, incl. one **new** risk introduced 2026-08-04) and §6 (recommendations + proposed follow-up tickets) |

**What this document changes** in the prior three: three refinements and one correction, all in §7.
Nothing in part 3's core mechanism is withdrawn.

---

## 1. The fresh log window

Pulled from Render `/v1/logs` (walked backwards via `nextEndTime`, deduplicated by
`(timestamp, message)`), instance attribution from each line's `instance` label. Method as
part 3 §8.

### 1.1 Nothing has been fixed

Checked against `origin/main` at `028bced`, not against the local checkout:

| Fix recommended in parts 1–3 | State |
| --- | --- |
| Advisory lock / leader election (part 3 rec #1) | **Absent** — `grep -rn "advisory\|pg_try_advisory\|leader" --include="*.go" backend/` → no matches |
| Turn-machine CAS (part 3 rec #2) | **Absent** — `stepStartTurn` unchanged at `service.go:611-636` |
| Queue visibility timeout (part 3 rec #3) | **Absent** — `least(power(2, attempts)::bigint, 60)` still at `runtime/postgres/store.go:110` and `:127`, and the comment at `:83-85` that part 3 disproved is still there |
| Set/read `Turn.ProviderWorker` (parts 1–3) | **Absent** — no reference in `internal/agent/*.go` outside test doubles |
| Close SSE streams on shutdown (part 2 rec #1) | **Absent** — no `RegisterOnShutdown` or `BaseContext` anywhere in `backend/` |

The only relevant change to land is `57d8a1b` **feat(board): manual kill + reassign for a
ticket's sandbox** (deployed 2026-08-04 02:28). That is a *remediation surface*, not a fix — and
it introduces a new data-loss path (§5, DL3) as well as being the first control that can reach an
orphaned process (§5, DL2).

### 1.2 Duplicate turn starts — 14.9 %, up from 4.9 %

`agent.turn.started`, grouped by `idem_key`. **47 distinct turns, 7 started twice.**

| `idem_key` | ticket | gap | instances | `fresh` | instruction hash |
| --- | --- | --- | --- | --- | --- |
| 8910 | `0ecad1a2` | 2.11 s | b2xvb / pxl2k | false | identical |
| 8912 | `0ecad1a2` | 2.05 s | b2xvb / pxl2k | false | identical |
| **8928** | `61afd422` | **12.35 s** | b2xvb / jhhjp | **true** | identical |
| 9123 | `0d874a22` | 3.54 s | vrg5k / bz77b | false | identical |
| **9424** | `01ad248d` | **9.97 s** | bz77b / btrds | **true** | identical |
| **9566** | `f543cc2d` | **17.93 s** | 5lbjb / dgbms | **true** | identical |
| 9646 | `b752cc54` | 4.67 s | x5czn / bgfk7 | false | identical |

8910 and 8912 are part 3 §1.2's, re-derived here as a control on the method. **The other five are
new**, on five different tickets, in four different deploy windows.

- **7 of 7 cross-instance. 0 same-instance.** The in-process goroutine race of part 1 has now
  produced **zero** duplicate turn starts across two independent windows totalling ~65 h.
- **3 of 7 `fresh:true`** (8928, 9424, 9566) — each mints a *second independent Amika session*,
  so it is two `claude` processes with two separate conversations editing one working tree. Part 3
  saw this shape twice in 49 h; this window has it three times in 15 h.
- **All 7 carry the identical instruction hash** across both starts — one instruction, sent twice.
- **7 of 7 fall inside a deploy overlap window** (§1.4).

Per part 3 §4.1 this is a **lower bound**: `agent.turn.started` is logged only after `StartTurn`
returns, so a duplicate whose instance is killed inside the 12 s send window spawns a real agent
and logs nothing. `agent: persist turn … context canceled` — the proxy for that shape — appears
**39 times** in this window.

#### Normalizing the rate: "not falling" is the defensible claim, not "tripled"

The 4.9 % → 14.9 % rise is mostly **denominator**. This window is shorter and much quieter (47
turns vs 243) while the deploy rate held. Since a duplicate is caused by a deploy overlap and not
by a turn, per-deploy is the meaningful normalization:

| | Part 3 window | This window | Ratio |
| --- | --- | --- | --- |
| Duplicates per **turn** | 4.9 % | 14.9 % | **3.02×** |
| Duplicates per **deploy** | 0.34 | 0.54 | **1.57×** |
| Duplicates per **hour** | 0.24 | 0.45 | **1.86×** |

So the rate is flat-to-modestly-up, and the per-turn figure is inflated by a quiet window. What
genuinely worsened is the **severity mix**: `fresh:true` went from 2 of 12 (17 %) to 3 of 7 (43 %),
and that is the shape that mints a second independent Amika session. The claim this document makes
is **"the rate is not falling and the severe shape is more common"** — not that it tripled.

### 1.2a The fifth confirmed occurrence: this investigation's own dispatch

`idem_key` 9646 in the table above is **this document's dispatch**, and it is the only duplicate in
either window corroborated on both sides simultaneously — which makes it the series' strongest
single piece of evidence.

**OS level**, from inside sandbox `…-4181c8d97a28`:

```
1450   891  Tue Aug  4 03:16:05 2026  /usr/bin/zsh
1454  1450  Tue Aug  4 03:16:06 2026  claude … -p Resume the investigation on this ticket: "Investigate: recurring orchestration/render failures…
1510   891  Tue Aug  4 03:16:10 2026  /usr/bin/zsh
1514  1510  Tue Aug  4 03:16:10 2026  claude … -p Resume the investigation on this ticket: "Investigate: recurring orchestration/render failures…

sha256(argv) 1454 = 1514 = b1a06be7bacca586d3a44cb377a860cf5c24304dcc2f92b2a3c6174a8972a529
cwd:         1454 = 1514 = /home/amika/workspace/kiln
```

**Log level:** `03:16:17.401 x5czn` and `03:16:22.074 bgfk7`, same `idem_key` 9646, same
`instruction_hash sha256:2581a9ee8565`, same `turn_id delivery-9646`.

**The two sides interlock to within 0.6 s.** `StartTurn` is fire-and-forget and logs ~12 s after
the process is spawned (`agentSendTimeout`, `amika/client.go:63`): PID 1454 spawned 03:16:06 ⇒
predicts ~03:16:18, observed 03:16:17.40; PID 1514 spawned 03:16:10 ⇒ predicts ~03:16:22, observed
03:16:22.07. The process gap (4.58 s) and the log gap (4.67 s) agree. The deploy correlation holds
too: `dep-d9oli5nqj5pc738qu960` went live 03:15:47, `bgfk7` booted 03:15:40, `x5czn`'s last line
was 03:16:47 — a 66 s two-headed window containing both calls.

`fresh:false` here, so both sends land in the **same** Amika session: two agents replying into one
transcript (part 3's C4 baseline corruption) *and* two processes in one working tree.

**This is the third time an investigation into this bug has been corrupted by the bug** (part 3
§1.3, §1.5, and now this). Both processes wrote a full version of this document; PID 1514 wrote
from an isolated `git worktree` and PID 1454 wrote in the shared checkout, and **the document you
are reading is the hand-merge of the two**, done after both exited — the same procedure as
`aa61161` and `80e540d`. Provenance in §9.

### 1.3 Duplicate event claims — 6.5 %

`runtime.event.received`, grouped by `event_id`. **169 distinct events, 11 claimed twice, 11 of 11
cross-instance** (evts 1877, 1883, 1885, 1908, 1912, 1913, 2012, 2014, 2015, 2023, 2024). Both
`human.message` and `agent.turn_completed` are affected, so **a user message still produces two
brain passes**.

#### What a double claim actually costs: two contradictory orders to one agent

Part 3 established that a doubly-claimed event becomes two independent brain passes issuing two
differently-worded instructions. This window caught the sharpest form of it — **and on this very
ticket** (`b752cc54`), at 13:08:31, as instances `hgkv6` and `j4sm4` overlapped:

```
13:08:31.042  hgkv6  brain.tool send_to_agent  → "Proceed as you suggested: let PIDs 2393/2432 finish, then …"
13:08:31.389  j4sm4  brain.tool send_to_agent  → "Do not attempt the manual merge yet while 2393/2432 are s…"
13:08:31.515  j4sm4  agent.delivery.recorded  idem_key 9022
13:08:32.099  j4sm4  agent.delivery.recorded  idem_key 9026
13:09:16.534  j4sm4  agent.turn.started       idem_key 9022
13:09:29.019  j4sm4  agent.turn.started       idem_key 9026
```

The two instructions **contradict each other**: one says proceed, the other says wait. Both were
delivered to one worker 13 s apart. This is not the abstract "non-idempotent brain actions are
exposed" risk — it is two opposite orders reaching one agent, from one event, because the event was
claimed twice.

Note also what is *correct* here: both deliveries were recorded exactly once each, by whichever
instance's outbox worker picked them up. The board's transactional outbox remains sound (part 3
§3.4); the defect is entirely that there were two decisions to deliver.

Gaps run **1.0 s – 19.9 s**. Part 3 characterised these as "1.0–1.9 s — the 1-second visibility
timeout, exactly"; that is the *lower edge*, not the distribution. The row becomes claimable 1 s
after the first claim and is then taken whenever the other dispatcher is next free of that
project — so the gap measures the second dispatcher's availability, not the lease. The lease is
what makes it *possible*; it does not set the timing. This does not change the diagnosis, but it
does mean "gap ≈ 1 s" is the wrong signature to alert on (§6, rec 7).

### 1.4 Six more deploy overlap windows, 80–83 s

New instance's `kiln serving` → old instance's last line:

| New instance boots | Old instance's last line | Overlap |
| --- | --- | --- |
| jhhjp 12:46:38 | b2xvb 12:48:00 | **82 s** |
| 5gwbk 12:59:52 | jhhjp 13:01:12 | **80 s** |
| vrg5k 13:34:13 | j4sm4 13:35:35 | **82 s** |
| bz77b 13:37:09 | vrg5k 13:38:30 | **81 s** |
| 5lbjb 02:23:52 | x5hkr 02:25:13 | **81 s** |
| dgbms 02:29:54 | 5lbjb 02:31:17 | **83 s** |

Consistent with part 3's 68–83 s across six *different* deploys. 13 deploys in 15.4 h ⇒ ~18 min/day
two-headed, unchanged.

Worth noting where 9566 landed: the deploy that opened its window (`dep-d9oksnjbc2fs739jdq8g`,
02:28:46) is the one that **shipped `57d8a1b`, the manual kill/reassign feature** — i.e. deploying
the mitigation for this bug caused another instance of it.

### 1.5 Build failures: still not the story

**0 build failures** in this window; 13 of 13 deploys reached `live`. The single `build_failed` in
the service's whole retained history remains `dep-d95pdie1355s73ajc4f0` (2026-07-06). Part 2's
refutation of F3 stands and strengthens: **the "render failures" in the ticket title are runtime
failures, not build failures.** F1 (the gate never builds the production image) remains true and
remains unexercised.

---

## 2. New: the race window is the whole poll pass, not the poll interval

Part 3 §3.1 modelled the duplicate as both instances entering `StartTurn` inside one shared
`worker_ready` window, and observed gaps of 0.6–3.8 s consistent with that. **Three of this
window's duplicates do not fit that model.** Take 9566:

```
02:30:05.420  5lbjb  agent.turn.started  idem 9566   fresh=true
02:30:23.346  dgbms  agent.turn.started  idem 9566   fresh=true      gap 17.93 s
```

`agent.turn.started` is logged *after* `StartTurn` returns (`service.go:630`), and `s.update`
moving the row to `turn_started` follows immediately (`service.go:633-635`). So `5lbjb` had already
written `turn_started` at ~02:30:05. `agentSendTimeout` is 12 s (`amika/client.go:63`), so
`dgbms`'s POST began at ~02:30:11 — **six seconds after the row stopped being `worker_ready`.**
Both instances being simultaneously inside one send window cannot produce this.

The explanation is in the read-decide-act shape, one level up from where part 3 looked:

```go
// service.go:505-514
func (s *Service) pollOnce(ctx context.Context) {
    rows, err := s.store.ListNonTerminal(ctx)   // ← one snapshot for the whole pass
    ...
    for _, t := range rows {
        s.advance(ctx, t)                       // ← acts on the snapshot, serially
    }
}
```

`advance` → `advanceSend` → `stepStartTurn` switches on **`t.Phase` from the snapshot**, and
`stepStartTurn` (`service.go:611-636`) **never re-reads the row** before calling `StartTurn`. Each
row it touches can block for a provider round trip in `ensureWorker` *and* up to the full 12 s in
`StartTurn`. So an instance that listed 12 rows and reached row 9 after 18 s decides row 9's fate
from an 18-second-old read.

**Two consequences beyond what part 3 recorded:**

**(a) The cross-process exposure window scales with board activity.** It is O(rows × provider
latency) per pass, not the 2 s `PollInterval`. It is therefore widest exactly when the board is
busiest — the opposite of the intuition that a tighter poll narrows the race. Within a single
process this is harmless (`loop` runs `pollOnce` serially and the next pass re-lists), which is why
it has never produced a same-instance duplicate; across processes it is the whole exposure.

**(b) `s.update` is an unconditional last-writer-wins clobber.** `s.update(ctx, t)` writes the
whole snapshot row back, so the second instance overwrites `provider_turn` with *its* session ref
on top of the ref the first instance wrote seconds earlier. The first session is not merely
"unreferenced" — its handle is actively destroyed. That is the precise mechanism by which a
`fresh:true` duplicate orphans a live `claude` process (part 3, C2), and it is why no after-the-fact
sweep can recover it: the only handle that could have stopped it has been overwritten in place.

**Why this matters for the fix.** It sharpens the ticket draft's existing warning that a CAS must
"re-read the row inside the claim" from a footnote into the load-bearing requirement. A CAS on
`phase` that still writes back the stale snapshot's other fields reintroduces (b). The claim must
re-read, and the update must be narrow.

---

## 3. Item (3), answered: no lock exists, at any layer — and the omission is documented

The prior documents established there is no lock. The ticket also asks **why not**, and that has a
specific, sourced answer: it was a deliberate design decision, recorded in the specs, resting on a
premise about the deployment that is false.

### 3.1 The four layers, and what each has

| Layer | Guard that exists | Excludes a second process? |
| --- | --- | --- |
| Board (ticket → worker binding) | `SELECT … FOR UPDATE` on the ticket row, short transactions, `03` §6 | **Yes** — and it held: part 3 §3.4 measured 333/333 clean deliveries |
| Events queue (event → brain pass) | `FOR UPDATE SKIP LOCKED` claim + an in-memory `busy` set + a 1 s visibility timeout | **No** — the row lock lasts microseconds; `busy` is process-local |
| Agent turn machine (delivery → `StartTurn`) | `agent_turns.idempotency_key` dedupes *recording* a delivery (`ON CONFLICT DO NOTHING`) | **No** — nothing guards *advancing* an already-recorded row |
| Provider (Amika `agent-send`) | none | **No** — part 3 §3.5; two sends 1.5 s apart yield two processes |

The pattern is exact and worth stating plainly: **the one layer that does not depend on
single-instance operation is the one layer that did not duplicate.**

### 3.2 Why: the design chose in-process serialization, on the record

Spec 04 §4, "Ordering & the single writer":

> **Events are processed strictly serially, in `id` order, by one worker goroutine.** This *is* the
> single-writer-per-project constraint `02` §7 asks for, **realized in-process**.

and its decision table, D3:

> Strictly serial event processing in `id` order — the single writer realized in-process.
> *Rejected:* Concurrent brain passes; per-ticket lanes. *Because:* One project, one user:
> concurrency buys nothing and creates prompt-state races (two passes reading the same board).

Spec 05's agent runtime is built on the matching assumption. It notes Amika gives us nothing
(`05:204`, "**No idempotency keys.** No request dedupe anywhere — hence the §7 table as our own")
and builds `agent_turns` as the compensating dedupe — but scoped to the *delivery*: "A repeated
`Send`/`Release` with a seen key returns success without side effects" (`05:221`). Dedupe of
*recording*; nothing about how many times a recorded row is advanced, because one poller was
assumed.

And the premise both rest on, spec 10 §1 and §7:

> v1.5 is **one region, one instance of each deployable**, deploys straight from `main`.
> **Region:** nearest Render region to the user; **single instance of each service**.

Spec 03 is the outlier that declined the assumption, and says so explicitly:

> **Correctness does not depend on the runtime.** `02` §7 contemplates a single writer per project;
> **if it holds**, these locks never contend. **The board does not rely on it** — locking and
> constraints make the mechanics safe under any interleaving.

### 3.3 So the answer is

There is no lock because **one was consciously judged unnecessary**, on the documented grounds that
exactly one process would be running. The reasoning was sound for the stated deployment and is
cheap where it holds. What no spec accounts for is that **`numInstances: 1` describes steady state,
not the deploy transition**: Render's zero-downtime deploy boots the replacement, waits for
`/healthz`, cuts traffic, and only then drains the old process — so the invariant every one of
these decisions rests on is suspended for 68–83 s, 13–16 times a day, and is suspended *precisely
when* tickets are moving (deploys follow merges follow agent work).

This is not a missing-lock oversight to be scolded; it is a **premise that was true when written
and is falsified by the platform's deploy model**, with nothing in the codebase or the gate able to
notice. That framing matters for the fix: the durable remedy is either to make the premise true
(single-owner via a lock — part 3 rec #1) or to stop depending on it (durable claims — recs #2/#3).
Recommendation: **do both**, because they fail differently (§6).

---

## 4. Other distinct failure modes in this window

Answering the ticket's "or are there other distinct failure modes". Ranked by frequency. Two are
new since part 3.

**M1 — Amika `502` / `503` upstream errors — 9 events. NEW.** Not in part 2's catalogue, which had
the HTML `403` shape.

```
agent: list agents: amika: 503 : upstream connect error or disconnect/reset before headers…
agent: list agents: amika: 502 : <html><title>Error 502 (Bad Gateway)!</title>…
```

Same handling gap as the `403`: an edge/proxy response that never reached the application, neither
retried as transient nor surfaced as a distinct condition — it just fails the sweep. Hits
`agent: liveness refresh` (skipping the project entirely), `agent: list workers`,
`api: list agents for board` and `steward: sweep project`. Part 2's rec (handle the non-JSON edge
shapes distinctly, with backoff) now covers three shapes, not one.

**M2 — the deploy-kill cascade — 39 `agent: persist turn … context canceled`, 6
`kiln exited with error`, 4 `runtime.event.failed … context canceled`, 4 `runtime: mark retry
… context canceled`, 3 `agent: persist turn … sql: database is closed`.** Part 2 §3, unchanged in
mechanism. **Refinement:** it fired on **6 of 13 deploys (46 %)**, not the "essentially every
deploy" (17 of 18) part 2 measured. That is consistent with part 2's own root cause rather than a
contradiction of it — `srv.Shutdown` only burns the 15 s when an SSE client is actually connected,
so the rate tracks whether anyone had the app open. It is a *conditional* deterministic failure.

**M3 — sandbox pool churn — 14 `rotating slot … past unadoptable record`, 3
`worker name conflict`.** The ~4.7:1 ratio matches part 2 §5's 12:1 direction: generation numbers
are overwhelmingly the unadoptable-record path, not the create race. Part 2's tombstone hypothesis
is **still unconfirmed and still un-instrumented** — its P1 logging recommendation (log the
candidate set and its statuses; log what `destroyUnkept` destroys) has not landed, so this remains
undiagnosable.

**M4 — steward interventions — 3 `poked stalled agent`, 2 `blocked stalled ticket`.** Working as
designed; the ticket-level backstop still fires. Note it is *also* a symptom surface: a ticket
whose two agents fought and wedged surfaces here, indistinguishable from an ordinary slow turn.

**M5 — brain-level errors — 4 `runtime.event.failed` (all `context canceled` from M2).** No
`brain: malformed model output` in this window. **Zero `mark dead` / dead-letter events** — the
`MaxAttempts` erosion path (part 2 §3, part 3 C5) is real but has still not actually dropped an
event.

**M6 — the non-terminal turn set leaks, and is growing. NEW, and not counted by any prior
document.** When an instance is killed mid-poll it logs one `agent: persist turn … context
canceled` per non-terminal turn it was advancing — which is an exact census of
`agent_turns WHERE phase <> 'done'` at that instant:

| Shutdown | Non-terminal turns being advanced |
| --- | --- |
| 2026-08-03T13:08:31 (`hgkv6`) | **12** |
| 2026-08-04T02:31:02 (`5lbjb`) | **18** |

**+6 in 13.4 h, and it is not churn — 10 of the 12 are the same rows** (`delivery-8295, 8445,
8567, 8741, 8767, 8861, 8914, 8968, 8971, 9000`). `delivery-8295` was still being polled on 08-04.
`8914` and `8968` are the **known orphan deliveries from part 3 §1.3 and §1.5** — the ones whose
instance died mid-`StartTurn`. They have never terminated and never will.

This is the silent wedge part 2 §6 predicted, now with a number on it. `CheckTurn` maps a 404 to
`Running: true` ("session not visible yet"), so a turn whose session no longer exists polls
forever; `ListNonTerminal` has no age bound; turns persist across restarts; and `recordFailure` is
never reached because the path returns no error. Each wedged row is polled every 2 s, indefinitely.

Three reasons it belongs in this document rather than a footnote:

- **It is the durable, countable residue of DL2.** Every orphaned duplicate contributes a row that
  can never complete, so this census is a *lower-bound running total* of orphans — the one
  after-the-fact measurement of a shape §1.2 cannot see.
- **The cost compounds**: 18 rows × every 2 s of Amika polling, growing monotonically, plus a
  `ListNonTerminal` result that never shrinks.
- **The steward makes it worse, not better.** It pokes a stalled Working ticket after ~5 min, which
  starts a *new* turn without terminating the wedged one, and it explicitly never touches a
  `building`/`starting` agent.

Slow rather than urgent — but monotonic, and nothing in the system reaps it.

**Verdict on item (2).** The concurrent-instruction race is **the** recurring cause of *corrupted
in-flight work*. M1–M6 are real and worth their own fixes, but none of them corrupts a working
tree: M1 fails a sweep, M2 kills and re-runs (recoverably), M3 wastes sandboxes, M4 is the
backstop, M5 is bounded, M6 leaks poll traffic. Only the duplicate-instruction race puts two
writers in one directory.

---

## 5. Data-loss register

Flagged explicitly, as the ticket asks. Severity is *risk to work that cannot be reconstructed*.

**DL1 — [Critical, confirmed, recurring] Two agents writing one working tree.** Uncommitted work in
a shared checkout, overwritten by a concurrent process with no file locking and no awareness. Four
occurrences in part 3 (§1.1, §1.3–§1.5) plus **three more `fresh:true` duplicates in this window**
(8928, 9424, 9566). Worst confirmed sub-case: on `2f9bc4cf` an unisolated agent edited
`schema/openapi.yaml` and its generated Go/TS output for 26 minutes alongside another writer —
concurrent regeneration produces output no reviewer can reconcile, because the generated files
carry no record of which source edit produced them. **Mitigated today only by whichever agent
happens to run `ps` first.**

**DL1a — [Critical, latent] …and the corruption does not stay in the sandbox: the working
agreement publishes it to `origin/main`.** This follows from `AGENTS.md`, not from any bug.
Agents are told to commit directly to `main` and **push to `origin/main`**, and ticket completion
*requires* a pushed SHA ("commit locally → push to `origin/main` → return that SHA"). So an
agent's normal, instructed final act is `git add` + `commit` + `push`. With two agents in one tree,
whichever commits first sweeps the other's half-written files into its commit and publishes them.
The result is a pushed commit on the branch everything deploys from that is **neither agent's
intent**, with a green gate if the interleaving happened to compile.

Two things make this worse than it first reads:

- **The gate cannot catch it.** `make check` verifies that the tree compiles and passes, not that
  it represents one coherent change. A mechanically-valid mixture of two agents' edits passes.
- **It is self-feeding.** The push to `main` triggers the auto-deploy (`autoDeploy: true`,
  no CI gate), and that deploy opens the next 66–83 s two-headed window (§3.3). The act that
  publishes the corruption is the same act that creates the conditions for the next occurrence.

Not yet observed in application code. The three occurrences that reached `main` so far were
**hand-merges of documentation** (`aa61161`, `80e540d`, and this document) — the most forgiving
possible expression of the bug. Prose can be reconciled by a human reading both versions; two
concurrent regenerations of `schema/` cannot.

**DL2 — [Critical, confirmed] Orphaned agent processes Kiln cannot see or stop.** An instance that
dies inside the 12 s send window has already caused Amika to spawn a `claude` process but never
records it; and per §2(b), a surviving duplicate *overwrites* the first session's handle. Either
way there is no turn row, no session handle, and no sweep that knows to spare or stop it. Confirmed
twice at the OS level (part 3 §1.3, §1.5); the `agent: persist turn … context canceled` proxy
appears **39 times** in this window. **No Kiln-side lock, lease or CAS can reach an already-spawned
orphan** — only something at the sandbox boundary can (§6, rec 9), or the new manual kill (DL3).

**DL3 — [High, NEW as of 2026-08-04 02:28] The manual "Kill sandbox" control destroys uncommitted
work with no preservation step.** `57d8a1b` adds `POST /api/tickets/{id}/sandbox/kill`; its own
doc comment says "the workspace behind the slot is thrown away". It commits nothing, stashes
nothing, and captures no diagnostic snapshot first. This is a deliberate and genuinely useful
escape hatch — and it is the **only** control that can currently reach a DL2 orphan, since the
process dies with the VM. But it is now a one-click, irreversible data-loss path, and the tickets
it will be pressed on are exactly the corrupted-tree incidents whose trees are worth capturing.
Recommend a "commit to a scratch branch / push a snapshot before destroy" step, and a confirmation
that says what will be lost — proposed as a follow-up ticket (§6).

**DL4 — [Medium, bounded, not yet realized] `MaxAttempts` erosion → dead-letter.** Every duplicate
claim and every deploy kill burns one of 8 attempts on an event that never failed. A dead-lettered
event is a permanently dropped board event. **Zero observed** across both windows, so this is a
margin being eaten, not a loss being taken.

**Explicitly NOT data loss:** the queue's claim-then-crash path. `runtime/worker.go:109` documents
it, part 2 confirmed it, and this window confirms it again — 0 dead letters, rows re-run on
restart. The 39 `context canceled` persist failures look alarming and are recoverable.

---

## 6. Recommendations, and the follow-up tickets

The engineering recommendations from parts 1–3 stand; this window changes their *evidence*, not
their content. Restated in priority order with what is new, and with the ticket drafts that now
exist for each. **Nothing is implemented here**, per the ticket.

### P0 — the three that stop duplicate instructions

1. **Advisory-lock the background loops** (part 3 rec #1). Ticket drafted:
   [`ticket-draft-advisory-lock.md`](ticket-draft-advisory-lock.md). *New evidence:* 7 more
   duplicates, all cross-instance, all inside an overlap window — all 7 prevented by this alone.
   Still the single cheapest change with the largest effect.
2. **Give the turn machine a durable claim (CAS)** (part 3 rec #2). Ticket drafted:
   [`ticket-draft-turn-claim-cas.md`](ticket-draft-turn-claim-cas.md). *New evidence:* §2 —
   the claim **must re-read the row**, and the write-back must be narrow, or §2(b)'s stale-snapshot
   clobber survives the fix. Required independently of #1: a leader lock does not prevent the
   orphan-at-birth shape (DL2).
3. **Fix the queue's 1-second visibility timeout** (part 3 rec #3). Ticket drafted:
   [`ticket-draft-queue-visibility-timeout.md`](ticket-draft-queue-visibility-timeout.md).
   *New evidence:* §1.3 — the observed gaps are 1–20 s, so the lease enables the double-claim but
   does not time it. Still a real bug after #1 lands, single-instance.

### P1 — shorten the window, make it observable

4. **Close the SSE streams on shutdown** (part 2 rec #1). Ticket drafted:
   [`ticket-draft-sse-shutdown.md`](ticket-draft-sse-shutdown.md). *Refined:* fires on 46 % of
   deploys, not ~100 % — it is conditional on a connected client. Worth 15 s off every window it
   does fire on, and it removes the DL2-producing cancellation cascade.
5. **Stamp a process-boot instance id into every log line** (part 3 rec #6). Four investigations
   have now depended on Render's `instance` label; the application still cannot attribute its own
   actions.
6. **Set and read `Turn.ProviderWorker`** (parts 1–3, still unwired). §2(b) makes this sharper: it
   is not only a pin, it is the handle whose loss makes an orphan unrecoverable.
7. **Alert on a second claim of an in-flight event**, primary; duplicate `agent.turn.started`,
   secondary; `agent: persist turn … context canceled`, third (part 3 rec #7 — ordering unchanged
   and re-validated: the 39 cancellations here again exceed the 7 visible duplicates). *Refined:*
   do **not** key the alert on a ≈1 s gap — §1.3 measured 1–20 s.
8. **Add the concurrency test with `-race`** (part 1 rec #7, part 3 rec #8, still open): two
   `Service` instances over one store, assert exactly one `StartTurn`. Note the gate still has no
   `-race` at all.

### P2 — the boundary, and the escape hatches

9. **Ask Amika to reject or serialize a second concurrent `agent-send` on one sandbox**, or have
   the sandbox wrapper refuse a second `claude` in the same `AMIKA_AGENT_CWD` (part 3 rec #9). The
   only measure that reaches a DL2 orphan short of destroying the sandbox.
10. **Per-turn `git worktree` in the wrapper, and reap wedged turns** (part 3 rec #10, plus M6).
    Ticket drafted: [`ticket-draft-worktree-and-reap.md`](ticket-draft-worktree-and-reap.md).
    Agents have now hand-rolled the worktree escape **three** times under collision, including for
    this document. Converts DL1 from tree corruption into two branches, one discarded, and is the
    only measure that covers DL1a (it makes the interleaved `git add` impossible rather than
    unlikely). Paired in one ticket with a wall-clock deadline for M6's leaking turns, because both
    are about surviving a collision the other fixes did not prevent.
11. **Make the manual kill non-destructive first** (DL3, new). Snapshot/commit the workspace to a
    scratch branch before destroy, and say in the confirmation what is being thrown away. Worth
    pairing with #10: with per-turn worktrees the snapshot is nearly free.

### P3 — unchanged, owner's call

12. `autoDeploy: false` + branch protection (parts 1–3). Argument unchanged and re-measured: 13
    deploys ⇒ ~18 min/day of two-headed operation. With P0 landed this reverts to hygiene.
13. Part 2's P1 pool-lifecycle logging — still the blocker on diagnosing M3's 14 rotations.
14. Part 1's P4/P5 hygiene (adopt-on-conflict, don't destroy a sandbox with a live turn, `-race`,
    image pinning, `make schema-verify` in `make check`).

### Sequencing

**#1 first** — it removes every symptom measured in §1.2–§1.3 and is ~40 lines with no migration.
**#2 and #3 are not optional follow-ons**: each is a real bug single-instance, and #2 is the only
one of the three that touches the orphan shape (DL2) that is doing the actual damage. **#4** is
independent and cheap. Everything below P1 can wait for these four.

---

## 7. Changes to the prior three documents

**Refinement 1 — part 3 §3.1's race window is understated.** The model was "both instances inside
one `worker_ready` window"; §2 here shows the window is a whole poll pass, evidenced by 12.4 s,
10.0 s and 17.9 s gaps that the old model cannot produce. Mechanism unchanged; magnitude and the
CAS's design requirement change.

**Refinement 2 — part 3 §3.1 did not name the stale write-back.** `s.update` clobbering
`provider_turn` from a stale snapshot (§2b) is the specific step that orphans a live session. Part 3
described the orphan as the loser's session being "unreferenced"; it is more active than that.

**Refinement 3 — part 3 §4's "most gaps are 1.0–1.9 s, the visibility timeout, exactly".** True of
that window, not of this one (1–20 s). The lease makes the double-claim possible; the second
dispatcher's availability times it. Affects only how the alert in rec #7 should be written.

**Correction — part 2 §3's "this fires on essentially every deploy".** Measured 17/18 there,
**6/13 (46 %)** here. Not a contradiction of part 2's root cause but a qualification of its
frequency claim: it is conditional on a connected SSE client. Part 2's fix and its P0 ranking are
unaffected.

**Everything else stands.** Part 3's core mechanism, its four occurrences, its consequence
register C1–C6, and its recommendation ordering are all corroborated on a fresh, non-overlapping
window.

---

## 8. Method note

Render `/v1/logs` for `srv-d953nmcvikkc73d8aq60`, owner `tea-d94pie5ckfvc73adqv30`, walked backwards
via `nextEndTime` with per-term `text` filters, deduplicated by `(timestamp, message)`, instance
attribution from each line's `instance` label. Window 2026-08-03T12:34Z → 2026-08-04T04:00Z, chosen
to start where part 3's pull ended so the two windows do not overlap — 8910/8912 appear in both by
construction and are used as a cross-check on the method, not counted as new.

Deploy list from `/v1/services/{id}/deploys` (13 deploys in-window, all `live` or `deactivated`,
0 `build_failed`). Code claims are against `origin/main` at `028bced`, fetched during this
investigation — **not** the local checkout, which was 6 commits behind at `3f0207a`; the fix-status
table in §1.1 would have been wrong if read locally. Spec quotations are from `docs/specs/` at the
same commit.

Counts are of log lines, not incidents. Turn-level duplicate counts are a **lower bound** for the
reason part 3 §4.1 gives, and the 39 `persist turn … context canceled` lines are the usable proxy
for the invisible remainder. Render's retention is 7 days, which bounds everything here.
Percentages come from a 15.4 h window with 47 turns, so they carry wide error bars — §1.2's
normalization table is the reason this document claims "not falling" rather than a rate estimate.

Live `/proc` inspection for §1.2a from inside worker sandbox `…-4181c8d97a28`: argv comparison is
`sha256` over `/proc/<pid>/cmdline` with `NUL` → newline, working directories from
`/proc/<pid>/cwd`, so "same instruction, same directory" is measured rather than inferred.

---

## 9. Provenance of this document

This document is a **hand-merge of two independently written versions**, because the dispatch that
commissioned it was itself duplicated (§1.2a) — the third time this has happened to an
investigation into this bug.

PID 1454 wrote its version in the shared checkout; PID 1514 detected the collision via `ps` before
writing anything, isolated itself into a `git worktree` (`/tmp/kiln-wt-1514`, branch
`investigate/dup-2026-08-04`), and wrote there. Neither clobbered the other. The merge was
performed after 1454 exited.

**From the shared-tree version (1454), which is the base:** the whole document structure, §2's
finding that the race window is a whole poll pass rather than the poll interval (with the
stale-snapshot `s.update` clobber that explains why orphans are unrecoverable), §3's sourced
answer on why no lock exists, the M1–M5 catalogue, DL3 (the new manual-kill data-loss path from
`57d8a1b`), §1.3's correction that the claim-gap distribution is 1–20 s rather than ≈1 s, §7's
correction of part 2's "essentially every deploy" to 46 %, and the discipline of verifying code
against `origin/main` at `028bced` rather than the local checkout.

**From the worktree version (1514):** §1.2's rate normalization (per-deploy 1.57× rather than the
per-turn 3.02×, and the "not falling" framing), §1.2a's OS-level capture of the fifth occurrence
and its 0.6 s interlock with the logs, §1.3's contradictory-instruction evidence (deliveries 9022
and 9026), **M6** (the non-terminal turn leak, 12 → 18, uncounted by any prior document), **DL1a**
(corruption reaching `origin/main` via the working agreement, and its self-feeding deploy loop),
and the worktree/reap follow-up ticket.

**One methodological note carried from 1514:** a same-instance duplicate turn start (`idem_key`
9022) was recorded and then withdrawn — the second line was an `agent.delivery.recorded`, not an
`agent.turn.started`, because Render's log `text` filter matches loosely. The corrected count is
7/47, all cross-instance, which is what both versions report. No conclusion rested on it, but the
grouping method is prone to that error and anyone re-running it should filter on `msg` exactly.

That two agents produced substantially the same analysis independently is mild corroboration of the
findings. It is not a defence of the mechanism that produced them: this cost two full duplicate
investigations, and the merge was done by hand.
