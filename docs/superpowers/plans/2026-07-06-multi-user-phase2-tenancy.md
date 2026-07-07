# Multi-User Phase 2 — Tenancy Flip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement phase 2 of `docs/specs/11-multi-user.md`: thread `project_id` through every stateful table, build one brain per project from stored config via a tenant registry, gate all `/api/*` behind sessions, bootstrap-from-env, and move the e2e suites to dev-session auth.

**Architecture:** The unit of tenancy is the project (spec §6, D5). Stores and services stay singletons but every method gains a `projectID` parameter; only the **brain** and the **agent provider** become per-project instances, built lazily by a new `internal/tenant` registry that resolves `projects` + decrypted `user_config` and invalidates on dashboard writes. Event/outbox queues become per-project-serialized / cross-project-concurrent via an in-process dispatcher (single backend process — spec 10), not DB locks. Bootstrap-from-env adopts legacy rows into the operator's project at boot, then flips `project_id` to `NOT NULL`.

**Tech Stack:** Go 1.26 (stdlib mux, lib/pq, embedded SQL migrations), Postgres 16, React/TS frontend (pnpm, vitest), Playwright e2e, Docker Compose.

## Global Constraints

- Hard gate before merge: `make check` (gofmt + golangci-lint + `go build` + `pnpm typecheck` + unit + `-tags=integration` + frontend tests) — spec 02 §4. `export PATH="$HOME/go/bin:$PATH"` first (oapi-codegen/golangci-lint live there).
- Wire-schema rule: never hand-edit `backend/internal/wire/generated.go` or `frontend/src/schema/generated.ts`. **This plan changes no wire types** — endpoint shapes are unchanged; scoping is implicit via session (spec §4). `/api/dev/*` stays out of the schema.
- Secrets hygiene: never log decrypted credentials; never put them in wire types; never `cat` the repo `.env` (AGENTS.md).
- Real-service runs use `KILN_BRAIN_MODEL=claude-haiku-4-5-20251001` and `AGENT_MODE=mock` unless Amika is explicitly under test; kill any Amika sandboxes created (memory: real-service-test-hygiene).
- Work stays in worktree `.claude/worktrees/multi-user-phase2` on branch `worktree-multi-user-phase2`.
- Integration tests need `TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable` (compose `db` up, `kiln_test` database created).
- Commit after every task. Tasks 3–7 note "module-local gate": `go build ./... ` may be red at repo level mid-flip; run that module's own tests, commit with `--no-verify`, and the full gate goes green at Task 11 (wiring) before anything else lands on top.

## Locked design decisions (deviations & refinements vs the spec text)

1. **`project_id` is a plain `uuid` column, NOT a foreign key.** Modules apply their own embedded migrations independently and cross-module references are by value (established convention: `agent_turns.worker_id` is "by value; NOT a FK"). An FK to `projects` would couple every module's migration set to identity's. Isolation is enforced by query predicates + tests (spec §8), not referential integrity.
2. **`NOT NULL` is applied at boot, not in a migration.** Migrations add nullable columns (existing prod rows). A boot-time finalizer (Task 9) adopts orphan rows into the bootstrap project, then conditionally `ALTER ... SET NOT NULL` per table once no NULLs remain. Idempotent every boot.
3. **Bootstrap = find-or-create, not only-when-empty.** Spec §7 says "if `users` is empty" — but prod already has the operator's user from phase 1 sign-in, and legacy board rows still need adoption. So: when `KILN_BOOTSTRAP_GITHUB_USER` is set, find-or-create that user, find-or-create their project, seed **only unset** config fields from env (never overwrite dashboard-written values), adopt orphan rows. No-op on every subsequent boot. This preserves the spec's intent (prod board migrates into the operator's account; local `docker compose up` works with `.env`).
4. **Per-project serialization is in-process.** One dispatcher goroutine per queue claims with a busy-project exclusion list and fans out to goroutines (bounded). The claim SQL gains `AND project_id <> ALL($busy)`. Durability semantics (attempts/backoff/dead) unchanged. Sound because there is exactly one backend process (spec 10); this matches today's "the events worker IS the single-writer constraint realized in-process" (worker.go doc).
5. **brain.Service stays project-agnostic.** The registry builds a per-project brain whose ports are adapters with `projectID` pre-bound (closures over the singleton board/runtime services). Brain internals (tool loop, prompts) untouched.
6. **agent.Service stays a singleton** with one Run loop; it becomes multi-project: reconciler iterates projects (via a `Projects` resolver port), provider is resolved per project, and `agent_turns` carry `project_id` so the poller resolves the right provider per turn. Per-project sandbox name prefix: `cfg.WorkerPrefix + projectID[:8] + "-"` — keeps two projects on one Amika account from adopting/destroying each other's sandboxes.
7. **Identity unmounted ⇒ app 401s everything under `/api/*`** (no legacy single-tenant fallback mode). `/healthz`, `/auth/*`, static SPA stay public. Local dev always mounts identity (OAuth vars are in `.env` since phase 1).
8. **`push_settings` singleton is replaced by `push_user_settings(user_id, mode)`**; the legacy table stays in place (dead) and its mode value is copied to the bootstrap user at adoption. Push subscriptions gain `user_id`; send fan-out targets the project owner's subscriptions.
9. **`AGENT_MODE` stays platform-level.** `mock` → registry hands every project a `mock.New()` instance; `amika` → per-project `amika.New` with the owner's key + project repo/snapshot + platform base URL.
10. **Per-project repo clone** at `KILN_REPO_DIR/<projectID>` (lazy, at first registry build; failure ⇒ disabled shell, non-fatal — existing `repo.New` semantics).
11. **`/api/dev/session` keeps its `{github_login}` body** (dashboard e2e mints throwaway users). Board-driving e2e specs mint as `KILN_BOOTSTRAP_GITHUB_USER` so seeds land in the bootstrapped project.
12. **Dev seeds + reset become project-scoped.** `POST /api/dev/reset` (mounted unconditionally, used by the app UI) resets only the caller's project: scoped DELETEs + destroy that project's sandboxes + reseed its worker rows.

---

### Task 1: Tenancy migrations (all modules)

**Files:**
- Create: `backend/internal/board/postgres/migrations/0008_project_id.sql`
- Create: `backend/internal/runtime/postgres/migrations/0007_project_id.sql`
- Create: `backend/internal/agent/postgres/migrations/0002_project_id.sql`
- Create: `backend/internal/steward/postgres/migrations/0002_project_id.sql`
- Create: `backend/internal/push/postgres/migrations/0003_user_tenancy.sql`
- Modify: `backend/cmd/kiln/wiring.go:67-88` (`moduleMigrations` — move identity first)

**Interfaces:**
- Produces: nullable `project_id uuid` on `tickets, workers, outbox, events, messages, notifications, agent_turns, steward_pokes`; `user_id uuid` on `push_subscriptions`; new `push_user_settings` table. Ledger keys are file paths, so reordering module sets is safe for existing DBs (applied files skipped by name).

- [ ] **Step 1: Write the five migrations.**

`0008_project_id.sql` (board):
```sql
-- 11 §3: tenant column. Nullable until boot-time adoption backfills and
-- flips NOT NULL (cmd/kiln bootstrap). Plain uuid by value — no FK across
-- module migration sets.
ALTER TABLE tickets ADD COLUMN project_id uuid;
ALTER TABLE workers ADD COLUMN project_id uuid;
ALTER TABLE outbox  ADD COLUMN project_id uuid;

CREATE INDEX tickets_project_live ON tickets (project_id) WHERE archived_at IS NULL;
DROP INDEX tickets_ready_pull_order;
CREATE INDEX tickets_ready_pull_order
    ON tickets (project_id, priority DESC, ready_at ASC, id ASC)
    WHERE state = 'ready';
CREATE INDEX workers_by_project ON workers (project_id);
CREATE INDEX outbox_due_project ON outbox (project_id, id) WHERE status = 'pending';
```

`0007_project_id.sql` (runtime):
```sql
ALTER TABLE events        ADD COLUMN project_id uuid;
ALTER TABLE messages      ADD COLUMN project_id uuid;
ALTER TABLE notifications ADD COLUMN project_id uuid;

CREATE INDEX events_due_project ON events (project_id, id) WHERE status = 'pending';
CREATE INDEX messages_recent_project ON messages (project_id, id DESC);
CREATE INDEX notifications_recent_project ON notifications (project_id, id DESC);
```

`0002_project_id.sql` (agent):
```sql
ALTER TABLE agent_turns ADD COLUMN project_id uuid;
```

`0002_project_id.sql` (steward):
```sql
ALTER TABLE steward_pokes ADD COLUMN project_id uuid;
```

`0003_user_tenancy.sql` (push):
```sql
ALTER TABLE push_subscriptions ADD COLUMN user_id uuid;
-- Replaces the push_settings singleton (kept in place, unread after 11 phase 2;
-- its mode is copied to the bootstrap owner at adoption).
CREATE TABLE push_user_settings (
    user_id uuid PRIMARY KEY,
    mode    text NOT NULL DEFAULT 'blocked'
);
```

- [ ] **Step 2:** Reorder `moduleMigrations()` in `wiring.go` so identity's set is first (identity → board → runtime → agent → steward → push) — projects/users tables exist before anything that will reference them by value on a fresh DB.
- [ ] **Step 3:** Run `cd backend && go test ./cmd/... && TEST_DATABASE_URL=... go test -tags=integration ./...` — existing integration tests must still pass (columns are additive+nullable). Expected: PASS.
- [ ] **Step 4:** Commit `feat(tenancy): add project_id/user_id tenant columns (nullable pre-adoption)`.

---

### Task 2: Identity runtime bridge

**Files:**
- Modify: `backend/internal/identity/store.go`, `backend/internal/identity/postgres/store.go`, `backend/internal/identity/service.go`, `backend/internal/identity/entities.go`
- Test: `backend/internal/identity/service_test.go`, `backend/internal/identity/postgres/store_integration_test.go`

**Interfaces (Produces):**
```go
// Store additions
GetProject(ctx context.Context, id string) (Project, error)      // by projects.id
ListProjects(ctx context.Context) ([]Project, error)             // all, ORDER BY created_at

// Service additions
type RuntimeConfig struct {
    Project          Project
    OwnerUserID      string
    AnthropicAPIKey  string // decrypted; empty = unset
    AmikaAPIKey      string
    AmikaClaudeCredID string
    GitHubAuthToken  string
}
func (s *Service) RuntimeConfig(ctx context.Context, projectID string) (RuntimeConfig, error)
func (s *Service) ListProjectIDs(ctx context.Context) ([]string, error)
func (s *Service) ProjectFor(ctx context.Context, userID string) (Project, error) // wraps GetProjectByOwner
func (s *Service) EnsureUser(ctx context.Context, login string) (User, error)     // find-or-create, no allowlist (same mechanics as DevSignIn)
func (s *Service) SetInvalidator(f func(projectID string))  // called after UpdateSettings (owner's project, if any) and UpsertProject
```

- [ ] Step 1: failing unit tests — `RuntimeConfig` decrypts round-tripped secrets via fake store; `SetInvalidator` fires with the project id on `UpdateSettings` + `UpsertProject`; `EnsureUser` is idempotent.
- [ ] Step 2: implement; `RuntimeConfig` = `GetProject` + `GetUser` (owner) + `GetUserConfig` + `cipher.Decrypt` per set column. Note `DevSignIn` should now call `EnsureUser` internally.
- [ ] Step 3: integration test additions for `GetProject`/`ListProjects`.
- [ ] Step 4: module tests green → commit `feat(identity): runtime credential resolution + registry invalidation hook`.

---

### Task 3: Board module tenancy  *(module-local gate)*

**Files:** `backend/internal/board/service.go`, `backend/internal/board/postgres/store.go`, board tests.

**Threading rule (applies to Tasks 3–7):** every store/service method that reads or writes a stateful table gains a leading `projectID string` parameter after `ctx`, and every SQL statement gains a `project_id = $n` predicate (or sets the column on INSERT). No method keeps a global read.

Specifics:
- `Snapshot(ctx, projectID)`; worker counts scoped `WHERE project_id=$1`.
- `GetTicket/LockTicket/InsertTicket/...(ctx, projectID, ...)` — `WHERE id=$2 AND project_id=$1` (a valid id from another tenant must be *not found*).
- `NextReadyTicket` (the deterministic pull, store.go:290): add `AND project_id = $1`; ordering unchanged (per-project by construction).
- `lockFreeCandidates`, `FreeWorker` recheck: scope to project.
- `ReconcileWorkers(ctx, projectID string, n int)` — count/insert `WHERE/WITH project_id`.
- `WorkerIDs(ctx, projectID)`.
- `AppendOutbox` writes `project_id`.
- `board.Service` public methods (`GetBoard`, `CreateTicket`, `MarkReady`, `SendToAgent`, `Seed`, ...) gain `projectID` and pass through.
- Tests: fixed `projA`/`projB` uuids; add one isolation assertion per query family (seed both projects, read one).

- [ ] Module tests green (`go test ./internal/board/...` unit+integration) → commit `--no-verify` `feat(board): project-scoped store and service`.

---

### Task 4: Agent module tenancy  *(module-local gate)*

**Files:** `backend/internal/agent/service.go`, `backend/internal/agent/turn.go`, `backend/internal/agent/postgres/store.go`, `backend/internal/agent/provider.go`, `backend/internal/agent/mock/provider.go`, tests.

**Interfaces (Produces):**
```go
// replaces the single provider + prefix at construction
type ProviderResolver interface {
    // For returns the provider and sandbox-name prefix for one project.
    For(ctx context.Context, projectID string) (Provider, string, error)
}
type Projects interface { ProjectIDs(ctx context.Context) ([]string, error) }
func NewService(store Store, providers ProviderResolver, projects Projects, events Events,
    slots Slots, clock Clock, refresh BoardRefresher) *Service
```
- `agent_turns` rows record `project_id` (source: the outbox `agent.send`/`agent.release` payload — runtime hands it through `Record`).
- `Slots` port becomes `WorkerIDs(ctx, projectID)`.
- Reconciler: `for _, pid := range projects.ProjectIDs()` → resolve provider+prefix → `adoptAndCreate`/`destroyOrphans` per project against that prefix. A project whose provider resolution fails (missing key) is logged and skipped — other projects continue (spec §6 failure isolation).
- Poller/liveness: resolve provider per turn via `turn.ProjectID`.
- `Inspect`/agent-status port (consumed by hub): gains `projectID`.

- [ ] Module tests green → commit `--no-verify` `feat(agent): per-project providers, reconcile, and turns`.

---

### Task 5: Push module tenancy  *(module-local gate)*

**Files:** `backend/internal/push/push.go`, `backend/internal/push/sender.go`, `backend/internal/push/postgres/store.go`, tests.

- `Store.Save(ctx, userID, sub)`, `List(ctx, userID)`, `Mode(ctx, userID)`, `SetMode(ctx, userID, mode)` — mode reads fall back to `'blocked'` when no row (`push_user_settings`).
- `Sender.Send(ctx, userID, n)` fans out to that user's subscriptions only.
- Delete the "single user in v1" doc comment (push.go:9-11).

- [ ] Module tests green → commit `--no-verify` `feat(push): per-user subscriptions and mode`.

---

### Task 6: Steward tenancy  *(module-local gate)*

**Files:** `backend/internal/steward/service.go`, `backend/internal/steward/postgres/store.go`, tests.

- Steward gains the same `Projects` resolver port as agent; its sweep iterates projects and calls its board/agent ports with `projectID`. `steward_pokes` rows record `project_id`; `Upsert` writes it.

- [ ] Module tests green → commit `--no-verify` `feat(steward): per-project poke sweep`.

---

### Task 7: Runtime module tenancy + per-project dispatcher  *(module-local gate)*

**Files:** `backend/internal/runtime/store.go`, `backend/internal/runtime/postgres/store.go`, `backend/internal/runtime/worker.go`, `backend/internal/runtime/service.go`, `backend/internal/runtime/queue.go`, `backend/internal/runtime/transcript.go`, `backend/internal/runtime/notifications.go`, tests.

**Claim SQL** (events; outbox identical with `topic`):
```sql
UPDATE events SET
    attempts = attempts + 1,
    next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
WHERE id = (
    SELECT id FROM events
    WHERE status = 'pending' AND next_attempt_at <= now()
      AND project_id <> ALL($1::uuid[])
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING id, project_id, type, payload, attempts, created_at
```
`ClaimNextDue(ctx, queue, busy []string)` — pass `pq.Array(busy)`; `Entry` gains `ProjectID`.

**Dispatcher** (`worker.go` rework — per-project serialized, cross-project concurrent, bounded):
```go
const maxInFlightProjects = 4

func (w *Worker) Run(ctx context.Context) {
    busy := make(map[string]struct{})
    done := make(chan string)
    for {
        for len(busy) < maxInFlightProjects {
            e, ok, err := w.store.ClaimNextDue(ctx, w.queue, keys(busy))
            if err != nil || !ok { break }
            busy[e.ProjectID] = struct{}{}
            go func(e Entry) { w.process(ctx, e); done <- e.ProjectID }(e)
        }
        select {
        case pid := <-done: delete(busy, pid)
        case <-w.nudge:
        case <-w.clock.After(pollInterval):
        case <-ctx.Done():
            for len(busy) > 0 { delete(busy, <-done) } // let in-flight passes finish marking
            return
        }
    }
}
```
`process` keeps today's safeHandle/MarkDone/MarkRetry/retire semantics.

**Service threading:**
- `InsertEvent`, `AppendUserMessageAndEnqueueEvent`, `AppendKilnMessage`, `Recent`, all notification store methods: gain `projectID` + predicates. `PostMessage(ctx, projectID, text)`, `Feed(ctx, projectID)`, etc.
- `handleEvent`: brain resolved per event via a new port —
  ```go
  type BrainResolver interface { For(ctx context.Context, projectID string) (Brain, error) }
  ```
  Resolution failure ⇒ feed-visible system-error `Say` on that project + `MarkDone` (no retry storm); other projects unaffected (spec §6).
- Hub push ports (`PushBoard/PushSay/PushFeed/PushActivity`) and `Notifier.Send` gain `projectID`; the notifier resolves owner user via a small `Owner(ctx, projectID) (string, error)` port (satisfied by identity in wiring).
- Outbox executors (`agent.send`, `notify.send`, `pull.evaluate`, ...) read `ProjectID` off the claimed entry and pass it down (incl. into `agent.Record` so `agent_turns.project_id` is set).
- Unit test for the dispatcher: fake store with events for projects A,A,B — assert A2 never starts before A1 finishes, and B overlaps A.

- [ ] Module tests green → commit `--no-verify` `feat(runtime): project-scoped stores + per-project serialized dispatcher`.

---

### Task 8: Tenant registry (`internal/tenant`)

**Files:**
- Create: `backend/internal/tenant/registry.go`, `backend/internal/tenant/registry_test.go`

**Interfaces (Produces):**
```go
package tenant

type Providers struct {
    ProjectID, OwnerUserID, WorkerPrefix string
    WorkerCount                          int
    Brain                                any // concretely runtime.Brain via cmd/kiln closure
    Agent                                agent.Provider
}

type Builder func(ctx context.Context, rc identity.RuntimeConfig) (*Providers, error)

type Registry struct { /* mu, cache map[string]*Providers, resolve func, build Builder */ }

func New(resolve func(ctx context.Context, projectID string) (identity.RuntimeConfig, error), build Builder) *Registry
func (r *Registry) For(ctx context.Context, projectID string) (*Providers, error)  // lazy build + cache
func (r *Registry) Invalidate(projectID string)                                     // drop cache entry
```
(If importing `identity`/`agent` from `tenant` creates a cycle, fall back to type parameters or `any` — check imports first; identity imports nothing from runtime/agent, so a plain import is expected to be fine.)

Semantics: `For` is single-flight per project (hold a per-key mutex so two concurrent events don't build twice); `Invalidate` is called by identity's `SetInvalidator` hook, so a corrected key takes effect on the next event with no restart (spec §6). The **build closure lives in cmd/kiln** (Task 11) and is where per-project brain, amika client, repo shell, and board `ReconcileWorkers` seeding happen.

- [ ] Unit tests: build-once/cache-hit, invalidate→rebuild, build error not cached, concurrent `For` builds once. Commit `feat(tenant): per-project provider registry`.

---

### Task 9: Bootstrap-from-env + NOT NULL finalizer

**Files:**
- Create: `backend/cmd/kiln/bootstrap.go`, `backend/cmd/kiln/bootstrap_integration_test.go`
- Modify: `backend/cmd/kiln/main.go` (Config + `KILN_BOOTSTRAP_GITHUB_USER`), `backend/cmd/kiln/wiring.go` (`serve`: run after `applyMigrations`, before `buildGraph`), `docker-compose.yml`, `.env.example`

**Behavior (runs every boot, idempotent):**
1. If identity unmounted: log warning if `KILN_BOOTSTRAP_GITHUB_USER` set; still run step 4 (fresh empty DBs finalize fine).
2. If `KILN_BOOTSTRAP_GITHUB_USER` set: `EnsureUser(login)`; ensure project — `ProjectFor(user)` else `UpsertProject` seeded from env: name = repo basename (else `"kiln"`), `repo_url` = `GITHUB_REPO_URL` else `AMIKA_REPO_URL`, `amika_snapshot` = `AMIKA_SNAPSHOT`, `brain_model` = `KILN_BRAIN_MODEL`, `worker_count` = clamp(`KILN_WORKER_COUNT`, 1, 10). Seed **only unset** `user_config` fields from `ANTHROPIC_API_KEY`, `AMIKA_API_KEY`, `AMIKA_CLAUDE_CRED_ID`, `GITHUB_AUTH_TOKEN`.
3. Adopt orphans (single tx): for each of `tickets, workers, outbox, events, messages, notifications, agent_turns, steward_pokes`: `UPDATE t SET project_id=$1 WHERE project_id IS NULL`; `UPDATE push_subscriptions SET user_id=$2 WHERE user_id IS NULL`; `INSERT INTO push_user_settings (user_id, mode) SELECT $2, mode FROM push_settings WHERE id=1 ON CONFLICT (user_id) DO NOTHING`.
4. Finalize: per table, if `information_schema.columns.is_nullable='YES'` and no NULL rows → `ALTER TABLE t ALTER COLUMN project_id SET NOT NULL` (`user_id` for push_subscriptions).
5. Remove the global `boardStore.ReconcileWorkers` call from `buildGraph` (worker seeding moves into the registry build, Task 11).

Compose/`.env.example`: add `KILN_BOOTSTRAP_GITHUB_USER: ${KILN_BOOTSTRAP_GITHUB_USER:-}` to the backend service env; document in `.env.example`.

- [ ] Integration test: fresh schema + env → user/project/config seeded + orphan ticket adopted + columns NOT NULL; run twice → no-op; dashboard-written config value survives a re-boot with different env. Commit `feat(kiln): bootstrap-from-env adoption + NOT NULL finalizer`.

---

### Task 10: API tenancy — session on everything, scoped hub, scoped dev tools

**Files:** `backend/internal/api/routes.go`, `backend/internal/api/session.go`, `backend/internal/api/hub.go`, `backend/internal/api/identity_handlers.go`, api tests.

**Middleware (Produces):**
```go
type ProjectResolver interface { ProjectFor(ctx context.Context, userID string) (identity.Project, error) }

func (s *Server) withProject(next func(http.ResponseWriter, *http.Request, identity.User, identity.Project)) http.HandlerFunc
// = withSession + ProjectFor; no project ⇒ 404 {"error":"no project configured"}
```
- Wrap **all** app endpoints: board, message(s), feed (all five), accept, stream, dev tickets/notifications/reset in `withProject`; voice token, push subscribe/key/mode in `withSession` (push handlers take `user.ID`).
- `/auth/*`, `/healthz`, `/api/dev/session`, SPA fallback stay public. Unauthenticated ⇒ existing 401 text.
- Hub: `client` gains `projectID`; `ServeStream(w, r, projectID)`; `broadcast(projectID, frame)` delivers only to matching clients; initial snapshot + agent-status join use the project. Push-port methods now carry `projectID` (from Task 7).
- Reset: `resetCoordinator.Reset(ctx, projectID)` — scoped DELETEs (tables list from Task 9 step 3), destroy that project's sandboxes via the registry provider + prefix, reseed that project's worker rows to `project.worker_count`.
- api unit tests: 401 without cookie on every gated route; 404 without project; A-cannot-see-B handler test with fake ports.

- [ ] Module tests green → commit `--no-verify` `feat(api): session+project scoping on the full surface`.

---

### Task 11: Composition root rewrite — the gate goes green here

**Files:** `backend/cmd/kiln/wiring.go`, `backend/cmd/kiln/adapters.go`, `backend/cmd/kiln/main.go`.

- Delete boot-time `newProvider`, `buildBrain`'s global construction, global repo clone; `KILN_BRAIN_MODEL`/`AMIKA_*`/`GITHUB_REPO_URL`/`GITHUB_AUTH_TOKEN`/`KILN_WORKER_COUNT` are read **only** by bootstrap seeding now.
- Build `tenant.Registry` with resolve = `idSvc.RuntimeConfig` and a build closure that: 
  - board: `boardStore.ReconcileWorkers(ctx, projectID, project.WorkerCount)`
  - brain: `repo.New(ctx, {rc.Project.RepoURL, rc.GitHubAuthToken, cfg.RepoDir+"/"+projectID})`, `brain.NewAdapterWithClient(anthropic client with option.WithAPIKey(rc.AnthropicAPIKey), brain.Config{Model: rc.Project.BrainModel})`, ports pre-bound to `projectID` via small adapters over `boardSvc`/`rtSvc`
  - agent provider: `AGENT_MODE=mock` ⇒ `mock.New()`; else `amika.New({BaseURL: cfg.AmikaBaseURL, APIKey: rc.AmikaAPIKey, RepoURL: rc.Project.RepoURL, Snapshot: rc.Project.AmikaSnapshot, ClaudeCredID: rc.AmikaClaudeCredID, WorkerPrefix: prefix}, nil)`
  - prefix: `cfg.WorkerPrefix + projectID[:8] + "-"`
- Wire: registry ⇒ runtime `BrainResolver` + agent `ProviderResolver` + `Projects` (identity `ListProjectIDs`) + notifier `Owner`; `idSvc.SetInvalidator(registry.Invalidate)`; identity may be nil ⇒ resolvers return "identity not configured" errors (events dead-letter feed-visibly, reconciler idles).
- `withProject`/`withSession` wiring for all routes (Server gains `EnableTenancy(projects ProjectResolver)` or extend `EnableIdentity`).

- [ ] **Full hard gate:** `export PATH="$HOME/go/bin:$PATH" && make check` — green, plus `make schema-verify` (no generated-type drift expected). Commit (hooks on) `feat(kiln): per-project runtime registry wiring — tenancy flip complete`.

---

### Task 12: Cross-tenant isolation + degradation tests (the spec §8 headline)

**Files:**
- Create: `backend/internal/api/tenancy_integration_test.go` (`//go:build integration`)

Seed two users+projects via identity service against `TEST_DATABASE_URL`; build a real `api.Server` over real postgres stores; mint two dev sessions; assert:
- A's board/feed/messages/notifications never contain B's rows (seed via B's session first).
- A cannot accept/dismiss B's ticket/card ids (404).
- SSE: A's stream receives no frame when B's board mutates.
- Degradation (unit-level, runtime): fake `BrainResolver` failing for A only → A's event dead-letters with a feed-visible Say; B's event processes.

- [ ] `go test -tags=integration ./internal/api/...` green → commit `test(tenancy): cross-tenant isolation + degradation coverage`.

---

### Task 13: Frontend session gate

**Files:**
- Create: `frontend/src/stores/session.tsx`, `frontend/src/components/SessionGate.tsx` (+ tests)
- Modify: `frontend/src/main.tsx` (wrap `/` and `/debug` routes)

Behavior: on mount `fetchMe()` — `null` ⇒ full-screen "Continue with GitHub" (`/auth/github/login`); `me.project == null` ⇒ "Finish setup on your dashboard" (link `/dashboard`); else render children. Loading state renders nothing (avoids SSE/feed churn before auth is known). `/dashboard` keeps its own existing gate. Vitest: three states via mocked transport.

- [ ] `pnpm run test` + typecheck green → commit `feat(web): session gate on the app root`.

---

### Task 14: E2E suites adopt dev-session auth

**Files:**
- Create: `tests/session.ts` — `mintSession(rc: APIRequestContext, login?: string)`: POST `/api/dev/session` `{github_login: login ?? process.env.KILN_BOOTSTRAP_GITHUB_USER ?? 'e2e-user'}` **relative to that context's own base** (page.request → :5173 proxy; request fixture → `KILN_E2E_API_URL`).
- Modify: every spec in `tests/tests/` — call `mintSession` in setup for **each** context the spec uses (`request` fixture and/or `page.request`; they have separate cookie jars). `dashboard-config.spec.ts` keeps its throwaway login.

- [ ] `cd tests && pnpm exec tsc --noEmit` (if configured) / Playwright lists tests → commit `test(e2e): mint dev session before driving the app`.

---

### Task 15: Local verification (the user-facing proof)

1. `cp /Users/mac/Desktop/kiln/.env ./.env` (worktree has none; never cat it).
2. Fresh stack: `docker compose down -v`, then up with `AGENT_MODE=mock KILN_BRAIN_MODEL=claude-haiku-4-5-20251001 KILN_DEV_ENDPOINTS=1 KILN_WORKER_COUNT=8 KILN_BOOTSTRAP_GITHUB_USER=crabtree-michael docker compose up --build -d`.
3. Boot assertions (via logs + API): migrations + bootstrap ran; `POST /api/dev/session {github_login:"crabtree-michael"}` → `GET /api/me` shows project + config fingerprints seeded from env.
4. 401 sweep: `curl` board/feed/message/stream/voice without cookie → 401.
5. Isolation smoke: mint `someone-else` session → empty board, cannot see crabtree's tickets.
6. `cd tests && pnpm test` (chromium project; voice smoke stays off) — full suite green.
7. Teardown check per memory: mock mode ⇒ no Amika sandboxes created; confirm.
8. Update memory (`multi-user-phase1-status` → phase 2 status) and mark spec 11 phase 2 implemented in the doc header if appropriate.

---

## Self-review notes

- Spec coverage: §2 boundary (Task 10), §3 tenant column (1, 9), §6 loop/registry/isolation/workers/voice+notify (7, 8, 4, 10, 11), §7 bootstrap/dev-auth/env (9, 14), §8 tests (12 + per-module), §9 frontend (13). Non-covered by design: per-user Amika base URL (platform env, spec amendment), multi-project UX (schema-ready only).
- Type consistency: `ProviderResolver.For` returns `(Provider, string, error)` — prefix travels with provider; registry `Providers.WorkerPrefix` feeds it. `RuntimeConfig` is the single credential-carrying type; it never crosses the wire.
- The `/debug` App view and `ResetSessionButton` ride the same gate + scoped reset.
