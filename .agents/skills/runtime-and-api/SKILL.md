---
name: runtime-and-api
description: Work in the Kiln runtime + api modules — the durable, deploy-resumable event loop (drains a Postgres queue, drives the brain once per event) and the client-facing HTTP + live-connection surface. Use when editing backend/internal/runtime or backend/internal/api, the event queue, or client endpoints.
---

# Runtime + API (backend/internal/runtime, backend/internal/api)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §7, realizing
`docs/specs/01-initial.md` §7–§8.

## Responsibility

- **runtime**: the durable service shell. Receives the two event types —
  `agent-turn-completed` (from Amika §8) and `human-voice-input` (from voice §9)
  — and drives the brain (§6) **once per event**. Wakes on events, not a timer.
- **api**: the only surface the untrusted client touches — HTTP endpoints + the
  live connection (WS/SSE) that pushes board updates.

## Where the code lives

Layered (02 §2): ingestion/handlers → services (the event loop, ordering) → infra
(Postgres-backed durable queue behind a port). Services depend on the queue,
brain, board, amika and notifications only through **injected ports**.

## Key invariants

- **Deploy-resumable**: all authoritative state (incl. the queue) is in Postgres.
  Recovery = drain the queue table on startup, never trust in-process memory
  (`01` §8).
- **Single writer per project**: turn-completed and voice events are serialized
  against each other so two events never mutate one board concurrently.

## What this area still has to decide (02 §7)

- Queue delivery semantics (at-least-once vs exactly-once) + ordering guarantees.
- How the single-writer-per-project constraint is enforced.
- Live-connection transport (WS vs SSE) — **shared decision with web-client §11**.
- Event/message schemas (client-facing ones live in `/schema`).

## Run the gate for this area

```bash
cd backend && go test ./internal/runtime/... ./internal/api/...
```

## Gotchas

- At-least-once delivery means handlers must be **idempotent** (pairs with brain
  idempotency, §6).
- Client-facing request/response shapes are generated from `/schema` — never
  hand-write them (see the `wire-contract` skill).

## Keep this skill current

Record queue semantics, the single-writer mechanism, and the WS-vs-SSE decision.
