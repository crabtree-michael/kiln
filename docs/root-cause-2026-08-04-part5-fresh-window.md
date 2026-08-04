# Root cause, part 5 — a fifth window, and the two collisions the ticket names

**Date:** 2026-08-04 · **Service:** `srv-d953nmcvikkc73d8aq60`
**Log window:** 2026-08-04T04:00Z → 14:15Z (~10.2 h, 9 deploys, 0 build failures) — starts where
[part 4](root-cause-2026-08-04-followup.md) ended, so it does not overlap any prior pull.
**Code checked at:** `a9fb658` (`origin/main`, and the local checkout — they are equal).

Fifth in the series, after
[part 1](root-cause-2026-08-02-concurrent-sandbox.md), [part 2](root-cause-2026-08-03-render-logs.md),
[part 3](root-cause-2026-08-03-duplicate-instances.md) and [part 4](root-cause-2026-08-04-followup.md).

> **The headline.** The mechanism established in parts 3–4 is confirmed again on a fresh window, and
> **still nothing has been fixed**. **5 of 28 turns (17.9 %) were started twice; all 5 are
> `fresh:true`, all 5 cross-instance, all 5 inside a deploy overlap window.** Both collisions named
> in the investigation ticket are in that set and are identified by idempotency key below (§2).
>
> **What is new:** the severe shape is now the *only* shape. `fresh:true` — the duplicate that mints
> a **second independent Amika session**, i.e. two unrelated `claude` conversations editing one
> working tree — went 2/12 (17 %) → 3/7 (43 %) → **5/5 (100 %)** across the three measured windows.
> And part 4's M6 turn leak is still growing: **21** never-terminating `agent_turns` rows, up from
> 18, with 18 of them the same rows part 4 listed.

This document adds evidence. It does not revise anything in parts 1–4; the diagnosis and the
recommendation ordering stand exactly as written there.

---

## 1. The mechanism, in one paragraph

Render's zero-downtime deploy boots the replacement instance, waits for `/healthz`, cuts traffic,
and only then drains the old one — so two full backend processes run concurrently for **67–83 s**
of every deploy (measured again below). Both run `agent.Service.Run`, and `pollOnce`
(`service.go:505`) lists `agent_turns WHERE phase <> 'done'` with **no lease, no claim, and no
`FOR UPDATE`**, then acts on that snapshot without re-reading the row. Both instances therefore see
the same `recorded`/`worker_ready` row and both call `stepStartTurn` (`service.go:611`), which under
the sync-send bridge mints a session (`POST …/sessions`) and fires `POST …/agent-send`. Two sessions,
two `claude -p` processes, one sandbox, one working tree. `s.update` then writes the stale snapshot
back last-writer-wins, destroying the losing session's handle — which is why nothing can reap it
afterwards. Spec 04 §4 chose in-process serialization deliberately, on spec 10's "single instance of
each service"; that premise is simply suspended during the deploy transition. Part 4 §2–§3 has the
full derivation.

## 2. This window's duplicates — including both collisions the ticket reports

`agent.turn.started`, grouped by `idem_key`, instance from each line's Render `instance` label.
**28 distinct turns, 5 started twice.** Every pair carries an identical `instruction_hash`.

| `idem_key` | ticket | gap | instances | `fresh` | ticket title (from `agent.delivery.recorded`) |
| --- | --- | --- | --- | --- | --- |
| 9768 | `ba9159e3` | 1.32 s | w88k2 / cljmb | **true** | Desktop app: fix toast click behavior |
| **9784** | `9e3d5dc4` | 10.57 s | kd6qk / cljmb | **true** | **Desktop app: redesign input as mic-first with unified transcript/text field** |
| 9906 | `5a4a0d6a` | 6.52 s | 9gb5d / fg8gz | **true** | Mobile & desktop: allow editing transcript before send |
| **9968** | `355c50c7` | 0.09 s | rpnfg / 999lv | **true** | **Ticket detail: edit via tapping body, remove pen icon** |
| 10024 | `2c57eb8b` | 0.30 s | 6lkjj / 4shn5 | **true** | Empty state: move "last word" to small subtext line |

The two bolded rows are the incidents the investigation ticket describes:

- **`idem_key` 9784 — the mic-first input ticket.** Started 12:09:23.259 by `kd6qk` and 12:09:33.827
  by `cljmb`. The stray `vitest` (pid 8319) found running beside the ticket's own agent (pid 8721)
  in that workspace is the losing duplicate's test run.
- **`idem_key` 9968 — the tap-body-to-edit ticket.** Started 12:43:51.067 by `rpnfg` and
  12:43:51.159 by `999lv`, **90 ms apart**. This is the pair the agent caught live-editing
  `TicketDetail.tsx`/`.css` and escaped by isolating itself into a `git worktree` — the fourth time
  an agent has hand-rolled that workaround (part 4 §9 counts three).

Both are `fresh:true`, so in each case the two processes were running *separate* Amika sessions with
no shared context — neither could see the other's conversation.

Also worth recording: `idem_key` 10009, the dispatch of **this** investigation, was started exactly
once (13:08:29, `4shn5`). Parts 3 and 4 were both duplicated mid-investigation; this one was not.
That is luck about deploy timing, not a change in the system.

Per part 3 §4.1 this count is a **lower bound**: `agent.turn.started` is logged only after
`StartTurn` returns, so a duplicate whose instance is killed inside the 12 s send window spawns a
real agent and logs nothing.

### 2.1 Rate

| | Part 3 window | Part 4 window | **This window** |
| --- | --- | --- | --- |
| Duplicates per **turn** | 4.9 % | 14.9 % | **17.9 %** |
| Duplicates per **deploy** | 0.34 | 0.54 | **0.56** |
| `fresh:true` share | 2/12 (17 %) | 3/7 (43 %) | **5/5 (100 %)** |

Per-deploy is the meaningful normalization (a duplicate is caused by a deploy, not by a turn) and it
is **flat** at ~0.55/deploy across the last two windows. The claim remains part 4's: the rate is not
falling. What has changed monotonically across all three windows is the `fresh:true` share.

Duplicate event claims are also unchanged: **8 of 50 events claimed twice, 7 of the 8
cross-instance** (`runtime.event.received`).

### 2.2 Deploy overlap, re-measured

Per-instance first/last log line. Every one of the five duplicates falls inside one of these:

| Old instance's last line | New instance's first line | Two-headed |
| --- | --- | --- |
| w88k2 12:02:16 | cljmb 12:00:53 | 83 s |
| cljmb 12:09:40 | kd6qk 12:08:33 | **67 s** ← 9784 |
| kd6qk 12:12:05 | 4rxqv 12:10:57 | 68 s |
| 9gb5d 12:31:49 | fg8gz 12:30:27 | 82 s ← 9906 |
| fg8gz 12:37:43 | rpnfg 12:36:21 | 83 s |
| rpnfg 12:44:37 | 999lv 12:43:14 | **82 s** ← 9968 |
| 999lv 13:08:02 | 4shn5 13:06:39 | 82 s |
| 4shn5 13:10:05 | 6lkjj 13:08:48 | 77 s ← 10024 |

Consistent with the 68–83 s parts 3 and 4 measured. 9 deploys in 10.2 h ⇒ ~20 min/day two-headed.

## 3. Fix status: nothing has landed

Checked against `origin/main` at `a9fb658` (the local checkout is equal to it, unlike part 4's).

| Fix recommended in parts 1–4 | State |
| --- | --- |
| Advisory lock / leader election (P0 #1) | **Absent** — no `advisory`/`leader` match anywhere in `backend/` |
| Turn-machine CAS (P0 #2) | **Absent** — `pollOnce` and `stepStartTurn` byte-identical to part 4's citation |
| Queue visibility timeout (P0 #3) | **Absent** — `least(power(2, attempts)::bigint, 60)` still at `runtime/postgres/store.go:110`, `:127` |
| Close SSE streams on shutdown (P1 #4) | **Absent** — no `RegisterOnShutdown`/`BaseContext` in `backend/` |
| Set/read `Turn.ProviderWorker` (P1 #6) | **Absent** outside the Amika adapter's own handle type |

Six ticket drafts sit in `docs/` (`ticket-draft-advisory-lock.md`, `-turn-claim-cas.md`,
`-queue-visibility-timeout.md`, `-sse-shutdown.md`, `-worktree-and-reap.md`,
`-kill-sandbox-snapshot.md`). None has been turned into board work.

## 4. M6 (the leaking turn set) is still growing

An instance killed mid-poll logs one `agent: persist turn … context canceled` per non-terminal turn
it was advancing — an exact census of `agent_turns WHERE phase <> 'done'` at that instant. The
largest census in this window, at the 12:44 shutdown:

**21 rows**, up from part 4's 18 (08-04 02:31) and 12 (08-03 13:08).

18 of the 21 are rows part 4 already listed — `8295, 8445, 8567, 8741, 8767, 8861, 8914, 8968, 8971,
9000, 9022, 9026, 9149, 9403, 9587, 9610, 9646, 9651`. `8295` is still being polled every 2 s.
`8914` and `8968` are part 3's confirmed orphans; `9646` is part 4's own duplicated dispatch. None
of them will ever terminate: `CheckTurn` maps a 404 to `Running: true`, `ListNonTerminal` has no age
bound, and `recordFailure` is never reached because the path returns no error.

Monotonic, unreaped, and each row costs an Amika round trip every 2 s.

## 5. Recommendation

Unchanged from part 4 §6, which is unchanged from part 3. **The investigation is finished; what is
missing is the fix.** In order:

1. **`docs/ticket-draft-advisory-lock.md`** — gate the four background loops behind
   `pg_try_advisory_lock` on a pinned connection. ~40 lines, no migration. **All 5 duplicates in
   this window, all 7 in part 4's, and all 4 in part 3's would have been prevented by this alone.**
2. **`docs/ticket-draft-turn-claim-cas.md`** — durable claim on the turn row, re-reading inside the
   claim and writing back narrowly (part 4 §2b). Required independently: a leader lock does not
   prevent the orphaned-at-birth shape.
3. **`docs/ticket-draft-queue-visibility-timeout.md`** — the 1 s lease on a fresh event row. A real
   bug single-instance.
4. **`docs/ticket-draft-sse-shutdown.md`** — takes 15 s off each overlap window when it fires.

Until #1 lands, the only working mitigation is the one agents have now improvised four times: check
`ps` for a sibling `claude` in `AMIKA_AGENT_CWD` before writing, and isolate into a `git worktree`
if there is one. That is worth saying explicitly in `AGENTS.md` as an interim measure —
`docs/ticket-draft-worktree-and-reap.md` proposes doing it properly in the sandbox wrapper.

## 6. Method

Render `/v1/logs` for `srv-d953nmcvikkc73d8aq60`, owner `tea-d94pie5ckfvc73adqv30`, walked backwards
via `nextEndTime`, filtered on `msg` exactly (part 4 §9's caveat about loose `text` matching),
instance attribution from each line's `instance` label. Deploy list from
`/v1/services/{id}/deploys`. Ticket titles recovered from the `instruction` summary on
`agent.delivery.recorded`. Sandbox pool cross-checked against Amika `GET /sandboxes`: 9 sandboxes,
3 running, every slot fragment distinct — confirming this is two turns in **one** sandbox, never two
slots resolving to the same sandbox. Render retention is 7 days, which bounds the window.
