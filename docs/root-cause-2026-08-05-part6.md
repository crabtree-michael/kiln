# Root cause, part 6 — the lock held; what replays now is one event, not one turn

**Date:** 2026-08-05 · **Service:** `srv-d953nmcvikkc73d8aq60`
**Log window:** 2026-08-04T13:47Z → 2026-08-05T13:40Z (~23.9 h, 14 deploys) — starts at the deploy
that shipped the advisory lock, so it does not overlap any prior pull.
**Code checked at:** `4bc61a7` (`origin/main`, and the local checkout — they are equal).

Sixth in the series, after
[part 1](root-cause-2026-08-02-concurrent-sandbox.md), [part 2](root-cause-2026-08-03-render-logs.md),
[part 3](root-cause-2026-08-03-duplicate-instances.md), [part 4](root-cause-2026-08-04-followup.md)
and [part 5](root-cause-2026-08-04-part5-fresh-window.md).

> **The headline.** Part 5's top recommendation landed, and it worked. `967a510` gates the four
> background loops behind a Postgres advisory lock, and across 14 deploys **0 of 31 turns were
> started twice**, down from 5 of 28 (17.9 %). The duplicate-agent fan-out — two independent `claude`
> sessions in one working tree — did not occur once.
>
> **What is left is a different mechanism at a different level.** 4 of 103 events (3.9 %) were
> re-processed. Every one is a deploy hard-killing an in-flight brain pass whose failure is then
> never recorded, because `Worker.process` marks the outcome on the *same cancelled context*. The
> row stays `pending` with a 1-second due date and the next leader re-claims it within 0.4 s. The
> pass re-runs from scratch with everything the first attempt already committed still committed.
>
> This is the shape the investigation ticket describes: **re-processed messages, and duplicate
> ticket creation one model judgment away.** 3 of the 4 sent the user two messages for one input.
> None created a duplicate ticket — but nothing in the system prevented it.

Parts 1–5 are not revised. Their diagnosis was correct and the fix ordering was correct; this
document records what the first fix changed and re-derives what remains.

---

## 1. The lock held

`967a510` (deployed 2026-08-04T13:47:20Z) wraps the four background loops in
`leader.New(...).Run(...)` at `backend/cmd/kiln/wiring.go:835`, gated on `cfg.LeaderLock`
(`KILN_LEADER_LOCK`, default `true` at `cmd/kiln/main.go:193`, not overridden in `render.yaml`). The
HTTP server is deliberately not gated, so a follower still serves clients.

Fifteen `leader.acquired` and fourteen `leader.released` records in the window, all on lock key
`7739836654315110401`, never two holders at once.

| | Part 3 | Part 4 | Part 5 | **This window** |
| --- | --- | --- | --- | --- |
| Turns started twice | 4.9 % | 14.9 % | 17.9 % (5/28) | **0 % (0/31)** |
| `fresh:true` share of those | 2/12 | 3/7 | 5/5 | **—** |
| Duplicates per deploy | 0.34 | 0.54 | 0.56 | **0** |

Part 5 asserted all five of its duplicates, all seven of part 4's and all four of part 3's would
have been prevented by this change alone. That prediction is confirmed on 14 deploys.

The release ordering is what makes it safe: `backgroundLoops` returns only once all four loops have
stopped, and the lock is released after that — so the successor's loops start strictly after the
predecessor's have ended. In all four cases below the new leader acquired 2.4–15.0 s *after* the old
leader's pass was already cancelled.

## 2. What replays now

**103 distinct events, 4 claimed twice (3.9 %).** These are not concurrent double claims. Each maps
1:1 to a `runtime.event.failed` on the first attempt, and each second claim lands milliseconds after
the successor's `leader.acquired`:

| event | type | claim 1 | pass failed | old instance exit | new `leader.acquired` | claim 2 | gap after acquire |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2121 | `human.message` | 11:40:08.205 `2dvrw` | 11:40:38.577 | 11:40:53.579 | 11:40:41.044 `86bnv` | 11:40:41.056 | **12 ms** |
| 2139 | `human.message` | 11:52:13.241 `4x5f8` | 11:52:27.809 | 11:52:42.810 | 11:52:28.306 `t9g8c` | 11:52:28.492 | 186 ms |
| 2143 | `agent.turn_completed` | 11:56:49.678 `t9g8c` | 11:57:33.955 | 11:57:48.956 | 11:57:35.990 `fmczq` | 11:57:36.392 | 402 ms |
| 2184 | `agent.turn_completed` | 13:10:27.514 `zmncg` | 13:10:33.596 | 13:10:48.597 | 13:10:34.935 `94ghb` | 13:10:35.091 | 156 ms |

All four failures are the same error:

```
kiln: brain handle event: brain: llm call: brain: anthropic messages.new: context canceled
```

Note the `pass failed` → `old instance exit` column: **15.001 s in all four cases**, exactly
`shutdownTimeout` (`wiring.go:60`). The context is cancelled at the signal, the brain pass dies
immediately, and the process then sits in a `srv.Shutdown` that never completes.

## 3. The chain, in order

1. **Render finishes a deploy and signals the old instance** — ~59 s after the new one goes live for
   three of the four (2139 sits in a pair of overlapping deploys).

2. **`srv.Shutdown` never completes.** Every instance exit in the window — **12 of 12** — is
   `kiln exited with error err="kiln: http shutdown: context deadline exceeded"`. This is part 3's
   P1 #4, still unfixed: no `RegisterOnShutdown`/`BaseContext` anywhere in `backend/`. The process
   is hard-killed 15 s after the signal rather than draining.

3. **The in-flight brain pass is cancelled mid-LLM-call**, producing the `runtime.event.failed`
   above. `runPass` has no mid-pass checkpoint by design (06 §5), so nothing records how far it got.

4. **The outcome write is cancelled too.** `Worker.process` (`internal/runtime/worker.go:165-180`)
   marks the outcome on the **same context it handled on**:

   ```go
   next := w.clock.Now().Add(backoff(e.Attempts))
   if err := w.store.MarkRetry(ctx, w.queue, e.ID, handleErr.Error(), next); err != nil {
       slog.Error("runtime: mark retry", "queue", w.queue, "id", e.ID, "err", err)
   }
   ```

   Logged 4 times out of 4, one per failed event:
   `runtime: mark retry ... err="runtime/postgres: mark retry: context canceled"`. The failure is
   never recorded: `last_error` stays empty and `next_attempt_at` is never moved to the real backoff.

5. **The claim's own push-out becomes the retry schedule.** The row is still `pending` with
   `next_attempt_at` = claim time **+ 1 s**, from
   `least(power(2, attempts)::bigint, 60)` reading the pre-update `attempts = 0`
   (`internal/runtime/postgres/store.go:110`). Part 3's P0 #3, still unfixed. The row was due again
   one second into a 30–44 s pass.

6. **The next leader re-claims immediately** — 12–402 ms after acquiring the lock, because the row
   has been due for the whole shutdown.

7. **The pass re-runs from scratch**, with every side effect the first attempt committed still
   committed and no record that it happened.

`MarkDone` at `worker.go:168` is on the same context. A pass that *succeeds* and is cancelled before
`MarkDone` lands replays in full with nothing to make it idempotent. **Zero occurrences in this
window** (no `runtime: mark done` errors at all), but it is the same defect with a worse blast
radius, and it is the one that would produce duplicate tickets outright.

## 4. Why the replay is not safe

`internal/brain/service.go:87-91` states the replay-safety argument:

> *"Idempotency (06 §6) is not a mechanism here: a replayed call re-reads fresh state via
> reader/convo, so it sees whatever a crashed prior call already committed; the board's strict
> preconditions (03 D8) turn a re-issued action into `ErrInvalidTransition`, which the model receives
> as a tool result and is instructed (prompt.go) to treat as already done."*

That holds for **transitions**. It does not hold for **creation or speech**, because those three
tools have no precondition to violate:

| tool | handler | guard |
| --- | --- | --- |
| `create_ticket` | `internal/brain/tools.go:479` → `internal/board/service.go:41` | non-empty title only; a repeated identical call inserts a second row |
| `say` | `internal/brain/tools.go:803` | non-empty text only |
| `post_update` | `internal/brain/tools.go:849` | non-empty body only |

So on replay, the *only* thing standing between the user and a duplicate is the model reading the
board back and choosing not to repeat itself.

### 4.1 Event 2121 — one human message, processed twice

The message asked for two desktop fixes. Reconstructed from `brain.tool`, both attempts on
`turn_id evt-2121`:

| attempt 1 — `2dvrw` | attempt 2 — `86bnv` (+2.9 s) |
| --- | --- |
| 11:40:11 `list_tickets` | 11:40:45 `list_tickets` |
| 11:40:20 **`create_ticket`** — "Desktop: toast overlapping/clipping mic radiation animation" | 11:40:50 `get_ticket` `7ffccb59` |
| 11:40:20 **`create_ticket`** — "Transcript placeholder overlaps text area while speaking" | 11:40:50 `get_ticket` `8c6fbe6e` |
| 11:40:30 `update_ticket` ×2 → `ready` | |
| 11:40:33 **`post_update`** "Queued two more desktop fixes: mic radiation getting clipped by toasts, and the transcript placeholder…" | 11:40:54 **`say`** "Got it — both are already tracked as ready tickets (toast clipping the mic radiation, and transcript…)" |
| ✗ killed 11:40:38 in the next LLM call | ✓ completed |

Attempt 2 avoided creating two more tickets **only because the model listed the board, recognised
its predecessor's two tickets, and decided not to re-create them.** Nothing in the board would have
rejected the second create. That is a coin flip dressed as an invariant.

The user still got two messages for one request — a `post_update` from the dead pass and a `say`
from the replay, in different words.

### 4.2 The others

- **2143** — attempt 1 (`t9g8c`) posted `say` "Got the agent's full report on the signup ticket…",
  then three `update_ticket` calls that all errored, then died. Attempt 2 (`fmczq`) posted a second,
  differently-worded `say` about the same report. **Two messages, one event.**
- **2184** — attempt 1 (`zmncg`) posted `say` "Same Amika GitHub-auth glitch again… ignoring", then
  died. Attempt 2 (`94ghb`) re-ran and hit an errored `get_ticket`.
- **2139** — attempt 1's only call was an errored `update_ticket`; attempt 2 retried it, read the
  ticket, and said one thing. **Benign** — the sole case where the replay cost nothing.

So: **3 of 4 replays were user-visible as a duplicate message. 1 of 4 was within one model decision
of duplicate tickets. 0 of 4 actually created one.**

## 5. A second, unrelated source of duplicate tickets

Worth separating out, because it will confound anyone chasing the board rather than the logs.
Over 2026-08-02T18:00Z → 2026-08-05T13:40Z there were **68 successful `create_ticket` calls, of
which 3 repeat an earlier title exactly**:

| title | first | second |
| --- | --- | --- |
| Onboarding: guided setup flow (github → project → provider) | 08-02 19:15 `evt-1666` | 08-03 11:50 `evt-1841` |
| Settings: fix stale GitHub connect link on RepoField | 08-02 20:44 `evt-1729` | 08-03 11:10 `evt-1769` |
| Debug panel: manual coach-turn trigger with mock game state picker | 08-03 12:56 `evt-1875` | 08-04 02:13 `evt-2017` |

These are hours to days apart on unrelated events — **not replay**. This is the brain re-proposing a
ticket in a later pass, which `create_ticket`'s lack of any dedupe permits. Different cause, same
symptom on the board. Out of scope for the replay fix; recorded so it is not mistaken for one.

## 6. M6, the leaking turn set

Still growing and still unaddressed. `agent: persist turn … context canceled` fired **73 times** in
the 11:30–13:40 slice alone. `CheckTurn` still maps a 404 to `Running: true`, `ListNonTerminal`
still has no age bound, and `recordFailure` is still unreachable on that path.

## 7. Fix status

| Fix recommended in parts 1–5 | State at `4bc61a7` |
| --- | --- |
| Advisory lock / leader election (P0 #1) | **LANDED** `967a510`, deployed 08-04T13:47Z — confirmed effective, §1 |
| Turn-machine CAS (P0 #2) | **Absent** — `stepStartTurn` still has no durable claim (`agent/leader_concurrency_integration_test.go:212` says so in-tree) |
| Queue visibility timeout (P0 #3) | **Absent** — `least(power(2, attempts)::bigint, 60)` still at `runtime/postgres/store.go:110`, `:127`; the false comment at `:83-85` is still there |
| Close SSE streams on shutdown (P1 #4) | **Absent** — 12/12 exits are `http shutdown: context deadline exceeded` |
| Set/read `Turn.ProviderWorker` (P1 #6) | **Absent** |
| **Detached outcome writes (new, this document)** | **Absent** — [`ticket-draft-detached-outcome-writes.md`](ticket-draft-detached-outcome-writes.md) |

## 8. Recommendation

The ordering has changed, because the lock removed the concurrency that made #2 and #3 urgent.

1. **[`ticket-draft-detached-outcome-writes.md`](ticket-draft-detached-outcome-writes.md)** — mark
   the outcome on a context detached from shutdown. ~5 lines, no migration, and the idiom is already
   used twice in-tree (`wiring.go:791`, `leader/leader.go:283`). This is the whole of §3 step 4, and
   it is the cheapest thing on the list. **All four replays in this window would have retried on a
   real backoff with a recorded error instead of instantly.**
2. **[`ticket-draft-sse-shutdown.md`](ticket-draft-sse-shutdown.md)** — stops the hard kill that
   starts the chain, and reclaims the ~6 % of brain spend that
   [`brain-optimization-2026-08-05.md`](brain-optimization-2026-08-05.md) §5 attributes to retried
   passes.
3. **[`ticket-draft-queue-visibility-timeout.md`](ticket-draft-queue-visibility-timeout.md)** —
   demoted from P0. With the leader lock there is no second dispatcher racing the 1 s lease, so it
   no longer causes concurrent double claims. It still matters: it is what makes an unrecorded
   failure retry after one second rather than on the D8 schedule.
4. **Make replay safe rather than probabilistically safe.** An idempotency key on
   `create_ticket`/`post_update`/`say` derived from the event id, or a per-event log of committed
   actions replayed into the pass context. This is a design decision, not a patch — but
   `service.go:87-91` should be amended either way, because it currently claims a guarantee that
   covers only transitions.

Items 2–4 are scoped follow-ups and are **not** in scope for the ticket drafted alongside this
document.

## 9. Method

Render `/v1/logs` for `srv-d953nmcvikkc73d8aq60`, owner `tea-d94pie5ckfvc73adqv30`, paged backwards
via `nextEndTime`, one pull per `msg` value, instance attribution from each line's `instance` label.
Deploy list and `finishedAt` from `/v1/services/{id}/deploys`. Passes reconstructed by joining
`brain.tool` on `turn_id` (`evt-<event_id>`). Duplicate claims from `runtime.event.received` grouped
by `event_id`; duplicate turns from `agent.turn.started` grouped by `idem_key`, the same key part 5
used, so the two windows are directly comparable.

Two caveats carried forward. Part 3 §4.1: `agent.turn.started` is logged only after `StartTurn`
returns, so the turn count is a lower bound — the 0/31 means no duplicate *survived to log*, not
that none was ever attempted. Part 4 §9: filters are on exact `msg`, not loose `text`, except §5's
`create_ticket` sweep which necessarily matches on argument text and is therefore de-duplicated by
`turn_id` before counting.
