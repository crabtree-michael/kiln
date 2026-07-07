# Kiln — Multi-User Transition (v1)

**Date:** 2026-07-05
**Status:** Implemented (phase 1 merged 2026-07-06; phase 2 — the tenancy flip — implemented 2026-07-07)
**Scope:** lifts the "single user" pin from `01`–`10`
**Relationship to `01`–`10`:** Amends `01` §10 and `10` §1's "multi-user anything" non-goal.
The module topology (`02` §2), wire-contract discipline (`02` §3), and hard gate (`02` §4)
are unchanged. `10`'s hosting stays: one Render instance, one Postgres — Kiln becomes
**one hosted, multi-tenant deployment**, not per-user installs. Signup is **GitHub OAuth
only**, gated by an **allowlist of GitHub usernames** (invite-only; §10, D1).

## 1. Purpose & scope

Today every provider credential is a deployment-wide env var (`docker-compose.yml`,
`cmd/kiln` `Config`) and every stateful table assumes one implicit tenant. This document
decides how Kiln becomes multi-user:

- **Identity**: GitHub OAuth, username allowlist, cookie sessions (§2).
- **Data model**: `users` / `projects` / `user_config` and where each config value
  lives; secret encryption (§3).
- **API surface**: new session-protected endpoints and their wire types (§4).
- **The dashboard** at `/dashboard`: the desktop onboarding + settings surface (§5).
- **Runtime tenancy** (phase 2): `project_id` threading, one brain per project (§6).
- **Migration & rollout**: bootstrap-from-env, dev/e2e auth, phase gates (§7).
- **Testing**, with cross-tenant isolation as the headline (§8).

**Two phases, one destination.** The design below is the end state, but it ships in two
strictly ordered phases (§10, D6):

- **Phase 1 — dashboard + auth, zero touch on the current app.** The app at `/` stays
  exactly as it is: unauthenticated, env-driven, single-tenant runtime, every existing
  endpoint open. Alongside it, `/dashboard` ships with GitHub sign-in, the allowlist,
  the new tables, encrypted config storage, and connection verification. In phase 1 the
  runtime does **not** read the stored config; env still drives it. The dashboard is a
  working config store proving out auth and onboarding ahead of the flip.
- **Phase 2 — the tenancy flip.** `project_id` threads through the stateful tables, the
  runtime builds one brain per project from stored config, the app's endpoints gain the
  session requirement, existing data migrates into the bootstrap account, and the e2e
  suites move to dev-session auth. Only then are users invited.

Phase 1 is the implementation contract of this spec; phase 2 is designed here (§6–§7)
and implemented behind its own plan once the dashboard is proven.

**Non-goals:** open signup, billing/quotas, teams or shared projects, multiple projects
per user (schema-ready, UX-deferred — §3), a desktop board/chat experience (the
dashboard is onboarding + settings only), and any auth provider dependency (§10, D2).

## 2. Identity & auth

**Sign-in: GitHub OAuth web application flow, self-implemented** (§10, D2). Two
endpoints and one token exchange:

- `GET /auth/github/login` — sets an OAuth `state` nonce (short-lived cookie), redirects
  to GitHub's authorize URL. No scopes requested beyond default public identity: Kiln
  needs only the username. The repo token used by the brain's inspection tool remains a
  separately supplied PAT in `user_config` — sign-in identity and repo access are
  deliberately decoupled.
- `GET /auth/github/callback` — verifies `state`, exchanges the code
  (`GITHUB_OAUTH_CLIENT_ID`/`GITHUB_OAUTH_CLIENT_SECRET`), fetches `GET /user`, then:
  - username on the allowlist → find-or-create the `users` row, create a session,
    redirect to `/dashboard`;
  - not on the allowlist → a friendly "Kiln is invite-only" page; **no user row is
    created**.

**Allowlist: `KILN_ALLOWED_GITHUB_USERS`** — comma-separated GitHub usernames,
case-insensitive (§10, D1). An env var, not a table: adding a user is a one-line Render
env change — config-as-code, reviewable, agent-operable per `10`'s doctrine. The check
runs on **every** login, so removing a username blocks future sign-ins; live sessions
persist until expiry (acceptable at invite-only scale).

**Sessions.** A random 256-bit token in an `HttpOnly; Secure; SameSite=Lax` cookie;
the server stores only its hash in `sessions` (§3) with a ~30-day sliding expiry.
`POST /auth/logout` deletes the session row and clears the cookie. No JWTs: a DB lookup
per request is nothing at this scale, and revocation stays trivial. The dashboard and
the app are one SPA on one origin, so the same cookie covers both surfaces when phase 2
turns auth on for the app.

**Protection boundary.**

- *Phase 1:* session middleware guards **only the new routes** — `/api/me`,
  `PUT /api/settings`, `PUT /api/project`, `POST /api/settings/verify`. Every existing
  endpoint (`/api/message`, feed, stream, accept, voice token, dev seeds) stays open and
  env-driven. Unauthenticated requests to guarded routes get `401`; the SPA routes to
  sign-in.
- *Phase 2:* the middleware extends to all `/api/*`, resolving session → user → project
  so handlers never see "no tenant". `/auth/*`, `/healthz`, and static assets stay
  public. Dev endpoints stay behind `KILN_DEV_ENDPOINTS=1`, scoped to the caller's
  project.

## 3. Data model

New tables (runtime Postgres, same database — `02` §3):

- **`users`** — `id`, `github_login` (unique, lower-cased), `github_id`,
  `display_name`, `avatar_url`, `created_at`.
- **`sessions`** — `token_hash` (PK), `user_id` (FK), `created_at`, `expires_at`.
- **`user_config`** — one row per user; **credentials only** (§10, D4):
  `anthropic_api_key`, `amika_api_key`, `amika_claude_cred_id`, `github_auth_token`.
  Credentials follow the person and are shared by every brain the user owns.
- **`projects`** — `id`, `owner_user_id` (FK), `name`, `created_at`, plus the fields
  that parameterize *this* project's brain and board (§10, D4, D5):
  - `repo_url` — **one** field replacing today's `AMIKA_REPO_URL` + `GITHUB_REPO_URL`
    pair (they were always the same repo twice): it feeds both the Amika sandbox source
    and the brain's repo-inspection clone.
  - `amika_snapshot` — a snapshot is built from a specific repo, so it rides with it.
  - `brain_model` — per-brain model choice (was `KILN_BRAIN_MODEL`).
  - `worker_count` — per-board WIP cap (was `KILN_WORKER_COUNT`), default 3.

  One project per user for now, enforced by a unique index on `owner_user_id` that is
  **dropped, not migrated,** when multi-project arrives. Everything downstream keys on
  `project_id` from day one, so "many projects" is a UI/API change, not a schema change.

**Secrets at rest** (§10, D7). Secret columns are encrypted app-side with AES-256-GCM
under one master key, `KILN_SECRETS_KEY` (32 bytes, env, Render env group; the backend
refuses to boot with the var set but malformed). Secrets are **write-only through the
API**: after a write, reads return only presence + a last-4 fingerprint ("configured ·
…x4Kd"). No endpoint ever returns a stored secret.

**Tenant column (phase 2).** `project_id` (FK, `NOT NULL` after backfill — §7) is added
to every stateful table: board tables (tickets, workers, outbox), `events`, `messages`,
`notifications`, and push subscriptions (which also gain `user_id` — the notify target
is the project owner's subscriptions). Every store query gains a `project_id`
predicate; uniqueness constraints and the deterministic pull (`03`) become per-project.

**Stays platform-level:** `ASSEMBLYAI_API_KEY` (voice works for everyone on the company
key; the mint endpoint requires a session from phase 2), Sentry DSNs, `DATABASE_URL`,
the new auth/crypto vars (§7), and `AMIKA_BASE_URL` (amended 2026-07-06: the Amika
sandbox host is one deployment's env, not a per-user credential — every user's
sandboxes are provisioned against the same Amika account and endpoint).

## 4. API surface & wire types

New endpoints, all session-protected from day one, all types generated from `/schema`
(wire-schema rule, `02` §3–§4):

- `GET /api/me` → `{user, project, config_status}` — identity, the project row, and
  per-field config status (`set` + fingerprint, never values). The dashboard renders
  entirely from this.
- `PUT /api/settings` — partial update of `user_config`. Blank/omitted fields are left
  unchanged (secrets are write-only, so "clear" is an explicit `null`).
- `PUT /api/project` — `name`, `repo_url`, `amika_snapshot`, `brain_model`,
  `worker_count`. Creates the row on first write (onboarding), updates thereafter.
- `POST /api/settings/verify` — live connection checks, one per configured credential:
  Amika authenticated ping, `git ls-remote` on `repo_url` with the stored PAT, a
  minimal Anthropic call. Returns per-check `{ok, message}`. This is the only place a
  stored secret is *used* before phase 2, and it gives users real feedback that their
  keys work even while the runtime still runs on env.
- `GET /auth/github/login`, `GET /auth/github/callback`, `POST /auth/logout` (§2) —
  browser endpoints, outside `/api`, no wire types beyond the redirect contract.

Existing endpoints change **nothing** in phase 1. In phase 2 their shapes still don't
change — they become implicitly scoped by session → project in middleware.

## 5. The dashboard (`/dashboard`)

The SPA gains client-side routing — the first router in the app. `/` keeps rendering
the current mobile app **untouched**; `/dashboard` mounts alongside it, served by the
same backend SPA fallback. Desktop-first layout: this is deliberately the first
non-mobile surface in the product (`07`'s mobile-first pin applies to the app, not the
dashboard).

**States:**

1. **Signed out** → one screen: "Continue with GitHub" → `/auth/github/login`.
2. **Signed in, project unconfigured** → onboarding flow: name the project + repo URL +
   snapshot → paste credentials (Anthropic key, Amika key/cred id, GitHub PAT — the
   Amika base URL is platform env, not a user credential, amended 2026-07-06) → verify
   connections (`POST /api/settings/verify`, per-check results inline) → done.
3. **Signed in, configured** → settings: account card (GitHub identity, sign out) ·
   credentials section (write-only secret inputs showing "configured · …x4Kd", per-
   credential test button) · project section (repo URL, snapshot, brain model, worker
   count) · a pointer to open `/` on your phone.

Not-allowlisted users never reach the dashboard (§2). The app at `/` gets **no** sign-in
gate, interstitial, or visual change in phase 1.

## 6. Runtime tenancy (phase 2)

**The unit of tenancy in the runtime is the project — one brain per project** (§10,
D5). A user with several projects later runs several independent brains sharing one
credential set.

- **Event loop.** `events` carry `project_id`. The queue claim becomes *per-project
  serialized, cross-project concurrent*: at most one event in flight per project —
  preserving `06`'s "the brain wakes on one event at a time" invariant per board —
  while different tenants' events process in parallel. This changes the claim query's
  ordering/locking, not the queue's durability semantics (`04`).
- **Tenant registry.** Today `cmd/kiln` builds one Amika client, one brain LLM client,
  and one repo clone at boot. That becomes a registry keyed by `project_id`: processing
  an event for project P resolves (and caches) the owner's decrypted `user_config` +
  P's project fields into a provider set — Amika client, brain client (their key, P's
  model), and a per-project repo workspace (clone dir keyed by project id, cloned
  lazily with their PAT). Dashboard config writes invalidate the cache entry, so a
  corrected key takes effect on the next event with no restart.
- **Failure isolation.** Missing or rejected credentials never crash the runtime: the
  affected action fails with a feed-visible error on that project ("Amika key
  rejected"), and every other brain keeps processing.
- **Worker pool.** `projects.worker_count` caps concurrent Amika workers per board,
  exactly as `KILN_WORKER_COUNT` does today.
- **Voice & notifications.** The STT token mint keeps the platform AssemblyAI key but
  requires a session. Push subscriptions are per-user; "notify the user" (`02` §10)
  resolves to the project owner's subscriptions.

## 7. Migration & rollout

**Bootstrap-from-env — one mechanism for the prod migration *and* local dev
ergonomics.** On boot, if `users` is empty and `KILN_BOOTSTRAP_GITHUB_USER` is set, the
backend creates that user and one project, seeds `user_config` and the project fields
from today's env vars (`ANTHROPIC_API_KEY`, `AMIKA_*`, `GITHUB_*`, `KILN_BRAIN_MODEL`,
`KILN_WORKER_COUNT`), and — in phase 2 — adopts every legacy row (tickets, events,
messages, notifications) into that project before `project_id` goes `NOT NULL`.
Idempotent; a no-op once any user exists. In prod this migrates the existing board and
transcript into the operator's account; locally it means `docker compose up` with the
existing `.env` still yields a working instance with no onboarding clicks.

**Dev/e2e auth.** Real GitHub OAuth is impossible in tests. `KILN_DEV_ENDPOINTS=1`
(never prod) adds `POST /api/dev/session` → mints a session cookie for the bootstrap
user. Existing e2e suites keep passing in phase 1 untouched (nothing they call is
guarded); in phase 2 they add this one setup call.

**New platform env vars:** `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`,
`KILN_ALLOWED_GITHUB_USERS`, `KILN_SECRETS_KEY`, `KILN_BOOTSTRAP_GITHUB_USER`. The
per-user credential env vars disappear from prod once bootstrap has seeded them
(compose keeps them for local dev seeding).

**Rollout order:**

1. **Phase 1 ships dark:** new tables + auth + dashboard deploy; register the GitHub
   OAuth app; set the new env vars; the operator signs in and configures via the
   dashboard; `verify` passes. The app and runtime are provably untouched (their tests
   didn't change).
2. **Phase 2 flips tenancy:** `project_id` threading + registry + app auth + bootstrap
   adoption land behind the hard gate; e2e suites move to dev-session auth; deploy;
   the operator's account owns the existing board.
3. **Invite the handful:** add usernames to the allowlist; each signs in, onboards on
   the dashboard, and gets an empty board driven by their own brain.

## 8. Error handling & testing

Per the hard gate (`02` §4), at three levels:

- **Cross-tenant isolation is the headline test (phase 2):** integration tests seed two
  tenants and assert one session can never read or mutate the other's board, feed,
  events, or notifications; store tests cover the `project_id` predicate on every
  query.
- **Unit:** allowlist matching (case, whitespace, empty var ⇒ nobody signs up), OAuth
  `state` verification, session expiry/sliding renewal/revocation, AES-GCM round-trip +
  fingerprinting, refuse-to-boot on malformed `KILN_SECRETS_KEY`, registry cache
  invalidation on config write (phase 2).
- **Integration:** OAuth callback against a faked GitHub (token exchange + `/user`),
  find-or-create semantics, config write → `GET /api/me` shows status but never values,
  `verify` fan-out with mixed pass/fail.
- **Runtime degradation (phase 2):** two seeded tenants, one with a revoked key — its
  actions fail feed-visibly, the other brain keeps processing.
- **E2E:** phase 1 — existing suites pass unchanged (the proof the app is untouched),
  plus a dashboard flow: dev-session → onboard via API → `GET /api/me` reflects config.
  Phase 2 — suites adopt `POST /api/dev/session`, then the standard seed-and-drive
  flows.
- **Secrets hygiene:** never logged, scrubbed from Sentry events, never in wire types
  that leave the backend (enforced by the fingerprint-only read shape).

## 9. Frontend module shape

- `frontend/src/router` — minimal route split: `/` → existing app root (unchanged
  import), `/dashboard` → dashboard root. No changes inside the app's components.
- `frontend/src/dashboard/` — screens (sign-in, onboarding, settings), talking only to
  the §4 endpoints through the existing transport layer; types from `/schema` codegen.
- `frontend/src/stores/session` — `GET /api/me` state shared by dashboard screens
  (the app does not consume it in phase 1).

## 10. Decision log

- **D1 — GitHub OAuth only, allowlist in an env var.** Users are developers with GitHub
  identities; one IdP keeps the surface minimal. `KILN_ALLOWED_GITHUB_USERS` over a DB
  table: config-as-code, one-line invite, no admin UI to build (`10` doctrine).
  Revisit when invites outgrow an env var.
- **D2 — Self-rolled OAuth over a managed auth provider.** The flow is two endpoints
  and one exchange; "GitHub-only + allowlist" leaves a provider (Clerk/WorkOS/Auth0)
  almost nothing to do, while adding an external dependency in every request path.
- **D3 — Shared schema with `project_id` over schema-per-tenant or instance-per-user.**
  One deployment, one migration path, standard row-scoping; isolation is enforced by
  query predicates and tested (§8). Instance-per-user was rejected: ops multiply per
  user and the dashboard becomes a provisioning system — wrong trade at invite-only
  scale.
- **D4 — Credentials on the user, repo + brain knobs on the project.** Keys follow the
  person (`user_config`); `repo_url`, `amika_snapshot`, `brain_model`, `worker_count`
  parameterize a specific brain/board (`projects`). `AMIKA_REPO_URL` and
  `GITHUB_REPO_URL` collapse into one `repo_url` — they were always the same repo.
- **D5 — One brain per project.** The brain is project-scoped state + a project-scoped
  event stream; users eventually run several. Registry keys on `project_id`, resolving
  credentials through the owner.
- **D6 — Two phases; the current app is untouched in phase 1.** Auth + dashboard prove
  out against real GitHub and real users while the env-driven single-tenant runtime
  keeps working; the tenancy flip lands only after. Cost: phase 1 has two config
  sources (env drives the runtime, DB holds dashboard writes) — acceptable because
  phase 1 makes no promise the dashboard config is live.
- **D7 — App-side AES-256-GCM with one env master key; write-only secrets.** Simplest
  scheme that keeps a DB dump from leaking keys; no KMS dependency at this scale.
  Fingerprint-only reads make "never returns secrets" a structural property of the wire
  types rather than a handler convention.
