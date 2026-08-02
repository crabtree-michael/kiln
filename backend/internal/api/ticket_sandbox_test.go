package api_test

// Route tests for POST /api/tickets/{id}/sandbox — the per-ticket sandbox
// option. Unlike accept/delete this one does NOT route through the brain, so
// these assert the direct board write: the decoded flag reaches the setter with
// the session's project and the path's ticket id, a missing ticket is 404, and
// the route is absent until it is wired.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// fakeTicketSandboxSetter is api.TicketSandboxSetter: it records the one call it
// receives, or fails with an injected error (board.ErrNotFound for the 404 path).
type fakeTicketSandboxSetter struct {
	mu        sync.Mutex
	calls     int
	projectID string
	id        board.TicketID
	keep      bool
	err       error
}

func (f *fakeTicketSandboxSetter) SetKeepSandbox(
	_ context.Context, projectID string, id board.TicketID, keep bool,
) (board.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.projectID, f.id, f.keep = projectID, id, keep
	if f.err != nil {
		return board.Ticket{}, f.err
	}
	return board.Ticket{ID: id, KeepSandbox: keep}, nil
}

func (f *fakeTicketSandboxSetter) last() (int, string, board.TicketID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.projectID, f.id, f.keep
}

// newTicketSandboxServer builds a session-enabled server with the setter wired.
func newTicketSandboxServer(setter api.TicketSandboxSetter) *httptest.Server {
	srv := newBareServer()
	srv.EnableTicketSandbox(setter)
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
