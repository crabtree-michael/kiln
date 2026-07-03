package brain_test

// Test doubles for the golden decision tests (06 §9): a scripted LLM fake
// that plays back a fixed LLMResponse sequence and records every LLMRequest
// it receives, plus recording fakes for BoardAPI/BoardReader, Say, and
// ConversationReader. No real Postgres, no real LLM — everything here is an
// in-memory double implementing the exported ports in
// internal/brain/ports.go and internal/brain/llm.go.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// Shared literals hoisted to package-level consts so no single string recurs
// 3+ times across the brain_test package (goconst).
const (
	ticketT1        = "t-1"
	ticketT99       = "t-99"
	opMarkReady     = "mark_ready"
	methodMarkReady = "MarkReady"
)

// --- scripted LLM -----------------------------------------------------

// scriptedLLM is the LLM port's fake (06 §9): Do returns responses[i] (or
// errs[i], if set) for the i-th call, in order, and records every request it
// was sent so tests can assert exactly what context/tool-results reached the
// model. If repeatLast is true and the script is exhausted, the last
// response is replayed forever — used to script a runaway model for the
// round-cap tests (06 §5, D4). If the script is exhausted and repeatLast is
// false, Do returns StopEndTurn so an under-scripted test fails on a
// specific assertion rather than hanging.
type scriptedLLM struct {
	mu         sync.Mutex
	responses  []brain.LLMResponse
	errs       []error
	repeatLast bool

	requests []brain.LLMRequest
}

func (f *scriptedLLM) Do(_ context.Context, req brain.LLMRequest) (brain.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	idx := len(f.requests)
	f.requests = append(f.requests, req)

	if idx < len(f.errs) && f.errs[idx] != nil {
		return brain.LLMResponse{}, f.errs[idx]
	}
	if idx < len(f.responses) {
		return f.responses[idx], nil
	}
	if f.repeatLast && len(f.responses) > 0 {
		return f.responses[len(f.responses)-1], nil
	}
	return brain.LLMResponse{StopReason: brain.StopEndTurn}, nil
}

func (f *scriptedLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *scriptedLLM) requestAt(t *testing.T, i int) brain.LLMRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 || i >= len(f.requests) {
		t.Fatalf("requestAt(%d): only %d requests were recorded", i, len(f.requests))
	}
	return f.requests[i]
}

// requireCalled fails the test now (rather than panicking on a later index)
// if the LLM was never invoked — the honest signal that runPass short-
// circuited before reaching the loop at all (e.g. the scaffold stub).
func (f *scriptedLLM) requireCalled(t *testing.T, atLeast int) {
	t.Helper()
	if got := f.callCount(); got < atLeast {
		t.Fatalf("expected at least %d LLM.Do call(s), got %d", atLeast, got)
	}
}

func toolUse(calls ...brain.ToolCall) brain.LLMResponse {
	return brain.LLMResponse{StopReason: brain.StopToolUse, Calls: calls}
}

func endTurn(text string) brain.LLMResponse {
	return brain.LLMResponse{StopReason: brain.StopEndTurn, Text: text}
}

// --- fake BoardAPI / BoardReader ---------------------------------------

type recordedCall struct {
	Method string
	Args   []any
}

// fakeBoard is a single recording double for both BoardAPI (the six
// mutation ops, 06 §4) and BoardReader (GetBoard, 06 §3.1/D3) — pass the same
// instance for both NewService parameters. Every method records its call
// (method name + args) before consulting an optional override func; with no
// override it returns a zero-ish success Ticket/Snapshot and a nil error, so
// tests only need to configure the methods a scenario cares about.
type fakeBoard struct {
	mu        sync.Mutex
	calls     []recordedCall
	getBoards int

	createTicketFn func(ctx context.Context, title, body string) (board.Ticket, error)
	shapeTicketFn  func(ctx context.Context, id board.TicketID, patch board.ShapePatch) (board.Ticket, error)
	markReadyFn    func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	sendToAgentFn  func(ctx context.Context, id board.TicketID, instruction string) (board.Ticket, error)
	markBlockedFn  func(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error)
	acceptToDoneFn func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	getBoardFn     func(ctx context.Context) (board.Snapshot, error)
}

func (f *fakeBoard) CreateTicket(ctx context.Context, title, body string) (board.Ticket, error) {
	f.record("CreateTicket", title, body)
	if f.createTicketFn != nil {
		return f.createTicketFn(ctx, title, body)
	}
	return board.Ticket{ID: "new-ticket", Title: title, Body: body, State: board.StateShaping}, nil
}

func (f *fakeBoard) ShapeTicket(ctx context.Context, id board.TicketID, patch board.ShapePatch) (board.Ticket, error) {
	f.record("ShapeTicket", id, patch)
	if f.shapeTicketFn != nil {
		return f.shapeTicketFn(ctx, id, patch)
	}
	return board.Ticket{ID: id, State: board.StateShaping}, nil
}

func (f *fakeBoard) MarkReady(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	f.record("MarkReady", id)
	if f.markReadyFn != nil {
		return f.markReadyFn(ctx, id)
	}
	return board.Ticket{ID: id, State: board.StateReady}, nil
}

func (f *fakeBoard) SendToAgent(ctx context.Context, id board.TicketID, instruction string) (board.Ticket, error) {
	f.record("SendToAgent", id, instruction)
	if f.sendToAgentFn != nil {
		return f.sendToAgentFn(ctx, id, instruction)
	}
	return board.Ticket{ID: id, State: board.StateWorking}, nil
}

func (f *fakeBoard) MarkBlocked(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error) {
	f.record("MarkBlocked", id, reason)
	if f.markBlockedFn != nil {
		return f.markBlockedFn(ctx, id, reason)
	}
	return board.Ticket{ID: id, State: board.StateBlocked, BlockedReason: &reason}, nil
}

func (f *fakeBoard) AcceptToDone(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	f.record("AcceptToDone", id)
	if f.acceptToDoneFn != nil {
		return f.acceptToDoneFn(ctx, id)
	}
	return board.Ticket{ID: id, State: board.StateDone}, nil
}

// GetBoard is the read port (BoardReader), tracked separately from the six
// mutation ops so mutation-sequence assertions (pass_loop_test.go) aren't
// perturbed by the once-per-pass snapshot read; getBoardCount() observes it.
func (f *fakeBoard) GetBoard(ctx context.Context) (board.Snapshot, error) {
	f.mu.Lock()
	f.getBoards++
	f.mu.Unlock()
	if f.getBoardFn != nil {
		return f.getBoardFn(ctx)
	}
	return board.Snapshot{}, nil
}

// record, recordedCalls, and getBoardCount are unexported and placed after the
// exported port methods (funcorder).
func (f *fakeBoard) record(method string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedCall{Method: method, Args: args})
}

func (f *fakeBoard) recordedCalls() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeBoard) getBoardCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getBoards
}

// --- fake Say -----------------------------------------------------------

// fakeSay records every text said (07 §3) and optionally returns a
// configured error.
type fakeSay struct {
	mu    sync.Mutex
	texts []string
	err   error
}

func (f *fakeSay) Say(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return f.err
}

func (f *fakeSay) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.texts))
	copy(out, f.texts)
	return out
}

// --- fake ConversationReader ---------------------------------------------

// fakeConvo serves Recent(n) from a fixed, oldest-first backing slice and
// records every n it was asked for, so tests can assert the transcript
// window (06 §3.2, TranscriptWindow=20).
type fakeConvo struct {
	messages []brain.Message
	recentNs []int
	err      error
}

func (f *fakeConvo) Recent(_ context.Context, n int) ([]brain.Message, error) {
	f.recentNs = append(f.recentNs, n)
	if f.err != nil {
		return nil, f.err
	}
	if n >= len(f.messages) {
		out := make([]brain.Message, len(f.messages))
		copy(out, f.messages)
		return out, nil
	}
	out := make([]brain.Message, n)
	copy(out, f.messages[len(f.messages)-n:])
	return out, nil
}

// --- construction helpers -------------------------------------------------

func newTestService(board *fakeBoard, say *fakeSay, convo *fakeConvo, llm *scriptedLLM) *brain.Service {
	return brain.NewService(
		board, board, say, convo, llm,
		brain.Config{Model: brain.DefaultModel}, brain.CurrentPromptVersion,
	)
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return raw
}

func newToolCall(t *testing.T, id string, name brain.ToolName, input any) brain.ToolCall {
	t.Helper()
	return brain.ToolCall{ID: id, Name: name, Input: mustMarshal(t, input)}
}

func humanMessageEvent(id int64, text string) brain.Event {
	payload, err := json.Marshal(brain.HumanMessagePayload{Text: text})
	if err != nil {
		panic(err)
	}
	return brain.Event{ID: id, Type: brain.EventHumanMessage, Payload: payload, CreatedAt: time.Now()}
}

func agentTurnCompletedEvent(id int64, p brain.AgentTurnCompletedPayload) brain.Event {
	payload, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return brain.Event{ID: id, Type: brain.EventAgentTurnCompleted, Payload: payload, CreatedAt: time.Now()}
}
