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
delete), `StateChangedAt` (0007, the "time in status" clock), `DoneCommit` (0010, the
landed commit), and `KeepSandbox` (0012, the per-ticket sandbox option — see below). A worker row is a capacity slot (01 §5's "sandbox", made provider-neutral per
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
06 §4 amended, superseding the strict D10 "no delete"; deletable from shaping/ready/**blocked**/
done — only *working* is refused, and a **blocked** delete releases the worker it held via
`agent.release` + `pull.evaluate`, mirroring AcceptToDone, keeping state=blocked as history —
2026-07-11-delete-blocked-ticket-design.md), `GetTicket`, `GetBoard`,
`SetWorkerHealth` (driven by the agent-liveness reconciler),
`SetKeepSandbox` (the per-ticket sandbox option — see below), `KillSandbox`/`ReassignSandbox`
(the manual sandbox overrides — see below), plus internal `RunPull`.
Preconditions are strict: invalid or repeated transitions are typed errors (`ErrNotFound`,
`ErrInvalidTransition`), never no-ops (D8). Every mutation returns the updated Ticket and
emits `board.updated`. (An archived ticket is `ErrNotFound` to every subsequent read/op.)

**One precondition table (`transitions.go`).** Which states each operation accepts lives in
exactly one place — `statePreconditions`, keyed by `Operation` (whose values *are* the names
`ErrInvalidTransition.Attempted` reports). The operations' own guards read it (`guardState`,
`guardBoundWorker`), and `State.AllowedOps()` derives the reverse view — "what can be done to
a ticket sitting here" — which the brain renders onto `get_ticket`/`list_tickets` so the model
stops discovering preconditions by failed call. Add a state-gated operation by adding its row;
never re-inline a `t.State != …` check, or the advertised set starts lying. State is not the
*whole* precondition everywhere: the worker-bound ops also need the binding, `ReassignSandbox`
a free slot, `AcceptToDone` an unspent commit — so an allowed op can still fail.

**Per-ticket sandbox option (`KeepSandbox`, migration 0012).** The user's "save this
ticket's sandbox" choice, set from the ticket detail sheet via `SetKeepSandbox` (behind
`POST /api/tickets/{id}/sandbox`). Its whole mechanical effect is one **suppression**: the
`agent.release` a ticket owes when it gives up its worker — `AcceptToDone`, and
`ArchiveTicket` on a blocked ticket — is not emitted, so the agent module never
destroys-and-recreates the slot's sandbox and the workspace survives for the next turn on
that slot (`releaseEmissions` in service.go is the single seam; both exits go through it).
Everything else is unchanged: the binding still clears and `pull.evaluate` still fires, so
the *slot* is freed — only the sandbox behind it is kept. `SetKeepSandbox` is the module's
one operation that is a **setting rather than a transition**: no precondition, legal in any
state, emits only `board.updated`.

**Manual sandbox overrides (`KillSandbox` / `ReassignSandbox`).** The user's direct escape
from a wedged or corrupted workspace, behind `POST /api/tickets/{id}/sandbox/kill|reassign` —
previously only the orchestrator could clear one up. Both act on the sandbox behind a slot,
not on the ticket's place on the board, and both require `state ∈ {working, blocked}` with a
bound worker (`ErrInvalidTransition` otherwise).

- `KillSandbox` emits `agent.release` for the ticket's own worker and changes **nothing** on
  the ticket — same state, same slot, no `UpdateTicket` at all. The slot recreates a fresh
  sandbox; no work is sent, so the ticket sits unbriefed until poked or reassigned.
- `ReassignSandbox` locks a free worker (`FreeWorker`, so an errored slot is skipped and the
  ticket's own busy slot can never come back), rebinds the ticket, and emits `agent.release`
  for the **old** slot plus `agent.send` carrying `workOrder(t)` on the new one. Result state
  is `working` with `blocked_reason` cleared — a briefed agent is on it again, exactly as
  `SendToAgent` leaves things. `ErrNoFreeWorker` when every slot is busy. **No
  `pull.evaluate`**: one slot is vacated and one taken, so free capacity is unchanged.

**Both deliberately ignore `KeepSandbox`** — the one place `releaseEmissions` is bypassed. The
option means "don't recycle this behind my back"; these are the user in front of it asking for
the recycle now, and a saved sandbox is exactly the case where a silent no-op would be worst.
If you add a third exit from Developing, route it through `releaseEmissions`; if you add
another override, don't.

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
constraints enforce I1/I3/I4; the partial unique index enforces I2. **I3 binds only
LIVE rows** (`archived_at IS NULL`, migration 0011): an archived row may be blocked with
a NULL `worker_id` — the delete-a-blocked-ticket path releases the worker while keeping
state=blocked as history. I4 still binds every row (a deleted blocked ticket keeps its
reason). Changing capacity =
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
                0003–0011 (approval, outbox topics, archived_at, completion topic,
                state_changed_at, project_id, worker_health, done_commit,
                worker_binding_live: I3 scoped to live rows so a deleted blocked
                ticket can be archived worker-less), 0012_keep_sandbox.sql (the
                per-ticket sandbox option)
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
- **A new board migration reaches an existing `kiln_test` on its own now — it did not used to.**
  The suite applies migrations through the same per-file `schema_migrations` ledger the app uses
  (`testutil.ApplyMigrations`, keyed by `postgres.MigrationsKey`), so adding a `.sql` file is
  enough. Before that it bootstrapped only when the `tickets` table was *missing*, which froze a
  long-lived scratch DB at its creation-day schema and failed every board integration test with
  `column "…" does not exist`. If you meet a database older than the ledger itself, the helper
  says so and names the fix: `make test-db-reset`.
- **A dependency edge is not a state.** `ticket_dependencies` (0013) says a ticket waits for
  others, and its ONLY mechanical effect is that `NextReadyTicket` skips a Ready ticket with an
  unmet dependency — it keeps its place in the pull order and holds no worker. There are still
  exactly five states (D1): "waiting" is derived on read
  (`Ticket.WaitingOnDependencies` = ready + unmet > 0), never stored.
- **An ARCHIVED dependency stops counting, deliberately.** Every dependency query carries
  `archived_at IS NULL`. An archived ticket can never reach done, so honouring an edge to one
  would strand its dependents forever — deleting a ticket therefore silently releases whatever
  waited on it. `ArchiveTicket` asks `Dependents` first and emits `pull.evaluate` when the answer
  is yes: the pullable set changed without any ticket changing state, and nothing else would
  notice. The same rule makes the cycle walk skip archived tickets — a ring that only closes
  through one is not a real cycle.
- **The cycle check runs under both tickets' row locks.** `AddDependency` locks the ticket AND the
  dependency, then walks `DependencyPathTo`. Drop either lock and two concurrent adds can each
  see an acyclic graph and commit edges that close a ring — which nothing downstream would detect,
  because the pull just skips every ticket in it forever.
- **`done_commit` uniqueness is lock-then-check.** `recordDoneCommit` runs under the target
  ticket's row lock with the partial unique index as the backstop; skip the lock and two
  concurrent accepts can race the same commit onto two tickets.
