---
name: amika-integration
description: Use when working in the agent-runtime module — the provider-neutral layer other modules use to reach coding agents (workers + send message + turn-output events; never sandboxes or sessions). Amika is the v1 provider adapter behind it. Backend anchor internal/agent (adapter in internal/agent/amika). Specs 02 §8, 05.
---

# Agent runtime (02 §8, mechanics decided by 05)

## Functional Requirements

**Responsibility.** The provider-neutral agent-runtime layer. Other modules see exactly
three things: **workers** (opaque handles = the board's capacity slots), **Send** (deliver
a message to a worker), and **output** (`agent.turn_completed` events). Every provider
concept — sandboxes, sessions, jobs, provisioning, auth — stays inside this module.
Fully specified in `docs/specs/05-agent-runtime.md`, designed against **Amika API
v0beta1** (`https://app.amika.dev/api/v0beta1/llms.txt`).

**The abstraction rule (05 §1).** Nothing outside this module may know Amika exists.
Swapping or adding an agent platform touches only a Provider adapter + config.

**Two seams (05 §2).**
- *Consumer contract:* `AgentRuntime{Send, Release}` — executes `agent.send` /
  `agent.release` outbox entries; record-and-return, idempotent by outbox id.
  Inbound: `EnqueueEvent(agent.turn_completed, {ticket_id, worker_id, is_error, output,
  cost_usd})` — every terminal outcome, mechanical failures included (D3); no provider
  handles in the payload.
- *Provider port (internal):* `ListWorkers / CreateWorker / WorkerReady / DestroyWorker /
  StartTurn / CheckTurn`. The state machine, reconciler, poller, dedupe table, and mock
  are written once against it; Amika (`internal/agent/amika`) is one implementation.

**Lifecycle (05 §4).** Pool + recreate on release: one long-lived provider worker per
board slot, named `kiln-worker-<board-worker-uuid>` (the whole board↔provider join, D5).
Startup reconciliation is **adopt-first**: list, match names, create only for slots with
no live worker, destroy orphans. `agent.release` (AcceptToDone) destroys + recreates for
a fresh workspace; dead-lettered recreates are healed by the 60 s reconciler sweep.

**Turn machine (05 §5, §7).** Per-operation machine
`recorded → worker_ready → turn_started → done/failed`, persisted in the module-owned
`agent_turns` table keyed by outbox id — **the idempotency dedupe Amika doesn't provide**.
A 2 s poller advances non-terminal machines; recovery = continue every non-terminal row.
Terminal failure → error-turn event; the brain decides what it means for the ticket.

**Amika mapping (05 §6).** Sandboxes ↔ workers; `agent-send-jobs` (async, never sync
`agent-send`) ↔ turns; `new_session` ⇔ first send of a conversation. `auto_stop` on,
`auto_delete` **off**. Config: `AGENT_MODE` (`amika`/`mock`), `AMIKA_BASE_URL`,
`AMIKA_API_KEY`, `KILN_REPO_URL`, `KILN_AGENT`, `KILN_WORKER_AUTO_STOP`.

**Mock (05 §8).** A mock **Provider** (not a mock of the whole module) — machinery, table,
and event path run for real. Instant lifecycle, scripted turns, failure injection,
conversation loss. Default in dev and e2e.

## Module layout (scaffolded; stubs return errNotImplemented)

- `internal/agent` — `provider.go` (Provider port, `ProviderWorker`/`TurnRef`/`TurnStatus`,
  `WorkerName`), `turn.go` (phases, `Turn` row, payload shapes, `PollInterval`/`ReconcileInterval`),
  `store.go` (Store port over `agent_turns`), `service.go` (Service: `Send`/`Release` — the
  shape of `runtime.AgentRuntime`, matched structurally, never imported — plus `Run` for
  reconciler+poller; ports `EventEnqueuer`, `Slots`, `Clock`).
- `internal/agent/postgres` — Store adapter + `migrations/0001_agent_turns.sql`.
- `internal/agent/mock` — mock Provider (exported knobs: `Script`, `FailProvisioning`,
  `FailStartTurns`, `DropConversation`).
- `internal/agent/amika` — v0beta1 adapter (`Config`, `Client`, `APIError` envelope).

Scaffold-time contract choices to know (tighten or revisit at implementation):

- `agent_turns` has a `message` column beyond the 05 §7 list — recovery must be able to
  StartTurn a never-started turn, so the message has to be durable.
- `Provider.StartTurn` takes the prior `conversation` handle alongside `fresh` — adapters
  are stateless and 05 §6 continues "the recorded session_id", which must come from the
  machinery.
- `Phase.Terminal()` is done-only: `failed` still owes the error `turn_completed` event
  (05 §5 `failed → done`), so the poller's working set is `phase <> 'done'`.
- The 05 §5 enqueue+mark-done single transaction is NOT resolved by the scaffold: the
  Service holds an `EventEnqueuer` port, `Store.Update` documents the requirement, and the
  seam (likely the board's shared-table outbox pattern, 03 §2.4) is an implementation
  decision.
- First-message-vs-continuation is derived via `Store.LatestForWorker` (no row or a
  release row ⇒ fresh).

## How to work here

- Never block a port call on the provider: record in `agent_turns`, return; the
  reconciler/poller advances the turn (05 D2).
- The module owns its own table and migration (`agent_turns`) — adapter state, not board
  state (03 I8 stays intact).
- The board is reached only via events (`EnqueueEvent`); this module never mutates board
  state (05 D3).
- New provider = new Provider adapter package beside `./amika` + config; if you find
  yourself touching the service or consumer contract, the abstraction is leaking.

## Common footguns

- Leaking a provider concept (session id, sandbox state, job id) into a consumer-facing
  type, event payload, or log line other modules parse.
- Blocking an outbox handler on provisioning — it fights the runtime's 8-attempt budget
  (04 D8). Record-and-return, always.
- Creating workers unconditionally at startup instead of adopt-first reconciliation —
  duplicates the pool on every deploy.
- Trusting the provider to dedupe: v0beta1 has **no idempotency keys**; every port call
  checks `agent_turns` first.

## Potential gotchas

- Amika sandbox `state` values are **not enumerated** in v0beta1 — `WorkerReady` must be
  defensive and get hardened against the real value set during implementation (05 §11).
- `GET /sandboxes/{id}` accepts id **or name** — adoption relies on this.
- A provider can lose a conversation between turns; fall back to a fresh conversation
  with the same message (context lost, workspace kept), never fail the ticket (05 §3).
- Amika's `auto_delete_interval` must stay off — it would yank a worker out from under a
  Blocked ticket waiting on the user overnight (05 D6). In v0beta1 the "off" sentinel is a
  **negative** interval (`-1`); the adapter sends `auto_delete_interval: -1`.

## Adapter implementation notes (`internal/agent/amika`, v0beta1, landed)

The Provider port is fully implemented over v0beta1. Where the docs are silent the adapter
is deliberately defensive; these are the hardening points to confirm against the live API:

- **State classification lives in `states.go`.** Both sandbox `state` and job `state` are
  un-enumerated in v0beta1, so `classifyState`/`classifyJob` match known strings and fall
  through to the safe default (sandbox → not-ready-yet, keep polling; job → keep polling
  unless it produced a result or `is_error`). Add real values there as they're observed —
  it's the one place to edit.
- **`auto_stop_interval` unit is undocumented.** The adapter sends whole **minutes**
  (`autoStopInterval`); verify the unit against a live sandbox and adjust if it's seconds.
- **`agent_session_id` is `null` in the create-job 202 response** — the session id is only
  assigned as the job runs. `StartTurn` records the passed conversation handle when the
  response omits it, and continuation falls back to omitting `session_id` (Amika continues
  the sandbox's current session) when the recorded handle is empty.
- **Conversation-loss detection is a heuristic**: a continuation (`fresh=false`) that fails
  with a 400/404/409 whose `error_code`/`message` mentions "session" maps to
  `agent.ErrConversationLost`. v0beta1 documents no per-error codes — tighten
  `isConversationLost` once the real session-not-found envelope is known.
- **Auth is `Authorization: Bearer <AMIKA_API_KEY>`** (not documented in `llms.txt`, per
  05 §9). Every 4xx/5xx decodes into `*APIError` (the uniform envelope); check status with
  `errors.As`/`statusIs` (404 on delete = success, 409 on start = already-starting).
- Tests are pure `httptest` (`client_test.go`) — no live calls, no recorded fixtures yet;
  the manual smoke checklist (05 §10) still gates the first real-Amika run.
