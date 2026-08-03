package api_test

// Route tests for POST /api/tickets/{id}/text — the ticket detail sheet's
// direct text edit. Like the sandbox option (and unlike accept/delete) it does
// NOT route through the brain, so these assert the direct board write: the
// decoded patch reaches ShapeTicket verbatim with the session's project and the
// path's ticket id, an omitted field stays nil (so the board leaves it alone),
// the two pre-write rejections are 400, a missing ticket is 404, a ticket past
// the backlog is 409 rather than a 500, and the route is absent until wired.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// fakeTicketTextEditor is api.TicketTextEditor: it records the one call it
// receives, or fails with an injected error (board.ErrNotFound for the 404 path,
// *board.ErrInvalidTransition for the 409 one).
type fakeTicketTextEditor struct {
	mu        sync.Mutex
	calls     int
	projectID string
	id        board.TicketID
	patch     board.ShapePatch
	err       error
}

func (f *fakeTicketTextEditor) ShapeTicket(
	_ context.Context, projectID string, id board.TicketID, patch board.ShapePatch,
) (board.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.projectID, f.id, f.patch = projectID, id, patch
	if f.err != nil {
		return board.Ticket{}, f.err
	}
	return board.Ticket{ID: id}, nil
}

func (f *fakeTicketTextEditor) last() (int, string, board.TicketID, board.ShapePatch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.projectID, f.id, f.patch
}

// lastPatch is last() for the tests that only care about what was written.
func (f *fakeTicketTextEditor) lastPatch() board.ShapePatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.patch
}

// newTicketTextServer builds a session-enabled server with the editor wired.
func newTicketTextServer(editor api.TicketTextEditor) *httptest.Server {
	srv := newBareServer()
	srv.EnableTicketText(editor)
	return httptest.NewServer(enableSession(srv).Handler())
}

func TestHandleTicketText_EditsTitleAndBody(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text",
		[]byte(`{"title":"Fix the login redirect","body":"Land on /app, not /."}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	calls, projectID, id, patch := editor.last()
	if calls != 1 {
		t.Fatalf("ShapeTicket called %d times, want 1", calls)
	}
	if projectID != testProjectID {
		t.Errorf("projectID = %q, want the session's project %q", projectID, testProjectID)
	}
	if id != board.TicketID(testTicketID) {
		t.Errorf("ticket id = %q, want %q", id, testTicketID)
	}
	if patch.Title == nil || *patch.Title != "Fix the login redirect" {
		t.Errorf("patch.Title = %v, want the decoded title", patch.Title)
	}
	if patch.Body == nil || *patch.Body != "Land on /app, not /." {
		t.Errorf("patch.Body = %v, want the decoded body", patch.Body)
	}
	// The edit touches text only — it must never smuggle a reprioritization in.
	if patch.Priority != nil {
		t.Errorf("patch.Priority = %v, want nil (this route edits text only)", patch.Priority)
	}
}

// The whole point of the pointer-shaped patch: the sheet may send only what
// changed, and an omitted field must arrive nil so the board leaves it alone
// rather than blanking it.
func TestHandleTicketText_OmittedFieldStaysNil(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{"title":"Just the title"}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	patch := editor.lastPatch()
	if patch.Title == nil || *patch.Title != "Just the title" {
		t.Errorf("patch.Title = %v, want the decoded title", patch.Title)
	}
	if patch.Body != nil {
		t.Errorf("patch.Body = %q, want nil for an omitted field", *patch.Body)
	}
}

// Clearing the description is a legal edit, so an explicitly-empty body must
// reach the board as a present-but-empty value, not be mistaken for an omission.
func TestHandleTicketText_EmptyBodyIsAnEdit(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{"body":""}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	patch := editor.lastPatch()
	if patch.Body == nil {
		t.Fatal("patch.Body = nil, want a present empty string (clearing the body is an edit)")
	}
	if *patch.Body != "" {
		t.Errorf("patch.Body = %q, want the empty string", *patch.Body)
	}
}

// A title, unlike a body, is the ticket's whole identity on the board and in the
// feed — a blank one is rejected before any write.
func TestHandleTicketText_BlankTitleIs400(t *testing.T) {
	for _, title := range []string{`""`, `"   "`, `"\n\t"`} {
		editor := &fakeTicketTextEditor{}
		ts := newTicketTextServer(editor)

		resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{"title":`+title+`}`))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("title %s: status = %d, want 400", title, resp.StatusCode)
		}
		if calls, _, _, _ := editor.last(); calls != 0 {
			t.Errorf("title %s: ShapeTicket called %d times, want 0 (rejected before any write)", title, calls)
		}
		closeBody(t, resp)
		ts.Close()
	}
}

// A request naming neither field has nothing to do; 400 rather than an empty
// write that would still emit board.updated to every open client.
func TestHandleTicketText_NoFieldsIs400(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if calls, _, _, _ := editor.last(); calls != 0 {
		t.Errorf("ShapeTicket called %d times on an empty patch, want 0", calls)
	}
}

func TestHandleTicketText_MalformedBodyIs400(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`not json`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if calls, _, _, _ := editor.last(); calls != 0 {
		t.Errorf("ShapeTicket called %d times on a malformed body, want 0", calls)
	}
}

// An unknown ticket — or one owned by another project, which the board reports
// identically — is 404, not a 500.
func TestHandleTicketText_UnknownTicketIs404(t *testing.T) {
	ts := newTicketTextServer(&fakeTicketTextEditor{err: board.ErrNotFound})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/t-unknown/text", []byte(`{"title":"Renamed"}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Past the backlog the board refuses the write (the text is what the agent was
// briefed with). That is a precondition failure, not a server fault, so it is
// 409 — the client can then say *why* the edit didn't take.
func TestHandleTicketText_PastTheBacklogIs409(t *testing.T) {
	ts := newTicketTextServer(&fakeTicketTextEditor{
		err: &board.ErrInvalidTransition{From: board.StateWorking, Attempted: "ShapeTicket"},
	})
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{"title":"Too late"}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// The body cap runs before the decode, so an oversized request is refused
// without buffering it — 413, and no write.
func TestHandleTicketText_OversizedBodyIs413(t *testing.T) {
	editor := &fakeTicketTextEditor{}
	ts := newTicketTextServer(editor)
	defer ts.Close()

	huge := `{"body":"` + strings.Repeat("x", 300<<10) + `"}`
	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(huge))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if calls, _, _, _ := editor.last(); calls != 0 {
		t.Errorf("ShapeTicket called %d times on an oversized body, want 0", calls)
	}
}

// Unwired (no EnableTicketText) the route simply isn't mounted, so a deployment
// that never enabled it can't reach a nil editor.
func TestHandleTicketText_UnmountedWithoutEditor(t *testing.T) {
	ts := httptest.NewServer(enableSession(newBareServer()).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/text", []byte(`{"title":"Renamed"}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no editor is wired", resp.StatusCode)
	}
}
