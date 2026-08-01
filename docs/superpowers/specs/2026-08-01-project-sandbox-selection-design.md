# Design: Sandbox selection & dev-box capture in projects

**Date:** 2026-08-01
**Status:** proposed — first-pass research & plan, for review (no implementation)
**Scope:** `internal/agent` (neutral port + capability), `internal/agent/amika` (new
catalog calls), `internal/agent/{mock,devin}` (no-op), the agent `Service` read seam,
`internal/api` (new project-scoped routes), `schema/openapi.yaml` (+ regen), and the
project form in `/frontend` (`ConfigFields.tsx` + a new dev-box panel).
**Relationship to specs:** extends the agent-runtime abstraction (`05`, and the
2026-07-11 multi-provider design) and the multi-project dashboard (`11`, `12`). It does
**not** touch the board/brain, the outbox/turn machinery, the `agent.turn_completed`
payload, or the event queue. This is a **read/inspector + one-shot-action** seam, not a
turn.

---

## 1. Problem & objective

A project's base sandbox image is chosen today by typing an **opaque snapshot handle** into
a free-text field (`amika_snapshot`, `ConfigFields.tsx:285-294`). The user has no way to see
what snapshots exist, and no way to turn a sandbox they've been working in — with its
installed deps, cached builds, and configured tools — into a reusable base image. Everything
downstream of that field already works: the handle flows per-project into
`buildAmikaProvider` (`registry.go:77`) and every worker Amika creates starts from it
(`POST /sandboxes {snapshot}`, `client.go:217`). The gap is entirely **discovery and
capture** in front of a field that already exists.

**Objective.** On the project-management surface, a user whose project runs on a
snapshot-capable provider can (a) **list** the snapshots available for their project's repo
and pick one, replacing the blind text field with a real selector; and (b) **capture** a
live dev box into a new named snapshot that then appears in that list — all without any module
outside `internal/agent` learning that "Amika" or "snapshot endpoint" exists.

### Non-goals (this pass)

- Snapshotting a **live board worker** mid-lifecycle. Deferred to a follow-up — it needs the
  board to quiesce the slot first (§11, §13). This pass captures **standalone / released**
  dev boxes only.
- Editing snapshots, snapshot versioning/tags, or a snapshot "library" UI beyond a flat
  per-repo list.
- Any new provider concept on the board/brain/event surface. If this design ends up touching
  those, the abstraction is leaking (§7).
- Presets/sizes selection (`xs/m/l/xl`, `coder/coder-dind`) — Amika exposes no enumeration
  endpoint for them (§10); left as a later, separate knob.

---

## 2. Terminology — the reconciliation that this whole plan hinges on

The ticket's words and Amika's model don't line up one-to-one. Getting this wrong makes the
rest incoherent, so it is fixed here and used everywhere below.

| Ticket says | Means (Kiln) | Amika object | Kiln today |
|---|---|---|---|
| "a sandbox" (selectable, one **per project**) | a saved base image you pick | **snapshot** (`/sandbox-snapshots`) | `projects.amika_snapshot` (opaque string) |
| "a dev box" (the box you *worked in*) | a live, running box | **sandbox** (`/sandboxes`) | a **worker** (board slot) |
| "list available sandboxes" | list selectable base images | `GET /sandbox-snapshots` | — (missing) |
| "save a dev box as a sandbox" | freeze a live box into a base image | `POST /sandbox-snapshots {sandbox_id}` | — (missing) |

So: **the thing you select per project is an Amika _snapshot_; the thing you capture _from_ is
an Amika _sandbox_ (a live box).** Because Kiln already calls Amika sandboxes "workers", this
doc uses the neutral terms **snapshot** (the selectable image) and **dev box** (a live box) to
keep both the ticket's intent and the abstraction rule satisfied. "Sandbox" as a bare word is
avoided from here on.

---

## 3. Current-state map (verified)

| Concern | Where | State today |
|---|---|---|
| Snapshot storage | `projects.amika_snapshot` col (`0001_identity.sql`); `identity.Project.AmikaSnapshot` (`entities.go:71`) | opaque string, per-project |
| Snapshot → adapter | `buildAmikaProvider` (`cmd/kiln/registry.go:77`) `Snapshot: d.Runtime.Project.AmikaSnapshot` | per-project value wins over `AMIKA_SNAPSHOT` bootstrap default |
| Snapshot → Amika | `CreateWorker` (`amika/client.go:213-236`), `snapshot` field, `omitempty` (`types.go:31`) | passed verbatim on every worker create |
| Snapshot edit UI | `ProjectFields` free-text input (`ConfigFields.tsx:167, 285-294`) | typed by hand; no validation, no list |
| Provider capability | `Capabilities{Snapshots: true}` (`amika/client.go:144-151`); wire `ProviderCapabilities.snapshots` (`openapi.yaml:1172`) | **`snapshots` already on the wire but unread in the frontend** — the natural gate |
| Live-box listing | `ListWorkers` → `GET /sandboxes`, filtered to `KILN_WORKER_PREFIX` (`client.go:193-209`) | lists Kiln workers only; no snapshot listing, no capture, anywhere |
| Per-project provider | `resolveTenantProvider`/`providerKeyFor` (`registry.go:124,203`); `Project.AgentProvider` | already resolves a `(Provider, prefix)` per project |
| Read/inspector seam | agent `Service.ListAgents`/`GetAgentUpdates` (used by `agentJoin` in `api/routes.go`) | precedent for a provider read that is **not** a turn |

Net: the *select* path exists end-to-end minus the picklist; the *list* and *capture* paths
are net-new and have **no** adapter method, DTO, port method, wire type, or route today.

---

## 4. Amika API surface (verified against `v0beta1` `llms.txt`, 2026-08-01)

Everything the feature needs already exists in the API Kiln targets. Auth is
`Authorization: Bearer <AMIKA_API_KEY>` (the per-**user** org key, already in `RuntimeConfig`).

**Snapshots (the selectable base images):**

| Method | Path | Notes |
|---|---|---|
| `GET` | `/sandbox-snapshots` | list; query filters **`repository_id`**, `source_sandbox_id`; returns `{ items: [...] }` |
| `POST` | `/sandbox-snapshots` | capture from a live box; body `{ name (req), description?, mode: scrub_and_delete\|full, sandbox_id\|sandbox_name\|sandbox_ref }`; **202**, async |
| `GET` | `/sandbox-snapshots/{ref}` | read; `by=name\|id\|ref\|name_or_id` |
| `DELETE` | `/sandbox-snapshots/{ref}` | 204 |
| `GET` | `/sandbox-snapshots/scrub-preview` | `?sandbox=&by=&sandbox_id=` → `{ files, env_vars }` that *would* be scrubbed |

Snapshot object: `id`, `snapshot` (the ref used at create time), `description`, `base_snapshot`,
`source_sandbox_id`, `source_sandbox_name`, **`repository_id`, `repository_url`**,
`sandbox_preset`, `sandbox_size`, `capture_mode`, **`state`**, `error_message`, `created_at`,
`updated_at`.

**Live boxes (dev boxes):**

| Method | Path | Notes |
|---|---|---|
| `GET` | `/sandboxes` | list the **org's** boxes (no project/scope query param) |
| `GET` | `/sandboxes/{id}` | read (id **or** name) |
| `POST/DELETE` | `/sandboxes`, `/sandboxes/{id}` | already used by `CreateWorker`/`DestroyWorker` |
| `POST` | `/sandboxes/{id}/start\|/stop` | start already used |

Live-box object carries `repo_url`, `snapshot`/`snapshot_name`, `state`
(`creating\|starting\|running\|stopping\|suspending\|suspended\|snapshotting\|failed\|unknown`),
`created_by{name,email}`, `secret_names`, `created_at`.

**Repositories (for scoping, §5):** `GET /repositories` → items with `id`, `repo_url`,
`default_snapshot`, `sandbox_preset`, `sandbox_size`. Lets us map a project's `repo_url` → a
`repository_id`.

**No enumeration endpoint** exists for presets (`coder`/`coder-dind`) or sizes
(`xs/m/l/xl`) — they are inline request params only.

---

## 5. Scoping: org-global vs per-repo vs per-Kiln-project

This is the single biggest semantic point, and it answers the ticket's "per project or
globally?" directly:

- **Live boxes are org-global.** `GET /sandboxes` has **no** project or scope filter. Kiln
  already lives with this: `ListWorkers` list-and-matches on `KILN_WORKER_PREFIX`
  (`client.go:206`). The multi-env prefix rule (05, adopt-first) exists precisely because the
  list is org-wide.
- **Snapshots are scopable by _repository_, not by Kiln project.** `GET /sandbox-snapshots`
  takes `repository_id`, and each snapshot carries `repository_url`. A Kiln project maps to
  exactly one `repo_url`, so **"snapshots for this project" = "snapshots whose
  `repository_url` matches the project's `repo_url`."**
- **Consequence to document for reviewers:** two Kiln projects on the **same repo** see the
  **same** snapshot list. That is correct and expected — the base image is a property of the
  code, not of the Kiln project record. There is no Kiln-project-private snapshot namespace and
  this design does not invent one.

**How we get the filter.** Two options, recommended in order:

1. **Client-side filter by `repository_url`** (recommended v1). List all snapshots, keep those
   whose `repository_url == project.RepoURL` (normalized). Zero extra lookups, no need to know
   the opaque `repository_id`. Cost: one unfiltered list per open of the selector.
2. **Resolve `repository_id` first** via `GET /repositories` (match `repo_url`), then
   `GET /sandbox-snapshots?repository_id=`. Server-side filter, one extra call, more robust if
   `repository_url` normalization is fussy. Fall back to this if (1) proves flaky against the
   live API.

Either way, always **include the project's current `amika_snapshot` value in the selector even
if it isn't in the list** (operator base images baked outside `/sandbox-snapshots`, e.g. the
`AMIKA_SNAPSHOT` default from `scripts/amika/setup.sh`, may not be listed), plus a free-text
"custom / other" escape so the field never becomes *less* capable than today.

---

## 6. The two questions of "what data is required"

**To select a snapshot for a project** (the ticket's "select one per project"): just the
snapshot **`snapshot` ref** (its stable handle) → stored in the existing
`projects.amika_snapshot`. Nothing new to persist; the selector only needs the *list* to
choose from. Show `name`/`description`/`created_at`/`state` to make the choice, store the ref.

**To capture a dev box into a snapshot** (the ticket's "save a dev box as a sandbox"):
- the **dev-box ref** (Amika `sandbox_id` or name) of the live box to freeze,
- a **name** for the new snapshot (required by the API),
- an optional **description**,
- a **scrub choice** — `scrub_and_delete` (default, strips Amika-injected secrets) vs `full`
  (capture as-is). Default to scrub; surface `scrub-preview` so the user sees exactly which
  files/env vars are removed before committing.

Capture returns **202 + a snapshot in a non-terminal `state`**; it is not immediately
selectable. The UI polls `GET /sandbox-snapshots/{ref}` until `state` is terminal, then
refreshes the selector. (Same async-then-poll shape the adapter already uses for worker
provisioning.)

---

## 7. Design principle: the abstraction rule is non-negotiable

Per `05 §1` and the multi-provider design: **nothing outside `internal/agent` may know Amika
exists.** So "list snapshots" cannot be an `amika`-shaped route. The plan introduces a
**neutral catalog seam**, mirroring how `CapabilityReporter` and `ListAgents` already let the
core vary provider-visible affordances without naming a provider:

- a new **optional** provider interface (`SandboxCatalog`), implemented by Amika, absent on
  Devin/mock;
- **neutral DTOs** (`agent.Snapshot`, `agent.DevBox`, `agent.CaptureRequest`) carrying no
  Amika field names, no session/job ids;
- gated in the UI by the **already-existing** `ProviderCapabilities.snapshots` bit (which the
  frontend currently ignores). No new required wire capability is strictly needed; adding a
  dedicated `snapshot_catalog` bit is an option (§8.5) if we want to distinguish "accepts a
  handle" from "can enumerate/capture."

If any of the board, brain, runtime event payloads, or wire types outside the two new
read/action DTOs end up mentioning a snapshot/dev-box provider concept, that is the tripwire
that the abstraction is leaking.

---

## 8. Proposed design

### 8.1 Provider port — a new optional interface (`internal/agent/provider.go`)

```go
// SandboxCatalog is the optional read/capture seam for providers that expose a
// selectable base-image catalog. Providers without a managed image catalog
// (devin, mock) simply do not implement it; the Service treats its absence as
// "no catalog" and the API returns an empty list / 501-shaped neutral error.
type SandboxCatalog interface {
    // ListSnapshots returns selectable base images, filtered to repoURL when non-empty.
    ListSnapshots(ctx context.Context, repoURL string) ([]Snapshot, error)
    // ListDevBoxes returns live boxes eligible to be captured, filtered to repoURL.
    ListDevBoxes(ctx context.Context, repoURL string) ([]DevBox, error)
    // CaptureSnapshot freezes a live box into a new snapshot (async; returns the
    // pending snapshot). Scrub=true maps to scrub_and_delete.
    CaptureSnapshot(ctx context.Context, req CaptureRequest) (Snapshot, error)
    // SnapshotStatus re-reads one snapshot so callers can poll capture completion.
    SnapshotStatus(ctx context.Context, ref string) (Snapshot, error)
    // ScrubPreview lists what CaptureSnapshot(scrub=true) would strip (names only).
    ScrubPreview(ctx context.Context, devBoxRef string) (ScrubPreview, error)
}

type Snapshot struct {
    Ref, Name, Description, RepoURL string
    Ready   bool      // terminal & usable (classified from provider state)
    Failed  bool
    Detail  string    // error_message when Failed, else ""
    CreatedAt time.Time
}
type DevBox struct {
    Ref, Name, RepoURL, State string
    CreatedBy string           // display only
    CreatedAt time.Time
}
type CaptureRequest struct { DevBoxRef, Name, Description string; Scrub bool }
type ScrubPreview struct { Files, EnvVars []string }
```

`Snapshot.Ready/Failed` are classified in the adapter (like `states.go`), so no provider state
string crosses the port. Optional `CapabilityReporter` unchanged; the Service discovers catalog
support by a plain interface assertion.

### 8.2 Amika adapter (`internal/agent/amika`)

Add to `client.go` (new calls) + `types.go` (new DTOs) + `states.go` (snapshot-state
classifier), all following the existing patterns:

- `ListSnapshots(repoURL)` → `GET /sandbox-snapshots`, decode `{items}`, **client-side filter
  by `repository_url == repoURL`** (§5 option 1; keep option 2 as a fallback flag). Map each to
  `agent.Snapshot`, `Ready/Failed` via `classifySnapshotState(state)` — un-enumerated, so
  default-defensive exactly like `classifyState` (05 §11 hardening note).
- `ListDevBoxes(repoURL)` → `GET /sandboxes`, filter by `repo_url` and to boxes in a
  capture-eligible state (`running`/`suspended`, **not** `snapshotting`/`creating`/`failed`).
  Deliberately **not** prefix-filtered: dev boxes worth capturing may be boxes the user spun up
  in Amika's own UI, not just `kiln-worker-*`. (Kiln workers still appear, labelled.)
- `CaptureSnapshot(req)` → `POST /sandbox-snapshots {name, description, mode, sandbox_ref}`
  (`mode = scrub_and_delete` when `Scrub`, else `full`); return the 202 snapshot as pending.
- `SnapshotStatus(ref)` → `GET /sandbox-snapshots/{ref}?by=name_or_id`.
- `ScrubPreview(ref)` → `GET /sandbox-snapshots/scrub-preview?sandbox=<ref>&by=name_or_id`.
- Interface assertion `_ agent.SandboxCatalog = (*Client)(nil)`; keep `Capabilities.Snapshots
  = true`.

These are **pure reads/actions** — they do **not** touch `agent_turns`, the outbox, or the
poller. They go through the existing `do()`/`APIError` plumbing and are covered by `httptest`
like `client_test.go`.

### 8.3 Agent `Service` — extend the read/inspector seam (`internal/agent/service.go`)

The Service already resolves a per-project provider (`ProviderResolver.For(projectID)`) for
`ListAgents`. Add thin methods that resolve, assert `SandboxCatalog`, and delegate:

```go
func (s *Service) ListSnapshots(ctx, projectID, repoURL) ([]agent.Snapshot, error)
func (s *Service) ListDevBoxes(ctx, projectID, repoURL) ([]agent.DevBox, error)
func (s *Service) CaptureSnapshot(ctx, projectID, agent.CaptureRequest) (agent.Snapshot, error)
func (s *Service) SnapshotStatus(ctx, projectID, ref) (agent.Snapshot, error)
func (s *Service) ScrubPreview(ctx, projectID, devBoxRef) (agent.ScrubPreview, error)
```

If the resolved provider does not implement `SandboxCatalog`, return a sentinel
`agent.ErrNoCatalog` → the API maps it to an empty result / neutral 501. These calls are
on-demand (user opened the panel), never on the reconcile/poll loops.

### 8.4 Runtime / API (`internal/api`)

New **project-scoped, owner-authorized** routes, registered beside the existing
`/api/projects/{pid}/...` handlers (`routes.go:522+`, all already behind `withProject`):

| Method | Path | Body / query | → |
|---|---|---|---|
| `GET` | `/api/projects/{pid}/snapshots` | — | `Service.ListSnapshots` (repoURL from the project) |
| `GET` | `/api/projects/{pid}/dev-boxes` | — | `Service.ListDevBoxes` |
| `GET` | `/api/projects/{pid}/dev-boxes/{ref}/scrub-preview` | — | `Service.ScrubPreview` |
| `POST` | `/api/projects/{pid}/snapshots` | `SnapshotCaptureRequest` | `Service.CaptureSnapshot` → 202 |
| `GET` | `/api/projects/{pid}/snapshots/{ref}` | — | `Service.SnapshotStatus` (capture polling) |

The API resolves `repoURL` from the project (never trusts the client for it), enforces the
same ownership check as the other `{pid}` routes, and hands the agent Service a `projectID` +
neutral request. **Selecting** a snapshot needs **no new endpoint** — it is the existing
`PUT /api/projects/{pid}` with `amika_snapshot` set to the chosen ref.

### 8.5 Wire schema (`schema/openapi.yaml`, then regen Go + TS)

Add neutral component schemas — no Amika field names:

- `Snapshot { ref, name, description, repo_url, ready, failed, detail, created_at }`
- `DevBox { ref, name, repo_url, state, created_by, created_at }`
- `SnapshotList { items: Snapshot[] }`, `DevBoxList { items: DevBox[] }`
- `SnapshotCaptureRequest { dev_box_ref, name, description?, scrub? }`
- `ScrubPreview { files: string[], env_vars: string[] }`
- the five routes above under `paths:`.

Capability gating reuses the **existing** `ProviderCapabilities.snapshots`. *Optional:* add a
`snapshot_catalog: bool` to `ProviderCapabilities` to distinguish "accepts a snapshot handle at
create" from "can enumerate/capture"; note this is a **required-field** wire change (all
providers must report it) so weigh it against just gating on `snapshots`. Recommendation:
**ship on `snapshots` first**, add the dedicated bit only if a provider appears that accepts a
handle but can't enumerate. Never hand-edit `internal/wire`/`generated.ts` — change the schema
and regenerate (per `wire-schema`).

### 8.6 Frontend (`/frontend`)

Two changes, both in the project surface, both capability-gated on
`provider.capabilities.snapshots` (the descriptor the form already receives via
`me.providers`; the frontend currently ignores this bit — this is the intended consumer):

1. **Snapshot field → selector** (`ConfigFields.tsx`, replacing the input at 285-294). When
   the selected provider reports `snapshots`, fetch `GET /api/projects/{pid}/snapshots` and
   render a `<select>` of `{name — description}` storing `ref` into the existing
   `amikaSnapshot` state; **keep a "custom…" option** that reveals the current free-text input
   so nothing regresses (and so operator base images / the `AMIKA_SNAPSHOT` default remain
   selectable). When the provider lacks the capability, render today's plain text input
   unchanged. Onboarding has no `pid` yet → free-text there, selector on edit.
2. **"Save current dev box as a snapshot" panel** (new, in the same collapsible project row).
   Lists `GET /api/projects/{pid}/dev-boxes`; on pick, shows `scrub-preview` (files + env vars
   to be stripped) and a name/description form + scrub toggle; `POST`s the capture; polls
   `GET …/snapshots/{ref}` until ready; then refetches the selector so the new snapshot is
   immediately pickable. Hidden entirely when `!capabilities.snapshots`.

Types come from the regenerated wire schema (TS-escape-hatch ban still applies). Follows the
existing data-driven-`<select>` precedent already used for the provider picker.

### 8.7 How selection actually takes effect

Setting `amika_snapshot` changes the **base image future workers start from**. Live workers
were already `CreateWorker`'d from the old snapshot and are **not** retroactively rebased. The
new image applies as the pool recycles (release → recreate, `05 §4`). The UI copy should say
so ("applies to workers created after this change"); if we want it to take effect immediately
we can trigger the existing worker-pool reconcile/recreate that `buildTenantProviders` already
performs on config change (`wiring.go:313`) — call that out as a decision (§12).

---

## 9. Flows

**List & select (the common path).**
1. User expands a project row → FE sees `provider.capabilities.snapshots` → `GET
   /api/projects/{pid}/snapshots`.
2. API resolves the project's `repoURL`, calls `Service.ListSnapshots(pid, repoURL)` →
   Amika `GET /sandbox-snapshots` → filter by `repository_url`.
3. FE renders the selector; user picks → `PUT /api/projects/{pid}` with the ref.
4. Future workers start from it.

**Capture (save a dev box).**
1. FE `GET /api/projects/{pid}/dev-boxes` → live boxes for the repo.
2. User picks one → FE `GET …/dev-boxes/{ref}/scrub-preview` → shows what's stripped.
3. FE `POST /api/projects/{pid}/snapshots {dev_box_ref, name, scrub:true}` → 202 pending.
4. FE polls `GET …/snapshots/{ref}` until `ready`.
5. FE refetches the snapshot list; the new snapshot is selectable; user selects it (flow
   above). Done.

---

## 10. API gaps & needs (Amika)

Findings that reviewers/the Amika team should note; none are blockers, but two shape the
design:

1. **No per-Kiln-project scope on any list.** Live boxes are org-global; snapshots scope only
   by **repository**. We adapt with repo-URL filtering (§5). *Nice-to-have:* a `repo_url` query
   param on `GET /sandboxes` (today we list-all-and-filter) and confirmation that
   `repository_url` on snapshots is stable/normalized enough for client-side matching (else use
   `repository_id`).
2. **Snapshot `state` is un-enumerated** (like sandbox `state`, `05 §11`). The adapter must
   classify defensively and be hardened against real values during implementation — the manual
   smoke run against live Amika is the gate.
3. **Capture is async (202)** with no idempotency key (v0beta1 has none anywhere). A
   double-click could start two captures; the FE must disable-on-submit and the API should be
   safe to retry-read. Not exactly-once, but capture is a deliberate user action, not an
   autonomous retry loop, so this is acceptable — *do not* route it through the outbox trying
   to make it so.
4. **No preset/size enumeration endpoint** — `coder/coder-dind`, `xs/m/l/xl` are inline params
   only. Preset/size selection is therefore out of scope here; if wanted later it's a static
   picklist, not a fetched one.
5. **Auth** for all catalog calls is the same per-user org Bearer key already in
   `RuntimeConfig.AmikaAPIKey`; no new secret, scope, or storage.

---

## 11. Security & correctness considerations

- **Scrub by default.** Capturing a dev box can bake in Amika-injected secrets and the owner's
  git credential (private-repo support passes the PAT into the sandbox clone,
  2026-07-12 design). Default `scrub_and_delete`, always show `scrub-preview` before capture,
  and require an explicit opt-in for `full`. Never default to `full`.
- **Ownership.** Every new route is behind the existing `{pid}` owner-authorization check; a
  user can only list/capture against their own project's repo. But note the **org-global**
  reality: the underlying Amika key can see the whole org's boxes/snapshots. The API's repo
  filter is what keeps a user from browsing another project's boxes — treat the filter as a
  **security boundary**, apply it server-side, and never let the client pass an arbitrary
  `repo_url`.
- **No board coupling this pass.** Capturing a *live board worker* would race the board's pull
  (a `snapshotting` box must not receive a ticket). Excluding board workers from the
  capture-eligible set (or requiring release first) sidesteps it; the full "snapshot the box
  the agent just worked in" story is deferred to §13 with a board-quiesce step.

---

## 12. Open decisions for review

1. **Capture source (v1).** Standalone/released dev boxes only (recommended), vs. also allowing
   in-pool board workers with a quiesce step. Recommendation: standalone first.
2. **Repo filter mechanism.** Client-side `repository_url` match (recommended) vs.
   `repository_id` resolution via `GET /repositories`. Pick based on how clean live
   `repository_url` values are.
3. **Dedicated capability bit** `snapshot_catalog` vs. reusing `snapshots`. Recommendation:
   reuse `snapshots` now.
4. **Selection effect timing.** "Applies to new workers" (simplest) vs. trigger an immediate
   pool recreate on snapshot change. Recommendation: document "new workers"; add immediate
   recreate only if users expect it.
5. **Where the panel lives.** Inline in each `ProjectFields` row (recommended — consistent with
   the shared form) vs. a dedicated `/projects/{id}/sandboxes` sub-route.

---

## 13. Phasing

- **Phase 1 — Select (highest value, lowest risk).** Port `ListSnapshots`, adapter call,
  Service method, `GET …/snapshots` route, wire types, and the FE selector (with free-text
  fallback). No capture. Ships the ticket's core "list + select per project."
- **Phase 2 — Capture standalone dev boxes.** `ListDevBoxes`/`CaptureSnapshot`/`SnapshotStatus`/
  `ScrubPreview` port + adapter + routes, and the FE capture panel with scrub preview + polling.
- **Phase 3 (deferred) — Capture a live board worker.** Add a board-quiesce handshake so a slot
  can be frozen without racing the pull; only then expose in-pool workers as capture sources.

---

## 14. Testing

- **Adapter:** `httptest` for each new call (list filter, capture 202, status poll, scrub
  preview, `APIError` envelopes) — mirror `client_test.go`; no live calls.
- **Service:** mock provider with/without `SandboxCatalog` → asserts `ErrNoCatalog` path and
  delegation; confirms these never touch `agent_turns`/outbox.
- **API:** ownership enforcement, server-side repo filter, project→repoURL resolution, empty
  list for non-catalog providers.
- **Frontend:** capability-gated rendering (selector vs. text input vs. hidden panel), the
  custom/free-text fallback, capture-poll state machine; image-snapshot the new panel per the
  `web-client` snapshot targets.
- **Manual smoke (the real gate, `05 §10`):** run each call against live Amika once to harden
  `classifySnapshotState` and confirm `repository_url` filtering — the un-enumerated states can
  only be nailed down against the live API.

---

## 15. File-touch map (appendix, for the implementer)

- `backend/internal/agent/provider.go` — `SandboxCatalog`, `Snapshot`/`DevBox`/`CaptureRequest`/
  `ScrubPreview`, `ErrNoCatalog`.
- `backend/internal/agent/amika/{client.go,types.go,states.go,client_test.go}` — calls, DTOs,
  `classifySnapshotState`, tests.
- `backend/internal/agent/service.go` — five delegating read/action methods.
- `backend/internal/api/{routes.go,identity_handlers.go or a new sandbox_handlers.go}` — five
  routes + repo-URL resolution + ownership.
- `schema/openapi.yaml` (+ regenerated `backend/internal/wire` and `frontend/src/schema`).
- `frontend/src/dashboard/ConfigFields.tsx` (selector), a new dev-box capture component, and
  `frontend/src/transport/transport.ts` (typed calls).
- No board / brain / runtime-event / outbox changes — if you touch them, stop; the abstraction
  is leaking.
</content>
</invoke>
