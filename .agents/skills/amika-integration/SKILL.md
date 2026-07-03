---
name: amika-integration
description: Work in the Kiln amika module — the agent-platform bridge (dispatch/instruct/receive-result/queue), held as an interface with a mock until Amika's real API lands. Use when editing backend/internal/amika, the Amika port/mock, or sandbox-lifecycle-to-board-binding logic.
---

# Amika integration (backend/internal/amika)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §8, realizing
`docs/specs/01-initial.md` §4. Amika's real API is **DEFERRED** (`01` §11).

## Responsibility

Bridge to the agent platform: dispatch an agent into a sandbox, instruct a
running/blocked agent, receive a turn's result, and expose the queue the runtime
(§7) triggers off of. Recovers safely across deploys.

## The rule that shapes everything here

Amika is defined as an **interface (port) with a mock implementation**. The rest
of the system is built and the end-to-end loop tested against the **mock** — no
real agents. Do **not** couple other modules to a concrete Amika client; depend on
the interface. When the real API/SDK lands, it becomes a second adapter behind the
same port.

## Where the code lives

`backend/internal/amika`, layered (02 §2): the dispatch/instruct/receive-result
contract (the port) → services (sandbox-lifecycle ↔ board-binding, failure
surfacing) → infra (the **mock adapter** today; the real client later).

## What this area still has to decide (02 §8)

- The concrete interface shape and auth.
- How a turn result arrives (webhook vs poll) and maps to a runtime event.
- Sandbox lifecycle ↔ board binding: acquire on pull, hold through Blocked,
  release on Done (coordinate with board §5).
- Retry + dispatch-failure surfacing (`01` §8).
- **What the mock must simulate** to make the e2e loop testable without agents.

## Run the gate for this area

```bash
cd backend && go test ./internal/amika/...
```

## Gotchas

- The mock is load-bearing: the whole e2e test (§14) depends on it faithfully
  simulating dispatch → turn-completed. Keep it honest.

## Keep this skill current

When the real Amika docs land, record the mapping decisions and auth here.
