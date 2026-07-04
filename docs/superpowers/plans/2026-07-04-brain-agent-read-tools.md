# Brain agent-management read tools — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the orchestrator brain two provider-neutral read tools — `list_agents` and `get_agent_updates` — so it can observe running agents and read an agent's latest completed output, backed by a new read seam into the agent-runtime module.

**Architecture:** The agent-runtime `Service` gains two inspector methods (`ListAgents`, `GetAgentUpdates`) and the internal `Provider` port gains `ReadLatestOutput` (Amika: `GET /sandboxes/{id}/sessions/latest`). The brain declares its own `AgentInspector` port + value types (it cannot import `internal/agent`), exposes the two tools, and a thin adapter in `cmd/kiln` bridges `*agent.Service` → `brain.AgentInspector`. No provider concept (sandbox/session) crosses into the brain.

**Tech Stack:** Go 1.25 (`wg.Go` is in use), standard library `net/http`/`httptest`, Anthropic tool-use shapes (already modelled as `brain.ToolDef`). No new dependencies, no migration, no `/schema` change.

## Global Constraints

- **Provider-neutral (05 §1):** no sandbox/session/job id or provider `state` string may appear in any brain-facing type, tool result, or the adapter's output. Worker ids only.
- **Brain import rule:** `internal/brain` must NOT import `internal/agent` (mirrors the no-runtime-import rule). Neutral types are declared independently in each module and bridged by a `cmd/kiln` adapter.
- **Prompt changes are versioned (06 D7):** never edit a shipped `systemPromptVN`; add a new version and bump `CurrentPromptVersion`. Prompt changes ride the golden-test gate.
- **Hard gate (end-to-end-development, 02 §4):** every task ends green on `cd backend && go build ./... && go test ./...` and `golangci-lint run` (from `backend/`). Match existing file conventions (doc comments citing spec sections, `//nolint:cyclop` on the flat dispatch switch).
- **Deviation from spec (recorded here):** `AgentStatus` is v1-simplified to `working | idle` (derived from `agent_turns`), dropping `stopped | errored` — those would require a per-worker provider `state` read the pool doesn't otherwise need. `AgentUpdate.IsError` is still surfaced best-effort from a terminally-failed turn row. Widening status is a later, additive change.

---

## File Structure

- `backend/internal/agent/inspector.go` **(new)** — neutral inspector types: `AgentStatus`, `AgentInfo`, `AgentUpdate`, `TurnOutput`.
- `backend/internal/agent/provider.go` — add `ReadLatestOutput` to the `Provider` interface.
- `backend/internal/agent/service.go` — implement `ListAgents` / `GetAgentUpdates` (+ `resolveWorker` helper).
- `backend/internal/agent/amika/{client.go,types.go}` — `ReadLatestOutput` via `sessions/latest`; add `Timestamp` to `sessionMessage`.
- `backend/internal/agent/mock/provider.go` — `ReadLatestOutput` + `lastOutput` tracking + `SeedLatestOutput` test knob.
- `backend/internal/brain/{types.go,ports.go}` — brain-local `AgentStatus`/`AgentInfo`/`AgentUpdate` + `AgentInspector` port; thread through `Service`/`NewService`.
- `backend/internal/brain/tools.go` — two tool defs, input structs, dispatch cases, output formatting.
- `backend/internal/brain/prompt.go` — `systemPromptV3` + version bump to 3.
- `backend/cmd/kiln/{wiring.go,adapters.go}` — `agentInspectorAdapter` bridging `*agent.Service` → `brain.AgentInspector`, wired into `brain.NewService`.
- Test files: `internal/agent/amika/client_test.go`, `internal/agent/mock/provider_test.go`, `internal/agent/inspector_test.go` (new), `internal/brain/dispatch_test.go`, `internal/brain/fakes_test.go`.

---

### Task 1: `Provider.ReadLatestOutput` — port + both adapters

Adds the read primitive to the internal Provider port and implements it in the Amika adapter and the mock. Adding a method to the interface breaks all implementors, so both land in this task to keep the build green.

**Files:**
- Create: `backend/internal/agent/inspector.go`
- Modify: `backend/internal/agent/provider.go` (add interface method)
- Modify: `backend/internal/agent/amika/client.go`, `backend/internal/agent/amika/types.go`
- Modify: `backend/internal/agent/mock/provider.go`
- Test: `backend/internal/agent/amika/client_test.go`, `backend/internal/agent/mock/provider_test.go`

**Interfaces:**
- Produces: `agent.TurnOutput{Output string; At time.Time}`; `Provider.ReadLatestOutput(ctx, ProviderWorker) (TurnOutput, error)`; mock `SeedLatestOutput(name string, out agent.TurnOutput)`.

- [ ] **Step 1: Create the neutral `TurnOutput` type**

Create `backend/internal/agent/inspector.go` (the other inspector types are added in Task 2):

```go
package agent

import "time"

// TurnOutput is the provider-neutral result of ReadLatestOutput (05 §2): the
// most recent completed assistant output for a worker's current conversation.
// Empty Output means "no completed turn yet" — never an error. No provider
// handle (session/sandbox id) is carried.
type TurnOutput struct {
	Output string
	At     time.Time
}
```

- [ ] **Step 2: Add `ReadLatestOutput` to the Provider port**

In `backend/internal/agent/provider.go`, add to the `Provider` interface (after `CheckTurn`, before the closing brace):

```go
	// ReadLatestOutput returns the most recent completed assistant output for
	// the worker's current conversation, provider-neutral (05 §2). Empty
	// TurnOutput when there is no completed turn yet; not an error. Used by the
	// brain's get_agent_updates read tool via the Service inspector methods.
	ReadLatestOutput(ctx context.Context, w ProviderWorker) (TurnOutput, error)
```

- [ ] **Step 3: Run the build to confirm it fails (adapters don't implement it yet)**

Run: `cd /Users/mac/Desktop/kiln/backend && go build ./internal/agent/...`
Expected: FAIL — `*Client` and `*Provider` (mock) do not implement `agent.Provider` (missing `ReadLatestOutput`).

- [ ] **Step 4: Write the failing Amika test**

In `backend/internal/agent/amika/client_test.go`, add a path const near the others (~line 30-36) and a test. Use the existing `pathSandbox`/`sessID` conventions:

```go
// pathSessionsLatest is GET .../sessions/latest — the current conversation's
// transcript, which ReadLatestOutput reads for get_agent_updates.
const pathSessionsLatest = pathSessions + "/latest"

func TestReadLatestOutput(t *testing.T) {
	worker := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID}
	ts := "2026-07-04T15:03:46.256Z"

	t.Run("returns the last assistant message with its timestamp", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox + "/sessions/latest"}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sessionObject{ID: sessID, Metadata: sessionMetadata{Messages: []sessionMessage{
					{Role: "user", Content: "build it", Timestamp: ts},
					{Role: roleAssistant, Content: "pong", Timestamp: ts},
				}}})
			},
		})
		out, err := c.ReadLatestOutput(context.Background(), worker)
		if err != nil {
			t.Fatalf("ReadLatestOutput: %v", err)
		}
		if out.Output != "pong" {
			t.Errorf("Output = %q, want %q", out.Output, "pong")
		}
		if out.At.IsZero() {
			t.Errorf("At is zero, want parsed %q", ts)
		}
	})

	t.Run("empty metadata yields empty output, no error", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox + "/sessions/latest"}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sessionObject{})
			},
		})
		out, err := c.ReadLatestOutput(context.Background(), worker)
		if err != nil || out.Output != "" {
			t.Errorf("got (%+v, %v), want empty output and nil error", out, err)
		}
	})

	t.Run("404 (no session yet) yields empty output, no error", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox + "/sessions/latest"}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("not_found", "nope"))
			},
		})
		out, err := c.ReadLatestOutput(context.Background(), worker)
		if err != nil || out.Output != "" {
			t.Errorf("got (%+v, %v), want empty output and nil error", out, err)
		}
	})
}
```

> Note: `pathSandbox` is the existing const for `/sandboxes/{sbID}`. If `pathSessions` is defined as `pathSandbox + "/sessions"`, the handler key `pathSandbox + "/sessions/latest"` matches the request path exactly. Confirm the exact `sbID`/`sessID` const names when editing (client_test.go:20-36).

- [ ] **Step 5: Add `Timestamp` to the wire `sessionMessage`**

In `backend/internal/agent/amika/types.go`, extend `sessionMessage`:

```go
type sessionMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}
```

- [ ] **Step 6: Implement `ReadLatestOutput` in the Amika client**

In `backend/internal/agent/amika/client.go`, add (near `CheckTurn`/`assistantMessages`). Note the doc comment linking it to the sync-bridge read path:

```go
// ReadLatestOutput reads the worker's current conversation transcript
// (GET …/sessions/latest) and returns the last assistant message as the
// worker's latest completed output (05 §2). Amika materializes the transcript
// only at turn completion (verified 2026-07-04: no mid-turn/partial output),
// so this is exactly the latest finished turn. A missing session (404) or empty
// metadata is "nothing yet" — empty TurnOutput, not an error.
func (c *Client) ReadLatestOutput(ctx context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	ref := workerRef(w)
	var s sessionObject
	path := "/sandboxes/" + url.PathEscape(ref) + "/sessions/latest"
	if err := c.do(ctx, http.MethodGet, path, nil, &s); err != nil {
		if statusIs(err, http.StatusNotFound) {
			return agent.TurnOutput{}, nil
		}
		return agent.TurnOutput{}, err
	}
	var last *sessionMessage
	for i := range s.Metadata.Messages {
		if s.Metadata.Messages[i].Role == roleAssistant {
			last = &s.Metadata.Messages[i]
		}
	}
	if last == nil {
		return agent.TurnOutput{}, nil
	}
	at, _ := time.Parse(time.RFC3339, last.Timestamp) // best-effort; zero At is acceptable
	return agent.TurnOutput{Output: last.Content, At: at}, nil
}
```

- [ ] **Step 7: Run the Amika test — expect PASS**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/agent/amika/ -run TestReadLatestOutput -v`
Expected: PASS (all three sub-tests).

- [ ] **Step 8: Write the failing mock test**

In `backend/internal/agent/mock/provider_test.go`, add:

```go
func TestReadLatestOutput(t *testing.T) {
	p := mock.New()
	w := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: agent.WorkerName("w1")}

	// Nothing seeded → empty output, no error.
	got, err := p.ReadLatestOutput(context.Background(), w)
	if err != nil || got.Output != "" {
		t.Fatalf("got (%+v, %v), want empty", got, err)
	}

	// Seeded output is returned.
	p.SeedLatestOutput(w.Name, agent.TurnOutput{Output: "done"})
	got, err = p.ReadLatestOutput(context.Background(), w)
	if err != nil || got.Output != "done" {
		t.Errorf("got (%+v, %v), want output %q", got, err, "done")
	}
}
```

- [ ] **Step 9: Implement `ReadLatestOutput` + tracking in the mock**

In `backend/internal/agent/mock/provider.go`:

1. Add a field to `Provider` (after `jobs`):
```go
	lastOutput map[string]agent.TurnOutput // worker name → latest completed output
```
2. Allocate it in `init()`:
```go
	if p.lastOutput == nil {
		p.lastOutput = map[string]agent.TurnOutput{}
	}
```
3. Change `CheckTurn`'s signature to bind the worker (`_ agent.ProviderWorker` → `w agent.ProviderWorker`) and record on completion, just before the terminal `return`:
```go
	p.lastOutput[w.Name] = agent.TurnOutput{Output: j.output, At: time.Now()}
	return agent.TurnStatus{Running: false, Output: j.output, IsError: j.isError, CostUSD: 0}, nil
```
4. Add the method + test knob:
```go
// ReadLatestOutput returns the worker's last completed turn output (05 §2),
// recorded by CheckTurn or seeded via SeedLatestOutput. Empty when none.
func (p *Provider) ReadLatestOutput(_ context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	return p.lastOutput[w.Name], nil
}

// SeedLatestOutput presets a worker's latest output so inspector tests can read
// it without driving a full turn (05 §8 test knob).
func (p *Provider) SeedLatestOutput(name string, out agent.TurnOutput) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	p.lastOutput[name] = out
}
```

- [ ] **Step 10: Run the whole agent tree — expect PASS and a green build**

Run: `cd /Users/mac/Desktop/kiln/backend && go build ./... && go test ./internal/agent/...`
Expected: build succeeds (both adapters now satisfy `Provider`); all tests pass.

- [ ] **Step 11: Lint and commit**

Run: `cd /Users/mac/Desktop/kiln/backend && golangci-lint run ./internal/agent/...`
Then:
```bash
git add backend/internal/agent/inspector.go backend/internal/agent/provider.go \
  backend/internal/agent/amika/client.go backend/internal/agent/amika/types.go \
  backend/internal/agent/amika/client_test.go backend/internal/agent/mock/provider.go \
  backend/internal/agent/mock/provider_test.go
git commit -m "feat(agent): add Provider.ReadLatestOutput (sessions/latest) + mock"
```

---

### Task 2: agent `Service` inspector methods (`ListAgents`, `GetAgentUpdates`)

Implements the module's read seam over the Provider + Store, provider-neutral, keyed by board worker id.

**Files:**
- Modify: `backend/internal/agent/inspector.go` (add the three types)
- Modify: `backend/internal/agent/service.go` (methods + helper)
- Test: `backend/internal/agent/inspector_test.go` **(new)**

**Interfaces:**
- Consumes: `Provider.ReadLatestOutput`, `Store.LatestForWorker`, `Provider.ListWorkers` (Task 1 + existing).
- Produces: `agent.AgentStatus` (`AgentWorking`/`AgentIdle`); `agent.AgentInfo{WorkerID, TicketID string; Status AgentStatus; UpdatedAt time.Time}`; `agent.AgentUpdate{WorkerID string; Status AgentStatus; LatestOutput string; IsError bool; At time.Time}`; `(*Service).ListAgents(ctx) ([]AgentInfo, error)`; `(*Service).GetAgentUpdates(ctx, workerID string) (AgentUpdate, error)`.

- [ ] **Step 1: Add the inspector types**

Append to `backend/internal/agent/inspector.go`:

```go
// AgentStatus is the neutral busy/free state the brain sees (05 §2). v1 is
// working|idle, derived from agent_turns; provider liveness beyond this is not
// surfaced (a pooled worker is up unless auto-stopped).
type AgentStatus string

const (
	AgentWorking AgentStatus = "working" // a send turn is in flight
	AgentIdle    AgentStatus = "idle"    // no turn running (done/released/none)
)

// AgentInfo is one running worker as list_agents reports it (05 §2). WorkerID is
// the board slot uuid (03 §2.3) — never a sandbox id. TicketID is the worker's
// most-recent send binding, "" if it never ran a send.
type AgentInfo struct {
	WorkerID  string
	TicketID  string
	Status    AgentStatus
	UpdatedAt time.Time
}

// AgentUpdate is get_agent_updates' answer for one worker (05 §2): its status
// plus the latest completed assistant output. IsError is best-effort from a
// terminally-failed turn row (the transcript read carries no error flag).
type AgentUpdate struct {
	WorkerID     string
	Status       AgentStatus
	LatestOutput string
	IsError      bool
	At           time.Time
}
```

- [ ] **Step 2: Write the failing inspector test**

Create `backend/internal/agent/inspector_test.go`. It uses the mock provider and a minimal fake Store (the four `Store` methods). Package `agent_test`:

```go
package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
)

// fakeStore is a minimal agent.Store: only LatestForWorker is exercised here.
type fakeStore struct {
	latest map[string]agent.Turn
}

func (f *fakeStore) Record(context.Context, agent.Turn) (bool, error) { return true, nil }
func (f *fakeStore) ListNonTerminal(context.Context) ([]agent.Turn, error) { return nil, nil }
func (f *fakeStore) Update(context.Context, agent.Turn) error { return nil }
func (f *fakeStore) LatestForWorker(_ context.Context, workerID string) (agent.Turn, bool, error) {
	t, ok := f.latest[workerID]
	return t, ok, nil
}

// fakeSlots / fakeClock satisfy the remaining NewService ports (unused here).
type fakeSlots struct{}

func (fakeSlots) WorkerIDs(context.Context) ([]string, error) { return nil, nil }

type fakeClock struct{}

func (fakeClock) Now() time.Time                         { return time.Time{} }
func (fakeClock) After(time.Duration) <-chan time.Time   { return make(chan time.Time) }

type nopEvents struct{}

func (nopEvents) EnqueueEvent(context.Context, string, []byte) (int64, error) { return 0, nil }

func newInspector(t *testing.T, store *fakeStore) (*agent.Service, *mock.Provider) {
	t.Helper()
	p := mock.New()
	svc := agent.NewService(store, p, nopEvents{}, fakeSlots{}, fakeClock{})
	return svc, p
}

func TestListAgents_ReportsBusyAndIdle(t *testing.T) {
	busy := "11111111-1111-1111-1111-111111111111"
	idle := "22222222-2222-2222-2222-222222222222"
	store := &fakeStore{latest: map[string]agent.Turn{
		busy: {Kind: agent.KindSend, TicketID: "tkt-a", Phase: agent.PhaseTurnStarted},
		idle: {Kind: agent.KindSend, TicketID: "tkt-b", Phase: agent.PhaseDone},
	}}
	svc, p := newInspector(t, store)
	// Make both workers live in the provider.
	mustCreate(t, p, agent.WorkerName(busy))
	mustCreate(t, p, agent.WorkerName(idle))

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	byID := map[string]agent.AgentInfo{}
	for _, a := range got {
		byID[a.WorkerID] = a
	}
	if a := byID[busy]; a.Status != agent.AgentWorking || a.TicketID != "tkt-a" {
		t.Errorf("busy worker = %+v, want working on tkt-a", a)
	}
	if a := byID[idle]; a.Status != agent.AgentIdle || a.TicketID != "tkt-b" {
		t.Errorf("idle worker = %+v, want idle on tkt-b", a)
	}
}

func TestGetAgentUpdates_ReturnsLatestOutput(t *testing.T) {
	id := "33333333-3333-3333-3333-333333333333"
	store := &fakeStore{latest: map[string]agent.Turn{
		id: {Kind: agent.KindSend, TicketID: "tkt-c", Phase: agent.PhaseTurnStarted},
	}}
	svc, p := newInspector(t, store)
	name := agent.WorkerName(id)
	mustCreate(t, p, name)
	p.SeedLatestOutput(name, agent.TurnOutput{Output: "all done"})

	u, err := svc.GetAgentUpdates(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAgentUpdates: %v", err)
	}
	if u.LatestOutput != "all done" || u.Status != agent.AgentWorking {
		t.Errorf("update = %+v, want output 'all done' and working", u)
	}
}

func TestGetAgentUpdates_UnknownWorkerIsEmptyNotError(t *testing.T) {
	svc, _ := newInspector(t, &fakeStore{latest: map[string]agent.Turn{}})
	u, err := svc.GetAgentUpdates(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetAgentUpdates: %v", err)
	}
	if u.LatestOutput != "" || u.Status != agent.AgentIdle {
		t.Errorf("update = %+v, want empty idle", u)
	}
}

func mustCreate(t *testing.T, p *mock.Provider, name string) {
	t.Helper()
	if _, err := p.CreateWorker(context.Background(), name); err != nil {
		t.Fatalf("CreateWorker(%s): %v", name, err)
	}
}
```

- [ ] **Step 3: Run the test to confirm it fails**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/agent/ -run 'TestListAgents|TestGetAgentUpdates' -v`
Expected: FAIL to compile — `svc.ListAgents` / `svc.GetAgentUpdates` undefined.

- [ ] **Step 4: Implement the inspector methods**

Append to `backend/internal/agent/service.go` (needs `strings`, already imported):

```go
// ListAgents reports every live worker this module owns with its neutral
// busy/idle status and current ticket binding (05 §2) — backs the brain's
// list_agents tool. Status and ticket come from the module's own agent_turns
// (LatestForWorker); no provider handle is exposed.
func (s *Service) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	live, err := s.provider.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent: list agents: %w", err)
	}
	out := make([]AgentInfo, 0, len(live))
	for _, w := range live {
		workerID := strings.TrimPrefix(w.Name, WorkerNamePrefix)
		info := AgentInfo{WorkerID: workerID, Status: AgentIdle}
		if prev, found, lerr := s.store.LatestForWorker(ctx, workerID); lerr == nil && found {
			info.UpdatedAt = prev.UpdatedAt
			if prev.Kind == KindSend {
				info.TicketID = prev.TicketID
				if isRunning(prev.Phase) {
					info.Status = AgentWorking
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// GetAgentUpdates returns one worker's status plus its latest completed output
// (05 §2) — backs the brain's get_agent_updates tool. An unknown/never-created
// worker is an empty idle update, not an error (best-effort read, 05 D2).
func (s *Service) GetAgentUpdates(ctx context.Context, workerID string) (AgentUpdate, error) {
	u := AgentUpdate{WorkerID: workerID, Status: AgentIdle}
	if prev, found, lerr := s.store.LatestForWorker(ctx, workerID); lerr == nil && found && prev.Kind == KindSend {
		if isRunning(prev.Phase) {
			u.Status = AgentWorking
		}
		u.IsError = prev.Phase == PhaseFailed
	}
	w, err := s.resolveWorker(ctx, WorkerName(workerID))
	if err != nil {
		return AgentUpdate{}, fmt.Errorf("agent: get agent updates: %w", err)
	}
	if w == (ProviderWorker{}) {
		return u, nil // worker not live yet — status only
	}
	out, err := s.provider.ReadLatestOutput(ctx, w)
	if err != nil {
		return AgentUpdate{}, fmt.Errorf("agent: read latest output: %w", err)
	}
	u.LatestOutput = out.Output
	u.At = out.At
	return u, nil
}

// resolveWorker returns the cached provider worker for a name, falling back to a
// list-and-match (never creating one — this is a read path). A zero
// ProviderWorker means "not live", handled by the caller as an empty update.
func (s *Service) resolveWorker(ctx context.Context, name string) (ProviderWorker, error) {
	if w, ok := s.getWorker(name); ok {
		return w, nil
	}
	live, err := s.provider.ListWorkers(ctx)
	if err != nil {
		return ProviderWorker{}, err
	}
	for _, w := range live {
		if w.Name == name {
			s.putWorker(w)
			return w, nil
		}
	}
	return ProviderWorker{}, nil
}

// isRunning reports whether a send machine's phase means a turn is in flight
// (05 §5) — everything before the two resting/terminal phases.
func isRunning(p Phase) bool { return p != PhaseDone && p != PhaseFailed }
```

- [ ] **Step 5: Run the inspector tests — expect PASS**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/agent/ -run 'TestListAgents|TestGetAgentUpdates' -v`
Expected: PASS (all three tests).

- [ ] **Step 6: Full agent tree, lint, commit**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/agent/... && golangci-lint run ./internal/agent/...`
Then:
```bash
git add backend/internal/agent/inspector.go backend/internal/agent/service.go backend/internal/agent/inspector_test.go
git commit -m "feat(agent): Service.ListAgents + GetAgentUpdates inspector seam"
```

---

### Task 3: brain `AgentInspector` port + two read tools

Adds the brain-local port + value types, threads the port through the Service, and adds the two tools with dispatch and output formatting. Uses a fake inspector in tests; real wiring is Task 5.

**Files:**
- Modify: `backend/internal/brain/types.go` (value types)
- Modify: `backend/internal/brain/ports.go` (`AgentInspector` port)
- Modify: `backend/internal/brain/service.go` (struct field + `NewService` param)
- Modify: `backend/internal/brain/tools.go` (tool consts, defs, inputs, dispatch, formatting)
- Test: `backend/internal/brain/fakes_test.go` (fake inspector + threaded constructor), `backend/internal/brain/dispatch_test.go` (two dispatch tests + tool-count update)

**Interfaces:**
- Consumes (structurally, at wiring time): the shape of `agent.Service.ListAgents`/`GetAgentUpdates`, converted by the Task 5 adapter.
- Produces: `brain.AgentStatus`, `brain.AgentInfo`, `brain.AgentUpdate`; `brain.AgentInspector` port; `brain.NewService(...)` gains a trailing-but-before-cfg `agents AgentInspector` param (see exact position in Step 3); tool consts `ToolListAgents = "list_agents"`, `ToolGetAgentUpdates = "get_agent_updates"`; `GetAgentUpdatesInput{WorkerID string json:"worker_id"}`.

- [ ] **Step 1: Add brain-local neutral value types**

In `backend/internal/brain/types.go` (needs `time`, already imported for `Message`), add:

```go
// AgentStatus / AgentInfo / AgentUpdate mirror the agent runtime's neutral
// inspector shapes by value (06 §4, amended) — brain cannot import internal/agent
// (same rule as the runtime payloads above), so the cmd/kiln adapter converts
// agent.AgentInfo → brain.AgentInfo. No provider handle ever appears here.
type AgentStatus string

const (
	AgentWorking AgentStatus = "working"
	AgentIdle    AgentStatus = "idle"
)

type AgentInfo struct {
	WorkerID  string
	TicketID  string
	Status    AgentStatus
	UpdatedAt time.Time
}

type AgentUpdate struct {
	WorkerID     string
	Status       AgentStatus
	LatestOutput string
	IsError      bool
	At           time.Time
}
```

- [ ] **Step 2: Add the `AgentInspector` port**

In `backend/internal/brain/ports.go`, add (no compile-time assertion — satisfied structurally by the cmd/kiln adapter, like `NotificationStore`):

```go
// AgentInspector is the brain's read seam into the agent runtime (05 §2, 06 §4
// amended): list running agents and read one agent's latest completed output.
// Provider-neutral — worker ids in, neutral status/output out. Best-effort: a
// read failure becomes a tool error the pass loop absorbs (06 §5), never a pass
// failure. Satisfied structurally by a cmd/kiln adapter over *agent.Service;
// brain cannot import internal/agent, so there is no assertion here.
type AgentInspector interface {
	// ListAgents → tool list_agents.
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	// GetAgentUpdates → tool get_agent_updates, keyed by board worker id.
	GetAgentUpdates(ctx context.Context, workerID string) (AgentUpdate, error)
}
```

- [ ] **Step 3: Thread the port through `Service` and `NewService`**

In `backend/internal/brain/service.go`:

1. Add a field to `Service` (after `convo ConversationReader`):
```go
	agents        AgentInspector
```
2. Add the param to `NewService` (insert `agents AgentInspector` immediately after `convo ConversationReader,`), and set it in the returned struct literal (`agents: agents,`). The updated signature:
```go
func NewService(
	board BoardAPI, reader BoardReader, say Say, notifications NotificationStore,
	convo ConversationReader, agents AgentInspector, llm LLM, cfg Config, promptVersion PromptVersion,
) *Service {
```
Update the struct doc comment at `service.go:29-31` from "its six ports" to "its seven ports".

- [ ] **Step 4: Add the two tools (consts, defs, inputs, dispatch, formatting)**

In `backend/internal/brain/tools.go`:

1. Add two `ToolName` consts after `ToolRetractUpdate`:
```go
	ToolListAgents      ToolName = "list_agents"
	ToolGetAgentUpdates ToolName = "get_agent_updates"
```
2. Update the `ToolName` doc comment count ("the ten tools" → "the twelve tools"; note the two read tools).
3. Add input structs (near the other `…Input` types):
```go
// ListAgentsInput — list_agents takes no arguments (06 §4 amended).
type ListAgentsInput struct{}

// GetAgentUpdatesInput — get_agent_updates → AgentInspector.GetAgentUpdates(worker_id).
type GetAgentUpdatesInput struct {
	WorkerID string `json:"worker_id"`
}
```
4. Add a field const near `fieldNotificationID`:
```go
	fieldWorkerID = "worker_id"
```
5. Append two `ToolDef`s to the end of the `Tools` slice (after `ToolRetractUpdate`, preserving order for cache stability):
```go
	{
		Name: ToolListAgents,
		Description: "List the running agents (workers) and whether each is working a " +
			"ticket or idle. Read-only.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolGetAgentUpdates,
		Description: "Read an agent's latest completed output by worker id — use to check " +
			"what a working agent last produced. Read-only.",
		InputSchema: objectSchema([]string{fieldWorkerID}, map[string]any{
			fieldWorkerID: stringSchema("Board worker id, from list_agents or the board snapshot."),
		}),
	},
```
6. Add two dispatch cases in `dispatchOne`'s switch (after `ToolRetractUpdate`):
```go
	case ToolListAgents:
		return s.doListAgents(ctx, call)
	case ToolGetAgentUpdates:
		return s.doGetAgentUpdates(ctx, call)
```
7. Add the handlers + formatters at the end of the file:
```go
func (s *Service) doListAgents(ctx context.Context, call ToolCall) (ToolResult, bool) {
	agents, err := s.agents.ListAgents(ctx)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatAgents(agents)}, false
}

func (s *Service) doGetAgentUpdates(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in GetAgentUpdatesInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	u, err := s.agents.GetAgentUpdates(ctx, in.WorkerID)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatUpdate(u)}, false
}

// formatAgents renders list_agents' result as one line per worker for the model.
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "no running agents"
	}
	var b strings.Builder
	for i, a := range agents {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "worker %s — %s", a.WorkerID, a.Status)
		if a.TicketID != "" {
			fmt.Fprintf(&b, " (ticket %s)", a.TicketID)
		}
	}
	return b.String()
}

// formatUpdate renders get_agent_updates' result for the model.
func formatUpdate(u AgentUpdate) string {
	head := fmt.Sprintf("worker %s — %s", u.WorkerID, u.Status)
	if u.IsError {
		head += " (last turn errored)"
	}
	if u.LatestOutput == "" {
		return head + "\nno completed output yet"
	}
	return head + "\nlatest output:\n" + u.LatestOutput
}
```
8. Add `"strings"` to `tools.go`'s imports (it currently imports `context`, `encoding/json`, `fmt`, `board`).

- [ ] **Step 5: Update the tool-count golden test + add dispatch tests**

In `backend/internal/brain/dispatch_test.go`:

1. Update `TestToolSet_IsExactlyTenToolsInFixedOrder` — rename to `...TwelveTools...`, extend its `want` slice to end with `brain.ToolListAgents, brain.ToolGetAgentUpdates`, and fix the count assertion (10 → 12). (Keep the exact assertion style already in the test.)
2. Add two tests mirroring the existing `TestDispatch_*RoutesTo*` pattern:
```go
func TestDispatch_ListAgents_RoutesToInspector(t *testing.T) {
	fi := &fakeInspector{list: []brain.AgentInfo{
		{WorkerID: "w-1", TicketID: "tkt-1", Status: brain.AgentWorking},
		{WorkerID: "w-2", Status: brain.AgentIdle},
	}}
	svc := newTestServiceI(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fi, &scriptedLLM{})

	call := newToolCall(t, "la-1", brain.ToolListAgents, brain.ListAgentsInput{})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("IsError = true, want false (%q)", result.Content)
	}
	if !strings.Contains(result.Content, "w-1") || !strings.Contains(result.Content, "tkt-1") ||
		!strings.Contains(result.Content, "w-2") {
		t.Errorf("Content = %q, want both workers", result.Content)
	}
}

func TestDispatch_GetAgentUpdates_RoutesToInspector(t *testing.T) {
	fi := &fakeInspector{update: brain.AgentUpdate{WorkerID: "w-1", Status: brain.AgentWorking, LatestOutput: "all done"}}
	svc := newTestServiceI(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fi, &scriptedLLM{})

	call := newToolCall(t, "gu-1", brain.ToolGetAgentUpdates, brain.GetAgentUpdatesInput{WorkerID: "w-1"})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("IsError = true, want false (%q)", result.Content)
	}
	if fi.gotWorkerID != "w-1" {
		t.Errorf("GetAgentUpdates worker = %q, want w-1", fi.gotWorkerID)
	}
	if !strings.Contains(result.Content, "all done") {
		t.Errorf("Content = %q, want the latest output", result.Content)
	}
}
```
(Ensure `strings` is imported in the test file.)

- [ ] **Step 6: Add the fake inspector + threaded test constructors**

In `backend/internal/brain/fakes_test.go`:

1. Add the fake:
```go
// fakeInspector is a canned brain.AgentInspector for dispatch tests.
type fakeInspector struct {
	list        []brain.AgentInfo
	update      brain.AgentUpdate
	err         error
	gotWorkerID string
}

func (f *fakeInspector) ListAgents(context.Context) ([]brain.AgentInfo, error) {
	return f.list, f.err
}

func (f *fakeInspector) GetAgentUpdates(_ context.Context, workerID string) (brain.AgentUpdate, error) {
	f.gotWorkerID = workerID
	return f.update, f.err
}
```
2. Thread the new port through the existing constructors so all current tests keep passing with a default (empty) inspector, and add an inspector-aware variant:
```go
func newTestService(board *fakeBoard, say *fakeSay, convo *fakeConvo, llm *scriptedLLM) *brain.Service {
	return newTestServiceN(board, say, &fakeNotifications{}, convo, llm)
}

func newTestServiceN(
	board *fakeBoard, say *fakeSay, notifications brain.NotificationStore,
	convo *fakeConvo, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, notifications, convo, &fakeInspector{}, llm,
		brain.Config{Model: brain.DefaultModel}, brain.CurrentPromptVersion,
	)
}

// newTestServiceI is newTestService with an explicit inspector.
func newTestServiceI(
	board *fakeBoard, say *fakeSay, convo *fakeConvo, agents brain.AgentInspector, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, &fakeNotifications{}, convo, agents, llm,
		brain.Config{Model: brain.DefaultModel}, brain.CurrentPromptVersion,
	)
}
```

- [ ] **Step 7: Run the brain tests — expect PASS**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/brain/ -run 'TestToolSet|TestDispatch_ListAgents|TestDispatch_GetAgentUpdates' -v`
Expected: PASS. Then run the full brain package: `go test ./internal/brain/` — all existing golden/dispatch tests still pass with the threaded constructors.

- [ ] **Step 8: Lint and commit**

Run: `cd /Users/mac/Desktop/kiln/backend && golangci-lint run ./internal/brain/`
Then:
```bash
git add backend/internal/brain/types.go backend/internal/brain/ports.go \
  backend/internal/brain/service.go backend/internal/brain/tools.go \
  backend/internal/brain/dispatch_test.go backend/internal/brain/fakes_test.go
git commit -m "feat(brain): AgentInspector port + list_agents/get_agent_updates tools"
```

---

### Task 4: prompt v3 — teach the brain the two read tools

Adds a new prompt version that (a) corrects the "there is no board-read tool" line so it no longer implies zero read tools, and (b) tells the brain when to reach for `list_agents` / `get_agent_updates`.

**Files:**
- Modify: `backend/internal/brain/prompt.go`
- Test: `backend/internal/brain/pass_loop_test.go` or wherever `CurrentPromptVersion` is asserted (search first)

**Interfaces:**
- Produces: `systemPromptV3` constant; `CurrentPromptVersion = 3`; `promptTemplates[3]` registered.

- [ ] **Step 1: Check who pins the prompt version**

Run: `cd /Users/mac/Desktop/kiln && grep -rn "CurrentPromptVersion\|PromptVersion(2)\|systemPromptV2" backend/internal/brain/`
Expected: note every test asserting version 2 so Step 4 can update them.

- [ ] **Step 2: Add `systemPromptV3`**

In `backend/internal/brain/prompt.go`, copy `systemPromptV2` verbatim into a new `systemPromptV3` constant. Make exactly two edits:

1. Replace the board-read line (v2 has, ~prompt.go:118-119):
```
- The snapshot you are given is authoritative for this turn; there is no
  board-read tool because you already have the whole board.
```
with:
```
- The board snapshot you are given is authoritative for this turn; there is no
  board-read tool because you already have the whole board. Agent activity is
  different: use list_agents to see which workers are running or idle, and
  get_agent_updates(worker_id) to read what a working agent last produced.
```
2. In the TOOL-USAGE CONTRACT section, add a line:
```
- list_agents and get_agent_updates are read-only — they change nothing. Use
  them to check on agents before deciding, not as busy-work every pass.
```

- [ ] **Step 3: Register v3 and bump the current version**

In `prompt.go`:
```go
var promptTemplates = map[PromptVersion]*template.Template{
	1: template.Must(template.New("system_v1").Parse(systemPromptV1)),
	2: template.Must(template.New("system_v2").Parse(systemPromptV2)),
	3: template.Must(template.New("system_v3").Parse(systemPromptV3)),
}
```
```go
const CurrentPromptVersion PromptVersion = 3
```
Update the `CurrentPromptVersion` doc comment to note "v3 adds the agent read tools (list_agents, get_agent_updates)".

- [ ] **Step 4: Update any version-pinned tests + render check**

Update tests found in Step 1 that assert version `2` to `3` (or `brain.CurrentPromptVersion`). Add a minimal render assertion if a prompt-render test exists:
```go
func TestRenderSystemPrompt_V3_MentionsAgentTools(t *testing.T) {
	out := brain.RenderSystemPrompt(3, brain.PromptData{Role: "orchestrator"})
	if !strings.Contains(out, "list_agents") || !strings.Contains(out, "get_agent_updates") {
		t.Errorf("v3 prompt missing agent read-tool guidance:\n%s", out)
	}
}
```
> If `RenderSystemPrompt`/`PromptData` are unexported, drop this test — the version bump + golden pass loop already cover it.

- [ ] **Step 5: Run brain tests — expect PASS**

Run: `cd /Users/mac/Desktop/kiln/backend && go test ./internal/brain/`
Expected: PASS.

- [ ] **Step 6: Lint and commit**

Run: `cd /Users/mac/Desktop/kiln/backend && golangci-lint run ./internal/brain/`
```bash
git add backend/internal/brain/prompt.go backend/internal/brain/*_test.go
git commit -m "feat(brain): prompt v3 — agent read-tool guidance"
```

---

### Task 5: wire the real adapter in `cmd/kiln`

Bridges `*agent.Service` to `brain.AgentInspector` and passes it into `brain.NewService`, completing the end-to-end path.

**Files:**
- Modify: `backend/cmd/kiln/adapters.go` (new adapter type)
- Modify: `backend/cmd/kiln/wiring.go` (pass adapter into `brain.NewService`)

**Interfaces:**
- Consumes: `agent.Service.ListAgents`/`GetAgentUpdates` (Task 2); `brain.AgentInspector` + brain value types (Task 3).
- Produces: `agentInspectorAdapter` satisfying `brain.AgentInspector`.

- [ ] **Step 1: Add the adapter**

In `backend/cmd/kiln/adapters.go` (where `convoAdapter` etc. live), add:

```go
// agentInspectorAdapter bridges *agent.Service to brain.AgentInspector,
// converting the agent module's neutral inspector shapes to the brain's own
// value-copies (brain cannot import internal/agent). No provider handle crosses.
type agentInspectorAdapter struct {
	inner *agent.Service
}

func (a *agentInspectorAdapter) ListAgents(ctx context.Context) ([]brain.AgentInfo, error) {
	infos, err := a.inner.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]brain.AgentInfo, len(infos))
	for i, in := range infos {
		out[i] = brain.AgentInfo{
			WorkerID:  in.WorkerID,
			TicketID:  in.TicketID,
			Status:    brain.AgentStatus(in.Status),
			UpdatedAt: in.UpdatedAt,
		}
	}
	return out, nil
}

func (a *agentInspectorAdapter) GetAgentUpdates(ctx context.Context, workerID string) (brain.AgentUpdate, error) {
	u, err := a.inner.GetAgentUpdates(ctx, workerID)
	if err != nil {
		return brain.AgentUpdate{}, err
	}
	return brain.AgentUpdate{
		WorkerID:     u.WorkerID,
		Status:       brain.AgentStatus(u.Status),
		LatestOutput: u.LatestOutput,
		IsError:      u.IsError,
		At:           u.At,
	}, nil
}
```
Ensure `adapters.go` imports `context`, `github.com/crabtree-michael/kiln/backend/internal/agent`, and `.../internal/brain` (add whichever are missing). Add a compile-time assertion near the other adapters:
```go
var _ brain.AgentInspector = (*agentInspectorAdapter)(nil)
```

- [ ] **Step 2: Pass the adapter into `brain.NewService`**

In `backend/cmd/kiln/wiring.go`, update the `brain.NewService(...)` call (currently at ~wiring.go:145) to insert the inspector after the `convoAdapter` argument. `agentSvc` is already in scope (built at ~wiring.go:134):

```go
	brainSvc := brain.NewService(
		boardSvc, boardSvc, rtSvc, rtSvc, &convoAdapter{rt: rtSvc},
		&agentInspectorAdapter{inner: agentSvc},
		brain.NewAdapter(brain.Config{Model: cfg.BrainModel}),
		brain.Config{Model: cfg.BrainModel}, brain.CurrentPromptVersion,
	)
```

- [ ] **Step 3: Full build + entire backend test suite (the hard gate)**

Run: `cd /Users/mac/Desktop/kiln/backend && go build ./... && go test ./... && golangci-lint run`
Expected: build succeeds; all packages pass; lint clean.

- [ ] **Step 4: Commit**

```bash
git add backend/cmd/kiln/adapters.go backend/cmd/kiln/wiring.go
git commit -m "feat(kiln): wire agent inspector into the brain"
```

---

## Self-Review

**1. Spec coverage:**
- "List running agents" → Task 2 `ListAgents` + Task 3 `list_agents` tool. ✅
- "Get latest updates" (latest completed output) → Task 1 `ReadLatestOutput` + Task 2 `GetAgentUpdates` + Task 3 `get_agent_updates`. ✅
- "Send messages" → pre-existing `send_to_agent`; unchanged (spec §1). ✅
- Provider-neutral seam, brain never sees sessions/sandboxes → brain-local types + `cmd/kiln` adapter (Task 3/5). ✅
- Amika `sessions/latest`, best-effort `IsError`, mock provider knob → Task 1. ✅
- 06 D3/§4 amendment (7→9 tools count is 10→12 in code since 08 added three feed tools) → Task 3 tool-set test + Task 4 prompt. ✅ (Spec said "7→9"; the codebase already has 10 tools from spec 08, so the real transition is 10→12 — reconciled here.)
- No `/schema` regen → confirmed, no wire-contract change. ✅

**2. Placeholder scan:** No TBD/TODO; every code step shows real code; commands have expected output. The two "confirm the const name"/"if unexported, drop this test" notes are explicit conditional guidance, not placeholders.

**3. Type consistency:** `ReadLatestOutput`, `TurnOutput`, `AgentInfo{WorkerID,TicketID,Status,UpdatedAt}`, `AgentUpdate{WorkerID,Status,LatestOutput,IsError,At}`, `AgentStatus{AgentWorking,AgentIdle}` used identically across agent + brain modules; `isRunning`, `resolveWorker`, `formatAgents`, `formatUpdate`, `fakeInspector`, `newTestServiceI`, `agentInspectorAdapter` referenced consistently. `NewService` param position (after `convo`, before `llm`) is applied identically in service.go, fakes_test.go, and wiring.go.

**Deviation from spec (intentional):** `AgentStatus` simplified to `working|idle` for v1 (see Global Constraints); status widening is additive later.
