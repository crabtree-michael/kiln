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
  service.go       Service — GitHub App login + allowlist, sliding-window sessions,
                    Me/UpdateSettings/UpsertProject/Verify
  installation.go   TokenSource + the per-installation mint cache (singleflight,
                    refresh-before-expiry)
  cipher.go         AES-GCM envelope for secrets-at-rest (KILN_SECRETS_KEY)
  entities.go/store.go   User/Project/Settings + the Store port
  postgres/         Store adapter + migrations (users, projects, settings, sessions)
  githubapi/        GitHub client — the App's user-authorization half (client.go:
                    code exchange, /user, repo listing) + its installation half
                    (app.go: install URL, app JWT, mint, installation repo listing)
  verify/           live connection checks (anthropic/amika/repo) — 11 §4
backend/internal/api/
  auth_handlers.go       GET /auth/github/connect·/callback, POST /auth/logout
  identity_handlers.go   GET /api/me, PUT /api/settings, PUT /api/project,
                          POST /api/settings/verify, POST /api/dev/session (dev-only)
```

- **Env gating** (`buildIdentity` in `cmd/kiln/wiring.go`): identity is **all-or-nothing** and
  needs **all six** of `KILN_GITHUB_APP_ID`, `_SLUG`, `_PRIVATE_KEY`, `_CLIENT_ID`,
  `_CLIENT_SECRET`, and `KILN_SECRETS_KEY` (the list lives once, in `identityEnvVars`). Any
  missing → `EnableIdentity` is never called, so `/auth/*` and `/api/me` etc. are simply
  **absent** (404, not 401), and the warning names the missing vars. All absent is silent —
  that is a deployment that never turned identity on. A malformed `KILN_SECRETS_KEY` or App
  private key fails the boot **hard** rather than silently running with broken crypto or a
  connect flow whose every mint fails.
- **The App private key is base64-encoded** (`KILN_GITHUB_APP_PRIVATE_KEY`). GitHub emits a
  multi-line PEM and multi-line secrets do not survive a hosting provider's environment;
  `parseAppPrivateKey` decodes it once at boot, accepting a raw PEM too.
- **Dev session mint**: `POST /api/dev/session` (gated by `KILN_DEV_ENDPOINTS=1` **and**
  identity enabled) signs in — or creates — a user from a plain `{github_login}` body and
  mints a real session cookie, bypassing the OAuth dance. This is how e2e establishes an
  authenticated session (`tests/tests/dashboard-config.spec.ts`); never part of `/schema`,
  never mounted without dev endpoints on.
- **Write-only secrets**: `PUT /api/settings` accepts raw secret values but `GET /api/me`
  only ever returns a `{set, tail}` status per secret (encrypted at rest via `cipher.go`,
  fingerprint/tail derived at write time) — the plaintext never round-trips over the wire.
- **Login runs through a GitHub App** (`docs/superpowers/specs/2026-08-04-github-app-repo-selection-design.md`;
  the OAuth App it replaced is gone as of 2026-08-06, and every session was invalidated by
  migration `0009` rather than migrated). The user picks on GitHub's own chooser which repos
  Kiln may reach. Five things to know before touching it:
  - **The credential is a function, not a value.** `identity.TokenSource` resolves per
    git/`gh` invocation because an installation token dies within the hour. `RuntimeConfig`
    carries `GitHubToken TokenSource`; `repo.Config.Token` and `verify.VerifyRepo` take one.
    Never hoist a resolved token into a longer-lived variable.
  - **The adapter is stateless.** A mint is a network call; `identity.InstallationTokens`
    caches until `refreshMargin` before `ExpiresAt`, with a per-installation singleflight so
    a waking fleet costs one round trip. That cache belongs in `identity`, not `githubapi` —
    same as `ExchangeCode` not remembering tokens.
  - **A rejected mint is recorded, not just returned.** `githubapi.ErrInstallationUnavailable`
    (401/403/404) fires `Service.recordInstallationRevoked`, which is what flips the card to
    `needs_reconnect`. A transport failure must NOT — a network blip is not a revoked grant.
    This is also why `GET /api/me` stays a pure DB read: what GitHub thinks is learned when
    a credential is *used* and written down for the read to find.
  - **`ListInstallationRepos` takes a USER token, not the minted one**, and hits
    `/user/installations/{id}/repositories`. This is deliberate: the installation-wide listing
    would offer an org member repos they cannot themselves reach.
  - **A stored token with no installation still works** — a hand-typed PAT through
    `PUT /api/settings`, or the deployment's bootstrap `GITHUB_AUTH_TOKEN`. It reads as
    `unknown` (stored, not granted by Kiln), and writing one clears any installation so the
    card never claims a connection the runtime is not using.
- **The callback has two shapes** (`auth_handlers.go`). `code` + `installation_id` is the
  full identity path, state-checked. `installation_id` alone — someone installing from
  GitHub's own Apps page, who never passed through `/auth/github/connect` and so has no state
  cookie — attaches to the existing session instead; no session means a redirect to connect.
- **Whole surface is project-scoped now (11 phase 2).** `withProject` authenticates the
  session and resolves the caller's project before every `/api/*` handler runs, so identity is
  no longer confined to `/dashboard` — the board/chat (`/app`) and `/debug` are session-gated
  too. Only the public marketing/onboarding routes and `/healthz` sit outside the gate.

**The background loops are single-owner (`internal/leader`).** Render's zero-downtime deploy
runs the old and new instance side by side for **67–83 s of every deploy**, and both used to
run the full set of background loops — so both polled for the same pending work and both could
start an agent against the same sandbox and working tree. Five investigations
(`docs/root-cause-2026-08-02-*` … `-08-04-part5-*`) confirmed it; the fix landed 2026-08-04.

- `graph.startLoops` (`cmd/kiln/wiring.go`) runs the **four** loops — `runWorker(events)`,
  `runWorker(outbox)`, `runAgent`, `runSteward` — inside `leader.Elector.Run`. Anything else
  that polls or sweeps on a timer belongs **inside** `backgroundLoops`, not beside it.
- **The HTTP server is deliberately NOT gated.** A follower serves board/SSE/API normally; it
  just does no background work. Its writes still land (events queue up), the leader drains them.
- The lock is `pg_try_advisory_lock(leader.LockKey)` on a **pinned `*sql.Conn`**, not the pool:
  a lock taken on one pooled connection and released on another is a silent no-op. The Elector
  unlocks *before* returning the conn to the pool, and re-verifies via `pg_locks` every 5 s that
  its own backend still holds it — acquiring once at boot is not enough, because a dead
  connection drops the lock silently. Losing it cancels the loops' context.
- Handoff is fast because a session-scoped lock dies with the session: measured **5 ms** to
  release on SIGTERM (successor picks up within its 3 s retry) and **53 ms** end-to-end on
  SIGKILL. No lease, no heartbeat, no migration — advisory locks are session state, not schema.
- Log with `leader.acquired` / `leader.standby` / `leader.released` / `leader.lost`, and every
  record now carries `instance` (`obs.InstanceID()`, stamped on the default logger in `main.go`)
  so a handoff is legible across two instances' interleaved streams.
- `KILN_LEADER_LOCK=0` is the operator escape hatch. It restores the pre-lock behaviour — every
  instance polls — which is the collision itself. Never set it to make a test pass.
- **A leader lock is not a claim.** `agent` `stepStartTurn` still has no CAS
  (`docs/ticket-draft-turn-claim-cas.md`) and the event queue still gives a fresh row a 1 s
  visibility lease (`docs/ticket-draft-queue-visibility-timeout.md`). Both remain real
  single-instance bugs; the lock does not subsume either.

## Common footguns

- **Starting a new background loop with a bare `go …` in `graph.run`.** It would run on every
  instance, re-opening the duplicate-work window the leader lock closes. Add it to
  `graph.backgroundLoops` instead.

## Potential gotchas

- **Session-scoped advisory locks need direct Postgres connections.** Behind a
  transaction-pooling proxy (PgBouncer in transaction mode) they are meaningless, the Elector's
  periodic re-verification fails, and *no* instance leads — the symptom is "nothing ever starts
  working" rather than an error. Render's managed Postgres is a direct connection.
- **`leader.LockKey`'s high 32 bits must stay below 2^31.** Postgres stores a one-argument
  advisory key split across `classid`/`objid`, and the re-verification reassembles it with
  `(classid::bigint << 32) | objid::bigint`, which overflows above that.
  `TestLockKeyFitsPgLocksReassembly` guards it.
