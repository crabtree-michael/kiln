package brain_test

// The per-pass memo (memo.go): within one pass, a read the model asks for twice
// is answered from the conversation it is already holding, and a mutation the
// board refused is not re-attempted while nothing has changed. 4.1% of all
// measured tool calls were an exact repeat inside a single pass, and 10.3% of
// update_ticket calls still fail on a transition the state does not accept,
// with the failures clustering on single ids because the model re-issues the
// refused edit (docs/brain-optimization-2026-08-08-measured.md §10, §11).
//
// These are the mechanical backstop's tests. The behavior change the prompt and
// the tool descriptions are aiming at — the model not emitting the repeat in the
// first place — is not visible to a scripted-fake suite; prompt_test.go and
// dispatch_test.go pin that the framing is there, and the production log query
// that measured §10/§11 is what sizes the effect.

import (
	"context"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// reuseMarker opens the stand-in a repeated read gets, refusedMarker the one a
// re-issued refusal gets (memo.go). Asserted as the recognizable prefix, not the
// whole sentence — the wording is prose and free to change.
const (
	reuseMarker   = "not re-read"
	refusedMarker = "not retried"
)

// TestHandleEvent_RepeatedRead_DoesNotReachThePortTwice is §10 in its most
// literal form: the same get_ticket, a round apart, inside one pass. The second
// one never reaches BoardReader, and what comes back points the model at the
// result it is already holding rather than repeating it — a second copy would
// grow every remaining round's prefix, which is the cost being avoided.
func TestHandleEvent_RepeatedRead_DoesNotReachThePortTwice(t *testing.T) {
	fb := &fakeBoard{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "read-1", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})),
		toolUse(newToolCall(t, "read-2", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})),
		endTurn(""),
	}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(20, "what is t-1 doing?")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := countCalls(fb.recordedCalls(), methodGetTicket); got != 1 {
		t.Errorf("GetTicket reached the port %d times, want 1 — the second identical read in a "+
			"pass must be answered from the conversation, not the board", got)
	}

	res := requireToolResult(t, llm.requestAt(t, 2), "read-2")
	if res.IsError {
		t.Errorf("a reused read must not read as a failure; got IsError with %q", res.Content)
	}
	if !strings.Contains(res.Content, reuseMarker) {
		t.Errorf("reused read result = %q, want it to open with %q", res.Content, reuseMarker)
	}
	if strings.Contains(res.Content, "ticket "+ticketT1+":") {
		t.Errorf("a reused read must point at the earlier result, not repeat it; got %q", res.Content)
	}
}

// TestHandleEvent_DuplicateReadsInOneRound_ReachThePortOnce covers the other
// half of the same shape. The measured repeats include a pass that read one
// ticket four times, and batching (the §7 nudge) makes a round with several
// reads the normal case — so the duplicate can land inside one round, not only
// across two. Calls in a round run in order, so the first is already recorded
// when the second is reached.
func TestHandleEvent_DuplicateReadsInOneRound_ReachThePortOnce(t *testing.T) {
	fb := &fakeBoard{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(
			newToolCall(t, "dup-1", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1}),
			newToolCall(t, "dup-2", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1}),
		),
		endTurn(""),
	}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(21, "check t-1")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := countCalls(fb.recordedCalls(), methodGetTicket); got != 1 {
		t.Errorf("GetTicket reached the port %d times, want 1 — two identical reads in one round "+
			"are still one read", got)
	}
	// Both calls are still answered: a round must feed back a result per call,
	// or the model is left with a dangling tool_use block.
	round2 := llm.requestAt(t, 1)
	requireToolResult(t, round2, "dup-1")
	if res := requireToolResult(t, round2, "dup-2"); !strings.Contains(res.Content, reuseMarker) {
		t.Errorf("the second of two identical reads in a round = %q, want the reuse note", res.Content)
	}
}

// TestHandleEvent_ReadAfterOwnChange_IsFetchedAgain is the invalidation that
// keeps the memory honest. The board only moves when the pass moves it (there is
// no mid-pass snapshot refresh, 06 §5) — so once the pass has moved it, the
// remembered read is stale and re-reading is the right call, not a duplicate.
func TestHandleEvent_ReadAfterOwnChange_IsFetchedAgain(t *testing.T) {
	fb := &fakeBoard{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "before", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})),
		toolUse(newToolCall(t, "change", brain.ToolUpdateTicket,
			brain.UpdateTicketInput{ID: ticketT1, State: new("ready")})),
		toolUse(newToolCall(t, "after", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})),
		endTurn(""),
	}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(22, "queue t-1")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := countCalls(fb.recordedCalls(), methodGetTicket); got != 2 {
		t.Errorf("GetTicket reached the port %d times, want 2 — a read taken before the pass "+
			"changed the ticket must not be reused after it", got)
	}
	if res := requireToolResult(t, llm.requestAt(t, 3), "after"); strings.Contains(res.Content, reuseMarker) {
		t.Errorf("the read after a change was answered from memory: %q", res.Content)
	}
}

// TestHandleEvent_RefusedUpdate_IsNotRetried is §11: the model re-issuing the
// same illegal edit rather than backing off. The second attempt never reaches
// the board, and the board's own refusal comes back inside it unchanged — the
// idempotency rule (06 §6) is written against that wording, and no alternative
// is offered alongside it, for the same reason allowedActions keeps the
// preventive line off the refusal (tools.go).
func TestHandleEvent_RefusedUpdate_IsNotRetried(t *testing.T) {
	boardErr := &board.ErrInvalidTransition{From: board.StateWorking, Attempted: methodShapeTicket}
	fb := &fakeBoard{
		shapeTicketFn: func(context.Context, board.TicketID, board.ShapePatch) (board.Ticket, error) {
			return board.Ticket{}, boardErr
		},
	}
	edit := brain.UpdateTicketInput{ID: ticketT1, Body: new("a better brief")}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "edit-1", brain.ToolUpdateTicket, edit)),
		toolUse(newToolCall(t, "edit-2", brain.ToolUpdateTicket, edit)),
		endTurn(""),
	}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(23, "reword t-1")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := countCalls(fb.recordedCalls(), methodShapeTicket); got != 1 {
		t.Errorf("ShapeTicket reached the board %d times, want 1 — a refused edit re-issued "+
			"unchanged must not be attempted again", got)
	}

	// The first refusal is still the board's, verbatim (allowed_actions_test.go's
	// TestUpdateTicket_RefusalStaysVerbatim, now under a pass).
	if res := requireToolResult(t, llm.requestAt(t, 1), "edit-1"); res.Content != boardErr.Error() {
		t.Errorf("first refusal = %q, want the board's error verbatim %q", res.Content, boardErr.Error())
	}

	res := requireToolResult(t, llm.requestAt(t, 2), "edit-2")
	if !res.IsError {
		t.Errorf("a suppressed retry is still a refusal; got IsError=false with %q", res.Content)
	}
	if !strings.Contains(res.Content, refusedMarker) {
		t.Errorf("suppressed retry = %q, want it to open with %q", res.Content, refusedMarker)
	}
	if !strings.Contains(res.Content, boardErr.Error()) {
		t.Errorf("suppressed retry = %q, want it to carry the board's refusal %q unchanged",
			res.Content, boardErr.Error())
	}
	if strings.Contains(res.Content, allowedMarker) {
		t.Errorf("suppressed retry names alternatives (%q); a refusal must not, or the "+
			"idempotency rule's never-retry reading is inverted into a retry", res.Content)
	}
}

// TestHandleEvent_RefusedUpdate_IsAttemptedAgainOnceSomethingChanges is the
// refusal memory's own invalidation. A precondition failure is a fact about the
// board as it stood; once the pass has successfully moved the board, the same
// call might now be legal, and answering it from memory would be inventing a
// failure the board never gave.
func TestHandleEvent_RefusedUpdate_IsAttemptedAgainOnceSomethingChanges(t *testing.T) {
	fb := &fakeBoard{
		shapeTicketFn: func(context.Context, board.TicketID, board.ShapePatch) (board.Ticket, error) {
			return board.Ticket{}, &board.ErrInvalidTransition{From: board.StateWorking, Attempted: methodShapeTicket}
		},
	}
	edit := brain.UpdateTicketInput{ID: ticketT1, Body: new("a better brief")}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "edit-1", brain.ToolUpdateTicket, edit)),
		toolUse(newToolCall(t, "block", brain.ToolUpdateTicket, brain.UpdateTicketInput{
			ID: ticketT1, State: new("blocked"), BlockedReason: new("needs a decision"),
		})),
		toolUse(newToolCall(t, "edit-2", brain.ToolUpdateTicket, edit)),
		endTurn(""),
	}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(24, "reword t-1, then block it")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := countCalls(fb.recordedCalls(), methodShapeTicket); got != 2 {
		t.Errorf("ShapeTicket reached the board %d times, want 2 — a successful change makes "+
			"an earlier refusal no longer a fact about the current board", got)
	}
	if res := requireToolResult(t, llm.requestAt(t, 3), "edit-2"); strings.Contains(res.Content, refusedMarker) {
		t.Errorf("the edit after a successful change was refused from memory: %q", res.Content)
	}
}

// TestHandleEvent_MalformedRepeat_IsNeverAnsweredFromMemory guards the seam
// between the memo and 06 §8. A malformed call never reached a port, so it is
// not a refusal and is not filed as one — if it were, the identical second
// malformed call would come back as a clean suppressed result, the pass would
// never see two malformed rounds, and the one-re-prompt-then-fail rule would
// silently stop firing.
func TestHandleEvent_MalformedRepeat_IsNeverAnsweredFromMemory(t *testing.T) {
	fb := &fakeBoard{}
	// state="sideways" is not a reachable transition: argument-shape wrong, so
	// malformed rather than a precondition failure (validateUpdateState).
	bad := newToolCall(t, "bad", brain.ToolUpdateTicket,
		brain.UpdateTicketInput{ID: ticketT1, State: new("sideways")})
	llm := &scriptedLLM{responses: []brain.LLMResponse{toolUse(bad), toolUse(bad)}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(25, "move t-1 sideways")); err == nil {
		t.Fatal("HandleEvent returned nil, want the pass to fail once malformed output repeats " +
			"— the memo must not suppress the second malformed call into a clean result")
	}
	if got := llm.callCount(); got != 2 {
		t.Errorf("LLM.Do was called %d times, want exactly 2 (one re-prompt, then fail)", got)
	}
}

// TestDispatch_HasNoPassMemory pins the memo's scope. Dispatch is the exported
// single-call entry point with no pass behind it, and the memo is per-pass
// state: two passes are separated by real time in which the board does move, so
// nothing may be carried between them.
func TestDispatch_HasNoPassMemory(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})
	call := newToolCall(t, "gt", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})

	for i := range 2 {
		if res := svc.Dispatch(context.Background(), call); res.IsError {
			t.Fatalf("Dispatch %d returned error: %q", i, res.Content)
		}
	}
	if got := countCalls(fb.recordedCalls(), methodGetTicket); got != 2 {
		t.Errorf("GetTicket reached the port %d times across two separate Dispatches, want 2", got)
	}
}

// countCalls counts how many of the fake board's recorded calls hit method.
func countCalls(calls []recordedCall, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// requireToolResult fails the test if req carries no ToolResult for toolCallID —
// every call in a round must be answered, or the model is left holding a
// dangling tool_use block.
func requireToolResult(t *testing.T, req brain.LLMRequest, toolCallID string) brain.ToolResult {
	t.Helper()
	res, ok := findToolResult(req, toolCallID)
	if !ok {
		t.Fatalf("request carries no ToolResult for %q; messages: %#v", toolCallID, req.Messages)
	}
	return res
}
