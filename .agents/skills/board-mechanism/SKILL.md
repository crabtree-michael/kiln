---
name: board-mechanism
description: Use when working in the board module — the authoritative state of one project's board (tickets, sandboxes, outbox emissions) and the mechanical rules over it (invariants, the deterministic pull, transactional-outbox side effects). Backend anchor internal/board. Specs 02 §5, 03.
---

# Board mechanism (02 §5, mechanics decided by 03)

## Functional Requirements

**Responsibility.** The authoritative state of one project's board plus the mechanical
rules that govern it. Single source of truth: nothing outside the module writes these
tables (I8). Mechanics are fully specified in `docs/specs/03-board-mechanics.md`; the
product rules they realize are `01` §5 and must not be re-opened here.

**Entity model (03 §2).** One five-value ticket `state` — `shaping | ready | working |
blocked | done`; column and zone are **derived render groupings, not stored fields** (D1).
A sandbox row is a capacity slot, not a live resource handle: N rows seeded from config
*are* the WIP cap, and free vs busy is derived — busy iff an active (`working`/`blocked`)
ticket references it (D2). No status columns, no counters.

**Board API (03 §4)** — the only mutation surface, all named transition operations (no
generic move — D4): `CreateTicket`, `ShapeTicket` (priority is a field here; no separate
Reprioritize), `MarkReady`, `SendToAgent` (covers both Blocked→Working resume and
Working→Working new turn), `MarkBlocked` (called by the brain, or by the runtime on
mechanical failure), `AcceptToDone`, `GetBoard`, plus internal `RunPull`. Only the
diagrammed edges exist — no cancel/delete/reopen (D10). Preconditions are strict: invalid
or repeated transitions are typed errors (`ErrNotFound`, `ErrInvalidTransition`), never
no-ops (D8). Every mutation returns the updated Ticket and emits `board.updated`.

**Deterministic pull (03 §5).** Ready→Working happens **only** via `RunPull`, never by
brain action (I6) — it is not in the brain's tool set. Triggered by transactional
`pull.evaluate` outbox entries from `MarkReady` / `AcceptToDone`; idempotent, so
at-least-once drain and duplicate triggers are safe. Race-free via `FOR UPDATE SKIP
LOCKED` on both ticket and sandbox rows, with the partial unique index
`one_active_ticket_per_sandbox` (I2) as the DB backstop. Pull order: `priority DESC,
ready_at ASC, id ASC` (D9).

**Concurrency (03 §6).** One operation = one short READ COMMITTED transaction.
Lock-then-check: `SELECT … FOR UPDATE` the target ticket, then verify the precondition on
the locked row. `SKIP LOCKED` only in the pull; targeted operations conflict loudly.
Database constraints back up every invariant even if service code is wrong.

**Side effects (03 §7).** Transactional outbox: emissions are recorded atomically with the
state change and executed after commit by the runtime's drain loop, at-least-once with the
outbox `id` as idempotency key. Topics: `amika.dispatch`, `amika.instruct`, `notify.send`,
`pull.evaluate`, `board.updated` (triggers a full-snapshot push, not diffs — D7). Payloads
are emit-time snapshots. The outbox is distinct from the brain-waking event queue (02 §2).
An effect failure never rolls back the board; exhausted dispatch/instruct retries →
runtime calls `MarkBlocked` with the failure as reason.

**Topology (03 §9).** All in `/backend/internal/board`: `BoardService` (operations,
transition rules, and the pull — the pull is board logic, not runtime logic), a store
port private to the module, and a Postgres adapter owning the 03 §8 DDL/migrations.
**No Amika port** — the board appends outbox intent rows; the runtime's drain loop invokes
the Amika adapter (D5, superseding 02 §5's topology sketch). The board's only
infrastructure dependency is Postgres.

**Persistence (03 §8).** `text + CHECK` for `state`, not a native enum (D6). CHECK
constraints enforce I1/I3/I4; the partial unique index enforces I2. Changing capacity =
inserting or deleting sandbox rows (insert-only reconciliation at startup).

**Testing (03 §9).** Unit: `BoardService` transition rules and error paths against an
in-memory store fake — asserting emitted outbox rows *is* asserting side effects, no Amika
fake needed. Integration: real Postgres for constraint backstops (I1–I4) and a parallel
`RunPull` hammer test proving no double-binding.

## How to work here

_(Accumulate as you work: how to run migrations, how to test the Board API against a fake
repository, the module boundary to stay inside — `backend/internal/board`, reachable only
through the Board API.)_

## Common footguns

_(Accumulate: mistakes agents predictably make in this module.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
