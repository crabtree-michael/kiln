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

var errFakeMintFailed = errors.New("fakeVoiceTokenMinter: synthetic mint failure")

func newTestServer(boards *fakeBoardReader, poster *fakeMessagePoster, messages *fakeMessagesReader) *httptest.Server {
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, poster, messages, &fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
	return httptest.NewServer(enableSession(srv).Handler())
}

// newBareServer builds a *api.Server over all-default fakes (no live board,
// runtime, or feed) with NOTHING enabled — the base for tests that then layer
// on exactly the Enable* they exercise (EnableIdentity, EnableDev*, EnablePush,
// EnableHealthz, EnableSPA). Callers that hit a guarded app route wrap it in
// enableSession (or use newTestServer); callers that assert a route is absent
// when its Enable* was never called rely on it staying bare.
func newBareServer() *api.Server {
	boards := &fakeBoardReader{}
	return api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
}

// doGet issues a context-ful, authenticated GET (carrying the kiln_session
// cookie every guarded route now requires) and fails the test on a transport
// error. Tests that exercise the unauthenticated 401 path build a cookieless
// request directly instead.
func doGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build GET %s: %v", url, err)
	}
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// doPost issues a context-ful, authenticated application/json POST (carrying
// the kiln_session cookie) and fails the test on a transport error.
func doPost(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// doGetNoAuth issues a cookieless GET, for asserting a guarded route rejects an
// unauthenticated caller with 401.
func doGetNoAuth(t *testing.T, url string) *http.Response {
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

// fakeAgentInspector is the api's live-worker status source for the board join.
type fakeAgentInspector struct {
	infos []api.AgentInfo
	err   error
}

func (f *fakeAgentInspector) ListAgents(context.Context, string) ([]api.AgentInfo, error) {
	return f.infos, f.err
}

// The board snapshot carries each live worker's real session status, keyed to
// its ticket, for the Streams view (amended 2026-07-05).
func TestHandleBoard_JoinsAgentStatuses(t *testing.T) {
	const ticketID = "t-9"
	boards := &fakeBoardReader{snapshot: board.Snapshot{
		Working:     []board.Ticket{{ID: ticketID, Title: "building one", State: board.StateWorking}},
		WorkerTotal: 2, WorkerFree: 1,
	}}
	hub := api.NewHub(boards)
	hub.SetAgentInspector(&fakeAgentInspector{infos: []api.AgentInfo{
		{WorkerID: "w-1", TicketID: ticketID, Status: "stopped"},
	}})
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	var got wire.Board
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Agents) != 1 {
		t.Fatalf("Agents = %+v, want one joined status", got.Agents)
	}
	if got.Agents[0].TicketId != ticketID || got.Agents[0].Status != wire.AgentStatusStatus("stopped") {
		t.Errorf("Agents[0] = %+v, want ticket %s status stopped", got.Agents[0], ticketID)
	}
}

// A failing status read must not blank the board — Streams just shows nothing
// new; the board still renders (amended 2026-07-05, best-effort join).
func TestHandleBoard_AgentInspectorError_BoardStillRenders(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 1}}
	hub := api.NewHub(boards)
	hub.SetAgentInspector(&fakeAgentInspector{err: errFakeBoardFailed})
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a status-read failure must not fail the board)", resp.StatusCode)
	}
	var got wire.Board
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Agents) != 0 {
		t.Errorf("Agents = %+v, want empty on inspector failure", got.Agents)
	}
}

// Errored workers raise a persistent sandbox-health alert on the board snapshot
// (the permanent error band), counting only errored — never stopped/starting —
// against health, and phrasing it "N of M sandboxes failing".
func TestHandleBoard_ErroredWorkers_RaiseSandboxHealthAlert(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 3}}
	hub := api.NewHub(boards)
	hub.SetAgentInspector(&fakeAgentInspector{infos: []api.AgentInfo{
		{WorkerID: "wa-1", Status: "errored"},
		{WorkerID: "wa-2", Status: "idle"},
		{WorkerID: "wa-3", Status: "stopped"}, // auto-stopped is healthy, not failing
	}})
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	var got wire.Board
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Alerts) != 1 {
		t.Fatalf("Alerts = %+v, want exactly one sandbox-health alert", got.Alerts)
	}
	if got.Alerts[0].Kind != "sandbox_health" {
		t.Errorf("Alerts[0].Kind = %q, want sandbox_health", got.Alerts[0].Kind)
	}
	if got.Alerts[0].Detail != "1 of 3 sandboxes failing" {
		t.Errorf("Alerts[0].Detail = %q, want %q", got.Alerts[0].Detail, "1 of 3 sandboxes failing")
	}
}

// A healthy pool (or a failed status read) raises no alert: the permanent band
// must stay clear, and a transient read failure must never flash a scary error.
func TestHandleBoard_HealthyOrUnreadable_NoAlert(t *testing.T) {
	for _, tc := range []struct {
		name      string
		inspector *fakeAgentInspector
	}{
		{"all healthy", &fakeAgentInspector{infos: []api.AgentInfo{
			{WorkerID: "wb-1", Status: "idle"},
			{WorkerID: "wb-2", Status: "building"},
		}}},
		{"inspector error", &fakeAgentInspector{err: errFakeBoardFailed}},
		{"no inspector", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 2}}
			hub := api.NewHub(boards)
			if tc.inspector != nil {
				hub.SetAgentInspector(tc.inspector)
			}
			srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
				&fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
			ts := httptest.NewServer(enableSession(srv).Handler())
			defer ts.Close()

			resp := doGet(t, ts.URL+"/api/board")
			defer closeBody(t, resp)
			var got wire.Board
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(got.Alerts) != 0 {
				t.Errorf("Alerts = %+v, want none", got.Alerts)
			}
			if got.Alerts == nil {
				t.Error("Alerts is nil, want a non-nil empty array (JSON [])")
			}
		})
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

func TestHandleMessage_OversizedBody_Returns413AndDoesNotPost(t *testing.T) {
	poster := &fakeMessagePoster{}
	ts := newTestServer(&fakeBoardReader{}, poster, &fakeMessagesReader{})
	defer ts.Close()

	// A raw JSON body well past the 64 KiB cap: the server must reject it
	// before buffering it whole, without ever reaching the port.
	body := append([]byte(`{"text":"`), bytes.Repeat([]byte("x"), 128<<10)...)
	body = append(body, []byte(`"}`)...)
	resp := doPost(t, ts.URL+"/api/message", body)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413 for a body over the size cap", resp.StatusCode)
	}
	if poster.callCount() != 0 {
		t.Errorf("PostMessage called %d times for an oversized body, want 0", poster.callCount())
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

// TestDevCreateTicket covers the dev-only seed endpoint (POST /api/dev/tickets):
// mounted only when enabled, and the {title, body, state, blocked_reason,
// approval_requested} body mapped straight onto board.SeedSpec.
func TestDevCreateTicket(t *testing.T) {
	newDevServer := func(seeder api.TicketSeeder) *httptest.Server {
		srv := newBareServer()
		srv.EnableDevTickets(seeder)
		return httptest.NewServer(enableSession(srv).Handler())
	}

	t.Run("not mounted unless enabled", func(t *testing.T) {
		ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, &fakeMessagesReader{})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets", []byte(`{"title":"x","body":"y"}`))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 when dev endpoints disabled", resp.StatusCode)
		}
	})

	t.Run("default seed is shaping", func(t *testing.T) {
		seeder := &fakeSeeder{}
		ts := newDevServer(seeder)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets", []byte(`{"title":"Do X","body":"emit done"}`))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		var out map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["id"] != devTicketID || out["state"] != string(board.StateShaping) {
			t.Errorf("response = %v, want id=%s state=shaping", out, devTicketID)
		}
		if seeder.spec.Title != "Do X" || seeder.spec.Body != "emit done" || seeder.spec.State != "" {
			t.Errorf("spec = %+v, want title/body passed through and empty (default) state", seeder.spec)
		}
	})

	t.Run("blocked seed passes state and reason through", func(t *testing.T) {
		seeder := &fakeSeeder{}
		ts := newDevServer(seeder)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets",
			[]byte(`{"title":"Auth","state":"blocked","blocked_reason":"need keys"}`))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		if seeder.spec.State != board.StateBlocked || seeder.spec.BlockedReason != "need keys" {
			t.Errorf("spec = %+v, want state=blocked blocked_reason=need keys", seeder.spec)
		}
	})

	t.Run("proposal seed passes approval_requested through", func(t *testing.T) {
		seeder := &fakeSeeder{}
		ts := newDevServer(seeder)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets",
			[]byte(`{"title":"Prop","state":"shaping","approval_requested":true}`))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		if seeder.spec.State != board.StateShaping || !seeder.spec.ApprovalRequested {
			t.Errorf("spec = %+v, want state=shaping approval_requested=true", seeder.spec)
		}
	})

	t.Run("no free worker is 409", func(t *testing.T) {
		ts := newDevServer(&fakeSeeder{seedErr: board.ErrNoFreeWorker})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets", []byte(`{"title":"x","state":"blocked"}`))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409 for ErrNoFreeWorker", resp.StatusCode)
		}
	})

	t.Run("seed failure is 500", func(t *testing.T) {
		ts := newDevServer(&fakeSeeder{seedErr: errFakeBoardFailed})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/tickets", []byte(`{"title":"x","body":"y"}`))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

// ---- GET /api/feed, POST /api/feed/seen, POST /api/tickets/{id}/accept -------

var errFakeResetFailed = errors.New("fakeResetter: synthetic reset failure")

// fakeResetter records reset calls (and the project id each was scoped to) and
// can fail on demand.
type fakeResetter struct {
	calls         int
	err           error
	lastProjectID string
}

func (f *fakeResetter) Reset(_ context.Context, projectID string) error {
	f.calls++
	f.lastProjectID = projectID
	return f.err
}

// TestHandleReset covers POST /api/dev/reset: mounted only when EnableReset was
// called, success maps to 204, and a Resetter error maps to 500.
func TestHandleReset(t *testing.T) {
	newResetServer := func(r api.Resetter) *httptest.Server {
		srv := newBareServer()
		srv.EnableReset(r)
		return httptest.NewServer(enableSession(srv).Handler())
	}

	t.Run("not mounted unless enabled", func(t *testing.T) {
		ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, &fakeMessagesReader{})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/reset", nil)
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 when reset not enabled", resp.StatusCode)
		}
	})

	t.Run("success returns 204 and calls Reset", func(t *testing.T) {
		r := &fakeResetter{}
		ts := newResetServer(r)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/reset", nil)
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		if r.calls != 1 {
			t.Errorf("Reset called %d times, want 1", r.calls)
		}
	})

	t.Run("reset error returns 500", func(t *testing.T) {
		r := &fakeResetter{err: errFakeResetFailed}
		ts := newResetServer(r)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/reset", nil)
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandleFeed_ReturnsMappedSnapshot(t *testing.T) {
	tid := "t-9"
	nid := int64(77)
	lastSeen := int64(70)
	feed := &fakeFeedReader{snapshot: runtime.FeedSnapshot{
		Summary: runtime.FeedSummary{
			BlockerCount: 1, UpdateCount: 2, StreamCount: 3, Building: 2, Idle: 1,
			LastSeenNotificationID: &lastSeen,
		},
		HasMoreHistory: true,
		Cards: []runtime.FeedCard{
			{Kind: "blocker", ID: "blocker:t-9", Label: "Auth", Body: "need keys", TicketID: &tid, CreatedAt: time.Now()},
			{Kind: "update", ID: "update:77", Label: "note", Body: "shipped", NotificationID: &nid, CreatedAt: time.Now()},
		},
	}}
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		feed, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/feed")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if feed.callCount() != 1 {
		t.Fatalf("Feed called %d times, want 1", feed.callCount())
	}
	var got wire.FeedSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Summary.BlockerCount != 1 || got.Summary.UpdateCount != 2 || got.Summary.StreamCount != 3 {
		t.Errorf("summary = %+v, want blocker=1 update=2 stream=3", got.Summary)
	}
	if len(got.Cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(got.Cards))
	}
	if got.Cards[0].Kind != wire.FeedCardKindBlocker || got.Cards[0].TicketId == nil || *got.Cards[0].TicketId != tid {
		t.Errorf("card0 = %+v, want blocker with ticket_id %s", got.Cards[0], tid)
	}
	card1 := got.Cards[1]
	if card1.Kind != wire.FeedCardKindUpdate || card1.NotificationId == nil || *card1.NotificationId != nid {
		t.Errorf("card1 = %+v, want update with notification_id %d", card1, nid)
	}
	if !got.HasMoreHistory {
		t.Errorf("HasMoreHistory = false, want true (carried through from the snapshot)")
	}
	if got.Summary.LastSeenNotificationId == nil || *got.Summary.LastSeenNotificationId != lastSeen {
		t.Errorf("LastSeenNotificationId = %v, want %d", got.Summary.LastSeenNotificationId, lastSeen)
	}
}

func TestHandleFeedHistory_PagesOlderUpdates(t *testing.T) {
	nid := int64(40)
	feed := &fakeFeedReader{
		history: []runtime.FeedCard{
			{Kind: "update", ID: "update:40", Body: "older", NotificationID: &nid, CreatedAt: time.Now()},
		},
		historyMore: true,
	}
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		feed, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/feed/history?before=50&limit=10")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if feed.historyBefore != 50 || feed.historyLimit != 10 {
		t.Errorf("FeedHistory called before=%d limit=%d, want 50/10", feed.historyBefore, feed.historyLimit)
	}
	var got wire.FeedHistoryPage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Cards) != 1 || got.Cards[0].NotificationId == nil || *got.Cards[0].NotificationId != nid {
		t.Fatalf("cards = %+v, want the single older update card", got.Cards)
	}
	if !got.HasMore {
		t.Errorf("HasMore = false, want true")
	}
}

func TestHandleFeedHistory_RejectsBadLimit(t *testing.T) {
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/feed/history?limit=999")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an out-of-bounds limit", resp.StatusCode)
	}
}

func TestHandleFeedSeen_CallsMarkSeen(t *testing.T) {
	seen := &fakeSeenAcker{}
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, seen, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	body := mustJSON(t, wire.FeedSeenRequest{LastNotificationId: 123})
	resp := doPost(t, ts.URL+"/api/feed/seen", body)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if ids := seen.seen(); len(ids) != 1 || ids[0] != 123 {
		t.Fatalf("MarkSeen called with %v, want a single call with 123", ids)
	}
}

func TestHandleFeedDismiss_CallsDismiss(t *testing.T) {
	seen := &fakeSeenAcker{}
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, seen, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/feed/77/dismiss", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if ids := seen.dismissed(); len(ids) != 1 || ids[0] != 77 {
		t.Fatalf("DismissNotification called with %v, want a single call with 77", ids)
	}
}

func TestHandleFeedDismissAll_CallsDismissAll(t *testing.T) {
	seen := &fakeSeenAcker{}
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, seen, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/feed/dismiss-all", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if n := seen.dismissedAll(); n != 1 {
		t.Fatalf("DismissAllNotifications called %d times, want 1", n)
	}
	// The literal /dismiss-all segment must not be captured by the {id}/dismiss
	// route as an id — no single-card dismiss should fire.
	if ids := seen.dismissed(); len(ids) != 0 {
		t.Fatalf("DismissNotification called with %v, want none", ids)
	}
}

func TestHandleFeedDismiss_RejectsBadID(t *testing.T) {
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/feed/not-a-number/dismiss", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-numeric notification id", resp.StatusCode)
	}
}

func TestHandleAccept_PostsSynthesizedMessageAndReturns202(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{
		Shaping: []board.Ticket{{ID: "t-42", Title: "Payment retries", State: board.StateShaping, ApprovalRequested: true}},
	}}
	poster := &fakeMessagePoster{messageID: 5, eventID: 9}
	srv := api.NewServer(
		boards, poster, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-42/accept", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if poster.callCount() != 1 {
		t.Fatalf("PostMessage called %d times, want 1", poster.callCount())
	}
	want := `The user tapped Accept on the proposal "Payment retries" (ticket t-42). ` +
		`Mark that ticket ready now; do not ask for confirmation.`
	if got := poster.lastText(); got != want {
		t.Errorf("posted text =\n  %q\nwant\n  %q", got, want)
	}
	var out wire.MessagePostResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.MessageId != 5 || out.EventId != 9 {
		t.Errorf("response = %+v, want message_id=5 event_id=9", out)
	}
}

func TestHandleAccept_UnknownTicketFallsBackToID(t *testing.T) {
	boards := &fakeBoardReader{} // empty snapshot: no ticket matches.
	poster := &fakeMessagePoster{}
	srv := api.NewServer(
		boards, poster, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-unknown/accept", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if got := poster.lastText(); !strings.Contains(got, `"t-unknown"`) || !strings.Contains(got, "ticket t-unknown") {
		t.Errorf("posted text = %q, want it to fall back to the id for both title and ticket", got)
	}
}

// ---- POST /api/voice/token (09 §2, §6) -------------------------------------

func TestVoiceToken_HappyPath(t *testing.T) {
	exp := time.Now().Add(8 * time.Minute).UTC().Truncate(time.Second)
	minter := &fakeVoiceTokenMinter{token: "tok-xyz", exp: exp}
	boards := &fakeBoardReader{}
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), minter)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/voice/token", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got wire.VoiceToken
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Token != "tok-xyz" {
		t.Errorf("token = %q, want tok-xyz", got.Token)
	}
	if !got.ExpiresAt.Equal(exp) {
		t.Errorf("expires_at = %v, want %v", got.ExpiresAt, exp)
	}
}

func TestVoiceToken_MintError_Returns502(t *testing.T) {
	minter := &fakeVoiceTokenMinter{err: errFakeMintFailed}
	boards := &fakeBoardReader{}
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), minter)
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/voice/token", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

// TestDevPostNotification covers the dev-only POST /api/dev/notifications seam.
func TestDevPostNotification(t *testing.T) {
	newDevServer := func(poster api.NotificationPoster) *httptest.Server {
		srv := newBareServer()
		srv.EnableDevNotifications(poster)
		return httptest.NewServer(enableSession(srv).Handler())
	}

	t.Run("not mounted unless enabled", func(t *testing.T) {
		ts := newTestServer(&fakeBoardReader{}, &fakeMessagePoster{}, &fakeMessagesReader{})
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/notifications", []byte(`{"kind":"update","body":"x"}`))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 when dev endpoints disabled", resp.StatusCode)
		}
	})

	t.Run("posts notification", func(t *testing.T) {
		poster := &fakeNotificationPoster{}
		ts := newDevServer(poster)
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/dev/notifications",
			[]byte(`{"kind":"preview","body":"rendered","ticket_id":"t-3","image_url":"http://img"}`))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		posted := poster.posted()
		if len(posted) != 1 {
			t.Fatalf("PostNotification called %d times, want 1", len(posted))
		}
		n := posted[0]
		if n.kind != "preview" || n.body != "rendered" {
			t.Errorf("posted = %+v, want kind=preview body=rendered", n)
		}
		if n.ticketID == nil || *n.ticketID != "t-3" || n.imageURL == nil || *n.imageURL != "http://img" {
			t.Errorf("posted ptrs = %+v, want ticket_id=t-3 image_url=http://img", n)
		}
	})
}
