---
name: board-mechanism
description: Use when working in the board module — the authoritative state of one project's board (tickets, columns, zones, sandbox bindings) and the mechanical rules over it (invariants, the deterministic pull, side-effect transitions). Backend anchor internal/board. Spec 02 §5.
---

# Board mechanism (doc 02 §5)

## Functional Requirements

**Responsibility.** The authoritative state of one project's board — tickets, columns,
zones, sandbox bindings — plus the mechanical rules that govern it: invariants, the
deterministic pull, and the side-effect transitions from `01` §5. This is the single
source of truth; nothing else mutates board state directly.

**Interface — the Board API.** The operations the brain (§6) and the pull system call to
mutate board state: create ticket, shape / mark-ready, move ticket (firing the `01` §5 side
effects), send-to-agent, accept-to-done. Each operation specifies what it returns, what
events it emits, and its authority.

**Dependencies.** State store engine (Postgres — `01`/§3); the Amika integration (§8) for
side effects that dispatch or instruct agents.

**Open decisions — resolved in `docs/specs/03-board-mechanics.md` (status: proposed).**
- [x] Persistence schema & entities → 03 §2, §8: single five-value ticket `state`
      (column/zone derived); sandboxes are capacity slots with free/busy derived.
- [x] Atomic WIP cap → 03 §3 (I2): structural — N sandbox rows + partial unique index
      `one_active_ticket_per_sandbox`; no counter.
- [x] Deterministic pull → 03 §5: idempotent `RunPull` triggered by transactional
      `pull.evaluate` outbox entries (from MarkReady / AcceptToDone); race-free via
      `FOR UPDATE SKIP LOCKED` + the I2 index as backstop.
- [x] Concurrency / locking → 03 §6: one op = one short READ COMMITTED transaction;
      lock-then-check; DB constraints as backstop.
- [x] Side-effect transactionality → 03 §7: transactional outbox — recorded atomically,
      executed after commit, at-least-once with the outbox id as idempotency key. Board
      holds **no Amika port** (supersedes 02 §5's topology sketch).

## How to work here

_(Accumulate as you work: how to run migrations, how to test the Board API against a fake
repository, the module boundary to stay inside — `backend/internal/board`, reachable only
through the Board API.)_

## Common footguns

_(Accumulate: mistakes agents predictably make in this module.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
