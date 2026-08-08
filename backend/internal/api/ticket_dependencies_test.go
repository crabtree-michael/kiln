package api_test

// Route tests for POST/DELETE /api/tickets/{id}/dependencies — the ticket
// detail sheet's direct edit of what a ticket waits for (0013). Like the
// sandbox option and the text edit, and unlike accept/delete, it does NOT route
// through the brain, so these assert the direct board write: the ids reach the
// controller verbatim with the session's project, an unsatisfiable cycle is 409
// rather than a 500, a missing ticket is 404, a malformed body is 400, and the
// routes are absent until wired.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// fakeDependencyController is api.TicketDependencyController: it records the
// call it receives, or fails with an injected error.
type fakeDependencyController struct {
	mu        sync.Mutex
	adds      int
	removes   int
	projectID string
	id        board.TicketID
	dependsOn board.TicketID
	err       error
}

func (f *fakeDependencyController) AddDependency(
	_ context.Context, projectID string, id, dependsOn board.TicketID,
) (board.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	f.projectID, f.id, f.dependsOn = projectID, id, dependsOn
	if f.err != nil {
		return board.Ticket{}, f.err
	}
	return board.Ticket{ID: id, DependsOn: []board.TicketID{dependsOn}}, nil
}

func (f *fakeDependencyController) RemoveDependency(
	_ context.Context, projectID string, id, dependsOn board.TicketID,
) (board.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes++
	f.projectID, f.id, f.dependsOn = projectID, id, dependsOn
	if f.err != nil {
		return board.Ticket{}, f.err
	}
	return board.Ticket{ID: id}, nil
}

// depCall is one recorded call to the controller, so the assertions read as
// fields rather than a five-value tuple.
type depCall struct {
	adds      int
	removes   int
	projectID string
	id        board.TicketID
	dependsOn board.TicketID
}

func (f *fakeDependencyController) last() depCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return depCall{f.adds, f.removes, f.projectID, f.id, f.dependsOn}
}

func newDependencyServer(ctrl api.TicketDependencyController) *httptest.Server {
	srv := newBareServer()
	srv.EnableTicketDependencies(ctrl)
	return httptest.NewServer(enableSession(srv).Handler())
}

const (
	otherTicketID = "bbbbbbbb-2222-4bbb-8bbb-bbbbbbbbbbbb"
	depBlockerID  = "blocker"
)

func TestHandleAddDependency_WritesTheEdge(t *testing.T) {
	ctrl := &fakeDependencyController{}
	ts := newDependencyServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies",
		[]byte(`{"depends_on":"`+otherTicketID+`"}`))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	got := ctrl.last()
	if got.adds != 1 {
		t.Fatalf("AddDependency calls = %d, want 1", got.adds)
	}
	if got.projectID != testProjectID {
		t.Errorf("projectID = %q, want the session's project %q", got.projectID, testProjectID)
	}
	if string(got.id) != testTicketID {
		t.Errorf("ticket id = %q, want the path's %q", got.id, testTicketID)
	}
	if string(got.dependsOn) != otherTicketID {
		t.Errorf("dependsOn = %q, want the body's %q", got.dependsOn, otherTicketID)
	}
}

// A cycle is a well-formed request the board refuses on the resulting graph, so
// it is 409 — and the message names both ends so the sheet can say what it
// collided with.
func TestHandleAddDependency_CycleIsConflict(t *testing.T) {
	ctrl := &fakeDependencyController{err: &board.ErrCircularDependency{
		Ticket: board.TicketID(testTicketID), DependsOn: board.TicketID(otherTicketID),
	}}
	ts := newDependencyServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies",
		[]byte(`{"depends_on":"`+otherTicketID+`"}`))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a circular dependency", resp.StatusCode)
	}
	raw, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		t.Fatalf("read body: %v", rerr)
	}
	body := string(raw)
	if !strings.Contains(body, testTicketID) || !strings.Contains(body, otherTicketID) {
		t.Errorf("body = %q, want both ticket ids named so the client can explain the refusal", body)
	}
}

func TestHandleAddDependency_MissingTicketIsNotFound(t *testing.T) {
	ctrl := &fakeDependencyController{err: board.ErrNotFound}
	ts := newDependencyServer(ctrl)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies",
		[]byte(`{"depends_on":"`+otherTicketID+`"}`))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleAddDependency_RejectsEmptyAndMalformedBodies(t *testing.T) {
	for name, body := range map[string]string{
		"empty depends_on": `{"depends_on":""}`,
		"blank depends_on": `{"depends_on":"   "}`,
		"not json":         `{`,
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := &fakeDependencyController{}
			ts := newDependencyServer(ctrl)
			defer ts.Close()

			resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies", []byte(body))
			defer closeBody(t, resp)

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			if adds := ctrl.last().adds; adds != 0 {
				t.Errorf("AddDependency calls = %d, want 0 — a rejected request must not write", adds)
			}
		})
	}
}

func TestHandleRemoveDependency_DropsTheEdge(t *testing.T) {
	ctrl := &fakeDependencyController{}
	ts := newDependencyServer(ctrl)
	defer ts.Close()

	resp := doDelete(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies/"+otherTicketID, nil)
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	got := ctrl.last()
	if got.removes != 1 {
		t.Fatalf("RemoveDependency calls = %d, want 1", got.removes)
	}
	if got.projectID != testProjectID {
		t.Errorf("projectID = %q, want %q", got.projectID, testProjectID)
	}
	if string(got.id) != testTicketID || string(got.dependsOn) != otherTicketID {
		t.Errorf("ids = (%q, %q), want (%q, %q)", got.id, got.dependsOn, testTicketID, otherTicketID)
	}
}

// Unwired, the routes do not exist — same rule as the sandbox and text seams.
func TestTicketDependencyRoutes_AbsentUntilWired(t *testing.T) {
	ts := httptest.NewServer(enableSession(newBareServer()).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/tickets/"+testTicketID+"/dependencies",
		[]byte(`{"depends_on":"`+otherTicketID+`"}`))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST status = %d, want 404 with no controller wired", resp.StatusCode)
	}
}

// The board snapshot carries the dependency fields through to the client, and
// an empty list serializes as [] rather than null — the client maps over it
// unguarded.
func TestBoardSnapshot_CarriesDependencyFields(t *testing.T) {
	snap := board.Snapshot{
		Ready: []board.Ticket{
			{
				ID: "waiter", Title: "Waiting", State: board.StateReady,
				DependsOn: []board.TicketID{"blocker"}, UnmetDependencies: 1,
			},
			{ID: "free", Title: "Free", State: board.StateReady},
		},
	}
	boards := &fakeBoardReader{snapshot: snap}
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{})
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	var got struct {
		Ready []struct {
			ID                    string   `json:"id"`
			DependsOn             []string `json:"depends_on"`
			UnmetDependencies     int      `json:"unmet_dependencies"`
			WaitingOnDependencies bool     `json:"waiting_on_dependencies"`
		} `json:"ready"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode board: %v", err)
	}
	if len(got.Ready) != 2 {
		t.Fatalf("ready = %d tickets, want 2", len(got.Ready))
	}
	waiter, free := got.Ready[0], got.Ready[1]
	if len(waiter.DependsOn) != 1 || waiter.DependsOn[0] != depBlockerID {
		t.Errorf("waiter depends_on = %v, want [blocker]", waiter.DependsOn)
	}
	if waiter.UnmetDependencies != 1 || !waiter.WaitingOnDependencies {
		t.Errorf("waiter unmet/waiting = %d/%v, want 1/true",
			waiter.UnmetDependencies, waiter.WaitingOnDependencies)
	}
	if free.DependsOn == nil {
		t.Error("depends_on must serialize as [] for a ticket with no dependencies, not null")
	}
	if free.WaitingOnDependencies {
		t.Error("a ticket with no dependencies is not waiting")
	}
}
