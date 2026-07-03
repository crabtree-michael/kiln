---
name: local-environment
description: Use when bringing the system up locally or figuring out where a service, its state, or its credentials live. v1 is local-only via Docker Compose (db, backend, frontend). Spec 02 §1, §3, §4.
---

# Working in the local environment (doc 02 §1, §3, §4)

## Functional Requirements

v1 runs **entirely locally via Docker Compose** — no cloud or production (§1). A developer or
agent brings the whole system up with a single `docker compose up`.

- **Services** (`docker-compose.yml`): `db` (Postgres — board state **and** the durable event
  queue, one engine), `backend` (Go monolith: api · runtime · brain · board · amika), and
  `frontend` (TS/React client).
- **Where state lives.** All authoritative state is in Postgres. The backend holds no
  authoritative state between events; a restart/deploy recovers by re-reading Postgres and
  draining the queue table.
- **Trust boundary.** `/backend` is the only trust boundary: it owns Postgres and all provider
  credentials (LLM, STT/TTS, push, Amika) and is the only writer of board state.

**Open decisions — TBD.**
- [ ] Fill in the `docker-compose.yml` services (currently `services: {}`) as surface areas
      are built.
- [ ] Runtime configuration and secret injection for the managed-API credentials.

## How to work here

_(Accumulate: the actual `docker compose up` invocation, service ports, how to seed/reset the
database, and how credentials are supplied locally — once the compose file is filled in.)_

## Common footguns

- Assuming a cloud/production target — v1 is local-only (§1); hosting is future work.
- Storing authoritative state anywhere but Postgres.

## Potential gotchas

_(Accumulate: non-obvious traps in the local setup.)_
