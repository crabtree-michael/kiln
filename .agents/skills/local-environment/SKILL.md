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
- [ ] Runtime configuration and secret injection for the managed-API credentials beyond the
      current `.env` pass-through (compose reads `.env` at the repo root).

## How to work here

- **First-time setup:** `cp .env.example .env` at the repo root (compose reads it
  automatically; never commit the real `.env`). Keys may stay blank until a surface area
  needs them.
- **Bring it up:** `make up` (= `docker compose up --build`), or
  `docker compose up -d db backend` for just the backend stack. `make down` tears down
  **and deletes volumes** (`-v`) — it wipes Postgres data.
- **Ports:** Postgres `5432` (user/pass/db all `kiln`), backend `8080`, frontend dev
  server `5173`. Backend reaches the db at `postgres://kiln:kiln@db:5432/kiln?sslmode=disable`.
- **Reset the database:** `docker compose down -v && docker compose up -d db`.
- **Check health:** `docker compose ps` (db has a `pg_isready` healthcheck; backend waits
  on it) and `docker compose logs backend` (JSON logs; expect `"kiln starting"` then
  `"kiln serving" addr=":8080"`). There is **no `/healthz` route** yet (the wire schema
  declares it but the backend only serves `/api/*`); use `GET /api/board` as a readiness
  probe — it returns the empty board snapshot once migrations have run.
- **End-to-end test:** the live-stack suite lives in `/tests` (Playwright, drives the real
  web client). Bring the stack up on the cheap model (`KILN_BRAIN_MODEL=claude-haiku-4-5-20251001 make up`),
  **onboard a project for the test user once** (see the footgun below), then `make e2e`. See the
  `end-to-end-development` skill and `/tests/README.md`.

## Common footguns

- **A fresh stack has no project, so `/api/*` and the board are empty until you onboard one**
  (spec 11 multi-user). Every route is project-scoped; a signed-in user with no project gets a
  404 from `GET /api/board` and the client shows "connect a project to light the kiln" instead
  of the board. The e2e specs mint a dev session but do **not** create a project, so a fresh DB
  fails them at `expect(board).toBeVisible()`. Seed one **once per fresh DB** as a real user
  would — mint a cookie via `POST /api/dev/session {github_login:"e2e-user"}` (needs
  `KILN_DEV_ENDPOINTS=1`, which compose defaults on), then
  `PUT /api/settings {anthropic_api_key, amika_api_key, amika_claude_cred_id}` (read the values
  from `.env`; never print them) and `PUT /api/project {name, repo_url, worker_count:1-10}`.
  `GET /api/board` → 200 confirms it. `make down` deletes the DB volume, so re-seed after.
- Assuming a cloud/production target — v1 is local-only (§1); hosting is future work.
- Storing authoritative state anywhere but Postgres.
- The backend Dockerfile's `golang:X-alpine` build image must satisfy the `go` directive in
  `backend/go.mod` — bumping the toolchain in go.mod without bumping the Dockerfile breaks
  `docker compose build` with "go.mod requires go >= X".
- **Frontend proxy target is container-relative.** The client talks same-origin (`/api/...`,
  transport.ts) and the vite dev server proxies `/api` to the backend. In compose the backend
  is the `backend` **service**, not `localhost` — so the frontend service sets
  `KILN_PROXY_TARGET=http://backend:8080` (vite.config.ts reads it, default `localhost:8080`
  for a bare `pnpm dev`). Point it at `localhost` inside the container and every `/api` hop —
  board fetch *and* the SSE stream — 500s with ECONNREFUSED, so the board stays `reconnecting`.

## Potential gotchas

- **Migrations ship embedded.** Each `internal/*/postgres` package `go:embed`s its
  `migrations/*.sql`, and the composition root applies them at startup from the embedded FS —
  so the single static binary carries them (the distroless image has no source tree). Add a
  new `.sql` file and it's picked up automatically; there is no `KILN_MIGRATIONS_DIR` to set.
