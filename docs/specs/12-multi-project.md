# Kiln — Multi-Project Support (Design)

**Date:** 2026-07-16
**Status:** Draft / design — for discussion
**Scope:** Lifts the "one project per user" pin left in place by `11` §3.
**Relationship to `01`–`11`:** `11` designed the whole system to be multi-tenant on
`project_id` and explicitly deferred *only the UX* of many-projects-per-user
("schema-ready, UX-deferred" — `11` §3, §10 non-goals). This document is that deferred
UX/API change. It does **not** revisit identity (`11` §2), secrets-at-rest (`11` §3 D7),
the module topology (`02` §2), or the wire-contract discipline (`02` §3–§4).

---

## 1. Purpose & the starting position

Today a user owns exactly one project. That limit is **not** structural — `11` built
tenancy on `project_id` from the ground up and left a single, deliberately-removable pin:

> "One project per user for now, enforced by a unique index on `owner_user_id` that is
> **dropped, not migrated,** when multi-project arrives. Everything downstream keys on
> `project_id` from day one, so 'many projects' is a UI/API change, not a schema change."
> — `11` §3

An audit of the current tree confirms this. The plumbing that *would* be hard is already
done:

- **Every stateful table already carries `project_id`** — board (tickets, workers,
  outbox), `events`, `messages`, `notifications`, push subscriptions
  (`0007/0008_project_id.sql`, `0002_project_id.sql` per module).
- **The event queue is already per-project serial, cross-project concurrent** — the claim
  excludes the in-flight project set (`project_id <> ALL($1::uuid[])`,
  `runtime/postgres/store.go:114`), so N of a user's projects process in parallel with the
  same one-event-at-a-time-per-board invariant (`06`).
- **The runtime is already one-brain-per-project** — the `tenant.Registry`
  (`internal/tenant/registry.go`) lazily builds and caches a provider bundle (brain, agent
  client, repo workspace, worker seeding) keyed by `project_id`, and enumerates *all*
  projects at startup via `ListProjectIDs` (`identity/service.go:358`). It never assumed
  one project.
- **Credentials already live on the user, not the project** (`user_config`, `11` §3 D4),
  so several projects owned by one user already share one credential set with no change.

What is left is genuinely small and concentrated. This document identifies the exact pins,
proposes how to remove them, and surfaces the decisions worth making deliberately.

### 1.1 The three pins that remain

Multi-project is blocked by exactly three couplings to "one project per user":

1. **The unique index + owner-keyed upsert** (data model). `one_project_per_owner`
   (`identity/postgres/migrations/0001_identity.sql:42`) and, riding on it,
   `UpsertProject`'s `ON CONFLICT (owner_user_id) DO UPDATE` (`postgres/store.go:305`).
   The upsert *overwrites the user's project in place* — it is structurally incapable of
   creating a second one.
2. **The scoping choke point** (API). `withProject` (`api/session.go:83`) resolves a
   request to a project by owner lookup — `ProjectFor(user.ID)` →
   `GetProjectByOwner` (`identity/service.go:325`). The client never says *which* project
   because there is only one. There is also **no ownership authorization** today (there
   can't be a wrong project to ask for), which multi-project must add.
3. **The implicit project on the client** (UX). The `/app` board/feed/stream/message
   calls carry **no project parameter** (`transport.ts` — bare `/api/board`, `/api/feed`,
   `new EventSource('/api/stream')`), the `Me` wire type carries a single optional
   `project` with **no `id`**, and no project name is shown anywhere in the app
   (`PrimaryScreenView`/`Dock`/`HeaderStatusMenu` reference no project). There is no
   client-side notion of a "current project".

Everything below is in service of removing exactly these three.

---

## 2. Data model

**Change 1 — drop the pin.** New migration in the identity module:

```sql
DROP INDEX one_project_per_owner;
ALTER TABLE projects ADD COLUMN deleted_at timestamptz;  -- NULL = live; set = soft-deleted (§5)
```

The nullable `deleted_at` column backs soft-delete (§5); every project read path filters
`deleted_at IS NULL`. No data migration (`11` §3): existing single-project users simply become
"users who happen to own one project". Keep the FK `owner_user_id → users(id) ON DELETE
CASCADE` and all later columns (`amika_snapshot`, `worker_count`, `merge_gate_mode`,
`agent_provider`, `amika_secrets`) exactly as they are.

**Change 2 — split create from update.** With the conflict target gone, `UpsertProject`
must split into two intents (`identity/service.go`, `postgres/store.go`):

- `CreateProject(ctx, ownerUserID, ProjectUpdate) (Project, error)` — plain `INSERT`,
  returns the server-generated `id`.
- `UpdateProject(ctx, projectID, ProjectUpdate) (Project, error)` — `UPDATE ... WHERE id
  = $1`, **guarded by an ownership check** (the caller's user must own `projectID`).

`GetProjectByOwner` (the single-row-by-owner query) is retired from the request path; the
runtime already uses `GetProject(id)` and `ListProjectIDs`, which are unaffected.

**Credentials stay on the user.** `user_config` remains one row per user, shared by every
brain the user owns — this is already how `RuntimeConfig` resolves (project → owner →
`user_config` → decrypt, `identity/service.go:408`). No change. See the trade-off in §7.1
about projects that need *different* repo tokens.

**Optional guardrails (decide in §7):**
- Uniqueness of `name` per owner (`UNIQUE (owner_user_id, lower(name))`) — nice for the
  switcher, but forces disambiguation on the user. Recommend **not** enforcing; show
  created-at ordering and let names collide.
- A soft per-user project cap (each project = one brain + one repo clone + up to
  `worker_count` workers; see §5.3). Recommend a config'd cap (`KILN_MAX_PROJECTS_PER_USER`,
  default e.g. 10) rather than an unbounded fan-out at invite scale.

---

## 3. API surface & scoping

Two categories of endpoint, matching how they scope today:

### 3.1 Project management (session-scoped — the dashboard's CRUD)

The ID-less singular endpoints become an ID'd collection. All session-protected, all types
regenerated from `/schema` (`02` §3):

| Today | Multi-project |
|---|---|
| `GET /api/me` → `{user, settings, project?, providers?}` | `GET /api/me` → `{user, settings, projects[], providers?}` |
| — | `GET /api/projects` → `MeProject[]` (each **with `id`**) |
| `PUT /api/project` (creates-or-updates the one) | `POST /api/projects` (create) → `MeProject`; `PUT /api/projects/{id}` (update) |
| — | `DELETE /api/projects/{id}` (see §5) |
| `POST /api/settings/verify` (uses the one project's repo) | `POST /api/projects/{id}/verify` (repo check is per-project; the Amika/Anthropic checks stay per-user) |

**The critical wire change: `MeProject` gains an `id`** (`schema/*`, regenerated to
`wire.MeProject` and `frontend/src/schema`). Today it has none — because there was never a
second one to distinguish. Every other field is unchanged.

`Me.project?` (optional singular) becomes `Me.projects` (a list, possibly empty). "Not
onboarded" changes from `me.project == null` to `me.projects.length === 0` — the single
discriminator that appears in exactly three UI spots (§6).

### 3.2 Board/feed/stream (project-scoped — the app's live surface)

These need the client to name *which* project. **Recommended: a `project` path segment**,
`/api/projects/{id}/{board,feed,feed/history,activity,messages,message,stream,tickets/...}`.
The one hard constraint driving this: **`EventSource` cannot set request headers**, so the
project cannot ride a header for the stream — it must be in the URL. A path segment is
chosen over a `?project=` query param for REST clarity and because it makes the
authorization boundary (`{id}` → ownership check) uniform across REST and SSE. (A query
param is a lighter diff if route churn is a concern — noted as an alternative in §7.2.)

**`withProject` is rewritten** (`api/session.go:83`) from *owner lookup* to *resolve +
authorize*:

```
withProject(next):
  user := withSession(...)
  id   := pathParam("id")
  proj := projects.GetProject(ctx, id)          // by id, not by owner; filters deleted_at IS NULL
  if err == ErrNotFound            → 404 (also covers a soft-deleted project — §5)
  if proj.OwnerUserID != user.ID   → 404 (not 403 — don't confirm existence to non-owners)
  next(w, r, user, proj)
```

This adds the **cross-tenant authorization check that does not exist today** (there was
never a foreign project to request). It is the security heart of this change and its
headline test (§8).

### 3.3 Voice & notifications

- **Voice** (`POST /api/voice/token`) stays user-scoped (platform AssemblyAI key,
  `11` §6). The resulting transcript is posted as a `human.message` to whatever project the
  client is currently viewing — i.e. it flows through the §3.2 project-scoped message
  endpoint. No token change.
- **Push subscriptions** stay per-user (`11` §3). A notification already carries its
  `project_id`; §6.3 covers deep-linking a tap to that project.

---

## 4. User experience

The mental model: **a user owns a set of projects; the app shows one at a time; the
dashboard manages all of them.**

### 4.1 The app (`/app`) — a current project + a switcher

- Introduce a **current-project** notion on the client (new store, e.g.
  `stores/current-project`), persisted to `localStorage` and defaulting to the
  most-recently-used (else the first by `created_at`). This is the single value that keys
  every board/feed/stream/message call in §3.2.
- Surface it in the app chrome. Add a compact **project switcher** in the header
  (`HeaderStatusMenu` is the natural home) listing the user's projects + "New project…". The
  client **names and references each project by its `project_id`**, not by a user-friendly
  name — the id from `MeProject.id` (§3.1) is the identifier the switcher keys on, the value
  the current-project store persists, and the token every §3.2 call scopes by. The dock and
  feed are otherwise unchanged.
- **Switching projects** tears down and re-opens the single `EventSource` against the new
  project's stream and refetches board/feed. Because the queue is already per-project
  serial/cross-project concurrent (§1), the other projects keep running server-side while
  hidden.
- **Zero projects** → the existing "Finish setup on your dashboard" gate
  (`SessionGate.tsx:84`), now keyed on `me.projects.length === 0`.

### 4.2 The dashboard (`/dashboard`) — manage many

- The single-project gate (`me.project ? Settings : Onboarding`, `Dashboard.tsx:33`)
  becomes a **project list**: account + credentials (per-user, unchanged) at top, then a
  list of project cards, each opening the existing `ProjectFields` form (`ConfigFields.tsx`
  is already reusable for create *and* edit — it just needs an `id` to target
  `PUT /api/projects/{id}`). A "New project" affordance runs the same onboarding form
  against `POST /api/projects`; "Delete" per card (§5).
- Onboarding (zero projects) is unchanged in shape — it just posts to the new create
  endpoint and lands the user in the list.

### 4.3 Creating / switching / managing — the flows

- **Create:** dashboard "New project" (or the app switcher's "New project…", which routes
  to the dashboard form) → name + repo + snapshot + worker/merge settings →
  `POST /api/projects`. Credentials are already set (per-user), so a second project skips
  the credential step entirely — a meaningfully lighter onboarding than the first.
- **Switch:** app header switcher, client-side, instant (re-scope calls; no server state).
- **Rename/reconfigure:** dashboard card → `PUT /api/projects/{id}`.
- **Delete:** dashboard card → confirm → `DELETE /api/projects/{id}` (§5).

---

## 5. Project deletion

Deletion is the one genuinely *new* piece of mechanism — everything else is generalizing
existing plumbing. It matters because **there is no database-level cascade to lean on**:
`projects` lives in the identity module's tables, while board/events/messages/notifications
live in other modules' schemas and carry `project_id` as a plain column with **no FK to
`projects`** (verified — the `*_project_id.sql` migrations add columns, not constraints).
So deletion is an **application-level cascade across modules**, not `ON DELETE CASCADE`.

The good news: the seam already exists. `Reset(ctx, projectID)`
(`api/routes.go:162`, the `Resetter` behind `POST /api/dev/reset`) already tears down one
project's board/events/messages and its Amika sandboxes. Deletion is *reset + evict the
tenant + soft-delete the project row (retained, not removed)*:

1. **Quiesce** — stop claiming new events for the project and let/await any in-flight event
   drain (the busy-set worker already serializes per project; deletion waits out or cancels
   the current claim).
2. **Cascade state** — reuse/extend the `Reset` path to purge board rows, events, messages,
   notifications, push targeting, and Amika sandboxes/workers for `project_id`.
3. **Evict runtime** — `tenant.Registry.Invalidate(projectID)` (already exists,
   `registry.go:205`) drops the cached bundle and closes per-tenant resources; also remove
   the on-disk repo clone at `RepoDir/<projectID>` (which `Invalidate` deliberately keeps —
   deletion is the one caller that should remove it).
4. **Soft-delete the row** — the `projects` row is **marked deleted, not removed**:
   `UPDATE projects SET deleted_at = now() WHERE id = $1 AND owner_user_id = $2` (new
   nullable `deleted_at` column in the identity migration of §2). The row is retained in the
   database; every read path filters it out (`WHERE deleted_at IS NULL`), so a soft-deleted
   project no longer resolves in `withProject` (§3.2), `ListProjectIDs`, `Me.projects`, or the
   switcher, and its id can never be reused. The state cascade (steps 2–3) still runs, so a
   soft-deleted project holds only its retained metadata row, not live board/runtime state.

**Decisions to make (§7):** whether to allow deleting a project with active/blocked tickets or
require it be idle first; and idempotency if a later cleanup is retried after the row is
marked deleted.

---

## 6. Integration points

Each existing feature and how it adapts. The theme: most need **nothing** because tenancy
was already threaded; the exceptions are the client (§4) and notification routing (§6.3).

### 6.1 Board, tickets, agents, workers — no runtime change

One brain per project already (`tenant.Registry`); per-project serial event processing
already; `worker_count` is already a per-project WIP cap (`projects.worker_count`,
`11` §6). A user with three projects simply has three cached bundles and three independent
event streams. **No board/brain/agent code changes** — they only ever saw a `project_id`.

### 6.2 Dashboard, verify, settings

Covered in §3.1/§4.2. `verify`'s repo check becomes per-project (`/api/projects/{id}/verify`);
the Amika/Anthropic credential checks remain per-user and can be surfaced once at the
account level rather than per project.

### 6.3 Notifications — deep-link the tap to the right project

Push subscriptions stay per-user; a notification already carries `project_id`. The **new**
requirement: a notification tap must land the app **on that project**. The notification
payload carries the `project_id` — the client references the project by that id (not a name),
consistent with the switcher (§4.1) — and the tap-handler
(`notifications` skill, frontend registration/tap path) sets the client's current project
(§4.1) before/while opening `/app`. Without this, a tap could open the app on a *different*
project than the event that fired the notification — a real cross-project confusion bug to
design out from the start.

### 6.4 Bootstrap & dev auth — unchanged

`ensureBootstrapProject` (`cmd/kiln/bootstrap.go:117`) still find-or-creates the operator's
first project from env — idempotent, still correct (it targets "does this user have *a*
project"; with the index gone it should check "any project" rather than rely on the upsert,
a one-line change). `POST /api/dev/session` still mints a user+session and lets the project
be created via the API — it never picked a project, so it is unaffected.

### 6.5 Automation / the event loop — unchanged

The per-project claim (`store.go:114`) and busy-set worker (`worker.go`) are already the
multi-project concurrency model. More projects = more distinct `project_id`s in flight = more
cross-project parallelism, bounded only by the worker pool size. No change.

---

## 7. Open questions & trade-offs

### 7.1 Per-user vs per-project credentials (the main real trade-off)
Credentials live on the user and are shared by all their projects (`11` §3 D4). This is
clean and already implemented, and it makes second-project onboarding nearly free. The
**GitHub repo token** is the one credential that is plausibly *repo-specific*: two projects
in different orgs may need different PATs, and the token also feeds the brain's repo
inspection. **Decision: the repo token is retrieved per-user, not per-project** — the
get-repo-token path resolves the token at the user level (project → owner → `user_config`),
never keying on `project_id`. We accept "one PAT must cover all your repos" (fine for a solo
dev's own repos). A per-project repo-token override in `projects` that falls back to
`user_config` remains a later, additive column — it does not block anything here, and it does
not change the default user-level retrieval decided here.

### 7.2 How the client names the project — path vs query vs header
`EventSource` can't send headers, so the stream forces the id into the URL. Recommended:
**path segment** (`/api/projects/{id}/...`) for uniform REST+SSE authorization. Alternative:
**query param** (`?project=`) — a smaller route diff but muddier for POSTs. A server-side
"active project" stored on the session was considered and rejected: it re-introduces mutable
server state, breaks multiple tabs viewing different projects, and hides the tenant boundary
that should be explicit and testable. Recommend explicit-in-URL.

### 7.3 Project deletion semantics
**Decided: soft delete** — the `projects` row is marked `deleted_at` and retained; the
state/runtime cascade still purges live board/events/messages/notifications/sandboxes, and
all read paths filter `deleted_at IS NULL` (§5). Retaining the row preserves an audit trail
and guarantees the id is never reused. Still open: block deletion of a project with
in-flight/blocked tickets vs force-drain; and retry/idempotency of the cross-module cascade
(§5), reusing the `Reset` path.

### 7.4 Guardrails
Name uniqueness per owner (recommend no — the client references projects by `project_id`, not
name (§4.1), so name collisions are harmless); a per-user project cap (recommend a config'd soft
cap — each project is a brain + a repo clone + a worker budget, so unbounded fan-out has a
real resource cost even at invite scale).

### 7.5 Explicitly out of scope (consistent with `11`)
Teams / shared projects / multiple owners, billing/quotas beyond a soft cap, and open
signup — all remain `11` non-goals. Access stays single-owner; authorization is the owner
check in §3.2.

### 7.6 Stream scope: current project only, or all owned?
Recommended: the app's one `EventSource` is scoped to the **current** project; background
projects reach the user via Web Push (§6.3), not a live stream. Multiplexing all owned
projects onto one stream is possible (server tags each event with `project_id`) but adds
fan-out and buys little while the UI shows one project at a time. Revisit if a
cross-project "activity across all my projects" view is ever wanted.

---

## 8. Testing (the hard gate, `02` §4)

The headline is unchanged from `11` §8 but now has *teeth within a single user*:

- **Cross-project authorization (the new security test):** a user who owns projects A and B
  can read/mutate A and B; a request for a project they do **not** own returns 404 (never
  200, never a leak); the SSE stream and every `/api/projects/{id}/*` route enforce the same
  `OwnerUserID` check (§3.2). This is the test that did not exist before, because a foreign
  project did not exist before.
- **Data model:** dropping `one_project_per_owner` lets a user hold ≥2 projects;
  `CreateProject` makes distinct ids; `UpdateProject`/`DELETE` refuse a project the caller
  doesn't own; store tests keep the `project_id` predicate on every query.
- **Runtime isolation (already covered by `11` §8, re-asserted per-user):** two projects of
  one user process concurrently; one with a broken credential fails feed-visibly on that
  project while the other keeps running.
- **Deletion:** cascade purges board/events/messages/notifications/sandboxes for the id,
  evicts the tenant bundle and repo clone, and is idempotent; the `projects` row is **retained
  with `deleted_at` set** and no longer resolves in `withProject`/`ListProjectIDs`/`Me.projects`
  (its id is never reused); a sibling project is untouched.
- **Frontend/E2E:** switch current project → board/feed/stream re-scope; second-project
  onboarding skips credentials; a notification tap lands on the firing project (§6.3).

---

## 9. Rollout

Additive and low-risk — the schema doesn't move (§1), so this ships as three ordered,
independently-deployable steps:

1. **Backend, backward-compatible.** Add `id` to `MeProject`; add `GET /api/projects`,
   `POST /api/projects`, `PUT/DELETE /api/projects/{id}`, `POST /api/projects/{id}/verify`;
   add the project-scoped `/api/projects/{id}/{board,feed,stream,...}` routes with the
   ownership-authorizing `withProject`; drop the unique index; split create/update. Keep the
   old singular `PUT /api/project` and bare board routes alive, mapped to "the user's first
   project", so nothing breaks mid-migration.
2. **Frontend.** Current-project store + header switcher; scope all app calls by id;
   dashboard project list + create/delete; notification deep-link. Now a user can hold and
   switch between many projects.
3. **Retire the singular routes** once the client no longer calls them.

No user data migrates; existing accounts wake up owning exactly one project and can add a
second whenever they like.

---

## 10. Decision log (proposed)

- **DP1 — Drop the pin, don't migrate.** `DROP INDEX one_project_per_owner`; no data change
  (`11` §3 designed this).
- **DP2 — Split `UpsertProject` into create + update-by-id.** The `ON CONFLICT
  (owner_user_id)` upsert is structurally single-project; ids must be explicit.
- **DP3 — Project id in the URL (path segment), authorized by owner in `withProject`.**
  `EventSource` can't carry a header; explicit-in-URL keeps the tenant boundary visible and
  testable; the owner check is the new security boundary. No server-side "active project".
- **DP4 — Credentials stay per-user, including the repo token.** Already implemented; makes
  2nd-project onboarding free. The get-repo-token path resolves per-user (not per-project); a
  per-project repo-token override is a later additive option (§7.1).
- **DP5 — Client owns "current project", referenced by `project_id`.** The client names and
  keys projects by their `project_id` (not a user-friendly name); `localStorage`-persisted,
  MRU default; stateless server; supports multiple tabs. Stream scoped to current project;
  background projects use Web Push.
- **DP6 — Deletion is a soft delete plus an app-level state cascade via the existing `Reset`
  seam.** No DB FK cascade exists across module schemas; reuse `Reset(projectID)` + tenant
  `Invalidate` + clone removal to purge live state, then **mark the `projects` row
  `deleted_at` (retain, don't remove)** and filter `deleted_at IS NULL` on every read path.
- **DP7 — Single-owner only.** Teams/sharing/billing/open-signup stay `11` non-goals;
  authorization is the owner check.
</content>
</invoke>
