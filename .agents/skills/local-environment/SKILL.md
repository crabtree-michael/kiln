---
name: local-environment
description: How to run Kiln locally — the Docker Compose stack (Postgres + backend + frontend), ports, env/secrets, and per-surface dev servers. Use when bringing the system up, debugging a service that won't start, or wiring env vars.
---

# Running Kiln locally

**Spec:** `docs/specs/02-initial-technical-architecture.md` §1, §4. v1 is
**local-only** — no cloud, no IaC. The whole system runs on one machine.

## Bring the whole system up

```bash
cp .env.example .env      # fill in provider keys as surface areas need them
make up                   # docker compose up --build
make down                 # tear down (removes volumes)
```

Services and ports:

| Service  | Port | What |
| -------- | ---- | ---- |
| db       | 5432 | Postgres — board state **and** the durable event queue (02 §3) |
| backend  | 8080 | Go monolith (api·runtime·brain·board·amika) |
| frontend | 5173 | Vite dev server (proxies `/api` → backend) |

## Per-surface dev (faster inner loop)

```bash
cd frontend && pnpm install && pnpm dev     # http://localhost:5173
cd backend  && go run ./cmd/kiln            # needs DATABASE_URL
```

## Secrets (02 §12)

Provider credentials (LLM, STT/TTS, Amika, push) live in `.env`, injected by
Compose. **Never commit `.env`** — only `.env.example` is tracked. The backend is
the single trust boundary; the client never holds credentials.

## State & recovery

All authoritative state is in Postgres, including the queue. Restarting `backend`
recovers by re-reading durable state / draining the queue table (`01` §8) — you do
not lose in-flight work to a restart.

## Gotchas

- First `make up` builds the Go image and installs frontend deps; it's slow once,
  fast after (volumes cache `node_modules`).
- If `backend` exits immediately, check `DATABASE_URL` and that `db` is healthy
  (Compose waits on the healthcheck).

## Keep this skill current

Record new services, ports, env vars, and startup gotchas here.
