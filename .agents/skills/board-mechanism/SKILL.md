---
name: board-mechanism
description: Work in the Kiln board module — authoritative board state (tickets, columns, zones, sandbox bindings), invariants, the deterministic pull, and side-effect transitions. Use when editing backend/internal/board, the Board API, board schema/migrations, WIP-cap or pull logic.
---

# Board mechanism (backend/internal/board)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §5, realizing
`docs/specs/01-initial.md` §5. Read both before changing behavior.

## Responsibility

The authoritative state of one project's board and the mechanical rules over it:
invariants, the deterministic pull, and the side-effect transitions from `01` §5.
This module is the **single source of truth** — nothing else mutates board state.

## Where the code lives

`backend/internal/board`, layered (02 §2): thin handlers → services (business
logic over Ticket / Sandbox / column / zone / event) → infra (Postgres repository
behind a port). Services depend on the repo **only through the port interface**,
never on `*sql.DB`.

## Interface — the Board API

The operations the brain (§6) and the pull system call: create ticket,
shape/mark-ready, move ticket (firing `01` §5 side effects), send-to-agent,
accept-to-done. Each returns a result and emits events; document what it emits.

## What this area still has to decide (02 §5)

- Persistence schema; each entity's fields, valid states, invariants.
- WIP cap (= available sandboxes) enforced **atomically**.
- The deterministic pull made **race-free** (a ready ticket exists AND a sandbox
  is free) — single transaction, or a locking model that can't double-pull.
- Whether side effects are transactional with the state change or fire after
  commit. Board mutation + enqueued events should commit in **one Postgres
  transaction** (02 §3).

## Run the gate for this area

```bash
cd backend && go test ./internal/board/... && golangci-lint run ./internal/board/...
```

## Gotchas

- Never let another module write board state — expose an operation instead.
- Test services against an **in-memory repo fake**, not real Postgres (02 §2).

## Keep this skill current

When you learn a board invariant, a migration convention, or a pull-race
subtlety, write it here so the next agent inherits it (AGENTS.md).
