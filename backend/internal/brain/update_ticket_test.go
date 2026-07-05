package brain_test

// Tests for the CRUD-consolidation additions (06 §4 amended): the update_ticket
// facade's routing/ordering/validation, and the new feed tools list_updates and
// edit_update.

import (
	"context"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// update_ticket applies field edits before the state transition, in one call:
// {body, state:"ready"} routes to ShapeTicket then MarkReady, in that order.
func TestUpdateTicket_EditsThenTransitionsInOneCall(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "u1", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, Body: new("revised body"), State: new("ready"),
	})
	res := svc.Dispatch(context.Background(), call)
	if res.IsError {
		t.Fatalf("update_ticket returned error: %q", res.Content)
	}

	calls := fb.recordedCalls()
	if len(calls) != 2 || calls[0].Method != methodShapeTicket || calls[1].Method != methodMarkReady {
		t.Fatalf("recorded methods = %v, want [ShapeTicket MarkReady] in order", methodsOf(calls))
	}
}

// approval_requested and state are mutually exclusive — supplying both is a
// malformed call that never reaches the board.
func TestUpdateTicket_ApprovalAndStateMutuallyExclusive(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := brain.ToolCall{
		ID: "u2", Name: brain.ToolUpdateTicket,
		Input: []byte(`{"id":"t-1","approval_requested":true,"state":"ready"}`),
	}
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("update_ticket with approval_requested+state should be an error")
	}
	if len(fb.recordedCalls()) != 0 {
		t.Errorf("a malformed update_ticket must not reach the board; recorded %v", methodsOf(fb.recordedCalls()))
	}
}

// An unrecognized state value is malformed and never reaches the board.
func TestUpdateTicket_BadStateRejected(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := brain.ToolCall{ID: "u3", Name: brain.ToolUpdateTicket, Input: []byte(`{"id":"t-1","state":"shaping"}`)}
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("update_ticket with state=shaping should be an error (no transition to shaping)")
	}
	if len(fb.recordedCalls()) != 0 {
		t.Errorf("a bad state value must not reach the board; recorded %v", methodsOf(fb.recordedCalls()))
	}
}

// An empty patch (just an id) is malformed — nothing to update.
func TestUpdateTicket_EmptyPatchRejected(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := brain.ToolCall{ID: "u4", Name: brain.ToolUpdateTicket, Input: []byte(`{"id":"t-1"}`)}
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("update_ticket with nothing to change should be an error")
	}
	if len(fb.recordedCalls()) != 0 {
		t.Errorf("an empty patch must not reach the board; recorded %v", methodsOf(fb.recordedCalls()))
	}
}

// When a later routed step fails, the error names the steps that already
// applied so the model can re-issue only the remainder (06 §6).
func TestUpdateTicket_PartialFailureReportsAppliedSteps(t *testing.T) {
	fb := &fakeBoard{
		markReadyFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, &board.ErrInvalidTransition{From: board.StateReady, Attempted: "MarkReady"}
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "u5", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, Body: new("revised"), State: new("ready"),
	})
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("update_ticket should surface the MarkReady failure")
	}
	if !strings.Contains(res.Content, "applied fields") {
		t.Errorf("partial-failure content = %q, want it to name the already-applied 'fields' step", res.Content)
	}
	// The field edit did happen (it is not rolled back); only the transition failed.
	if calls := methodsOf(fb.recordedCalls()); len(calls) != 2 || calls[0] != methodShapeTicket {
		t.Errorf("recorded methods = %v, want ShapeTicket to have run before the failed MarkReady", calls)
	}
}

// list_updates routes to FeedReader.ListUpdates and surfaces the card ids.
func TestDispatch_ListUpdates_RoutesToFeedReader(t *testing.T) {
	ff := &fakeFeed{updates: []brain.Update{
		{ID: 7, Kind: "update", Body: "shipped the parser"},
	}}
	svc := newTestServiceF(&fakeBoard{}, &fakeSay{}, ff, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "lu", brain.ToolListUpdates, brain.ListUpdatesInput{})
	res := svc.Dispatch(context.Background(), call)

	if res.IsError {
		t.Fatalf("list_updates returned error: %q", res.Content)
	}
	if ff.calls != 1 {
		t.Errorf("FeedReader.ListUpdates calls = %d, want 1", ff.calls)
	}
	if !strings.Contains(res.Content, "7") || !strings.Contains(res.Content, "shipped the parser") {
		t.Errorf("list_updates content = %q, want it to carry the id and body", res.Content)
	}
}

// edit_update routes to NotificationStore.EditNotification with kind derived
// from the image's presence (preview when an image is set).
func TestDispatch_EditUpdate_RoutesToNotificationStore(t *testing.T) {
	fn := &fakeNotifications{}
	svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

	img := "https://img/x.png"
	call := newToolCall(t, "eu", brain.ToolEditUpdate, brain.EditUpdateInput{
		NotificationID: 9, Body: "amended text", ImageURL: &img,
	})
	res := svc.Dispatch(context.Background(), call)

	if res.IsError {
		t.Fatalf("edit_update returned error: %q", res.Content)
	}
	edits := fn.edited()
	if len(edits) != 1 {
		t.Fatalf("EditNotification calls = %d, want 1", len(edits))
	}
	e := edits[0]
	if e.ID != 9 || e.Kind != "preview" || e.Body != "amended text" || e.ImageURL == nil || *e.ImageURL != img {
		t.Errorf("edit = %+v, want id=9 kind=preview body='amended text' image=%q", e, img)
	}
}

// edit_update with an empty body is rejected (requireField) and never reaches
// the store — the same silent-empty guard as post_update.
func TestDispatch_EditUpdate_EmptyBodyRejected(t *testing.T) {
	fn := &fakeNotifications{}
	svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

	call := brain.ToolCall{ID: "eu2", Name: brain.ToolEditUpdate, Input: []byte(`{"notification_id":9,"body":"  "}`)}
	res := svc.Dispatch(context.Background(), call)

	if !res.IsError {
		t.Fatalf("edit_update with a blank body should be an error")
	}
	if len(fn.edited()) != 0 {
		t.Errorf("a blank-body edit_update must not reach the store; got %v", fn.edited())
	}
}

// methodsOf projects recorded board calls to their method names, for order
// assertions.
func methodsOf(calls []recordedCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Method
	}
	return out
}
