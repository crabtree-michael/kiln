package api_test

// Route handler unit tests (04 §7, 07 §4): thin decode/delegate/encode
// against fake ports, driven over real net/http via httptest — no runtime,
// no board, no Postgres.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

var errFakeBoardFailed = errors.New("fakeBoardReader: synthetic failure")

func newTestServer(boards *fakeBoardReader, poster *fakeMessagePoster, messages *fakeMessagesReader) *httptest.Server {
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, poster, messages, hub)
	return httptest.NewServer(srv.Handler())
}

// doGet issues a context-ful GET and fails the test on a transport error.
func doGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// doPost issues a context-ful application/json POST and fails the test on a
// transport error.
func doPost(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// closeBody closes a response body, reporting a close error.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
}

// mustJSON marshals v, failing the test on error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// ---- GET /api/board (04 §7) ------------------------------------------------

func TestHandleBoard_ReturnsSnapshotAsWireBoard(t *testing.T) {
	reason := "waiting on API keys"
	boards := &fakeBoardReader{snapshot: board.Snapshot{
		Shaping: []board.Ticket{{ID: "t-1", Title: "shape me", Body: "b", State: board.StateShaping, Priority: 1}},
		Ready:   []board.Ticket{{ID: "t-2", Title: "ready one", Body: "b2", State: board.StateReady, Priority: 5}},
		Blocked: []board.Ticket{{
			ID: "t-3", Title: "blocked one", Body: "b3", State: board.StateBlocked, BlockedReason: &reason,
		}},
		Working:     nil,
		Done:        nil,
		WorkerTotal: 3,
		WorkerFree:  1,
	}}
	ts := newTestServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{})
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got wire.Board
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Shaping) != 1 || got.Shaping[0].Id != "t-1" {
		t.Errorf("Shaping = %+v, want one ticket t-1", got.Shaping)
	}
	if len(got.Ready) != 1 || got.Ready[0].Id != "t-2" {
		t.Errorf("Ready = %+v, want one ticket t-2", got.Ready)
	}
	if len(got.Blocked) != 1 || got.Blocked[0].BlockedReason == nil || *got.Blocked[0].BlockedReason != reason {
		t.Errorf("Blocked = %+v, want one ticket t-3 with blocked_reason %q", got.Blocked, reason)
	}
	if got.WorkerTotal != 3 || got.WorkerFree != 1 {
		t.Errorf("worker_total/free = %d/%d, want 3/1", got.WorkerTotal, got.WorkerFree)
	}
}

func TestHandleBoard_StoreError_Returns5xx(t *testing.T) {
	boards := &fakeBoardReader{err: errFakeBoardFailed}
	ts := newTestServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{})
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	if boards.callCount() != 1 {
		t.Fatalf("BoardReader.GetBoard called %d times, want exactly 1 (the handler must actually call through, "+
			"not short-circuit before reaching the port)", boards.callCount())
	}
	if resp.StatusCode < 500 {
		t.Errorf("status = %d, want a 5xx when BoardReader fails", resp.StatusCode)
	}
}

// ---- POST /api/message (07 §3-§4) ------------------------------------------

func TestHandleMessage_PostsAndReturns202(t *testing.T) {
	poster := &fakeMessagePoster{messageID: 42, eventID: 7}
	ts := newTestServer(&fakeBoardReader{}, poster, &fakeMessagesReader{})
	defer ts.Close()

	body := mustJSON(t, wire.MessageRequest{Text: "build the widget"})
	resp := doPost(t, ts.URL+"/api/message", body)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if poster.callCount() != 1 || poster.lastText() != "build the widget" {
		t.Fatalf("MessagePoster.PostMessage calls = %d, lastText = %q, want 1 call with %q",
			poster.callCount(), poster.lastText(), "build the widget")
	}

	var got wire.MessagePostResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.MessageId != 42 || got.EventId != 7 {
		t.Errorf("response = %+v, want message_id=42 event_id=7", got)
	}
}

func TestHandleMessage_EmptyText_Returns400AndDoesNotPost(t *testing.T) {
	poster := &fakeMessagePoster{}
	ts := newTestServer(&fakeBoardReader{}, poster, &fakeMessagesReader{})
	defer ts.Close()

	body := mustJSON(t, wire.MessageRequest{Text: ""})
	resp := doPost(t, ts.URL+"/api/message", body)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty text (schema/openapi.yaml MessageRequest minLength 1)", resp.StatusCode)
	}
	if poster.callCount() != 0 {
		t.Errorf("PostMessage called %d times for invalid input, want 0", poster.callCount())
	}
}

func TestHandleMessage_TooLongText_Returns400(t *testing.T) {
	poster := &fakeMessagePoster{}
	ts := newTestServer(&fakeBoardReader{}, poster, &fakeMessagesReader{})
	defer ts.Close()

	body := mustJSON(t, wire.MessageRequest{Text: strings.Repeat("x", 4001)})
	resp := doPost(t, ts.URL+"/api/message", body)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for text over maxLength 4000 (schema/openapi.yaml)", resp.StatusCode)
	}
	if poster.callCount() != 0 {
		t.Errorf("PostMessage called %d times for invalid input, want 0", poster.callCount())
	}
}

func TestHandleMessage_MalformedJSON_Returns400(t *testing.T) {
	poster := &fakeMessagePoster{}
	ts := newTestServer(&fakeBoardReader{}, poster, &fakeMessagesReader{})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/message", []byte(`{not json`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON body", resp.StatusCode)
	}
	if poster.callCount() != 0 {
		t.Errorf("PostMessage called %d times for malformed input, want 0", poster.callCount())
	}
}

// ---- GET /api/messages (07 §4) ---------------------------------------------

func TestHandleMessages_DefaultLimitIs50(t *testing.T) {
	messages := &fakeMessagesReader{messages: []runtime.Message{
		{ID: 1, Role: runtime.RoleUser, Text: "hi", CreatedAt: time.Now()},
		{ID: 2, Role: runtime.RoleKiln, Text: "hello", CreatedAt: time.Now()},
	}}
	ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, messages)
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/messages")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ns := messages.requestedNs()
	if len(ns) != 1 || ns[0] != 50 {
		t.Fatalf("Recent requested with n=%v, want a single call with 50 (schema default)", ns)
	}

	var got []wire.Message
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 || got[0].Text != "hi" || got[1].Text != "hello" {
		t.Fatalf("got %+v, want oldest-first [hi, hello]", got)
	}
	if got[0].Role != wire.User || got[1].Role != wire.Kiln {
		t.Errorf("roles = [%q, %q], want [user, kiln]", got[0].Role, got[1].Role)
	}
}

func TestHandleMessages_CustomLimit(t *testing.T) {
	messages := &fakeMessagesReader{}
	ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, messages)
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/messages?limit=10")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ns := messages.requestedNs()
	if len(ns) != 1 || ns[0] != 10 {
		t.Fatalf("Recent requested with n=%v, want a single call with 10", ns)
	}
}

func TestHandleMessages_LimitOutOfRange_Returns400(t *testing.T) {
	for _, limit := range []string{"0", "501", "-1"} {
		messages := &fakeMessagesReader{}
		ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, messages)

		resp := doGet(t, ts.URL+"/api/messages?limit="+limit)
		closeBody(t, resp)
		ts.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%s: status = %d, want 400 (schema/openapi.yaml bounds 1-500)", limit, resp.StatusCode)
		}
	}
}
