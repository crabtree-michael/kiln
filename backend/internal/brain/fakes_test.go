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
	ticketT1           = "t-1"
	ticketT7           = "t-7"
	ticketT99          = "t-99"
	opMarkReady        = "mark_ready"
	methodMarkReady    = "MarkReady"
	methodShapeTicket  = "ShapeTicket"
	methodAcceptToDone = "AcceptToDone"
	workerW1           = "w-1"
	sayHello           = "hello"
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

	createTicketFn    func(ctx context.Context, title, body string) (board.Ticket, error)
	shapeTicketFn     func(ctx context.Context, id board.TicketID, patch board.ShapePatch) (board.Ticket, error)
	markReadyFn       func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	sendToAgentFn     func(ctx context.Context, id board.TicketID, instruction string) (board.Ticket, error)
	markBlockedFn     func(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error)
	acceptToDoneFn    func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	requestApprovalFn func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	archiveTicketFn   func(ctx context.Context, id board.TicketID) (board.Ticket, error)
	getBoardFn        func(ctx context.Context) (board.Snapshot, error)
	getTicketFn       func(ctx context.Context, id board.TicketID) (board.Ticket, error)
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
	f.record(methodAcceptToDone, id)
	if f.acceptToDoneFn != nil {
		return f.acceptToDoneFn(ctx, id)
	}
	return board.Ticket{ID: id, State: board.StateDone}, nil
}

func (f *fakeBoard) RequestApproval(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	f.record("RequestApproval", id)
	if f.requestApprovalFn != nil {
		return f.requestApprovalFn(ctx, id)
	}
	return board.Ticket{ID: id, State: board.StateShaping}, nil
}

func (f *fakeBoard) ArchiveTicket(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	f.record("ArchiveTicket", id)
	if f.archiveTicketFn != nil {
		return f.archiveTicketFn(ctx, id)
	}
	now := time.Now()
	return board.Ticket{ID: id, State: board.StateShaping, ArchivedAt: &now}, nil
}

// GetBoard is a read port (BoardReader), tracked separately from the mutation
// ops so mutation-sequence assertions (pass_loop_test.go) aren't perturbed by a
// board read; getBoardCount() observes it.
func (f *fakeBoard) GetBoard(ctx context.Context) (board.Snapshot, error) {
	f.mu.Lock()
	f.getBoards++
	f.mu.Unlock()
	if f.getBoardFn != nil {
		return f.getBoardFn(ctx)
	}
	return board.Snapshot{}, nil
}

// GetTicket is the single-ticket read port (BoardReader), backing get_ticket.
func (f *fakeBoard) GetTicket(ctx context.Context, id board.TicketID) (board.Ticket, error) {
	f.record("GetTicket", id)
	if f.getTicketFn != nil {
		return f.getTicketFn(ctx, id)
	}
	return board.Ticket{ID: id, State: board.StateShaping}, nil
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

// --- fake NotificationStore ----------------------------------------------

// postedNotification records one post_update call (08 §7).
type postedNotification struct {
	Kind     string
	Body     string
	Ticket   *string
	ImageURL *string
}

// fakeNotifications is the NotificationStore port's recording double (08 §7):
// PostNotification/RetractNotification record their args and optionally return
// a configured error, so golden tests can assert the tool -> port mapping.
type fakeNotifications struct {
	mu         sync.Mutex
	posts      []postedNotification
	retracts   []int64
	edits      []editedNotification
	postErr    error
	retractErr error
	editErr    error
}

// editedNotification records one edit_update call (06 §4 amended).
type editedNotification struct {
	ID       int64
	Kind     string
	Body     string
	ImageURL *string
}

func (f *fakeNotifications) PostNotification(_ context.Context, kind, body string, ticketID, imageURL *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, postedNotification{Kind: kind, Body: body, Ticket: ticketID, ImageURL: imageURL})
	return f.postErr
}

func (f *fakeNotifications) RetractNotification(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retracts = append(f.retracts, id)
	return f.retractErr
}

func (f *fakeNotifications) EditNotification(_ context.Context, id int64, kind, body string, imageURL *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, editedNotification{ID: id, Kind: kind, Body: body, ImageURL: imageURL})
	return f.editErr
}

func (f *fakeNotifications) edited() []editedNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]editedNotification, len(f.edits))
	copy(out, f.edits)
	return out
}

func (f *fakeNotifications) posted() []postedNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]postedNotification, len(f.posts))
	copy(out, f.posts)
	return out
}

func (f *fakeNotifications) retracted() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.retracts))
	copy(out, f.retracts)
	return out
}

// --- fake FeedReader ------------------------------------------------------

// fakeFeed is the FeedReader port's recording double (06 §4 amended): ListUpdates
// returns canned active cards and records that it was called.
type fakeFeed struct {
	updates []brain.Update
	err     error
	calls   int
}

func (f *fakeFeed) ListUpdates(context.Context) ([]brain.Update, error) {
	f.calls++
	return f.updates, f.err
}

// --- fake AgentInspector --------------------------------------------------

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

// --- fake RepoShell -------------------------------------------------------

// fakeRepo is a canned brain.RepoShell for dispatch tests: it records the last
// command it was asked to run and returns a scripted RepoResult (or err).
type fakeRepo struct {
	result     brain.RepoResult
	err        error
	gotCommand string

	verify    brain.RepoVerify
	verifyErr error
	gotSHA    string
}

func (f *fakeRepo) Run(_ context.Context, command string) (brain.RepoResult, error) {
	f.gotCommand = command
	return f.result, f.err
}

func (f *fakeRepo) VerifyOnMain(_ context.Context, sha string) (brain.RepoVerify, error) {
	f.gotSHA = sha
	return f.verify, f.verifyErr
}

// VerifyInPR shares the scripted verify/verifyErr with VerifyOnMain: a service
// runs exactly one of the two per gate mode, so one script suffices per test.
func (f *fakeRepo) VerifyInPR(_ context.Context, sha string) (brain.RepoVerify, error) {
	f.gotSHA = sha
	return f.verify, f.verifyErr
}

// --- construction helpers -------------------------------------------------

func newTestService(board *fakeBoard, say *fakeSay, convo *fakeConvo, llm *scriptedLLM) *brain.Service {
	return newTestServiceN(board, say, &fakeNotifications{}, convo, llm)
}

// newTestServiceN is newTestService with an explicit NotificationStore, for
// the feed-tool golden tests (08 §7) that assert post_update/edit_update/
// retract_update.
func newTestServiceN(
	board *fakeBoard, say *fakeSay, notifications brain.NotificationStore,
	convo *fakeConvo, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, notifications, &fakeFeed{}, convo, &fakeInspector{}, &fakeRepo{}, llm,
		brain.Config{Model: brain.DefaultModel},
	)
}

// newTestServiceF is newTestService with an explicit FeedReader, for the
// list_updates golden test.
func newTestServiceF(
	board *fakeBoard, say *fakeSay, feed brain.FeedReader, convo *fakeConvo, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, &fakeNotifications{}, feed, convo, &fakeInspector{}, &fakeRepo{}, llm,
		brain.Config{Model: brain.DefaultModel},
	)
}

// newTestServiceI is newTestService with an explicit inspector.
func newTestServiceI(
	board *fakeBoard, say *fakeSay, convo *fakeConvo, agents brain.AgentInspector, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, &fakeNotifications{}, &fakeFeed{}, convo, agents, &fakeRepo{}, llm,
		brain.Config{Model: brain.DefaultModel},
	)
}

// newTestServiceR is newTestService with an explicit repo shell, for the bash
// tool golden tests.
func newTestServiceR(
	board *fakeBoard, say *fakeSay, convo *fakeConvo, repo brain.RepoShell, llm *scriptedLLM,
) *brain.Service {
	return brain.NewService(
		board, board, say, &fakeNotifications{}, &fakeFeed{}, convo, &fakeInspector{}, repo, llm,
		brain.Config{Model: brain.DefaultModel},
	)
}

// newTestServiceRGate is newTestServiceR with an explicit merge-gate mode, for
// the done-gate tests that exercise the "pr" gate (06 §7).
func newTestServiceRGate(
	board *fakeBoard, say *fakeSay, convo *fakeConvo, repo brain.RepoShell, llm *scriptedLLM,
	gate brain.GateMode,
) *brain.Service {
	return brain.NewService(
		board, board, say, &fakeNotifications{}, &fakeFeed{}, convo, &fakeInspector{}, repo, llm,
		brain.Config{Model: brain.DefaultModel, GateMode: gate},
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
