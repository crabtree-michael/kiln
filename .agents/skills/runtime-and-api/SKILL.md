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

**Module layout** (fully implemented; every contract is in the doc comments):

```
backend/internal/runtime/
  doc.go        package doc — the two queues, delivery ownership split vs board
  queue.go      QueueName · EventType · Entry/Event · retry constants (BackoffBase/Cap, MaxAttempts=8)
  store.go      Store port (InsertEvent, ClaimNextDue, MarkDone/MarkRetry/MarkDead) · Clock
  worker.go     Worker — serial drain loop, Nudge(), Handler/DeadLetter types
  service.go    Service — EnqueueEvent + the executor ports: Brain, Puller, Blocker,
                AgentRuntime (Send/Release — 05 §2.1), Notifier, SnapshotPusher
  feed.go · notifications.go · transcript.go   the 07/10 additions (feed cards, notify.send, transcript)
  postgres/     store adapter
    migrations/ 0001_events.sql (04 §2; outbox DDL lives in board's 0002_outbox.sql), 0002+ since
backend/internal/api/
  doc.go        package doc — thin handlers, shapes come from /schema
  routes.go     Server — registers the whole HTTP surface (see below); ports BoardReader,
                MessagePoster, plus feed/activity/push/voice/identity ports
  auth_handlers.go       GET /auth/github/login·/callback, POST /auth/logout
  identity_handlers.go   GET /api/me, PUT /api/settings, PUT /api/project,
                          POST /api/settings/verify, POST /api/dev/session (dev-only)
  hub.go        Hub — SSE fan-out; implements runtime.SnapshotPusher
backend/cmd/kiln/
  main.go       entrypoint + composition-root package doc (04 §8, D9)
  wiring.go     the actual graph construction (buildGraph/buildIdentity/enableServerRoutes),
                alongside bootstrap.go · adapters.go
```

**Route surface (`routes.go`).** The original 04 §7 seam — `GET /api/stream` (now carries
four SSE events: `board`, `say`, `feed`, `activity`), `GET /api/board`, `POST /api/message`,
`GET /api/messages` — is now a subset of a much larger surface: `GET /api/activity`; the feed
group (`GET /api/feed`, `/api/feed/history`, `POST /api/feed/seen`, `/api/feed/dismiss-all`,
`POST /api/feed/{id}/dismiss`); ticket actions (`POST /api/tickets/{id}/accept|delete`);
`POST /api/voice/token`; the push group (`POST`/`DELETE /api/push/subscribe`,
`GET /api/push/key`, `GET`/`PUT /api/push/mode`); the identity group (see below); dev routes
(`/api/dev/*`, gated); **`GET /healthz`** (liveness + DB ping, 200 ok / 503 degraded, mounted
outside `/api`); and the SPA `/` catch-all. Every `/api/*` handler is wrapped in `withProject`
(11 phase 2) — session-authenticated and project-scoped before it runs.

- Build/check from `/backend`: `gofmt -l . && go vet ./... && go build ./...`.
- The runtime consumes the board through the narrow `Puller`/`Blocker` ports it names, not
  `*board.Service` directly; adapt at the composition root (02 §2 — services depend on ports).
- Unit-test the `Worker` against fake `Store`/`Handler`s with the `Clock` interface — the
  backoff schedule must be testable without sleeping (04 §9).

**07 additions (proposed):** the runtime owns the persisted transcript — `messages` table
(append user row + enqueue event in one transaction; Say port = append kiln row + SSE
push; ConversationReader port feeds the brain's context). notify.send executor is a
structured log line until 10 lands.

**Identity + tenancy (spec 11).** A sibling module, not part of runtime/board — `api`
consumes it through two ports (`Authenticator`, `AccountService` in `routes.go`), same
pattern as `BoardReader`. Phase 1 added the account surface; phase 2 made the whole app
project-scoped (`withProject`, `ProjectResolver`).

```
backend/internal/identity/
  service.go       Service — OAuth login + allowlist, sliding-window sessions,
                    Me/UpdateSettings/UpsertProject/Verify
  cipher.go         AES-GCM envelope for secrets-at-rest (KILN_SECRETS_KEY)
  entities.go/store.go   User/Project/Settings + the Store port
  postgres/         Store adapter + migrations (users, projects, settings, sessions)
  githubapi/        GitHub OAuth + user-info client
  verify/           live connection checks (anthropic/amika/repo) — 11 §4
backend/internal/api/
  auth_handlers.go       GET /auth/github/login·/callback, POST /auth/logout
  identity_handlers.go   GET /api/me, PUT /api/settings, PUT /api/project,
                          POST /api/settings/verify, POST /api/dev/session (dev-only)
```

- **Env gating** (`buildIdentity` in `cmd/kiln/wiring.go`): identity is **all-or-nothing** and
  needs **all three** of `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, and
  `KILN_SECRETS_KEY`. Any missing → `EnableIdentity` is never called, so `/auth/*` and
  `/api/me` etc. are simply **absent** (404, not 401). A malformed `KILN_SECRETS_KEY` (wrong
  length/encoding) fails the boot hard rather than silently running with broken crypto.
- **Dev session mint**: `POST /api/dev/session` (gated by `KILN_DEV_ENDPOINTS=1` **and**
  identity enabled) signs in — or creates — a user from a plain `{github_login}` body and
  mints a real session cookie, bypassing the OAuth dance. This is how e2e establishes an
  authenticated session (`tests/tests/dashboard-config.spec.ts`); never part of `/schema`,
  never mounted without dev endpoints on.
- **Write-only secrets**: `PUT /api/settings` accepts raw secret values but `GET /api/me`
  only ever returns a `{set, tail}` status per secret (encrypted at rest via `cipher.go`,
  fingerprint/tail derived at write time) — the plaintext never round-trips over the wire.
- **Whole surface is project-scoped now (11 phase 2).** `withProject` authenticates the
  session and resolves the caller's project before every `/api/*` handler runs, so identity is
  no longer confined to `/dashboard` — the board/chat (`/app`) and `/debug` are session-gated
  too. Only the public marketing/onboarding routes and `/healthz` sit outside the gate.

## Common footguns

_(Accumulate: mistakes agents predictably make in these modules.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
