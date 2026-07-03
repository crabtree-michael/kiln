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
the agent-runtime module 05) and `human.message` (from POST /api/message — 07 A1; voice 09 later feeds the same seam). Client-facing contract: the live
connection that pushes board updates and the endpoints the client calls. Message / event
schemas.

**Dependencies.** Durable queue (Postgres queue table — §3); brain (§6); board (§5);
notifications (§10); agent runtime (§8, 05).

**Open decisions — resolved in `docs/specs/04-runtime-and-api.md` (status: proposed).**
- [x] Delivery semantics → 04 §3: at-least-once, execute-then-mark; backoff
      `min(1s × 2^(attempts−1), 60s)`, 8 attempts, per-topic dead-letter actions.
      Single writer → 04 §4: one serial event-worker goroutine, `id` order.
- [x] Deploy-safe recovery → 04 §5: no recovery code path — restart just re-finds
      `pending` rows; nudge channel + 1 s poll fallback for wakeup.
- [x] Live-connection transport → 04 §7: SSE (server→client) + plain HTTP POST
      (client→server); absolute snapshots, reconnect = fresh snapshot, no replay.
- [x] Event serialization → 04 §4: turn-completed and human-message events share the `events`
      table and serialize by insertion (`id`) order; outbox drains on its own serial
      worker. Queue DDL (both tables + delivery-state columns) → 04 §2.

## How to work here

**Scaffold layout** (stubs return `errNotImplemented`; every contract is in the doc comments):

```
backend/internal/runtime/
  runtime.go    package doc — the two queues, delivery ownership split vs board
  queue.go      QueueName · EventType · Entry/Event · retry constants (backoff, MaxAttempts=8)
  store.go      Store port (InsertEvent, ClaimNextDue, MarkDone/Retry/Dead) · Clock
  worker.go     Worker — serial drain loop, Nudge() (implemented), Handler/DeadLetter types
  service.go    Service — EnqueueEvent + the executor ports: Brain, Puller, Blocker,
                AgentRuntime (Send/Release — 05 §2.1), Notifier, SnapshotPusher
  postgres/     store adapter stub
    migrations/ 0001_events.sql (04 §2; outbox DDL lives in board's 0002_outbox.sql)
backend/internal/api/
  api.go        package doc — thin handlers, shapes come from /schema
  routes.go     Server — GET /api/stream · GET /api/board · POST /api/message · GET /api/messages (04 §7, 07 §4);
                ports: BoardReader, EventEnqueuer
  hub.go        Hub — SSE fan-out; implements runtime.SnapshotPusher
backend/cmd/kiln/
  main.go       composition root (04 §8, D9) — wiring diagram in the package doc
```

- Build/check from `/backend`: `gofmt -l . && go vet ./... && go build ./...`.
- The runtime consumes the board through the narrow `Puller`/`Blocker` ports it names, not
  `*board.Service` directly; adapt at the composition root (02 §2 — services depend on ports).
- Unit-test the `Worker` against fake `Store`/`Handler`s with the `Clock` interface — the
  backoff schedule must be testable without sleeping (04 §9).

**07 additions (proposed):** the runtime owns the persisted transcript — `messages` table
(append user row + enqueue event in one transaction; Say port = append kiln row + SSE
push; ConversationReader port feeds the brain's context). notify.send executor is a
structured log line until 10 lands.

## Common footguns

_(Accumulate: mistakes agents predictably make in these modules.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
