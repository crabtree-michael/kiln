---
name: amika-integration
description: Use when working in the agent-runtime module — the provider-neutral layer other modules use to reach coding agents (workers + send message + turn-output events; never sandboxes or sessions). Amika is one registered provider behind it. Backend anchor internal/agent. Spec docs/specs/05-agent-runtime.md.
---

# Agent runtime (mechanics decided by spec 05)

## What it is

The provider-neutral agent-runtime layer. Other modules see **workers** (opaque handles = the
board's capacity slots), **Send** (deliver a message to a worker), **output**
(`agent.turn_completed` events), a **read/inspector seam** (`ListAgents`/`GetAgentUpdates`,
backing the brain's `list_agents`/`get_agent_updates`), and a **worker-health** signal into the
board.

**The abstraction rule (05 §1): nothing outside this module may know Amika exists.** Every
provider concept — sandboxes, sessions, jobs, provisioning, auth — stays inside. Swapping or
adding an agent platform touches only a Provider adapter + config. *(The skill is still named
`amika-integration` for continuity; the module is provider-neutral and Amika is one of several
registered providers.)*

**Two seams (05 §2).**
- *Consumer contract:* `AgentRuntime{Send, Release}` executes `agent.send` / `agent.release`
  outbox entries — record-and-return, idempotent by outbox id. Inbound: `EnqueueEvent` with an
  `idempotencyKey`, carrying `{ticket_id, worker_id, is_error, output, cost_usd}` for **every
  terminal outcome, mechanical failures included** (D3). The key makes completion
  **exactly-once at the event seam**. No provider handles in the payload.
- *Provider port (internal):* list/create/ready/destroy a worker, start/check a turn, read
  latest output, report status. The state machine, reconciler, poller, dedupe table and mock
  are written **once** against it; each adapter is one implementation, resolved **per project**.

**Lifecycle (05 §4): pool + recreate on release.** One long-lived provider worker per board
slot, named `<prefix><board-worker-uuid>` — the whole board↔provider join (D5). Startup
reconciliation is **adopt-first**: list, match names, create only for slots with no live
worker, destroy orphans. `agent.release` destroys + recreates for a fresh workspace;
dead-lettered recreates are healed by the 60 s reconciler sweep.

**Turn machine (05 §5, §7).** Per-operation `recorded → worker_ready → turn_started →
done/failed`, persisted in the module-owned `agent_turns` table keyed by outbox id — **the
idempotency dedupe the provider doesn't give us**. A 2 s poller advances non-terminal machines;
recovery = continue every non-terminal row. Terminal failure → error-turn event; the brain
decides what it means for the ticket.

**Mock (05 §8).** A mock **Provider**, not a mock of the whole module — the machinery, table
and event path all run for real. Instant lifecycle, scripted turns, failure injection,
conversation loss. Default in dev and the keyless e2e lane.

## Per-project provider + prefix (11 §3 tenancy flip)

The Service holds a `ProviderResolver`, not one process-wide `Provider`, and resolves a
per-project `(Provider, prefix)` for every reconcile/poll/inspect. The prefix is composed at
the composition root as `cfg.WorkerPrefix + <project scope> + "-"`, so `KILN_WORKER_PREFIX` is
only the per-environment **base**.

**The prefix is the ownership scope** — adopt, create, sweep and reset all stay inside it.
**Environments sharing one provider account MUST use distinct base prefixes**: with a shared
one, each instance's orphan sweep destroys the other environments' live workers within 60 s
(their slot uuids live in a different DB). This is what killed prod agents "on every deploy"
before the per-env prefix landed 2026-07-05. docker-compose defaults local dev to
`kiln-dev-worker-`; the e2e teardown follows the same env var; prod keeps the historical
`kiln-worker-`.

## Multi-provider registry

Amika is **one registered provider among several**, not *the* provider
(`docs/superpowers/specs/2026-07-11-multi-provider-agent-runtime-design.md`).

- **Capability descriptor.** `agent.Capabilities` + `CapabilityReporter`, read via
  `agent.CapabilitiesOf(p)` — the ONE leak-free way the core varies a core-visible affordance
  without naming a provider. A provider that omits the interface gets the conservative zero
  value. **Never type-switch on a concrete provider.**
- **Registry.** `cmd/kiln/registry.go` maps a key (`amika`/`mock`/`devin`) to a
  `ProviderFactory`. Adding a provider = one map entry + its adapter package; validation is
  "is the key registered", never an if-ladder. `AGENT_MODE` is the deployment default.
- **Per-project override.** `identity.Project.AgentProvider` (empty ⇒ deployment default). An
  unregistered key fails **LOUD** with `ErrProviderUnavailable` — never a silent fallback.
- **Devin** (`internal/agent/devin`) is the *virtual-worker* shape: no managed sandbox, empty
  worker listing, synthetic workers, session created lazily on the first turn, ACU→USD
  best-effort cost. It is the proof the abstraction holds for a provider unlike Amika.
- **Still frozen, and that is the whole point:** `AgentRuntime{Send,Release}`, the
  `agent.turn_completed` payload, the `agent_turns` dedupe, the reconciler/poller. **A provider
  addition that touches board/brain/runtime/wire (beyond the dashboard descriptor) means the
  abstraction is leaking.**

## Sandbox selection — the snapshot catalog seam

Replaces a free-text snapshot handle with a dashboard picker of the account's real snapshots,
plus "save a running dev box as a snapshot" — and stays provider-neutral.

- **Optional Provider extension `agent.SandboxCatalog`** (`catalog.go`): list snapshots, list
  dev boxes, save a snapshot, over neutral `Snapshot`/`DevBox` types. Read via
  `agent.SandboxCatalogOf(p)`, mirroring `CapabilitiesOf` — **never a type switch**. A provider
  without it offers no catalog. `Snapshot.Ref` is exactly what the project stores and passes
  back at create time: an opaque handle, **no provider vocabulary crosses**.
- **API:** `GET/POST /api/snapshots`, `GET /api/dev-boxes`, dual-mounted per project. A
  provider with no catalog → 404, so the client hides the picker; other failures → 502. The
  frontend consequences (per-project hook, no global store) are `web-client`'s.
- Amika's impl filters dev boxes to the **complement** of the worker prefix — the user's own
  boxes, not the pooled workers.

## Config

`AGENT_MODE` (`amika`/`mock`/`devin`), `AMIKA_BASE_URL`, `AMIKA_API_KEY`, `AMIKA_REPO_URL`,
`AMIKA_SNAPSHOT`, **`AMIKA_CLAUDE_CRED_ID`** (required for agent auth), per-project encrypted
sandbox secrets, and `KILN_WORKER_PREFIX` (per-environment base; trailing `-` appended at
load). Note `KILN_AGENT` / `KILN_WORKER_AUTO_STOP` exist as struct-comment intentions but are
**not wired** at the composition root — they fall to the defaults.

## Settled contract choices (load-bearing, no longer open)

- `agent_turns` carries a `message` column beyond the 05 §7 list — recovery must be able to
  start a never-started turn, so the message has to be durable.
- `StartTurn` takes the prior conversation handle alongside `fresh`: adapters are stateless, so
  the recorded session id must come from the machinery.
- `Phase.Terminal()` is **done-only** — `failed` still owes the error `turn_completed` event,
  so the poller's working set is `phase <> 'done'`.
- The 05 §5 enqueue+mark-done is resolved **not** as a cross-table transaction but as
  exactly-once *at the event seam*: emit-then-mark-done with a plain single-row update, and a
  re-emit is deduped by the idempotency key.
- First-message-vs-continuation is derived from the store (no row, or a release row ⇒ fresh).

## How to work here

- **Never block a port call on the provider**: record in `agent_turns`, return; the
  reconciler/poller advances the turn (05 D2).
- The module owns its own table and migration — adapter state, not board state (03 I8 stays
  intact).
- The board is reached **only via events**; this module never mutates board state (05 D3).
- New provider = new adapter package + a registry entry. **If you find yourself touching the
  service or the consumer contract, the abstraction is leaking.**

## Common footguns

- Leaking a provider concept (session id, sandbox state, job id) into a consumer-facing type,
  event payload, or log line other modules parse.
- **Blocking an outbox handler on provisioning** — it fights the runtime's 8-attempt budget.
  Record-and-return, always.
- Creating workers unconditionally at startup instead of adopt-first reconciliation —
  duplicates the pool on every deploy.
- **Trusting the provider to dedupe:** Amika v0beta1 has **no idempotency keys**; every port
  call checks `agent_turns` first.
- Running two environments on one provider account with the same base prefix (see above).

## Potential gotchas

- A provider can **lose a conversation between turns**; fall back to a fresh conversation with
  the same message (context lost, workspace kept) — **never fail the ticket** (05 §3).
- **`auto_delete` must stay off** — it would yank a worker out from under a Blocked ticket
  waiting on the user overnight (05 D6). In Amika v0beta1 the "off" sentinel is a **negative**
  interval (`-1`).
- **Out-of-credits is fail-fast and terminal.** The adapter maps the credit-exhausted response
  to `ErrOutOfCredits` and the Service treats it as a terminal turn outcome rather than
  retrying forever.
- **Worker-health feeds the board.** The liveness loop reports per-worker health through the
  optional `BoardRefresher.SetWorkerHealth`, so the pull binds Ready tickets only to healthy
  sandboxes (see `board-mechanism`).
- Adding schema enums can collide with existing generated constants — oapi-codegen re-qualifies
  them (e.g. `wire.Errored` → `wire.AgentStatusStatusErrored`). Expected: **update the
  consumer, don't rename the schema.**

## Amika adapter notes (`internal/agent/amika`, v0beta1)

Designed against `https://app.amika.dev/api/v0beta1/llms.txt`. Where the docs are silent the
adapter is deliberately defensive; these are the hardening points.

> **Sync-send bridge (still active, 2026-07).** The adapter mints a session up front
> (`POST …/sessions`) and fires a **synchronous** `POST …/agent-send` with a bounded wait —
> **not** the async `agent-send-jobs` that 05 §6 specifies — because Amika's async endpoint
> 500s org-wide ("Agent launch failed"). Consequence: the adapter always passes the up-front
> `SessionID`, so the recorded conversation handle is never empty. **Revert to
> `agent-send-jobs` once Amika fixes it**; the historical path returned `agent_session_id:
> null` in its 202 and fell back to omitting `session_id` on continuation, so restore that
> handling with it. Restore the async types from git history.

- **State classification lives in `states.go`.** Sandbox `state`, job `state` and snapshot
  `state` are all **un-enumerated** in v0beta1, so the classifiers match known strings and fall
  through to the safe default (sandbox → not ready yet, keep polling; job → keep polling unless
  it produced a result or `is_error`). **Add real values there as they're observed — it is the
  one place to edit.**
- **`auto_stop_interval`'s unit is undocumented.** The adapter sends whole **minutes**; verify
  against a live sandbox and adjust if it is seconds.
- **Conversation-loss detection is a heuristic**: a continuation that fails with a 400/404/409
  whose error code or message mentions "session" maps to `ErrConversationLost`. v0beta1
  documents no per-error codes — tighten it once the real envelope is known.
- **Auth is `Authorization: Bearer <AMIKA_API_KEY>`** (undocumented in `llms.txt`). Every
  4xx/5xx decodes into the uniform `*APIError` envelope; check status with `errors.As` (404 on
  delete = success, 409 on start = already starting).
- **`GET /sandboxes/{id}` accepts id *or* name** — adoption relies on this.
- Tests are pure `httptest` — no live calls, no recorded fixtures; the manual smoke checklist
  (05 §10) still gates the first real-Amika run.
