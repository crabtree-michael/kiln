# Brain agent-management read tools — design

**Status:** proposed
**Date:** 2026-07-04
**Amends:** 05 (agent-runtime) §2, 06 (orchestrator-brain) §4 & D3

## 1. Goal

Extend the orchestrator brain's fixed tool set so it can **fully manage agents**:
list running agents, read an agent's latest updates, and send it a message. Sending
already exists (`send_to_agent`). This design adds the two missing **read**
capabilities and the provider-neutral seam that backs them.

The brain must stay provider-neutral: nothing it sees may reveal that Amika,
sandboxes, or sessions exist (05 §1). The read seam is therefore keyed by board
worker ids and returns neutral shapes only.

## 2. Verified Amika v0beta1 capability (empirical)

Probed live against the org in `.env` (read-only GETs plus one trivial send):

| Capability | Endpoint | Result |
|---|---|---|
| List running agents | `GET /sandboxes` | ✅ `id, name, state ("started"/"stopped"), current_session_id, error_message, auto_stop_interval, provider, services, created_at` |
| Latest completed output | `GET /sandboxes/{id}/sessions/latest` (or `/sessions/{id}`) | ✅ `metadata.messages = [{role, content, timestamp}]` + `claude_session_id`; materialized at turn completion |
| Send message | `POST /sandboxes/{id}/agent-send` (sync) | ✅ inline `{response, is_error, is_new_session, session_id, cost_usd}` |
| Mid-turn / in-progress peek | — | ❌ Not available. During a running turn the transcript still showed only the prior exchange; new messages appear atomically at completion. No streaming/SSE. |
| Activity/event feed | `GET /sandboxes/{id}/workflow/events` | ❌ Empty for plain coding turns (workflow-only) |
| Async job status | `POST /sandboxes/{id}/agent-send-jobs` | ⚠️ Still 500s org-wide ("Agent launch failed") — unchanged from the existing sync bridge note |

**Decisive consequence.** Amika exposes nothing mid-turn, so "get latest updates" can
only ever mean **the latest _completed_ assistant output for a worker**. A live read is
no fresher than the completion event already was; its value is letting the brain re-read
output for a worker it was *not* just woken for, or after context truncation.

`agent_turns` does **not** persist output (output leaves the module only via the
`turn_completed` event, then the row goes to `done`), so "latest updates" is served by a
live Amika read behind the module — not from stored module state.

## 3. Design

### 3.1 New seam: `AgentInspector` (agent-runtime consumer contract)

A read port on the agent module, sibling to the existing `Send`/`Release` contract,
consumed **structurally** by the brain (never imported — matching the `AgentRuntime`
pattern, 05 §2). Keyed by board worker ids; no provider handles cross it.

```go
// AgentInspector is the brain's read seam into the agent runtime (05 §2).
// Provider-neutral: worker ids in, neutral status/output out. Best-effort —
// a read failure is a tool error the brain loop absorbs, never a pass failure.
type AgentInspector interface {
    ListAgents(ctx context.Context) ([]AgentInfo, error)
    GetAgentUpdates(ctx context.Context, workerID string) (AgentUpdate, error)
}

type AgentInfo struct {
    WorkerID  string
    TicketID  string      // best-effort binding from the latest agent_turns row; "" if idle/none
    Status    AgentStatus
    UpdatedAt time.Time
}

type AgentUpdate struct {
    WorkerID     string
    Status       AgentStatus
    LatestOutput string  // last completed assistant message; "" if none yet
    IsError      bool    // best-effort (not on the transcript read path) — see §3.2
    At           time.Time
}

// AgentStatus is the neutral lifecycle the brain sees. working = a turn is in
// flight; the rest map from provider liveness.
type AgentStatus string // "working" | "idle" | "stopped" | "errored"
```

**Status blend (no new stored state):** a non-terminal `agent_turns` row for the slot
(`phase <> 'done'` and not the failed-owes-event case) ⇒ `working`; otherwise the
provider's sandbox state ⇒ `idle` (started) / `stopped` / `errored`. The ticket binding
in `AgentInfo` comes from `Store.LatestForWorker` (the `send` row's `ticket_id`).

### 3.2 New internal Provider method

```go
// ReadLatestOutput returns the most recent completed assistant output for the
// worker's current conversation, provider-neutral. Empty output if none yet.
ReadLatestOutput(ctx context.Context, w ProviderWorker) (TurnOutput, error)

type TurnOutput struct {
    Output string
    At     time.Time
}
```

- **Amika impl:** `GET /sandboxes/{id}/sessions/latest` → last `assistant` entry in
  `metadata.messages` → `{Output: content, At: timestamp}`. `sessions/latest` is the
  worker's current conversation because the module keeps one session per binding
  (05 §6). A 404 / empty metadata ⇒ empty `TurnOutput`, not an error.
- **IsError/cost** are not on this read path (same limitation as `CheckTurn`, 05 §2.2) →
  `AgentUpdate.IsError` is best-effort `false`. Documented, not silently dropped.
- **Mock provider:** add a scripted `ReadLatestOutput` knob so brain/e2e runs need no
  live Amika.

### 3.3 Two brain tools (06 §4: 7 → 9)

- `list_agents` — no args → array of `AgentInfo`-shaped rows.
- `get_agent_updates(worker_id)` — the brain supplies `worker_id` from its injected
  snapshot or from a prior `list_agents` call.

Both are **pure reads**: results feed back into the existing bounded tool loop (06 §5,
max 8 rounds) and emit **no** outbox actions. They are additive to the write tools;
`send_to_agent`/`mark_blocked`/etc. are unchanged.

### 3.4 Data flow

```
event → brain pass → LLM calls get_agent_updates(worker_id)
      → AgentInspector port → agent Service
      → map worker_id → ProviderWorker (name = kiln-worker-<worker_id>)
      → Provider.ReadLatestOutput → GET sessions/latest → metadata.messages
      → neutral AgentUpdate → tool result → LLM continues the loop
```

### 3.5 Error handling

Read failure, no session yet, or unknown worker → the tool returns a benign result
(empty output / best-effort status) or an error string **fed back into the loop
verbatim** (06 §5). It never fails the pass. Consistent with the reactive, best-effort
design and with "never block/​fail on the provider" (05 D2).

## 4. Spec amendments recorded

- **06 D3** ("no board read; state is injected") is **superseded for agent reads**: the
  brain now has two read tools. Board reads remain out (state is still injected); this is
  narrowly about *agent* observability, which the injected snapshot cannot make fresh.
- **06 §4** tool count 7 → 9 (`list_agents`, `get_agent_updates`).
- **05 §2** gains a second consumer seam (`AgentInspector`) beside `AgentRuntime`, and
  the Provider port gains `ReadLatestOutput`.

## 5. Testing

- **Brain (primary suite — golden decision tests, 06 §9):** scripted LLM invokes
  `list_agents` / `get_agent_updates`; a fake `AgentInspector` feeds canned data; assert
  the tool-call sequence and that the fed data reaches the model. No real Postgres/LLM.
- **Agent module:** unit-test `ListAgents`/`GetAgentUpdates` status-blend over the mock
  Provider + a fake Store (working vs idle vs stopped vs errored). `httptest` for the
  amika `ReadLatestOutput` transcript parse, reusing `client_test.go` patterns (assistant
  message selection, empty-metadata → empty output, 404 → empty).
- **No `/schema` regen** — these are internal LLM tools, not the client↔server wire
  contract (wire-schema untouched).

## 6. Files

- `internal/agent/inspector.go` (new) — `AgentInspector`, `AgentInfo`, `AgentUpdate`,
  `AgentStatus`, `TurnOutput`.
- `internal/agent/provider.go` — add `ReadLatestOutput` to the `Provider` interface.
- `internal/agent/service.go` — implement `ListAgents`/`GetAgentUpdates` (status blend).
- `internal/agent/mock/…` — scripted `ReadLatestOutput`.
- `internal/agent/amika/{client.go,types.go}` — `ReadLatestOutput` via `sessions/latest`.
- `internal/brain/…` — two tool defs, the injected `AgentInspector` port, system-prompt
  template update (prompt change rides the golden-test gate, 06 D7), golden tests.
- `cmd/kiln/wiring.go` — wire the agent Service as the brain's `AgentInspector`.

## 7. Out of scope / dropped (with reason)

- **Mid-turn / streaming peek** — Amika materializes the transcript only at completion.
- **Workflow/activity event feed** — empty for plain coding turns.
- **Async job-status reads** — `agent-send-jobs` still 500s org-wide.
- **Board read tools** — 06 D3 otherwise stands; state stays injected.
- **Surfacing agent updates to the web client** — brain-internal only; no client change.

## 8. Hardening points to confirm at implementation

- `sessions/latest` shape when a worker has been released/recreated (session churn) —
  confirm it returns the *current* binding's session, not a stale one.
- `metadata.messages` ordering guarantee (we take the last `assistant` entry) — verified
  ordered in probing; assert defensively.
- `AgentStatus` mapping against the real, un-enumerated sandbox `state` set (reuse
  `states.go` classification rather than duplicating string matching).
