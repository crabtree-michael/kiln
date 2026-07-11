# Multi-provider agent runtime — design

**Date:** 2026-07-11
**Status:** Proposed (design only — no implementation in this change)
**Scope:** `internal/agent` (the Provider port and Service), `internal/agent/{amika,mock}`
and future adapter packages, `internal/identity` (the project entity's provider
config), `cmd/kiln` (composition-root provider selection), and the `AGENT_MODE` /
`AMIKA_*` configuration surface. No board, brain, runtime, or wire changes — that is
the whole point of the exercise.
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
- **Devon** (entropy-research) — an **open-source, local** pair-programmer that runs
  against the user's own machine. No managed sandbox, no hosted API, no billing layer.

If we add each of these by teaching the core new special cases, provider vocabulary
leaks back into the layer we deliberately cleaned — violating `05`'s abstraction rule
("nothing outside this module may know Amika exists"). Worse, some Amika vocabulary has
**already leaked upward** into config and the project entity (see §7). This document
decides how to add providers of *different shapes* while keeping the leak surface at
exactly one place: a new adapter package plus its config.

**Objective:** a provider model where adding Devin, Devon, or any future platform touches
only an adapter + a config registry, and where a provider's *shape* (sandbox vs session
vs local, cost-reporting vs not, idempotent vs not) is expressed through a small,
declared capability surface rather than by branching in the core.

## What already exists (and is already right)

Before proposing anything, the parts of `05` that survive multi-provider **unchanged**,
because getting credit for them is half the design:

| Concern | Where | Already neutral because |
| --- | --- | --- |
| The three-verb consumer contract | `AgentRuntime{Send, Release}` + `agent.turn_completed` event (05 §2.1–2.2) | Says nothing about sandboxes/sessions. A provider swap never touches it. |
| Idempotency / dedupe | `agent_turns` table keyed by outbox id (05 §7) | The module owns its own dedupe *precisely because* Amika lacks idempotency keys. A provider that *does* have them (Devin) simply doesn't need the module's — the table is harmless and stays. |
| Cost reporting | `TurnStatus.CostUSD`, "0 when the provider doesn't report cost" (`provider.go:106`) | Already models the absence of cost data. Devon reports nothing; the field is just 0. |
| Cross-provider error taxonomy | `ErrConversationLost`, `ErrOutOfCredits`, `ProviderErrorFields` (`provider.go:28–55`) | Neutral sentinels an adapter maps *into*; the machine reacts to the sentinel, not the platform. |
| Per-project provider resolution | `ProviderResolver.For(ctx, projectID) → (Provider, prefix)` (`provider.go:23`) | Already the seam where "which provider serves this project" is answered. Multi-provider is a *fuller* implementation of the same interface, not a new one. |
| The turn state machine, reconciler, poller, mock | `internal/agent/service.go` + `mock` | Written once against the port; provider-agnostic already. |

So the neutral **contract** is sound. The work is entirely about the **internal port
shape** (§4), the **config surface** (§7), and **provider selection** (§6).

## The three providers at a glance

Grounded in the API research (sources at the end). The axes are the ones that actually
change how an adapter maps onto the port.

| Axis | **Amika** (v0beta1) | **Devin** (Cognition) | **Devon** (entropy-research) |
| --- | --- | --- | --- |
| Hosting | Hosted-managed | Hosted-managed | **Self-hosted / local** |
| Caller-managed sandbox/worker? | **Yes** — `POST /sandboxes`, addressable by name; the only provider with this | **No** — environment hidden inside a session (`snapshot_id` is the only handle) | **No** — runs against the user's own filesystem |
| Unit of work | `agent-send-jobs` (async job) on a sandbox | `session` (created from a prompt) | local session in a localhost backend |
| Continue a conversation | reuse `session_id` on the sandbox | `POST /sessions/{id}/messages` | local, interactive |
| Sync vs async | **async + poll** (202, no webhooks) | **async + poll** (`GET /sessions/{id}`, `status_enum`) | local/interactive |
| Auth | `Authorization: Bearer <AMIKA_API_KEY>` | `Authorization: Bearer cog_…` (service key or PAT) | **BYO LLM key** (`ANTHROPIC_API_KEY`…); no platform auth |
| Idempotency | **None** (module supplies its own) | `idempotent` body flag + `is_new_session` | none |
| Cost reporting | `cost_usd` on the job | **ACUs** (`max_acu_limit`; per-session ACU accounting) | none — user pays their LLM directly |
| Getting started | Amika account + API key + repo/snapshot config | Team/Enterprise plan → Service User API key | `pipx install devon_agent` locally + LLM key |

**Devon disambiguation (must be resolved before any Devon adapter is built).** The name
"Devon" is overloaded. The literal match — `entropy-research/Devon` — is a **local tool
with no documented public HTTP API**; it executes on the developer's own machine and has
no managed sandbox, no bearer auth, and no usage accounting. That makes it a poor fit for
a *hosted orchestrator* like Kiln, which dispatches work to a remote worker it does not
sit next to. If the intent behind "Devon" was "the open-source Devin alternative that has
a real hosted API," the correct referent is almost certainly **OpenHands** (formerly
OpenDevin, org All Hands AI): a dual-mode system with a hosted **OpenHands Cloud** REST
API (`https://app.all-hands.dev`, `Authorization: Bearer …`, `POST
/api/v1/app-conversations`) *and* a self-host Docker-sandbox mode. This document treats
"Devon" as the **local/self-hosted, no-managed-sandbox archetype** for the architecture,
and calls out OpenHands as the concrete hosted variant if that is what's wanted. See
§8 open questions — this choice materially changes the Devon row above and should be
confirmed by the author, not guessed.

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

**Devon — local, out-of-band.** The local archetype does not fit a hosted orchestrator
without a **runner**: something that hosts the Devon backend where Kiln can reach it over
HTTP. Two honest options, deferred (§8): (a) treat "Devon" as OpenHands Cloud and write a
normal hosted adapter like Devin's; or (b) stand up a Kiln-side Devon runner (a container
per worker running `devon_agent`), at which point Devon *becomes* a sandbox-style
provider much like Amika. Either way the port shape is unchanged; only the adapter (and,
for (b), an ops component) differs. We do **not** attempt to drive a developer's laptop.

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
- Auth scheme and credential shape (Amika API key; Devin `cog_` bearer; BYO LLM keys).
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
// no snapshots) — the Devin/Devon shape.
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
- **Phase 3 — decide and handle "Devon" (§8).** Either register OpenHands Cloud as a
  normal hosted adapter (cheap, reuses Phase 2's shape) or design the Kiln-side Devon
  runner (a sandbox-style adapter plus an ops component). Gated on the §8 decision.
- **Phase 4 — polish the capability-driven UX.** Operator affordances (workspace reset,
  sandbox inspection, cost display) read capabilities so they light up per provider
  without core branches.

Phases 0–1 are the load-bearing ones and carry no provider risk; 2 onward are additive
and independently shippable.

## Out of scope / open questions

- **Which "Devon"?** (§3) — `entropy-research/Devon` (local, no public API) vs OpenHands
  Cloud (hosted API). This must be answered before Phase 3; it changes whether Devon is a
  hosted adapter or a runner-plus-adapter. Flagged for the author, not assumed.
- **Driving a truly local agent** (a developer's laptop) from a hosted orchestrator is
  out of scope — Kiln dispatches to reachable workers; a local agent needs a Kiln-hosted
  runner or is not a fit.
- **Multiple providers within one project** (e.g. some tickets to Amika, some to Devin) —
  out of scope. The `ProviderResolver` is per-project; a project has one provider.
  Per-ticket routing is a later question if it ever arises.
- **Cost normalization across billing models** (Amika USD vs Devin ACUs vs Devon's zero)
  beyond a best-effort ACU→USD estimate. The `cost_usd` field stays a single float; a
  richer usage model is deferred.
- **Backfilling the config blob** for historical projects beyond the one-time migration
  in Phase 1.

## Decision log

| # | Decision | Alternatives | Rationale |
| --- | --- | --- | --- |
| D1 | Keep the `Provider` port shape (incl. worker lifecycle); non-sandbox providers satisfy it *virtually*. | Split the port into `TurnExecutor` + optional `SandboxManager`; core composes them. | The board must see uniform workers for all providers (abstraction rule). A virtual-worker adapter already satisfies the existing port with zero core change; splitting the port ripples into the machinery for no gain. Revisit only if a provider can't be expressed virtually. |
| D2 | Provider shape is expressed by a declared `Capabilities` descriptor, read by the core. | Let the core switch on provider identity; or infer from method behaviour. | A declared capability is the one leak-free way to vary core-visible affordances — the core reads a boolean, never a provider name. Matches the existing `CostUSD == 0` pattern. |
| D3 | Provider selection becomes a **registry** keyed by config value. | Keep extending the `mock`/`amika` `if`/validation list. | A registry makes "add a provider" a one-line registration and keeps `buildGraph` validation data-driven. The `if`-ladder is O(providers) edits in two files. |
| D4 | Generalize `Amika*` config into a per-project provider name + opaque config blob. | Add parallel `Devin*` fields through `Config`, the project entity, and identity. | Parallel fields multiply the leak surface through layers that must not name providers. An opaque blob keeps identity/config provider-neutral; the adapter owns its schema. This is the change that actually delivers "no provider concepts in the core". |
| D5 | Treat "Devon" as the local/no-managed-sandbox archetype; flag OpenHands as the hosted variant; defer the choice. | Assume Devon == OpenHands and write a hosted adapter now; or assume the local tool and build a runner now. | The name is genuinely ambiguous and the two referents sit at opposite ends of the axis table. Guessing wrong wastes an adapter. Document both, decide before Phase 3. |
| D6 | Devin uses virtual workers; its sessions are created lazily in `StartTurn(fresh)`. | Model a Devin "worker" as a pre-created session at `CreateWorker` time. | Devin has no persistent workspace to pre-create; a session is per-conversation and ephemeral. Lazy creation matches Devin's model and keeps `Release`/fresh-workspace semantics automatic. |

## Sources (provider research)

- Devin API: `https://docs.devin.ai/api-reference/overview`,
  `/api-reference/authentication` (Bearer `cog_` tokens, service keys vs PATs),
  `/api-reference/v1/sessions/create-a-new-devin-session` (`prompt`, `idempotent`,
  `snapshot_id`, `max_acu_limit`, `is_new_session`),
  `/api-reference/v1/sessions/retrieve-details-about-an-existing-session` (`status_enum`,
  `structured_output`, `pull_request`; no sandbox object), `/admin/billing` (API needs
  Team/Enterprise; ACU accounting), `https://devin.ai/pricing/`.
- Devon (entropy-research): `https://github.com/entropy-research/Devon` (local install
  `pipx install devon_agent` / `npx devon-ui`; BYO LLM keys; local-filesystem execution;
  no managed sandbox; no documented public HTTP API), `https://pypi.org/project/devon-agent/`.
- OpenHands (the hosted "open Devin alternative"):
  `https://docs.openhands.dev/openhands/usage/cloud/cloud-api` (base
  `https://app.all-hands.dev`, Bearer key, `POST /api/v1/app-conversations`),
  `https://github.com/OpenHands/OpenHands` (OpenDevin rename; self-host Docker sandbox).
- Disambiguation: `https://github.com/e2b-dev/awesome-devins`,
  `https://e2b.dev/blog/open-source-alternatives-to-devin`.
- Amika: internal — spec `05` and `internal/agent/amika`, against Amika v0beta1
  (`https://app.amika.dev/api/v0beta1/llms.txt`).

_Research current as of 2026-07-11; provider APIs move quickly — reconfirm endpoint and
auth specifics against live docs before building an adapter._
