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
  auth_handlers.go       GET /auth/github/connect·/callback, POST /auth/logout
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
`POST /api/feed/{id}/dismiss`); ticket actions (`POST /api/tickets/{id}/accept|delete`, and
the two direct writes `POST /api/tickets/{id}/sandbox|text` — see below);
`POST /api/voice/token`; the push group (`POST`/`DELETE /api/push/subscribe`,
`GET /api/push/key`, `GET`/`PUT /api/push/mode`); the identity group (see below); dev routes
(`/api/dev/*`, gated); **`GET /healthz`** (liveness + DB ping, 200 ok / 503 degraded, mounted
outside `/api`); and the SPA `/` catch-all. Every `/api/*` handler is wrapped in `withProject`
(11 phase 2) — session-authenticated and project-scoped before it runs.

**The direct board writes (the D5 exceptions).** Everything the client does to the
*board* goes through the brain — Accept and Delete are synthesized human messages, not
mutations. Two ports break that, each optional so the exception stays visible in the type
surface rather than hiding as a method on `BoardReader`:

| Route | Port | Board op | Why it skips the brain |
| --- | --- | --- | --- |
| `POST /api/tickets/{id}/sandbox` | `TicketSandboxController` | `SetKeepSandbox` | A toggle; an LLM round-trip would be slow and non-deterministic for no gain. |
| `POST /api/tickets/{id}/sandbox/kill` | `TicketSandboxController` | `KillSandbox` | A manual override for a wedged sandbox — waiting on the orchestrator *is* the problem it solves, and routing it through the brain puts that wait back. |
| `POST /api/tickets/{id}/sandbox/reassign` | `TicketSandboxController` | `ReassignSandbox` | Same as the kill; the board rebinds and re-briefs itself, so there is no decision for the brain to make. |
| `POST /api/tickets/{id}/text` | `TicketTextEditor` | `ShapeTicket` | An LLM pass is the *thing being avoided* — dictating a wording change and letting the brain rewrite the ticket is what drifts from what the user meant. |

The three sandbox routes share **one** port and one `EnableTicketSandbox` because they are one
surface (per-ticket sandbox control), so a deployment can't end up with a kill route over a
nil setter. Ports are `Enable…`-gated (a nil port leaves the routes unmounted) and wired to
`boardSvc` directly in `enableServerRoutes`. Keep the *port* count at two: a third "just this
once" write is how D5 stops meaning anything. If you add one, it needs its own port, its own
`Enable…`, and a reason in the port's doc comment that is about *this* operation, not about
convenience.

The sandbox overrides are the other handlers that map board preconditions to **409**:
`*board.ErrInvalidTransition` (no worker bound → nothing to kill/move) and, for reassign,
`board.ErrNoFreeWorker` (every slot busy → nowhere to move to). Both are states the user can
act on, so neither is a 500. They take **no request body** — the ticket in the path is the
whole request.

The text route is the only handler that maps a board precondition to **409**: `ShapeTicket`
accepts only `shaping`/`ready`, so a ticket past the backlog gets `*board.ErrInvalidTransition`
→ 409, not a 500 (the client can then say why the edit didn't take). Its body-level rejections
run *before* the write — neither field present, or a whitespace-only title — because an empty
patch would still fan a `board.updated` out to every open client, and a nameless ticket has no
identity on the board or in the feed. An empty **body** is a legal edit; an empty title is not.

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
  githubapi/        GitHub client — OAuth App flow (client.go) + GitHub App
                    installation flow (app.go: install URL, app JWT, mint,
                    installation repo listing)
  verify/           live connection checks (anthropic/amika/repo) — 11 §4
backend/internal/api/
  auth_handlers.go       GET /auth/github/connect·/callback, POST /auth/logout
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
- **GitHub App migration, in progress** (`docs/superpowers/specs/2026-08-04-github-app-repo-selection-design.md`).
  Kiln is moving off the OAuth App (blanket `repo` scope) onto a GitHub App, so the user picks
  which repos Kiln may reach. `githubapi/app.go` is landed and unused so far: `InstallURL`,
  `MintInstallationToken` (RS256 app JWT → a **1-hour** installation token), and
  `ListInstallationRepos`. Three things to know before touching it:
  - **The adapter is stateless.** A mint is a network call; the caller caches until shortly
    before `ExpiresAt`. That cache belongs in `identity`, not here — same as `ExchangeCode`
    not remembering tokens.
  - **Both halves are live during the migration.** A client with no `AppID`/`AppPrivateKey`
    still serves every OAuth call and returns `ErrNoAppCredentials` only from the mint. Do not
    "fix" that by requiring App config in `New` — the boot gate is `cmd/kiln`'s job.
  - **`ListInstallationRepos` takes a USER token, not the minted one**, and hits
    `/user/installations/{id}/repositories`. This is deliberate: the installation-wide listing
    would offer an org member repos they cannot themselves reach.
- **Whole surface is project-scoped now (11 phase 2).** `withProject` authenticates the
  session and resolves the caller's project before every `/api/*` handler runs, so identity is
  no longer confined to `/dashboard` — the board/chat (`/app`) and `/debug` are session-gated
  too. Only the public marketing/onboarding routes and `/healthz` sit outside the gate.

## Common footguns

_(Accumulate: mistakes agents predictably make in these modules.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
