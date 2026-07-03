---
name: runtime-and-api
description: Use when working in the runtime / event-queue / client-facing API — the durable, deploy-resumable service shell that ingests events, drives the brain once per event, and faces the client. Backend anchors internal/runtime and internal/api. Spec 02 §7.
---

# Orchestrator API + event queue / runtime (doc 02 §7)

## Functional Requirements

**Responsibility.** The durable, deploy-resumable service shell that receives events, drives
the brain (§6) once per event, and faces the client. Implements the `01` decision that the
orchestrator **wakes on events, not a timer**.

**Interface.** Event ingestion for the two `01` event types — `agent-turn-completed` (from
Amika §8) and `human-voice-input` (from voice §9). Client-facing contract: the live
connection that pushes board updates and the endpoints the client calls. Message / event
schemas.

**Dependencies.** Durable queue (Postgres queue table — §3); brain (§6); board (§5);
notifications (§10); Amika (§8).

**Open decisions — TBD → §7.**
- [ ] Queue technology and delivery semantics (at-least-once vs exactly-once), ordering
      guarantees, and how a single-writer-per-project constraint is kept.
- [ ] Deploy-safe recovery: draining a durable queue rather than trusting in-process state
      (`01` §8).
- [ ] Live-connection transport choice (shared with the client §11: WebSocket vs SSE).
- [ ] How turn-completed and voice events are serialized against each other.

## How to work here

_(Accumulate: how to run the runtime locally, how to drive the event loop against fakes, the
two module boundaries — `backend/internal/runtime` and `backend/internal/api`.)_

## Common footguns

_(Accumulate: mistakes agents predictably make in these modules.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
