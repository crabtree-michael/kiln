# Multi-provider agent runtime — design

**Date:** 2026-07-11
**Status:** Proposed (design only — no implementation in this change)
**Scope:** `internal/agent` (the Provider port and Service), `internal/agent/{amika,mock}`
and future adapter packages, `internal/identity` (the project entity's provider
config), `cmd/kiln` (composition-root provider selection), the `AGENT_MODE` /
`AMIKA_*` configuration surface, and the `/dashboard` provider-config surface
(`frontend/src/dashboard`, `11` §5) plus the `/schema` wire types its config blob
generates. No board, brain, runtime, or **agent-event-contract** changes — the neutral
`Send`/`Release` verbs and the `agent.turn_completed` payload are frozen; that is the
whole point of the exercise. The config/dashboard surface *does* change, by design
(§7–§9): generalizing per-project provider config is how the leak gets paid down.
**Builds on:** spec `05` (agent runtime) and its abstraction rule; `02` §8; `11` §3
(the per-project `ProviderResolver`).

## Problem

Spec `05` already split the agent layer in two: a **provider-neutral contract** the
rest of Kiln depends on, and an **Amika adapter** that implements it. The neutral
`Provider` port (`internal/agent/provider.go`) was designed against one real platform —
**Amika v0beta1** — and Amika happens to expose *caller-managed sandboxes*: long-lived
remote workspaces the client creates, wakes, and destroys by name. The port's shape
quietly assumes that model: `ListWorkers / CreateWorker / WorkerReady / DestroyWorker`
are four of its seven methods.

We now want to support providers that **do not** work that way:

- **Devin** (Cognition) — hosted SaaS, but the execution environment is **hidden inside
  a session**. There is no caller-managed sandbox, machine, or VM object to list or
  destroy. You create a *session* from a prompt and poll it.

Devin is the concrete second provider this design is built against; the same shape
covers any future hosted platform whose execution environment lives inside an opaque
session rather than a caller-managed workspace.

If we add each of these by teaching the core new special cases, provider vocabulary
leaks back into the layer we deliberately cleaned — violating `05`'s abstraction rule
("nothing outside this module may know Amika exists"). Worse, some Amika vocabulary has
**already leaked upward** into config and the project entity (see §7). This document
decides how to add providers of *different shapes* while keeping the leak surface at
exactly one place: a new adapter package plus its config.

**Objective:** a provider model where adding Devin or any future platform touches only an
adapter + a config registry (and its dashboard descriptor, §8), and where a provider's
*shape* (sandbox vs session, cost-reporting vs not, idempotent vs not) is expressed
through a small, declared capability surface rather than by branching in the core.

## What already exists (and is already right)

Before proposing anything, the parts of `05` that survive multi-provider **unchanged**,
because getting credit for them is half the design:

| Concern | Where | Already neutral because |
| --- | --- | --- |
| The three-verb consumer contract | `AgentRuntime{Send, Release}` + `agent.turn_completed` event (05 §2.1–2.2) | Says nothing about sandboxes/sessions. A provider swap never touches it. |
| Idempotency / dedupe | `agent_turns` table keyed by outbox id (05 §7) | The module owns its own dedupe *precisely because* Amika lacks idempotency keys. A provider that *does* have them (Devin) simply doesn't need the module's — the table is harmless and stays. |
| Cost reporting | `TurnStatus.CostUSD`, "0 when the provider doesn't report cost" (`provider.go:106`) | Already models the absence of cost data. A provider that reports nothing leaves the field at 0. |
| Cross-provider error taxonomy | `ErrConversationLost`, `ErrOutOfCredits`, `ProviderErrorFields` (`provider.go:28–55`) | Neutral sentinels an adapter maps *into*; the machine reacts to the sentinel, not the platform. |
| Per-project provider resolution | `ProviderResolver.For(ctx, projectID) → (Provider, prefix)` (`provider.go:23`) | Already the seam where "which provider serves this project" is answered. Multi-provider is a *fuller* implementation of the same interface, not a new one. |
| The turn state machine, reconciler, poller, mock | `internal/agent/service.go` + `mock` | Written once against the port; provider-agnostic already. |

So the neutral **contract** is sound. The work is entirely about the **internal port
shape** (§4), the **config surface** (§7), and **provider selection** (§6).

## The two providers at a glance

Grounded in the API research (sources at the end). The axes are the ones that actually
change how an adapter maps onto the port.

| Axis | **Amika** (v0beta1) | **Devin** (Cognition) |
| --- | --- | --- |
| Hosting | Hosted-managed | Hosted-managed |
| Caller-managed sandbox/worker? | **Yes** — `POST /sandboxes`, addressable by name; the only provider with this | **No** — environment hidden inside a session (`snapshot_id` is the only handle) |
| Unit of work | `agent-send-jobs` (async job) on a sandbox | `session` (created from a prompt) |
| Continue a conversation | reuse `session_id` on the sandbox | `POST /sessions/{id}/messages` |
| Sync vs async | **async + poll** (202, no webhooks) | **async + poll** (`GET /sessions/{id}`, `status_enum`) |
| Auth | `Authorization: Bearer <AMIKA_API_KEY>` | `Authorization: Bearer cog_…` (service key or PAT) |
| Idempotency | **None** (module supplies its own) | `idempotent` body flag + `is_new_session` |
| Cost reporting | `cost_usd` on the job | **ACUs** (`max_acu_limit`; per-session ACU accounting) |
| Getting started | Amika account + API key + repo/snapshot config | Team/Enterprise plan → Service User API key |

These two sit at opposite ends of the one axis that matters most — **caller-managed
sandbox vs no managed sandbox** — which is exactly why they are the right pair to design
against: an architecture that cleanly spans both spans anything hosted in between. A
purely local agent (one that runs against a developer's own machine with no reachable
hosted endpoint) is a different beast and out of scope for a hosted orchestrator — see
the open questions.

## The core insight: one port, two capabilities

The `Provider` port bundles two *separable* responsibilities that Amika happens to
provide together:

1. **Turn execution** — begin a turn from a message, poll it to a terminal outcome, read
   the latest output. `StartTurn / CheckTurn / ReadLatestOutput`. **Every provider has
   this.** It is the irreducible "send a message to a coding agent, get a result" verb.
2. **Worker lifecycle** — provision, wake, and destroy a *caller-managed, long-lived*
   remote workspace, and enumerate the ones we own. `CreateWorker / WorkerReady /
   DestroyWorker / ListWorkers`. **Only a sandbox provider (Amika) has real semantics
   here.**

The board only ever deals in **workers = capacity slots** (`03` §2.3); it must keep
doing so for *all* providers, because the board cannot know a provider's shape without
the abstraction leaking. So the design does **not** remove worker lifecycle from the
port. Instead it makes explicit that a non-sandbox provider satisfies worker lifecycle
**virtually**, and confirms nothing in the machinery *requires* a real remote resource
behind a worker.

### How each provider maps onto the (unchanged-shape) port

**Amika — real sandboxes.** Exactly today's adapter (`05` §6). A worker *is* a remote
sandbox; the four lifecycle calls hit `/sandboxes`.

**Devin — virtual workers over sessions.** A "worker" is a *logical slot*, not a Devin
resource (Devin has none to create up front):

| Port call | Devin adapter |
| --- | --- |
| `CreateWorker(name)` | **No remote call.** Return a synthetic `ProviderWorker{Name, Ref: ""}`; the Devin session is created lazily on the first `StartTurn(fresh)`. |
| `ListWorkers` | Return **empty** (Devin has no persistent workspace to enumerate). Adoption then always "creates" — which is a no-op — so the reconciler's adopt-first sweep is trivially satisfied and its orphan-destroy branch has nothing to destroy. |
| `WorkerReady` | Always **true** (nothing to wake). |
| `StartTurn(w, conv, msg, fresh)` | `fresh` → `POST /v1/sessions` (record `session_id` as the conversation handle, optionally pass `idempotent`); else `POST /v1/sessions/{conv}/messages`. |
| `CheckTurn` | `GET /v1/sessions/{id}` → map `status_enum` to running/terminal; `expired` → `ErrConversationLost`; ACU-limit stop → `ErrOutOfCredits`; surface `structured_output`/PR as output. |
| `DestroyWorker` / `Release` | End/abandon the session (best-effort); the next `StartTurn(fresh)` starts a clean one. "Fresh workspace" is automatic because Devin sessions are ephemeral. |

The key realization: **the machinery already tolerates this.** Workers with no real
remote backing, a `ListWorkers` that returns empty, and an always-ready `WorkerReady`
are all valid states the reconciler and poller already handle. Devin needs *no core
change* — only an adapter that answers the lifecycle calls virtually.

**A hypothetical local agent — out of band.** A tool that runs against a developer's own
machine does not fit a hosted orchestrator without a **runner**: a Kiln-side container
per worker that hosts the agent where Kiln can reach it over HTTP — at which point it
*becomes* a sandbox-style provider much like Amika. Either way the port shape is
unchanged; only the adapter (plus an ops component for the runner) differs. We do **not**
attempt to drive a developer's laptop. This is deferred until a concrete such provider is
on the table (open questions).

## What's provider-agnostic vs provider-specific

The dividing line, stated as a contract so it can be enforced in review:

**Provider-agnostic (lives in `internal/agent`, the board, brain, runtime, wire — must
never mention a provider):**
- Workers as opaque capacity slots; the `<prefix><worker-uuid>` naming join.
- The `Send` / `Release` verbs and the `agent.turn_completed` event payload
  (`ticket_id, worker_id, is_error, output, cost_usd` — no provider handles).
- The turn state machine, the `agent_turns` dedupe table, the reconciler and poller.
- The neutral error taxonomy (`ErrConversationLost`, `ErrOutOfCredits`).
- Cost expressed as USD (`float64`, 0 when unknown).

**Provider-specific (lives *only* inside an adapter package):**
- Sandboxes, sessions, jobs, `snapshot_id`, ACUs, session ids, job ids — every platform
  noun.
- Whether a worker is a real remote resource or a virtual slot.
- Auth scheme and credential shape (Amika API key; Devin `cog_` bearer).
- Idempotency-key usage, cost-unit conversion (ACU→USD), state-string classification.
- Provider config (repo/snapshot/secret wiring) — see §7 for the leak to fix.

**The single legal escape hatch — a capability descriptor.** Some *core-visible*
behaviour genuinely depends on provider shape (e.g. an operator UI that offers "reset
this worker's workspace" only means something for a sandbox provider). Rather than let
the core sniff the provider type, an adapter **declares** a small capability set:

```go
// Capabilities is an adapter's self-description of provider shape. The core reads
// booleans/enums here to adapt affordances WITHOUT naming a provider (05 abstraction
// rule). An adapter that omits it gets the conservative default (no sandbox, no cost,
// no snapshots) — the Devin shape.
type Capabilities struct {
    ManagedSandbox bool // caller creates/destroys a real workspace (Amika: true)
    ReportsCost    bool // TurnStatus.CostUSD is meaningful (Amika, Devin: true)
    Snapshots      bool // supports a base-image/snapshot handle (Amika, Devin: true)
    SecretsInject  bool // accepts caller-supplied secret refs into the workspace
}

// Optionally implemented by a Provider; absent ⇒ zero-value (conservative) defaults.
type CapabilityReporter interface{ Capabilities() Capabilities }
```

This keeps the branch out of the core's *logic* — the core reads a declared boolean, the
same way it already reads `TurnStatus.CostUSD == 0`. It never asks "is this Amika?".

## Integration points to add a provider

The concrete checklist. Today, adding a provider requires touching **more than one
place** because selection and config are hard-coded; part of this design is shrinking
that list. Target end state — a new provider is:

1. **A new adapter package** `internal/agent/<provider>` implementing `agent.Provider`
   (and optionally `CapabilityReporter`). This is the *only* place that names the
   platform. Mirrors `internal/agent/amika`'s layout: `client.go` (HTTP), `types.go`,
   `states.go` (state classification), `client_test.go` (httptest, no live calls).
2. **A registry entry**, not a new `if`. Today provider selection is a hard-coded branch
   with a two-value validation list (`cmd/kiln/wiring.go:187` rejects anything but
   `mock`/`amika`; `wiring.go:317` branches on `== "mock"`). Replace with a
   **provider registry** keyed by `AGENT_MODE` value:
   `map[string]func(ProviderDeps) (agent.Provider, error)`. Adding a provider = register
   a constructor; `buildGraph`'s validation becomes "is the key registered".
3. **A config blob** for that provider's settings (§7), namespaced so `AMIKA_*` doesn't
   have to grow `DEVIN_*` siblings scattered across `Config` and the project entity.
4. **A mock capability** only if the provider has a shape the existing mock can't script
   (the mock is a `Provider`, so it already covers turn execution and virtual workers).

Nothing in the board, brain, runtime, wire schema, or frontend changes. If a provider
addition wants to touch any of those, the abstraction is leaking and the change is wrong.

## Handling provider-specific features — sandboxes stay Amika-only

The worked example the objective calls out. Amika's sandbox management must remain
invisible to everyone but the Amika adapter:

- **The board never learns about sandboxes.** It already deals only in workers (`05`
  amendment A2 renamed Sandbox→Worker everywhere and dropped `amika_ref`). A sandbox is
  an Amika-internal realization of a worker slot; Devin realizes the same slot as a
  virtual handle. The board cannot tell them apart, and must not try.
- **Sandbox lifecycle is a `Capabilities.ManagedSandbox` concern.** Operator-facing
  affordances that only make sense for a real workspace (force-reset, inspect sandbox
  state) read the capability flag, never the provider name. For a non-sandbox provider
  those affordances are hidden or no-op — `Release` still works (it just abandons the
  session), so the *core verb* is unchanged.
- **`auto_stop` / `auto_delete`, snapshots, secret injection** are Amika request fields
  set inside the adapter from the provider config blob (§7). Devin's analogues
  (`snapshot_id`, `secret_ids`) are set inside *its* adapter from *its* blob. Neither
  field name ever appears in neutral code.
- **The reconciler's orphan sweep is sandbox-safe already.** It operates strictly within
  the per-environment `KILN_WORKER_PREFIX` scope (`05` §4, amended 2026-07-05) and acts
  on whatever `ListWorkers` returns. A non-sandbox provider returns empty, so there is
  nothing to sweep — the mechanism degrades cleanly.

## The leaks to fix (config generalization)

The neutral *port* is clean, but provider vocabulary has leaked **upward** into config
and the project entity — the real debt this design must pay down:

- `identity.Project` / `identity` entities carry `AmikaSnapshot`, `AmikaClaudeCredID`,
  `AmikaSecrets` (`internal/identity/entities.go`).
- `Config` carries `AmikaBaseURL`, `AmikaAPIKey`, `AmikaClaudeCredID`, `AmikaSnapshot`
  and reads `AMIKA_*` env vars (`cmd/kiln/main.go:52–53`, `wiring.go:145–154`).
- `AGENT_MODE`'s legal values are a hard-coded two-element list (`wiring.go:187`).

**Proposal:** introduce a **provider-namespaced config** rather than growing parallel
`DEVIN_*` fields through every layer:

- A per-project `AgentProvider string` (the registry key: `amika`, `devin`, `mock`, …)
  plus an opaque, provider-scoped `ProviderConfig` blob (JSON) on the project entity,
  replacing the flat `Amika*` fields. The Amika adapter parses its own blob
  (`{snapshot, claude_cred_id, secrets, …}`); the Devin adapter parses `{api_key_ref,
  snapshot_id, max_acu_limit, …}`. The identity layer stores an opaque blob and never
  names a provider field.
- Deployment-global secrets (`ANTHROPIC_API_KEY` for the brain) stay as they are; a
  provider's *own* credential is part of its blob, resolved per project (`11` §3 already
  builds the Amika client from per-project owner credentials at `wiring.go:323`).
- **Migration:** a one-time move of the existing `Amika*` columns into the blob under the
  `amika` key; nullable/back-compatible, mirroring how `05`/`11` already thread
  per-project Amika credentials. No behaviour change for existing single-provider
  deployments.

This is the change that most directly serves "without leaking provider-specific concepts
into the core system": after it, `grep -ri amika internal/{board,brain,runtime,api,wire}`
and the identity entity returns nothing.

## Dashboard registration (§8)

The backend leak (§7) has a mirror on the client. The dashboard (`11` §5) hard-codes
Amika at every turn: `frontend/src/dashboard/ConfigFields.tsx` renders literal "Amika API
key", "Amika Claude credential ID", "Amika snapshot", and "Sandbox secrets" inputs;
`CHECK_NAME_FOR_CREDENTIAL` is a fixed `{anthropic, amika, repo}` map; and
`POST /api/settings/verify` (`11` §4) runs a fixed Amika authenticated ping. Registering
a new provider means generalizing this exactly as §7 generalizes the backend — the
dashboard must render provider fields **from data, not from hard-coded Amika inputs**.

**How a provider is registered.** The §6 registry entry gains a **dashboard descriptor**
— the single declaration the generic dashboard reads. It travels over the wire as a
generated type (`/schema`, `02` §3), so neither side hand-writes provider fields:

- **Identity** — the registry key + human label (`amika` → "Amika", `devin` → "Devin").
  The key is the same one `AGENT_MODE`/`AgentProvider` uses, and must name a registered
  backend constructor (§6).
- **Config-field schema** — the shape of the provider's project-scoped config blob (§7):
  per field a `{key, label, type (text | number | password | secret-list), writeOnly,
  help}`. The project form renders its provider section from this instead of the
  hard-coded "Amika snapshot" + "Sandbox secrets". Amika declares
  `{snapshot: text, secrets: secret-list}`; Devin declares
  `{snapshot_id: text, max_acu_limit: number}`.
- **Credential-field list** — the user-level secrets the provider needs (Amika:
  `api_key`, `claude_cred_id`; Devin: a `cog_` bearer). Rendered in the credentials
  section with the existing write-only convention (`11` §3 D7): the input never carries
  the stored value, only a `configured · …tail` placeholder.
- **Verify probe** — a check name plus a live-connection test, so `settings/verify` fans
  out one `{name, ok, message}` result per registered provider (Amika's ping today; for
  Devin a session-list/auth call). `CHECK_NAME_FOR_CREDENTIAL` becomes *derived from the
  registry*, not a literal map.
- **Capabilities** (§5, optional) — so the dashboard hides sandbox-only affordances
  (workspace reset, secret injection) for a provider that declares
  `ManagedSandbox: false`, reading the flag, never the name.

**What registration requires:** exactly those five — key, label, config-field schema,
credential list, verify probe (Capabilities optional). Nothing else; the descriptor is
the whole contract between a provider and the dashboard.

**Validations / checks:**
- **Key must resolve.** A dashboard descriptor whose key has no registered backend
  constructor (§6) is a composition-root error caught at startup, not a broken form at
  runtime — the descriptor set and the constructor set are validated to match.
- **The adapter owns blob validation.** The field schema drives client-side
  required/format hints, but the config blob is opaque to identity/config (§7): the
  adapter parses and validates its *own* blob server-side on `PUT /api/project`, and a
  malformed blob fails the save with the adapter's message. The dashboard never
  second-guesses a provider's config rules.
- **Live verify is the real gate.** A provider's config counts as "working" for a project
  only once its verify probe passes — exactly today's onboarding "verify connections"
  step (`11` §5 state 2), now one check per registered provider instead of a fixed three.

The payoff mirrors §7: afterwards the generic dashboard code names no provider —
`grep -ri amika frontend/src/dashboard` outside the descriptor data returns nothing, and
adding a provider's UI is "ship a descriptor", not "edit `ConfigFields.tsx`".

## Project-level provider defaults (§9)

A project already resolves to exactly one provider —
`ProviderResolver.For(ctx, projectID) → (Provider, prefix)` (`provider.go:23`), and "a
project has one provider" is a standing constraint (open questions). Multi-provider turns
that single provider into a **per-project default**: the provider every worker, turn, and
conversation in that project uses.

**Users select a default provider per project — yes.** The §7 per-project `AgentProvider`
field *is* that default (the registry key). The dashboard's project section gains a
**provider select** listing the registered providers (from the §8 descriptors); the
choice is stored as `Project.AgentProvider`. The config-field section below it re-renders
from the selected provider's schema (§8), so switching the select swaps which config
fields appear. A deployment that registers only Amika shows a one-option select (or hides
it) — no UX change for single-provider deployments.

**How the default is applied across the system.** Entirely at the existing seam, with no
new machinery downstream. `ProviderResolver.For` reads `Project.AgentProvider`, looks up
the registry constructor (§6), builds the provider from the project owner's credentials +
config blob (`11` §3, `wiring.go:323`), and returns `(Provider, prefix)`. Everything past
that point — board workers, brain, runtime, reconciler, wire — sees only the resolved
`Provider` and never learns which key was chosen (the `05` abstraction rule). The default
is resolved per turn, so it is always the *current* stored value.

**Deployment default vs project default.** `AGENT_MODE`/the registry still decides which
providers a deployment *offers* and the fallback for a project that has not chosen one. A
project with an empty `AgentProvider` resolves to the deployment default (today: `amika`)
— which is exactly the §7 migration's behavior (existing projects get `amika` implicitly,
with no backfill of a per-project choice). Per-project selection is purely additive on
top of a working single-provider default.

**When a provider is removed but is set as a project default.** Two distinct cases:

1. **Unregistered from the deployment** (its constructor is gone — a rollback, a dropped
   integration). `ProviderResolver.For` cannot build it, and it must fail **loud and
   contained**: a new neutral sentinel `ErrProviderUnavailable` (sibling to
   `ErrConversationLost` / `ErrOutOfCredits`, `provider.go:28`) so the runtime surfaces
   "this project's provider is unavailable" and the project's board **pauses**. It does
   **not** silently fall back to the deployment default — silent fallback is explicitly
   rejected, because it would run the project's tickets on a different agent with
   different credentials and a different billing model, invisibly. The board resumes when
   an admin re-registers the provider or the owner picks a new one. The dashboard shows
   the provider select in an error state ("provider *X* is no longer available — pick
   another") rather than rewriting the stored value behind the user's back.
2. **Deselected by the owner** (a normal `AgentProvider` change to another registered
   provider). In-flight turns finish on the old provider (the resolver is read per turn);
   new turns use the new one. Because workers are opaque slots and a conversation is a
   provider-scoped handle inside the blob, the switch starts fresh workers/conversations
   on the new side and releases the old provider's sessions/sandboxes best-effort via its
   `Release` / `DestroyWorker`. There is **no** cross-provider conversation migration —
   consistent with one-provider-per-project (open questions).

## Roadmap

Phased so each step ships value and none requires a big-bang rewrite:

- **Phase 0 — prove the port is already general (no new provider).** Add the
  `Capabilities` / `CapabilityReporter` surface with Amika reporting
  `{ManagedSandbox, ReportsCost, Snapshots, SecretsInject: true}` and the mock reporting
  conservative defaults. No behaviour change; establishes the escape hatch and a test
  that the core reads capabilities, never a provider name.
- **Phase 1 — provider registry + config generalization (§6, §7).** Replace the
  hard-coded `mock`/`amika` branch with a registry keyed by `AGENT_MODE`/`AgentProvider`;
  move `Amika*` config into the opaque per-provider blob with a back-compat migration.
  After this, adding a provider is genuinely "adapter + register + blob". Ship with only
  Amika + mock registered — pure refactor, fully covered by existing tests.
- **Phase 2 — first non-sandbox adapter (Devin).** Write `internal/agent/devin` as the
  *virtual-worker* proof (§4): sessions behind virtual workers, `idempotent` on create,
  `status_enum` classification in `states.go`, ACU→USD in cost. This is where we learn
  whether any hidden assumption in the machinery still expects a real remote worker; the
  design predicts none, and Phase 2 is the test of that prediction. Behind
  `AGENT_MODE=devin`, opt-in, mock-tested with httptest fixtures first.
- **Phase 3 — dashboard registration + per-project provider default (§8, §9).** Turn the
  hard-coded Amika dashboard fields into a data-driven provider descriptor, add the
  per-project provider select, and wire `ProviderResolver.For` to the stored
  `AgentProvider` (with `ErrProviderUnavailable` for an unregistered default). After this
  a project owner can actually pick Devin in the UI, not just via `AGENT_MODE`.
- **Phase 4 — polish the capability-driven UX.** Operator affordances (workspace reset,
  sandbox inspection, cost display) read capabilities so they light up per provider
  without core branches.

Phases 0–1 are the load-bearing ones and carry no provider risk; 2 onward are additive
and independently shippable.

## Out of scope / open questions

- **Driving a truly local agent** (a developer's laptop, or any self-hosted tool with no
  reachable hosted endpoint) from a hosted orchestrator is out of scope — Kiln dispatches
  to reachable workers; such an agent would need a Kiln-hosted runner (a container per
  worker), at which point it becomes a sandbox-style provider much like Amika. Deferred
  until a concrete such provider is on the table.
- **Multiple providers within one project** (e.g. some tickets to Amika, some to Devin) —
  out of scope. The `ProviderResolver` is per-project; a project has one provider (§9).
  Per-ticket routing is a later question if it ever arises.
- **Cost normalization across billing models** (Amika USD vs Devin ACUs) beyond a
  best-effort ACU→USD estimate. The `cost_usd` field stays a single float; a richer usage
  model is deferred.
- **Backfilling the config blob** for historical projects beyond the one-time migration
  in Phase 1.

## Decision log

| # | Decision | Alternatives | Rationale |
| --- | --- | --- | --- |
| D1 | Keep the `Provider` port shape (incl. worker lifecycle); non-sandbox providers satisfy it *virtually*. | Split the port into `TurnExecutor` + optional `SandboxManager`; core composes them. | The board must see uniform workers for all providers (abstraction rule). A virtual-worker adapter already satisfies the existing port with zero core change; splitting the port ripples into the machinery for no gain. Revisit only if a provider can't be expressed virtually. |
| D2 | Provider shape is expressed by a declared `Capabilities` descriptor, read by the core. | Let the core switch on provider identity; or infer from method behaviour. | A declared capability is the one leak-free way to vary core-visible affordances — the core reads a boolean, never a provider name. Matches the existing `CostUSD == 0` pattern. |
| D3 | Provider selection becomes a **registry** keyed by config value. | Keep extending the `mock`/`amika` `if`/validation list. | A registry makes "add a provider" a one-line registration and keeps `buildGraph` validation data-driven. The `if`-ladder is O(providers) edits in two files. |
| D4 | Generalize `Amika*` config into a per-project provider name + opaque config blob. | Add parallel `Devin*` fields through `Config`, the project entity, and identity. | Parallel fields multiply the leak surface through layers that must not name providers. An opaque blob keeps identity/config provider-neutral; the adapter owns its schema. This is the change that actually delivers "no provider concepts in the core". |
| D5 | Devin uses virtual workers; its sessions are created lazily in `StartTurn(fresh)`. | Model a Devin "worker" as a pre-created session at `CreateWorker` time. | Devin has no persistent workspace to pre-create; a session is per-conversation and ephemeral. Lazy creation matches Devin's model and keeps `Release`/fresh-workspace semantics automatic. |
| D6 | The dashboard renders a provider's config/credential fields from a declared **provider descriptor**, not hard-coded Amika inputs. | Keep the hand-written Amika fields and add parallel Devin fields in `ConfigFields.tsx`. | Hard-coded fields are the §7 leak on the frontend. A descriptor (key + field schema + credential list + verify probe) lets the generic dashboard name no provider, mirroring the opaque config blob on the backend (D4). |
| D7 | A project's provider is a **per-project default** (`AgentProvider`); an unavailable default fails loud (`ErrProviderUnavailable`), never silently falls back. | Fall back to the deployment default when a project's provider is missing; or allow per-ticket provider choice. | Silent fallback would run a project's tickets on the wrong agent and credentials, invisibly. Failing loud pauses the board until an admin re-registers the provider or the owner re-selects one. Per-ticket routing is out of scope (§9). |

## Sources (provider research)

- Devin API: `https://docs.devin.ai/api-reference/overview`,
  `/api-reference/authentication` (Bearer `cog_` tokens, service keys vs PATs),
  `/api-reference/v1/sessions/create-a-new-devin-session` (`prompt`, `idempotent`,
  `snapshot_id`, `max_acu_limit`, `is_new_session`),
  `/api-reference/v1/sessions/retrieve-details-about-an-existing-session` (`status_enum`,
  `structured_output`, `pull_request`; no sandbox object), `/admin/billing` (API needs
  Team/Enterprise; ACU accounting), `https://devin.ai/pricing/`.
- Amika: internal — spec `05` and `internal/agent/amika`, against Amika v0beta1
  (`https://app.amika.dev/api/v0beta1/llms.txt`).

_Research current as of 2026-07-11; provider APIs move quickly — reconfirm endpoint and
auth specifics against live docs before building an adapter._
