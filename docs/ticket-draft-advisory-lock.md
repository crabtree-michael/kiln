# Ticket draft — gate the background loops behind a Postgres advisory lock

> **STATUS: LANDED 2026-08-04.** `backend/internal/leader` + `graph.startLoops` in
> `backend/cmd/kiln/wiring.go`. Every acceptance criterion below is checked. Part 5's fix-status
> table (`docs/root-cause-2026-08-04-part5-fresh-window.md` §3) should read **present** for P0 #1
> from this date; a sixth investigation finding duplicates *after* 2026-08-04 is new information,
> not a repeat. The other three rows in that table are still absent and still required — a leader
> lock does not subsume the turn-machine CAS or the 1 s queue lease.

Drafted 2026-08-03 off `docs/root-cause-2026-08-03-duplicate-instances.md` §6 P0 item 1.
Paste the title/body below into the board; the rest of this file is the working detail.

---

## Title

Make the background loops single-owner via a Postgres advisory lock

## Body

Every deploy runs two backend instances side by side for **68–83 seconds** (measured across six
consecutive deploys), and *both* run the full set of background loops. Nothing in the
system prevents the second instance from claiming and acting on work the first is already mid-way
through. Four confirmed occurrences are written up in
`docs/root-cause-2026-08-03-duplicate-instances.md`; all four sit inside a deploy overlap window,
so all four would have been prevented by this change.

The worst observed state was **three `claude` processes in one working directory at once**, two of
them running a byte-identical instruction, all arising from a *single* board event
(§1.5, `idem_key` 8968).

Take `pg_try_advisory_lock(<fixed key>)` on a dedicated pinned `*sql.Conn` before starting the
background loops. An instance that does not hold the lock serves HTTP/SSE only and retries every
few seconds. This closes both independent gaps (§3.1 the turn machine's missing claim, §3.2 the
queue's 1-second visibility timeout) in one move, and it also fixes an ordering bug: today the new
instance starts *working* before it is even routed traffic — under the lock it starts working when
the old one stops.

**Scope: the leader lock only.** Two follow-ups stay separate tickets, and both are still required
because a leader lock does not subsume them:

- **Turn-machine CAS** (§6 item 2) — a leader lock does not prevent the "orphaned at birth"
  shape, where one instance dies mid-`StartTurn` after Amika has already spawned the process.
  Seen twice (`idem_key` 8914, 8968).
- **Queue visibility timeout** (§6 item 3) — `least(power(2, attempts), 60)` gives a fresh row a
  **1-second** lease, shorter than any brain pass. Still a real bug after this lands: a single
  instance whose pass outruns its own lease is one crash-recovery away from the same fan-out.

### Where the change goes

- `backend/cmd/kiln/wiring.go:751` — `graph.run` starts the four loops:
  `runWorker(events)`, `runWorker(outbox)`, `runAgent`, `runSteward`. These four are what the lock
  gates. The HTTP server start below them is **not** gated.
- `backend/cmd/kiln/wiring.go:170` — `graph` has no `db` field today. `buildGraph` already
  receives `db *sql.DB` (`wiring.go:195`), so either store it on `graph` or thread it into `run`.
- No migration. Advisory locks are session state, not schema — there is no existing
  `pg_advisory_*` usage in the tree, so pick and document the key constant in one place.

### Why a pinned connection

`*sql.DB` is a pool; a lock taken on one pooled connection and released on another is a silent
no-op. Take the lock on a `conn, err := db.Conn(ctx)` held for the process lifetime, and keep that
conn out of general use. Two consequences worth designing for:

- **Clean shutdown** releases the lock explicitly.
- **Hard exit** (SIGKILL, OOM, container stop) drops the TCP session, and Postgres releases a
  session-scoped advisory lock with it — so the new instance picks the lock up within seconds
  without any lease/heartbeat machinery. This is the main reason to prefer an advisory lock over a
  row-based leader election.
- **Connection death while alive** (pooler restart, network blip) silently drops the lock. The
  retry loop must therefore re-check that it still *holds* the lock, not just that it once
  acquired it; a bare `pg_try_advisory_lock` at boot is not sufficient.

### Acceptance criteria

- [x] Exactly one instance runs the four background loops at any time, across a rolling deploy.
      `graph.startLoops` gates all four inside `leader.Elector.Run`;
      `TestOnlyOneInstanceRunsTheLoops` and `TestTwoInstancesStartOneTurn` cover it.
- [x] The non-leader instance still serves HTTP and SSE normally — no user-visible degradation
      during the overlap window. The HTTP server start is outside the gate; verified with two live
      binaries on one database (`/healthz` 200 on the follower while it logs `leader.standby`).
- [x] The non-leader retries and takes over within a few seconds of the leader exiting, on both
      clean shutdown and hard kill. Measured: **5 ms** to release on SIGTERM (successor acquired
      1.08 s later, inside its 3 s retry) and **53 ms** end-to-end on SIGKILL.
- [x] Leadership acquisition/loss is logged, with the process-boot instance id (§6 item 6) so the
      transition is legible in Render's logs. `leader.acquired` / `.standby` / `.released` /
      `.lost`; `obs.InstanceID()` is stamped on the process default logger, so **every** line
      carries `instance` — item 6 is done, not just for these four messages.
- [x] A leader that loses its connection stops the loops rather than continuing to run un-locked.
      `pg_locks` re-verification every 5 s on the pinned conn;
      `TestLeaderStopsLoopsWhenConnectionDies` kills the backend and asserts the loops stop.

### Tests

- Unit: leadership acquisition/release/re-acquisition against a real Postgres (the lock has no
  meaningful fake — `pg_try_advisory_lock` semantics *are* the thing under test), integration-tagged.
  → `backend/internal/leader/leader_integration_test.go` (4 tests, own `0x7E57…` key block, no
  tables needed). Each was mutation-checked: faking the acquire, dropping the unlock, and removing
  the periodic re-verification each turn a test red.
- Concurrency (§6 item 8, still open from 08-02 rec #7), with `-race`: drive `pollOnce` from two
  `Service` instances over one store and assert exactly one `StartTurn`. Two `Service`s over one
  store is what production actually does, and no test in the gate covers it today.
  → `backend/internal/agent/leader_concurrency_integration_test.go`. The provider **parks** inside
  `StartTurn`, so the row sits at `worker_ready` for the whole window and a second poller would
  necessarily duplicate: remove the Electors and it fails `2 != 1` deterministically, not flakily.
  `make test-backend` now runs the integration suite with `-race`.

### Note for whoever picks this up

The comment at `backend/internal/runtime/postgres/store.go:85` claims "the dispatcher always marks
it well before then" about the visibility timeout. The investigation disproves that — evt 1877 was
re-claimed 1.29 s in, mid-pass. Fix the comment with item 3's ticket, not this one, but don't trust
it while working here.
