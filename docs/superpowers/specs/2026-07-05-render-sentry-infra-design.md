# Spec-10 slice: Render hosting + Sentry observability (phases 2–3)

**Date:** 2026-07-05
**Status:** In implementation (autonomous build)
**Parent:** `docs/specs/10-infrastructure.md` — this implements phases 2 (Production) and
3 (Observability). Phases 1 (CI gate) and 4 (sentinel/backups/ops) are out of scope.

## Decisions (some reverse spec 10 — recorded here as deltas)

| # | Spec 10 says | This slice does | Why |
| - | --- | --- | --- |
| a | Observability = Sentry **and** Grafana, one tool per signal (§6) | **Sentry only** — errors + traces + logs all in Sentry | one user; Grafana's metrics dashboards are the low-value signal at this scale; Sentry now has Logs + tracing |
| b | otel-go → OTLP export to Grafana (§6) | **native `sentry-go` tracing** | Sentry-only; no OTel SDK. Trade-off: loses the "swap exporter to add Grafana later" hatch — re-adding Grafana would mean adding OTel instrumentation then |
| c | `otelpgx` on the pgx pool (§6) | **skip DB-driver spans**; spans only around brain dispatch + outbox delivery | backend is `database/sql`+`lib/pq`, not pgx — otelpgx doesn't apply |
| d | Render auto-deploy **off**, CI is the only deploy trigger (§4) | **auto-deploy on `main`**, no CI gate | user's call. Property lost: failing tests / broken logic reach prod; only a bad *migration* self-gates via the health check. Mitigation deferred (add gate-only `ci.yml` later) |
| e | Frontend = separate Render static site + `/api/*` rewrite; verify SSE (§3, D2) | **embed `frontend/dist` in the Go binary** (D2's fallback, taken as primary) | same-origin is structural, SSE runs on the same server → the D2 rewrite-proxy SSE risk disappears; 2 Render resources not 3 |
| f | `SENTRY_DSN` (single) | `SENTRY_BACKEND_DSN` + `SENTRY_FRONTEND_DSN` (user's env names) | two Sentry projects: `kiln-backend`, `kiln-frontend` |

Setup note: the user's original `SENTRY_BACKEND_DSN` pointed at a non-existent project;
the `kiln-backend` project was created during setup and `.env` updated to its real DSN.

## Phase 2 — Production hosting (Render, 2 resources)

- **`/healthz`** — new backend handler, mounted outside `/api`. Returns `200` +
  `{status:"ok"}` when the process is up and `db.PingContext`/`SELECT 1` answers;
  `503` otherwise. Render health check + uptime target + first-curl diagnostic.
  Matches the existing (unimplemented) `openapi.yaml:24` `Health` schema.
- **Frontend embedding** — `//go:embed all:dist` in `backend/internal/web` serves the
  built client: `/` (SPA, `index.html` fallback for `/` and `/debug`), while
  `/api/*`, `/api/stream`, and `/healthz` keep their handlers. A committed placeholder
  `index.html` keeps `go build ./...` green locally; the Docker build overwrites `dist`
  with the real build.
- **`backend/Dockerfile`** — gains a `node:22-alpine` first stage: `pnpm install` +
  `pnpm build` (env `VITE_SENTRY_DSN` baked in, `@sentry/vite-plugin` uploads source
  maps via `SENTRY_AUTH_TOKEN`), output copied to `backend/internal/web/dist` before the
  Go stage embeds + builds. `VERSION` build arg reused as the Sentry `release`.
- **`render.yaml`** — web service (Docker, `healthCheckPath: /healthz`, region
  `virginia` / US-East, Starter) + managed Postgres 16 (Basic). `DATABASE_URL` via
  `fromDatabase`. Auto-deploy on `main`. No static-site resource.

### Embed contract (shared by the backend + Dockerfile work)
- Embed dir: `backend/internal/web/dist/`, placeholder `index.html` committed.
- `backend/internal/web/embed.go`: `//go:embed all:dist`, exports an `http.Handler`
  serving the SPA with `index.html` fallback for non-asset, non-`/api`, non-`/healthz`
  paths.
- Frontend build output: `frontend/dist/` from `pnpm build`.
- `@sentry/vite-plugin` config: `org:"macmail"`, `project:"kiln-frontend"`,
  `url:"https://de.sentry.io"`, `authToken: process.env.SENTRY_AUTH_TOKEN` — uploads
  only when the token is present (no-op locally).

## Phase 3 — Observability (Sentry, both sides)

- **Backend errors:** `sentry-go` init early in `main()` (before the `DATABASE_URL`
  early-return so panics report even in idle mode); deferred `sentry.Flush` on the
  existing 15s shutdown path; `recover()`→Sentry in the two queue workers + agent loop.
- **Backend HTTP + traces:** `sentryhttp` wraps the mux handler at the `http.Server`
  construction (`wiring.go`). **Must preserve `http.Flusher`** so the SSE board stream
  (`hub.go`) keeps flushing — verify, else exclude `/api/stream`. Manual
  `sentry.StartSpan` around brain dispatch (`runtime/service.go` `handleEvent`) and
  outbox delivery (`handleOutbox`, keyed on `e.Kind`).
- **Backend logs:** slog→Sentry-Logs handler composed into `obs.NewLogger`, carrying
  the existing `turn_id` as a tag. Same DSN, `enableLogs`.
- **Frontend:** `@sentry/react` init in `main.tsx` before `createRoot`; React-Router
  integration; the app's first `ErrorBoundary` around the routes; browser tracing.
  DSN from `import.meta.env.VITE_SENTRY_DSN`. PWA/service-worker: ensure source maps
  aren't precached and the SDK isn't broken by the SW.
- **Local = no-op:** unset DSN disables both SDKs; `make up` and tests unchanged.
- `.env.example` gains `SENTRY_BACKEND_DSN`, `SENTRY_FRONTEND_DSN` (names only).

## Env → Render mapping (autonomous provisioning)

| From `.env` | Render | Notes |
| --- | --- | --- |
| `SENTRY_BACKEND_DSN` | runtime env | |
| `SENTRY_FRONTEND_DSN` → `VITE_SENTRY_DSN` | build-time | baked into bundle |
| `SENTRY_AUTH_TOKEN` | build-time | source-map upload only |
| `ANTHROPIC_API_KEY`, `AMIKA_*`, `ASSEMBLYAI_API_KEY`, `GITHUB_AUTH_TOKEN`, `GITHUB_REPO_URL`, `KILN_LOG_LEVEL` | runtime env | app needs these in prod |
| `DATABASE_URL` | from Render managed Postgres | not from `.env` |
| `RENDER_API_KEY` | **excluded** | never inject the meta-key |

## Verification (acceptance)
- `curl /healthz` → 200; stop DB → 503.
- Local: forced backend panic → appears in Sentry `kiln-backend`; forced frontend error
  → symbolicated in `kiln-frontend`; a slog line → Sentry Logs with `turn_id`.
- `make check` + `make schema-verify` green before merge.
- Prod: push `main` → Render auto-deploys → `/healthz` green → **board + SSE load from a
  phone** at the public URL (the one human-only check).

## Status (2026-07-05) — deployed

Live at **https://kiln-iubn.onrender.com** (Render Virginia). Resources:
`srv-d953nmcvikkc73d8aq60` (web) + `dpg-d953mf8js32c73fc5ogg-a` (Postgres 16,
basic_256mb). Provisioned via the Render API (headless), not a Blueprint apply.
Verified in prod: `/healthz` 200, embedded SPA, `/debug` fallback, `/api/stream`
SSE. Backend Sentry ingestion confirmed end-to-end (`kiln-backend`). App honors
Render's `PORT`.

## Known follow-ups
- **Frontend source maps not uploading** — the Render build runs `@sentry/vite-plugin`
  but the upload 401s: the provided `SENTRY_AUTH_TOKEN` authenticates (403 on the org
  API = recognized) but lacks upload scope. Frontend errors still *capture*; stack
  traces stay minified until a token with `project:releases`+`project:write` is set in
  `.env` + Render. Non-fatal (build succeeds). Expected to be resolved in the US-Sentry
  migration below.
- **Sentry is EU (`de`), app is US** — org region is immutable; user will create a
  US-region Sentry org later (new DSNs/tokens; update `.env`, Render env, and the
  hardcoded `org`/`project`/`url` in `frontend/vite.config.ts`).
- **Sentry `release` is `dev`** — no `VERSION` build arg is passed; wire
  `RENDER_GIT_COMMIT` for release correlation.
- Setup created the `kiln-backend` Sentry project (the original `SENTRY_BACKEND_DSN`
  was stale) and corrected `.env`.

## Out of scope (phase 4)
`sentinel.yml`, `backup.yml`, healthchecks.io, ntfy, `production-ops` skill,
`mcp-grafana`, any CI gate.
