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
		{ID: 7, Kind: notifKindUpdate, Body: "shipped the parser"},
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
	if e.ID != 9 || e.Kind != notifKindPreview || e.Body != "amended text" || e.ImageURL == nil || *e.ImageURL != img {
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

// ---- depends_on: the whole-list reconcile (0013) ---------------------------

// The fake's recorded method names for the two edge writes.
const (
	methodAddDependency    = "AddDependency"
	methodRemoveDependency = "RemoveDependency"
)

// The model states the end state it wants; the facade turns that into the
// individual edges the board takes — adding what is missing and dropping what
// is no longer named.
func TestDispatch_UpdateTicket_DependsOnReconcilesTheList(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			// Currently waits on a and b.
			return board.Ticket{
				ID: id, State: board.StateReady,
				DependsOn: []board.TicketID{"a", "b"},
			}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	// Wanted: a and c — so b goes, c arrives, a is left alone.
	call := newToolCall(t, "d1", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{"a", "c"},
	})
	res := svc.Dispatch(context.Background(), call)
	if res.IsError {
		t.Fatalf("update_ticket depends_on returned error: %q", res.Content)
	}

	var added, removed []string
	for _, c := range fb.recordedCalls() {
		switch c.Method {
		case methodAddDependency:
			if dep, ok := c.Args[1].(board.TicketID); ok {
				added = append(added, string(dep))
			}
		case methodRemoveDependency:
			if dep, ok := c.Args[1].(board.TicketID); ok {
				removed = append(removed, string(dep))
			}
		}
	}
	if len(added) != 1 || added[0] != "c" {
		t.Errorf("added = %v, want only [c] — a is already there", added)
	}
	if len(removed) != 1 || removed[0] != "b" {
		t.Errorf("removed = %v, want only [b] — it is no longer named", removed)
	}
}

// An empty list is a real instruction ("this waits for nothing"), not an
// omission: it clears the ticket's dependencies.
func TestDispatch_UpdateTicket_EmptyDependsOnClearsTheList(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{
				ID: id, State: board.StateReady,
				DependsOn: []board.TicketID{"a", "b"},
			}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "d2", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{},
	})
	if res := svc.Dispatch(context.Background(), call); res.IsError {
		t.Fatalf("clearing depends_on returned error: %q", res.Content)
	}
	var removed int
	for _, c := range fb.recordedCalls() {
		if c.Method == methodRemoveDependency {
			removed++
		}
	}
	if removed != 2 {
		t.Errorf("RemoveDependency calls = %d, want 2 — an empty list clears every edge", removed)
	}
}

// Re-sending the list a ticket already has writes nothing: the model repeating
// itself must not churn the board.
func TestDispatch_UpdateTicket_UnchangedDependsOnWritesNothing(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{
				ID: id, State: board.StateReady,
				DependsOn: []board.TicketID{"a"},
			}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "d3", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{"a"},
	})
	if res := svc.Dispatch(context.Background(), call); res.IsError {
		t.Fatalf("unchanged depends_on returned error: %q", res.Content)
	}
	for _, c := range fb.recordedCalls() {
		if c.Method == methodAddDependency || c.Method == methodRemoveDependency {
			t.Errorf("unexpected %s — the list already matched", c.Method)
		}
	}
}

// The board's refusal reaches the model verbatim (06 §6), so it can name the
// tickets involved rather than retrying blind.
func TestDispatch_UpdateTicket_CircularDependencyIsFedBackVerbatim(t *testing.T) {
	cyc := &board.ErrCircularDependency{Ticket: ticketT1, DependsOn: "c"}
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{ID: id, State: board.StateReady}, nil
		},
		addDependencyFn: func(_ context.Context, _, _ board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, cyc
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "d4", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{"c"},
	})
	res := svc.Dispatch(context.Background(), call)
	if !res.IsError {
		t.Fatal("a circular dependency must come back as a tool error")
	}
	if !strings.Contains(res.Content, cyc.Error()) {
		t.Errorf("content = %q, want the board's own message %q verbatim", res.Content, cyc.Error())
	}
}

// depends_on alone is a valid patch — it must not be rejected as "nothing to
// update".
func TestDispatch_UpdateTicket_DependsOnAloneIsWork(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{ID: id, State: board.StateReady}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "d5", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{"a"},
	})
	if res := svc.Dispatch(context.Background(), call); res.IsError {
		t.Fatalf("depends_on alone should be a valid patch, got error: %q", res.Content)
	}
}

// Omitting depends_on leaves the list alone — nil means "unchanged", which is
// what makes every other update_ticket call safe on a ticket that has
// dependencies. Distinct from re-sending the same list: here the field is
// absent, so the facade must not even read it back to compare.
func TestDispatch_UpdateTicket_OmittedDependsOnTouchesNothing(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{
				ID: id, State: board.StateShaping,
				DependsOn: []board.TicketID{"a"},
			}, nil
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	title := "Just a rename"
	call := newToolCall(t, "d6", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, Title: &title,
	})
	if res := svc.Dispatch(context.Background(), call); res.IsError {
		t.Fatalf("title-only update returned error: %q", res.Content)
	}
	for _, c := range fb.recordedCalls() {
		if c.Method == methodAddDependency || c.Method == methodRemoveDependency {
			t.Errorf("unexpected %s — depends_on was omitted, so the list is untouched", c.Method)
		}
	}
}

// Adds run before removes, so a refused edge leaves the ticket's existing list
// whole. The model's next attempt then starts from the state it last saw,
// rather than from a list the failed call had already half-dismantled.
func TestDispatch_UpdateTicket_RefusedAddLeavesTheOldListIntact(t *testing.T) {
	fb := &fakeBoard{
		getTicketFn: func(_ context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{
				ID: id, State: board.StateReady,
				DependsOn: []board.TicketID{"keep"},
			}, nil
		},
		addDependencyFn: func(_ context.Context, id, dependsOn board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, &board.ErrCircularDependency{Ticket: id, DependsOn: dependsOn}
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	// "keep" is dropped and "bad" added — but the add is refused, so the remove
	// must never happen.
	call := newToolCall(t, "d7", brain.ToolUpdateTicket, brain.UpdateTicketInput{
		ID: ticketT1, DependsOn: &[]string{"bad"},
	})
	if res := svc.Dispatch(context.Background(), call); !res.IsError {
		t.Fatal("a circular dependency must come back as a tool error")
	}
	for _, c := range fb.recordedCalls() {
		if c.Method == methodRemoveDependency {
			t.Error("RemoveDependency ran after the add was refused — the existing " +
				"dependency list must survive a failed reconcile")
		}
	}
}
