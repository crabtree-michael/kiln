package brain_test

// The primary golden-decision suite (06 §9): fixture event in, expected
// tool-call/port-invocation sequence out, driven end-to-end through
// HandleEvent against the scripted LLM fake and the fake board/say/convo
// ports. No real Postgres, no real LLM.

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// TestHandleEvent_BoundedToolLoop_DispatchesInOrder pins 06 §5's pass loop:
// a scripted multi-action flow (create -> shape -> mark ready -> say, 06 §5
// "Multi-action flows ... are one pass") dispatches each tool call to the
// right port method with the right arguments, in the right order, feeding
// each round's tool results back before the next model call, and ends the
// pass when the model's StopReason is StopEndTurn.
func TestHandleEvent_BoundedToolLoop_DispatchesInOrder(t *testing.T) {
	fb := &fakeBoard{
		createTicketFn: func(ctx context.Context, title, body string) (board.Ticket, error) {
			return board.Ticket{ID: ticketT99, Title: title, Body: body, State: board.StateShaping}, nil
		},
	}
	fs := &fakeSay{}
	fc := &fakeConvo{}

	priority := 3
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "r1", brain.ToolCreateTicket, brain.CreateTicketInput{Title: "Fix bug", Body: "details"})),
		toolUse(newToolCall(t, "r2", brain.ToolUpdateTicket, brain.UpdateTicketInput{ID: ticketT99, Priority: &priority})),
		toolUse(newToolCall(t, "r3", brain.ToolUpdateTicket, brain.UpdateTicketInput{ID: ticketT99, State: new("ready")})),
		toolUse(newToolCall(t, "r4", brain.ToolSay, brain.SayInput{Text: "Created and readied t-99."})),
		endTurn(""),
	}}

	svc := newTestService(fb, fs, fc, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(1, "add a ticket to fix the bug and get it ready"))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	llm.requireCalled(t, 5)

	calls := fb.recordedCalls()
	wantMethods := []string{"CreateTicket", methodShapeTicket, methodMarkReady}
	gotMethods := make([]string, 0, len(calls))
	for _, c := range calls {
		gotMethods = append(gotMethods, c.Method)
	}
	if len(gotMethods) != len(wantMethods) {
		t.Fatalf("BoardAPI calls = %v, want %v", gotMethods, wantMethods)
	}
	for i, m := range wantMethods {
		if gotMethods[i] != m {
			t.Errorf("BoardAPI call[%d] = %q, want %q (full sequence: %v)", i, gotMethods[i], m, gotMethods)
		}
	}

	if got := fs.said(); len(got) != 1 || got[0] != "Created and readied t-99." {
		t.Errorf("fakeSay.said() = %v, want exactly [%q]", got, "Created and readied t-99.")
	}
}

// TestHandleEvent_BatchedReadRound_OneRoundCarriesEveryRead pins the pass shape
// the prompt's Rounds section now asks for: the three opening reads an
// agent.turn_completed pass needs — the roster, the ticket the event names, the
// output of the worker it names — arrive as one round, are all dispatched, and
// all come back in the single following request. That shape is what makes the
// nudge worth making: each round it removes is a full rewrite of the
// conversation prefix, which is ~50% of brain spend
// (docs/brain-optimization-2026-08-08-measured.md §1, §7). The loop already
// supported it (dispatchAll iterates every call); this keeps it supported, so a
// model that takes the prompt at its word is never punished by the harness.
func TestHandleEvent_BatchedReadRound_OneRoundCarriesEveryRead(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	agents := &fakeInspector{update: brain.AgentUpdate{WorkerID: workerW1, LatestOutput: "pushed the branch"}}

	readIDs := []string{"read-roster", "read-ticket", "read-agent"}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(
			newToolCall(t, readIDs[0], brain.ToolListTickets, brain.ListTicketsInput{}),
			newToolCall(t, readIDs[1], brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1}),
			newToolCall(t, readIDs[2], brain.ToolGetAgentUpdates, brain.GetAgentUpdatesInput{WorkerID: workerW1}),
		),
		toolUse(newToolCall(t, "act", brain.ToolUpdateTicket,
			brain.UpdateTicketInput{ID: ticketT1, State: new("blocked"), BlockedReason: new("needs a decision")})),
		endTurn(""),
	}}

	svc := newTestServiceI(fb, fs, &fakeConvo{}, agents, llm)
	err := svc.HandleEvent(context.Background(), agentTurnCompletedEvent(8, brain.AgentTurnCompletedPayload{
		TicketID: ticketT1, WorkerID: workerW1, Output: "pushed the branch",
	}))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	// Three reads batched into one round cost three LLM calls in total, not five:
	// the batched round, the action round, and the end-turn.
	if got := llm.callCount(); got != 3 {
		t.Fatalf("LLM.Do was called %d times, want exactly 3 — three batched reads must "+
			"cost one round, not one round each", got)
	}

	// Every call in the round actually ran against its port.
	if got := fb.getBoardCount(); got != 1 {
		t.Errorf("GetBoard called %d times, want 1", got)
	}
	if got := agents.gotWorkerID; got != workerW1 {
		t.Errorf("GetAgentUpdates worker id = %q, want %q", got, workerW1)
	}
	var sawGetTicket bool
	for _, c := range fb.recordedCalls() {
		if c.Method == methodGetTicket {
			sawGetTicket = true
		}
	}
	if !sawGetTicket {
		t.Errorf("get_ticket in a batched round never reached BoardReader; recorded %v", fb.recordedCalls())
	}

	// …and all three results come back together, in the very next request.
	round2 := llm.requestAt(t, 1)
	for _, id := range readIDs {
		if _, ok := findToolResult(round2, id); !ok {
			t.Errorf("round 2's request is missing the ToolResult for %q — a batched round must "+
				"feed back every call's result at once; messages: %#v", id, round2.Messages)
		}
	}
}

// TestHandleEvent_ToolErrorFedBackVerbatim pins 06 §5/§6/§8: when a port
// call returns a typed error, the very next LLMRequest carries that error
// text verbatim as the tool's ToolResult — not summarized, not dropped —
// and the pass does not crash.
func TestHandleEvent_ToolErrorFedBackVerbatim(t *testing.T) {
	wantErr := &board.ErrInvalidTransition{From: board.StateReady, Attempted: opMarkReady}
	fb := &fakeBoard{
		markReadyFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, wantErr
		},
	}
	fs := &fakeSay{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "r1", brain.ToolUpdateTicket, brain.UpdateTicketInput{ID: ticketT1, State: new("ready")})),
		endTurn("noted"),
	}}

	svc := newTestService(fb, fs, &fakeConvo{}, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(2, "mark t-1 ready again"))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	llm.requireCalled(t, 2)

	round2 := llm.requestAt(t, 1)
	result, ok := findToolResult(round2, "r1")
	if !ok {
		t.Fatalf("round 2's request does not carry a ToolResult for call r1; messages: %#v", round2.Messages)
	}
	if !result.IsError {
		t.Errorf("ToolResult for r1 has IsError=false, want true")
	}
	if result.Content != wantErr.Error() {
		t.Errorf("ToolResult.Content = %q, want verbatim %q", result.Content, wantErr.Error())
	}

	if calls := fb.recordedCalls(); len(calls) != 1 || calls[0].Method != methodMarkReady {
		t.Errorf("expected exactly one MarkReady call (no automatic retry), got %v", calls)
	}
}

// TestHandleEvent_RoundCap_TerminatesRunawayModel pins 06 §5/§8 (D4): a
// scripted LLM that always returns StopToolUse (never StopEndTurn) is cut
// off at MaxToolRounds — the pass terminates rather than looping forever,
// and the number of model round-trips stays close to the cap.
func TestHandleEvent_RoundCap_TerminatesRunawayModel(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	llm := &scriptedLLM{
		repeatLast: true,
		responses: []brain.LLMResponse{
			toolUse(newToolCall(t, "loop", brain.ToolSay, brain.SayInput{Text: "still working"})),
		},
	}

	svc := newTestService(fb, fs, &fakeConvo{}, llm)
	// A runaway model may make the forced wrap-up round return an error; the
	// round-cap contract is about the call count, so just log any error.
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(3, "do something")); err != nil {
		t.Logf("HandleEvent returned error (acceptable for the round-cap case): %v", err)
	}

	got := llm.callCount()
	if got <= brain.MaxToolRounds {
		t.Fatalf("LLM.Do was called %d times; want more than MaxToolRounds (%d) to "+
			"prove the loop actually ran to the cap rather than short-circuiting",
			got, brain.MaxToolRounds)
	}
	if got > brain.MaxToolRounds+1 {
		t.Fatalf("LLM.Do was called %d times; want at most MaxToolRounds+1 (%d) — the "+
			"loop must be bounded even when the model never ends its turn",
			got, brain.MaxToolRounds+1)
	}
}

// TestHandleEvent_ConfirmBeforeDestructive_AskEndsPassWithoutExecuting pins
// half of 06 §7/§9's "golden tests pin both": a pass that responds to an
// ambiguous/destructive-adjacent instruction with a say question, then ends
// its turn, must not have executed any BoardAPI mutation — the destructive
// action waits for the user's answer as the next human.message.
func TestHandleEvent_ConfirmBeforeDestructive_AskEndsPassWithoutExecuting(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	question := "Ticket t-1 has in-progress work — starting over will discard it. Proceed?"
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "ask", brain.ToolSay, brain.SayInput{Text: question})),
		endTurn(""),
	}}

	svc := newTestService(fb, fs, &fakeConvo{}, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(4, "start ticket t-1 over from scratch"))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if calls := fb.recordedCalls(); len(calls) != 0 {
		t.Fatalf("expected zero BoardAPI calls when the pass ends on a confirmation question, got %v", calls)
	}
	if got := fs.said(); len(got) != 1 || got[0] != question {
		t.Errorf("fakeSay.said() = %v, want exactly [%q]", got, question)
	}
}

// TestHandleEvent_ConfirmBeforeDestructive_UnambiguousExecutesImmediately
// pins the other half: an unambiguous destructive command (06 §7: "Unambiguous
// commands ... execute immediately") calls the destructive tool directly,
// with no preceding question.
func TestHandleEvent_ConfirmBeforeDestructive_UnambiguousExecutesImmediately(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "accept", brain.ToolUpdateTicket,
			brain.UpdateTicketInput{ID: "t-3", State: new("done"), DoneCommit: new("abc1234")})),
		toolUse(newToolCall(t, "say", brain.ToolSay, brain.SayInput{Text: "Accepted t-3."})),
		endTurn(""),
	}}

	// OnMain=true so the accepted commit clears the push gate.
	fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
	svc := newTestServiceR(fb, fs, &fakeConvo{}, fr, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(5, "accept ticket t-3"))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	calls := fb.recordedCalls()
	if len(calls) != 1 || calls[0].Method != methodAcceptToDone {
		t.Fatalf("expected exactly one AcceptToDone call, got %v", calls)
	}
	if len(calls[0].Args) != 2 || calls[0].Args[0] != board.TicketID("t-3") || calls[0].Args[1] != "abc1234" {
		t.Errorf("AcceptToDone args = %v, want [t-3 abc1234]", calls[0].Args)
	}
	if got := fs.said(); len(got) != 1 || got[0] != "Accepted t-3." {
		t.Errorf("fakeSay.said() = %v, want exactly [%q]", got, "Accepted t-3.")
	}
}

// TestHandleEvent_MalformedOutput_OneRepromptThenRecovers pins 06 §8: an
// unknown tool name is malformed output. The first occurrence gets exactly
// one re-prompt (with the parse error folded into the next request, not a
// failed pass); if the model then recovers, the pass completes normally.
func TestHandleEvent_MalformedOutput_OneRepromptThenRecovers(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(brain.ToolCall{ID: "bad", Name: brain.ToolName("delete_universe"), Input: []byte(`{}`)}),
		toolUse(newToolCall(t, "ok", brain.ToolSay, brain.SayInput{Text: "sorry, let me retry"})),
		endTurn(""),
	}}

	svc := newTestService(fb, fs, &fakeConvo{}, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(6, "do the thing"))
	if err != nil {
		t.Fatalf("HandleEvent returned error: %v, want nil (should recover after one re-prompt)", err)
	}

	if got := llm.callCount(); got != 3 {
		t.Fatalf("LLM.Do was called %d times, want exactly 3 (malformed round, re-prompt round, final round)", got)
	}
	if calls := fb.recordedCalls(); len(calls) != 0 {
		t.Errorf("the unknown tool call must never reach BoardAPI; recorded %v", calls)
	}
	if got := fs.said(); len(got) != 1 || got[0] != "sorry, let me retry" {
		t.Errorf("fakeSay.said() = %v, want exactly [%q]", got, "sorry, let me retry")
	}
}

// TestHandleEvent_MalformedOutput_RepeatedFailsThePass pins 06 §8's other
// half: if the malformed output repeats after the single re-prompt, the
// pass fails (returns an error to the runtime for its retry/backoff/dead-
// letter path) rather than looping or silently succeeding — and board state
// stays untouched.
func TestHandleEvent_MalformedOutput_RepeatedFailsThePass(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(brain.ToolCall{ID: "bad1", Name: brain.ToolName("delete_universe"), Input: []byte(`{}`)}),
		toolUse(brain.ToolCall{ID: "bad2", Name: brain.ToolName("delete_universe"), Input: []byte(`{}`)}),
	}}

	svc := newTestService(fb, fs, &fakeConvo{}, llm)
	err := svc.HandleEvent(context.Background(), humanMessageEvent(7, "do the thing"))
	if err == nil {
		t.Fatalf("HandleEvent returned nil error, want a failure after malformed output repeats")
	}

	if got := llm.callCount(); got != 2 {
		t.Fatalf("LLM.Do was called %d times, want exactly 2 (one re-prompt, then fail — no further calls)", got)
	}
	if calls := fb.recordedCalls(); len(calls) != 0 {
		t.Errorf("a failed pass must never mutate the board; recorded %v", calls)
	}
	if got := fs.said(); len(got) != 0 {
		t.Errorf("a failed pass's dead-letter say is the runtime's job (06 §8), not this method's; recorded %v", got)
	}
}

// findToolResult looks through req's messages for a ToolResult matching
// toolCallID.
func findToolResult(req brain.LLMRequest, toolCallID string) (brain.ToolResult, bool) {
	for _, msg := range req.Messages {
		for _, r := range msg.Results {
			if r.ToolCallID == toolCallID {
				return r, true
			}
		}
	}
	return brain.ToolResult{}, false
}
