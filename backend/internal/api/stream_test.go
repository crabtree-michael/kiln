package api_test

// SSE stream + hub fan-out tests (04 §7-§8, 07 §4-§5): snapshot-on-connect,
// fan-out of board/say events to every connected client, and reconnect
// getting a fresh snapshot rather than a replay. Driven over a real
// loopback httptest.Server, since SSE framing/flushing is genuinely an HTTP
// behavior worth exercising end to end.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// sseNameBoard is the SSE event name the board snapshot rides on (04 §7).
const sseNameBoard = "board"

// sseEvent is one parsed `event: name\ndata: payload\n\n` frame.
type sseEvent struct {
	name string
	data string
}

// sseClient wraps a connected /api/stream response so tests can read named
// events one at a time without hand-rolling SSE parsing per test.
type sseClient struct {
	t      *testing.T
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc
}

func connectStream(t *testing.T, baseURL string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/stream", nil)
	if err != nil {
		cancel()
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("GET /api/stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("close body: %v", cerr)
		}
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("close body: %v", cerr)
		}
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	return &sseClient{t: t, resp: resp, reader: bufio.NewReader(resp.Body), cancel: cancel}
}

func (c *sseClient) close() {
	c.cancel()
	if err := c.resp.Body.Close(); err != nil {
		c.t.Logf("close stream body: %v", err)
	}
}

// nextEvent reads lines until a complete "event: ...\ndata: ...\n\n" frame is
// assembled, skipping blank comment-only keepalive lines (": ping\n\n").
func (c *sseClient) nextEvent(timeout time.Duration) (sseEvent, bool) {
	c.t.Helper()
	type result struct {
		ev sseEvent
		ok bool
	}
	out := make(chan result, 1)
	go func() {
		var ev sseEvent
		for {
			line, err := c.reader.ReadString('\n')
			if err != nil {
				out <- result{}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "event:"):
				ev.name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				ev.data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			case line == "":
				if ev.name != "" {
					out <- result{ev: ev, ok: true}
					return
				}
				// blank line with no event name yet (e.g. a lone keepalive
				// comment already consumed) — keep reading.
			}
		}
	}()
	select {
	case r := <-out:
		return r.ev, r.ok
	case <-time.After(timeout):
		return sseEvent{}, false
	}
}

const streamReadTimeout = 2 * time.Second

// ---- snapshot on connect (04 §7) -------------------------------------------

func TestHandleStream_SendsBoardSnapshotImmediatelyOnConnect(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 4, WorkerFree: 2}}
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{}, &fakeFeedReader{}, &fakeSeenAcker{}, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := connectStream(t, ts.URL)
	defer client.close()

	ev, ok := client.nextEvent(streamReadTimeout)
	if !ok {
		t.Fatal("no event received on connect; want an immediate `board` event (04 §7)")
	}
	if ev.name != sseNameBoard {
		t.Fatalf("first event name = %q, want board", ev.name)
	}
	var got wire.Board
	if err := json.Unmarshal([]byte(ev.data), &got); err != nil {
		t.Fatalf("unmarshal board event data %q: %v", ev.data, err)
	}
	if got.WorkerTotal != 4 || got.WorkerFree != 2 {
		t.Errorf("board event = %+v, want worker_total=4 worker_free=2", got)
	}
}

// ---- fan-out to multiple clients (04 §7-§8) --------------------------------

func TestHub_PushBoard_FansOutToEveryConnectedClient(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 1, WorkerFree: 1}}
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{}, &fakeFeedReader{}, &fakeSeenAcker{}, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	a := connectStream(t, ts.URL)
	defer a.close()
	b := connectStream(t, ts.URL)
	defer b.close()

	// Drain each client's initial snapshot event first.
	if _, ok := a.nextEvent(streamReadTimeout); !ok {
		t.Fatal("client a: no initial board event")
	}
	if _, ok := b.nextEvent(streamReadTimeout); !ok {
		t.Fatal("client b: no initial board event")
	}

	boards.setSnapshot(board.Snapshot{WorkerTotal: 9, WorkerFree: 0})
	if err := hub.PushBoard(context.Background()); err != nil {
		t.Fatalf("Hub.PushBoard: %v", err)
	}

	for name, c := range map[string]*sseClient{"a": a, "b": b} {
		ev, ok := c.nextEvent(streamReadTimeout)
		if !ok {
			t.Fatalf("client %s: did not receive the pushed board event", name)
		}
		if ev.name != sseNameBoard {
			t.Fatalf("client %s: event name = %q, want board", name, ev.name)
		}
		var got wire.Board
		if err := json.Unmarshal([]byte(ev.data), &got); err != nil {
			t.Fatalf("client %s: unmarshal %q: %v", name, ev.data, err)
		}
		if got.WorkerTotal != 9 {
			t.Errorf("client %s: worker_total = %d, want 9 (the pushed snapshot, not the stale one)", name, got.WorkerTotal)
		}
	}
}

// TestHub_PushSay_DeliversSayEventToConnectedClients (07 §3-§4): the Say
// port's second half rides the same stream, distinguished by event name.
func TestHub_PushSay_DeliversSayEventToConnectedClients(t *testing.T) {
	boards := &fakeBoardReader{}
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{}, &fakeFeedReader{}, &fakeSeenAcker{}, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := connectStream(t, ts.URL)
	defer client.close()
	if _, ok := client.nextEvent(streamReadTimeout); !ok {
		t.Fatal("no initial board event")
	}

	at := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	msg := runtime.Message{ID: 55, Role: runtime.RoleKiln, Text: "shaping done", CreatedAt: at}
	if err := hub.PushSay(context.Background(), msg); err != nil {
		t.Fatalf("Hub.PushSay: %v", err)
	}

	ev, ok := client.nextEvent(streamReadTimeout)
	if !ok {
		t.Fatal("client did not receive the pushed say event")
	}
	if ev.name != "say" {
		t.Fatalf("event name = %q, want say", ev.name)
	}
	var got wire.SayEvent
	if err := json.Unmarshal([]byte(ev.data), &got); err != nil {
		t.Fatalf("unmarshal say event data %q: %v", ev.data, err)
	}
	if got.MessageId != 55 || got.Text != "shaping done" {
		t.Errorf("say event = %+v, want message_id=55 text=%q", got, "shaping done")
	}
}

// TestHandleStream_Reconnect_GetsFreshSnapshotNotReplay (07 §5, §8; 04 D6/D7):
// a fresh connection always gets the current snapshot as its first event —
// never a backlog of missed board events, and Last-Event-ID is unused.
func TestHandleStream_Reconnect_GetsFreshSnapshotNotReplay(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 1}}
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{}, &fakeFeedReader{}, &fakeSeenAcker{}, hub)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	first := connectStream(t, ts.URL)
	if _, ok := first.nextEvent(streamReadTimeout); !ok {
		t.Fatal("no initial board event on first connect")
	}
	first.close()

	// Push several board updates while nobody is connected to receive them.
	for i := range 3 {
		boards.setSnapshot(board.Snapshot{WorkerTotal: i + 10})
		if err := hub.PushBoard(context.Background()); err != nil {
			t.Fatalf("PushBoard: %v", err)
		}
	}
	boards.setSnapshot(board.Snapshot{WorkerTotal: 42})

	reconnect := connectStream(t, ts.URL)
	defer reconnect.close()
	ev, ok := reconnect.nextEvent(streamReadTimeout)
	if !ok {
		t.Fatal("no event on reconnect")
	}
	if ev.name != sseNameBoard {
		t.Fatalf("reconnect's first event = %q, want board", ev.name)
	}
	var got wire.Board
	if err := json.Unmarshal([]byte(ev.data), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WorkerTotal != 42 {
		t.Errorf("reconnect snapshot worker_total = %d, want 42 (the current state, not a replayed backlog)", got.WorkerTotal)
	}

	// And nothing else should follow immediately — a resync is exactly one
	// event, not the missed backlog.
	if ev2, ok := reconnect.nextEvent(300 * time.Millisecond); ok {
		t.Errorf("received an unexpected extra event after the resync snapshot: %+v", ev2)
	}
}
