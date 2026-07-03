---
name: amika-integration
description: Use when working in the Amika module — the bridge to the agent platform that dispatches, instructs, and receives results from sandboxed agents. Held as an interface + mock until Amika's real API lands. Backend anchor internal/amika. Spec 02 §8.
---

# Agent-platform integration — Amika (doc 02 §8)

## Functional Requirements

**Responsibility.** Bridge to the agent platform: dispatch an agent into a sandbox, instruct
a running / blocked agent, receive a turn's result, and expose the queue the runtime
triggers off of (`01` §4). Recovers safely across deploys.

**Interface.** The dispatch / instruct / receive-result / queue contract. Defined as a Go
**interface with a mock implementation** until Amika's real API/SDK is in hand (`01` §11),
so the rest of the system can be built and tested against the mock.

**Dependencies.** Amika's real API (**deferred** — stays an interface); board (§5) for the
sandbox-binding lifecycle.

**Open decisions — TBD → §8.**
- [ ] The concrete interface shape and auth.
- [ ] How a turn result arrives (webhook vs poll) and maps to a runtime event.
- [ ] Sandbox lifecycle ↔ board binding (acquire on pull, hold through Blocked, release on
      Done).
- [ ] Retry and dispatch-failure surfacing (`01` §8).
- [ ] What the mock must simulate to make the end-to-end loop testable without real agents.

## How to work here

**Work behind the interface.** Amika's real API is not landed — never hand-code against it.
All the rest of the system depends on the interface + mock.
_(Accumulate: how to run the mock, the module boundary — `backend/internal/amika`.)_

## Common footguns

- Coding against a real Amika API/SDK that does not exist yet instead of the interface + mock.

_(Accumulate more as you work.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
