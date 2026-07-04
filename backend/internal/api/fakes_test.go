package api_test

// Shared unit-test fakes for the api module (04 §9: "unit (api): routes
// against a fake runtime/board — decode/encode, snapshot-on-connect, fan-out
// to multiple SSE clients, keepalive"). Everything here is in-memory; the
// only real network is the loopback httptest.Server the route tests dial.

import (
	"context"
	"sync"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

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
