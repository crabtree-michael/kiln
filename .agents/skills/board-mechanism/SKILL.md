---
name: board-mechanism
description: Use when working in the board module — the authoritative state of one project's board (tickets, workers, outbox emissions) and the mechanical rules over it (invariants, the deterministic pull, transactional-outbox side effects). Backend anchor internal/board. Specs 02 §5, 03.
---

# Board mechanism (02 §5, mechanics decided by 03)

## Functional Requirements

**Responsibility.** The authoritative state of one project's board plus the mechanical
rules that govern it. Single source of truth: nothing outside the module writes these
tables (I8). Mechanics are fully specified in `docs/specs/03-board-mechanics.md`; the
product rules they realize are `01` §5 and must not be re-opened here.

**Entity model (03 §2).** One five-value ticket `state` — `shaping | ready | working |
blocked | done` (still exactly these five); column and zone are **derived render groupings,
not stored fields** (D1). Beyond `state` the Ticket now carries a few stored fields added
since scaffold: `ApprovalRequested` (0003, the 08 §5 proposal flag), `ArchivedAt` (0005, soft
delete), `StateChangedAt` (0007, the "time in status" clock), and `DoneCommit` (0010, the
landed commit). A worker row is a capacity slot (01 §5's "sandbox", made provider-neutral per
05 A2), not a live resource handle: N rows seeded from config *are* the WIP cap, and free vs
busy is derived — busy iff an active (`working`/`blocked`) ticket references it (D2). The
worker also carries a `health` column (0009, `'ok' | 'errored'`) that the pull filters on
(see health-aware pull below) — otherwise no counters, no provider refs.

**Board API (03 §4)** — the only mutation surface, all named transition operations (no
generic move — D4): `CreateTicket`, `ShapeTicket` (priority is a field here; no separate
Reprioritize), `RequestApproval` (surface a shaping ticket as an 08 §5 proposal),
`MarkReady`, `SendToAgent` (covers both Blocked→Working resume and Working→Working new turn),
`MarkBlocked` (called by the brain, or by the runtime on mechanical failure),
`AcceptToDone(ctx, projectID, id, link CompletionLink, doneCommit)` (records the landed
commit + emits the done card), `ArchiveTicket` (soft delete — the brain's `delete_ticket`,
06 §4 amended, superseding the strict D10 "no delete"), `GetTicket`, `GetBoard`,
`SetWorkerHealth` (driven by the agent-liveness reconciler), plus internal `RunPull`.
Preconditions are strict: invalid or repeated transitions are typed errors (`ErrNotFound`,
`ErrInvalidTransition`), never no-ops (D8). Every mutation returns the updated Ticket and
emits `board.updated`. (An archived ticket is `ErrNotFound` to every subsequent read/op.)

**Deterministic pull (03 §5).** Ready→Working happens **only** via `RunPull`, never by
brain action (I6) — it is not in the brain's tool set. Triggered by transactional
`pull.evaluate` outbox entries from `MarkReady` / `AcceptToDone`; idempotent, so
at-least-once drain and duplicate triggers are safe. Race-free via `FOR UPDATE SKIP
LOCKED` on both ticket and worker rows, with the partial unique index
`one_active_ticket_per_worker` (I2) as the DB backstop. Pull order: `priority DESC,
ready_at ASC, id ASC` (D9).

**Health-aware pull (added since scaffold).** A Ready ticket binds **only to a healthy
worker**: both `FreeWorker` and the free-slot count filter `health = 'ok'` (migration 0009).
Worker health is reconciled out-of-band by the agent-liveness reconciler via
`SetWorkerHealth`, so an errored sandbox stops receiving pulls until it recovers. The D2 "free
vs busy is derived" rule is therefore refined: a slot must be both un-referenced **and**
`health='ok'` to be pullable/counted-free.

**Concurrency (03 §6).** One operation = one short READ COMMITTED transaction.
Lock-then-check: `SELECT … FOR UPDATE` the target ticket, then verify the precondition on
the locked row. `SKIP LOCKED` only in the pull; targeted operations conflict loudly.
Database constraints back up every invariant even if service code is wrong.

**Side effects (03 §7).** Transactional outbox: emissions are recorded atomically with the
state change and executed after commit by the runtime's drain loop, at-least-once with the
outbox `id` as idempotency key. Topics: `agent.send` (RunPull's work order and
SendToAgent's instruction — one topic, 05 A1), `agent.release` (AcceptToDone → recycle the
worker), `notify.send` (fired on start, blocked, and done transitions), `pull.evaluate`,
`board.updated` (full-snapshot push, not diffs — D7), plus the feed topics `feed.updated`,
`activity.toast`, and `feed.completion` (the persistent "done" card — see below). Payloads
are emit-time snapshots. The outbox is distinct from the brain-waking event queue (02 §2). An
effect failure never rolls back the board; exhausted agent.send retries → runtime calls
`MarkBlocked` with the failure as reason.

**Done cards + commit linking (added since scaffold).** `AcceptToDone` records `done_commit`
under a **one-commit-to-one-ticket** rule — a partial unique index (0010) plus a lock-then-
check backstop, so a commit already linked to another ticket fails with `ErrCommitAlreadyUsed`.
It also emits a `feed.completion` row carrying the `GitHubURL`/`GitHubLabel`/`Summary` for the
persistent done card (`CompletionLink`).

**Topology (03 §9).** All in `/backend/internal/board`: `BoardService` (operations,
transition rules, and the pull — the pull is board logic, not runtime logic), a store
port private to the module, and a Postgres adapter owning the 03 §8 DDL/migrations.
**No agent-runtime port** — the board appends outbox intent rows; the runtime's drain loop
invokes the agent-runtime module (D5, superseding 02 §5's topology sketch). The board's
only infrastructure dependency is Postgres.

**Persistence (03 §8).** `text + CHECK` for `state`, not a native enum (D6). CHECK
constraints enforce I1/I3/I4; the partial unique index enforces I2. Changing capacity =
inserting or deleting worker rows; `ReconcileWorkers` at startup **grows or shrinks** the pool
to match the configured count (a shrink deletes only free slots, via `FOR UPDATE … SKIP
LOCKED`).

**Testing (03 §9).** Unit: `BoardService` transition rules and error paths against an
in-memory store fake — asserting emitted outbox rows *is* asserting side effects, no agent-runtime
fake needed. Integration: real Postgres for constraint backstops (I1–I4) and a parallel
`RunPull` hammer test proving no double-binding.

## How to work here

**Module layout** (fully implemented; every contract is in the doc comments):

```
backend/internal/board/
  board.go      package doc — the module boundary (I8)
  entities.go   State (5 values) · Ticket (incl. ApprovalRequested/ArchivedAt/StateChangedAt/
                DoneCommit) · Worker (incl. health) · Snapshot
  errors.go     ErrNotFound · ErrInvalidTransition{From, Attempted} · ErrEmptyTitle ·
                ErrNoFreeWorker · ErrCommitAlreadyUsed{SHA, OtherID}
  outbox.go     Topic constants (incl. feed.updated/activity.toast/feed.completion) ·
                Emission · Send/Release/Notify/Completion payload structs
  store.go      Store + Tx ports (lock-then-check seam; SKIP LOCKED pickers for the pull)
  service.go    Service — the Board API: all 03 §4 operations incl. RunPull
  postgres/     store adapter (implements board.Store / board.Tx)
    migrations/ 0001_board.sql (03 §8 DDL), 0002_outbox.sql (04 §2, shared with runtime),
                0003–0010 (approval, outbox topics, archived_at, completion topic,
                state_changed_at, project_id, worker_health, done_commit)
```

- Build/check from `/backend`: `gofmt -l . && go vet ./... && go build ./...` (module
  `github.com/crabtree-michael/kiln/backend`, Go 1.26).
- Implement `Service` methods against a fake `Store`/`Tx` in unit tests; the postgres
  adapter is exercised only in integration tests. Asserting appended `Emission`s *is*
  asserting side effects.
- Migration tooling is still TBD (02 §14); `.sql` files apply in filename order.

## Common footguns

- **Adding an outbox topic without widening the topic CHECK constraint.** The `outbox` topic
  column has a `CHECK (topic IN (...))`; a new topic needs a migration to widen it
  (`0004_outbox_topics.sql`, `0006_outbox_completion_topic.sql`) or every transition that emits
  it fails the CHECK at commit. This has bitten twice — 0006's header records that leaving out
  `feed.completion` made "every 'done' transition fail the CHECK."

## Potential gotchas

- **`state_changed_at` vs `updated_at`.** A Working→Working nudge (`SendToAgent`) bumps
  `updated_at` but must **not** advance `state_changed_at` — that column is the "time in
  status" clock (0007), so only a real state change touches it.
- **`done_commit` uniqueness is lock-then-check.** `recordDoneCommit` runs under the target
  ticket's row lock with the partial unique index as the backstop; skip the lock and two
  concurrent accepts can race the same commit onto two tickets.
