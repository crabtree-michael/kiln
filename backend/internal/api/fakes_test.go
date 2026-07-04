package api_test

// Shared unit-test fakes for the api module (04 §9: "unit (api): routes
// against a fake runtime/board — decode/encode, snapshot-on-connect, fan-out
// to multiple SSE clients, keepalive"). Everything here is in-memory; the
// only real network is the loopback httptest.Server the route tests dial.

import (
	"context"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// fakeVoiceTokenMinter is api.VoiceTokenMinter (09 §6 POST /api/voice/token):
// a canned token/expiry or an injected mint error.
type fakeVoiceTokenMinter struct {
	token string
	exp   time.Time
	err   error
}

func (f *fakeVoiceTokenMinter) MintStreamingToken(context.Context) (string, time.Time, error) {
	return f.token, f.exp, f.err
}

// fakeBoardReader is api.BoardReader: a single configurable snapshot,
// returned to every caller (GET /api/board and the hub's board pushes
// share this same port, 04 §7).
type fakeBoardReader struct {
	mu       sync.Mutex
	snapshot board.Snapshot
	err      error
	calls    int
}

func (f *fakeBoardReader) GetBoard(context.Context) (board.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snapshot, f.err
}

func (f *fakeBoardReader) setSnapshot(s board.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshot = s
}

func (f *fakeBoardReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeMessagePoster is api.MessagePoster (07 §3-§4).
type fakeMessagePoster struct {
	mu        sync.Mutex
	texts     []string
	messageID int64
	eventID   int64
	err       error
}

func (f *fakeMessagePoster) PostMessage(_ context.Context, text string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return f.messageID, f.eventID, f.err
}

func (f *fakeMessagePoster) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

func (f *fakeMessagePoster) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return ""
	}
	return f.texts[len(f.texts)-1]
}

// fakeMessagesReader is api.MessagesReader (07 §4 GET /api/messages).
type fakeMessagesReader struct {
	mu       sync.Mutex
	messages []runtime.Message
	err      error
	ns       []int
}

func (f *fakeMessagesReader) Recent(_ context.Context, n int) ([]runtime.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ns = append(f.ns, n)
	if f.err != nil {
		return nil, f.err
	}
	if n >= len(f.messages) {
		out := make([]runtime.Message, len(f.messages))
		copy(out, f.messages)
		return out, nil
	}
	out := make([]runtime.Message, n)
	copy(out, f.messages[len(f.messages)-n:])
	return out, nil
}

func (f *fakeMessagesReader) requestedNs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.ns...)
}

// fakeFeedReader is api.FeedReader (08 §3 GET /api/feed).
type fakeFeedReader struct {
	mu       sync.Mutex
	snapshot runtime.FeedSnapshot
	err      error
	calls    int
}

func (f *fakeFeedReader) Feed(context.Context) (runtime.FeedSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snapshot, f.err
}

func (f *fakeFeedReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeSeenAcker is api.SeenAcker (08 §3 POST /api/feed/seen).
type fakeSeenAcker struct {
	mu      sync.Mutex
	lastIDs []int64
	err     error
}

func (f *fakeSeenAcker) MarkSeen(_ context.Context, lastID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastIDs = append(f.lastIDs, lastID)
	return f.err
}

func (f *fakeSeenAcker) seen() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.lastIDs...)
}

// devTicketID is the id fakeSeeder mints, shared with the route assertions.
const devTicketID = "t-1"

// fakeSeeder is the double for the dev-only TicketSeeder port: it records the
// SeedTicket spec and can inject a failure (including board.ErrNoFreeWorker).
type fakeSeeder struct {
	seedErr     error
	spec        board.SeedSpec
	markedReady board.TicketID
}

func (f *fakeSeeder) SeedTicket(_ context.Context, spec board.SeedSpec) (board.Ticket, error) {
	if f.seedErr != nil {
		return board.Ticket{}, f.seedErr
	}
	f.spec = spec
	state := spec.State
	if state == "" {
		state = board.StateShaping
	}
	return board.Ticket{
		ID: devTicketID, Title: spec.Title, Body: spec.Body, State: state,
		ApprovalRequested: spec.ApprovalRequested,
	}, nil
}

// MarkReady records the ready transition the dev route triggers for a state=ready
// seed and returns the ticket in ready.
func (f *fakeSeeder) MarkReady(_ context.Context, id board.TicketID) (board.Ticket, error) {
	if f.seedErr != nil {
		return board.Ticket{}, f.seedErr
	}
	f.markedReady = id
	return board.Ticket{ID: id, Title: f.spec.Title, Body: f.spec.Body, State: board.StateReady}, nil
}

// fakeNotificationPoster is the double for the dev-only NotificationPoster port
// (08 §E.3 POST /api/dev/notifications).
type fakeNotificationPoster struct {
	mu    sync.Mutex
	calls []devNote
	err   error
}

type devNote struct {
	kind, body         string
	ticketID, imageURL *string
}

func (f *fakeNotificationPoster) PostNotification(
	_ context.Context, kind, body string, ticketID, imageURL *string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, devNote{kind: kind, body: body, ticketID: ticketID, imageURL: imageURL})
	return f.err
}

func (f *fakeNotificationPoster) posted() []devNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]devNote(nil), f.calls...)
}
