package api_test

// Route tests for the per-ticket sandbox surface: POST /api/tickets/{id}/sandbox
// (the save option) and its /kill and /reassign siblings (the manual overrides
// for a wedged workspace). None of the three routes through the brain, so these
// assert the direct board write: the call reaches the controller with the
// session's project and the path's ticket id, the board's typed refusals map to
// 404/409, and the routes are absent until they are wired.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// fakeTicketSandboxSetter is api.TicketSandboxController: it records the one call
// it receives — which method, for which project and ticket — or fails with an
// injected error (board.ErrNotFound for the 404 paths, a typed refusal for 409).
type fakeTicketSandboxSetter struct {
	mu        sync.Mutex
	calls     int
	op        string // "set" | "kill" | "reassign" — which method was invoked
	projectID string
	id        board.TicketID
	keep      bool
	err       error
}

func (f *fakeTicketSandboxSetter) SetKeepSandbox(
	_ context.Context, projectID string, id board.TicketID, keep bool,
) (board.Ticket, error) {
	if err := f.record("set", projectID, id); err != nil {
		return board.Ticket{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keep = keep
	return board.Ticket{ID: id, KeepSandbox: keep}, nil
}

func (f *fakeTicketSandboxSetter) KillSandbox(
	_ context.Context, projectID string, id board.TicketID,
) (board.Ticket, error) {
	if err := f.record("kill", projectID, id); err != nil {
		return board.Ticket{}, err
	}
	return board.Ticket{ID: id}, nil
}

func (f *fakeTicketSandboxSetter) ReassignSandbox(
	_ context.Context, projectID string, id board.TicketID,
) (board.Ticket, error) {
	if err := f.record("reassign", projectID, id); err != nil {
		return board.Ticket{}, err
	}
	return board.Ticket{ID: id}, nil
}

// record is the shared body of all three methods: count the call, remember what
// it carried, and return the injected error if there is one.
func (f *fakeTicketSandboxSetter) record(op, projectID string, id board.TicketID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.op, f.projectID, f.id = op, projectID, id
	return f.err
}

func (f *fakeTicketSandboxSetter) last() (int, string, board.TicketID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.projectID, f.id, f.keep
}

// op reports the method the server last invoked, so a test can prove the route
// reached the right one rather than merely reaching the controller.
func (f *fakeTicketSandboxSetter) lastOp() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.op
}

// newTicketSandboxServer builds a session-enabled server with the controller wired.
func newTicketSandboxServer(ctrl api.TicketSandboxController) *httptest.Server {
	srv := newBareServer()
	srv.EnableTicketSandbox(ctrl)
	return httptest.NewServer(enableSession(srv).Handler())
}

func TestHandleTicketSandbox_SavesTheTicketsSandbox(t *testing.T) {
	setter := &fakeTicketSandboxSetter{}
	ts := newTicketSandboxServer(setter)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox", []byte(`{"keep":true}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	calls, projectID, id, keep := setter.last()
	if calls != 1 {
		t.Fatalf("SetKeepSandbox called %d times, want 1", calls)
	}
	if projectID != testProjectID {
		t.Errorf("projectID = %q, want the session's project %q", projectID, testProjectID)
	}
	if id != board.TicketID(testTicketID) {
		t.Errorf("ticket id = %q, want %q", id, testTicketID)
	}
	if !keep {
		t.Error("keep = false, want the decoded true")
	}
}

// keep=false is a real value, not an omission: it must reach the setter so the
// user can turn the option back off.
func TestHandleTicketSandbox_FalseReachesTheSetter(t *testing.T) {
	setter := &fakeTicketSandboxSetter{}
	ts := newTicketSandboxServer(setter)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox", []byte(`{"keep":false}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if calls, _, _, keep := setter.last(); calls != 1 || keep {
		t.Errorf("SetKeepSandbox calls=%d keep=%v, want 1 call with keep=false", calls, keep)
	}
}

// An unknown ticket — or one owned by another project, which the board reports
// identically — is 404, not a 500.
func TestHandleTicketSandbox_UnknownTicketIs404(t *testing.T) {
	ts := newTicketSandboxServer(&fakeTicketSandboxSetter{err: board.ErrNotFound})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-unknown/sandbox", []byte(`{"keep":true}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleTicketSandbox_MalformedBodyIs400(t *testing.T) {
	setter := &fakeTicketSandboxSetter{}
	ts := newTicketSandboxServer(setter)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox", []byte(`not json`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if calls, _, _, _ := setter.last(); calls != 0 {
		t.Errorf("SetKeepSandbox called %d times on a malformed body, want 0", calls)
	}
}

// Unwired (no EnableTicketSandbox) the route simply isn't mounted, so a
// deployment that never enabled it can't reach a nil setter.
func TestHandleTicketSandbox_UnmountedWithoutSetter(t *testing.T) {
	ts := httptest.NewServer(enableSession(newBareServer()).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox", []byte(`{"keep":true}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no setter is wired", resp.StatusCode)
	}
}

// ---- the manual overrides: /sandbox/kill and /sandbox/reassign -------------

// Kill is a bodiless POST — the ticket in the path is the whole request — and it
// must reach KillSandbox specifically, not the save-option setter it shares a
// prefix and a port with.
func TestHandleKillSandbox_KillsTheTicketsSandbox(t *testing.T) {
	ctrl := &fakeTicketSandboxSetter{}
	ts := newTicketSandboxServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox/kill", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	calls, projectID, id, _ := ctrl.last()
	if calls != 1 || ctrl.lastOp() != "kill" {
		t.Fatalf("controller calls=%d op=%q, want 1 call to KillSandbox", calls, ctrl.lastOp())
	}
	if projectID != testProjectID {
		t.Errorf("projectID = %q, want the session's project %q", projectID, testProjectID)
	}
	if id != board.TicketID(testTicketID) {
		t.Errorf("ticket id = %q, want %q", id, testTicketID)
	}
}

// A ticket with no worker bound has no sandbox to kill. The board says so with a
// typed refusal, which is a 409 the client can explain — not a 500.
func TestHandleKillSandbox_NoSandboxIs409(t *testing.T) {
	ctrl := &fakeTicketSandboxSetter{err: &board.ErrInvalidTransition{
		From: board.StateShaping, Attempted: "KillSandbox",
	}}
	ts := newTicketSandboxServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox/kill", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleKillSandbox_UnknownTicketIs404(t *testing.T) {
	ts := newTicketSandboxServer(&fakeTicketSandboxSetter{err: board.ErrNotFound})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-unknown/sandbox/kill", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleReassignSandbox_MovesTheTicket(t *testing.T) {
	ctrl := &fakeTicketSandboxSetter{}
	ts := newTicketSandboxServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox/reassign", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	calls, projectID, id, _ := ctrl.last()
	if calls != 1 || ctrl.lastOp() != "reassign" {
		t.Fatalf("controller calls=%d op=%q, want 1 call to ReassignSandbox", calls, ctrl.lastOp())
	}
	if projectID != testProjectID || id != board.TicketID(testTicketID) {
		t.Errorf("call = (%q, %q), want the session's project and the path's ticket", projectID, id)
	}
}

// Every slot busy is the one refusal unique to reassign, and it is the user's to
// act on (wait, or kill in place) — so it is a 409 like the other precondition,
// never a 500.
func TestHandleReassignSandbox_NoFreeWorkerIs409(t *testing.T) {
	ts := newTicketSandboxServer(&fakeTicketSandboxSetter{err: board.ErrNoFreeWorker})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox/reassign", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleReassignSandbox_NoSandboxIs409(t *testing.T) {
	ctrl := &fakeTicketSandboxSetter{err: &board.ErrInvalidTransition{
		From: board.StateDone, Attempted: "ReassignSandbox",
	}}
	ts := newTicketSandboxServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/sandbox/reassign", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestHandleReassignSandbox_UnknownTicketIs404(t *testing.T) {
	ts := newTicketSandboxServer(&fakeTicketSandboxSetter{err: board.ErrNotFound})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-unknown/sandbox/reassign", nil)
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// The overrides ride the same wiring switch as the save option: one surface, one
// Enable call, so a deployment can't end up with a kill route over a nil port.
func TestHandleSandboxOverrides_UnmountedWithoutController(t *testing.T) {
	ts := httptest.NewServer(enableSession(newBareServer()).Handler())
	defer ts.Close()

	for _, path := range []string{"/sandbox/kill", "/sandbox/reassign"} {
		resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s status = %d, want 404 when no controller is wired", path, resp.StatusCode)
		}
		closeBody(t, resp)
	}
}
