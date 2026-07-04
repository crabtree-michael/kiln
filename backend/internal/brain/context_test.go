package brain_test

// Input-contract tests (06 §3): PassInput assembly injects the full board
// snapshot + last TranscriptWindow (20) transcript messages + the event
// into the first LLMRequest, with agent.turn_completed output truncated to
// AgentOutputTruncateBytes (head+tail), and a non-empty, stable system
// prompt rendered from the repo's template.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// requestText flattens everything the scripted LLM saw in one request into
// a single string, so tests can assert on presence/absence of markers
// without depending on the exact wire layout chosen for LLMRequest.Messages.
func requestText(req brain.LLMRequest) string {
	var b strings.Builder
	b.WriteString(req.System)
	for _, m := range req.Messages {
		b.WriteString(m.Text)
		for _, r := range m.Results {
			b.WriteString(r.Content)
		}
	}
	return b.String()
}

// TestHandleEvent_InjectsFullBoardSnapshotOncePerPass pins 06 §3.1/D3: the
// full GetBoard snapshot is read exactly once per pass and reaches the
// model's first request — there is no board-read tool, so the model never
// asks for it.
func TestHandleEvent_InjectsFullBoardSnapshotOncePerPass(t *testing.T) {
	fb := &fakeBoard{
		getBoardFn: func(ctx context.Context) (board.Snapshot, error) {
			return board.Snapshot{
				Ready: []board.Ticket{{ID: "marker-ticket-xyz", Title: "Marker Ticket", State: board.StateReady}},
			}, nil
		},
	}
	llm := &scriptedLLM{responses: []brain.LLMResponse{endTurn("ok")}}

	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(10, "what's on the board?")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if got := fb.getBoardCount(); got != 1 {
		t.Fatalf("GetBoard was called %d times, want exactly 1 per pass (06 §3, D3)", got)
	}

	llm.requireCalled(t, 1)
	first := requestText(llm.requestAt(t, 0))
	if !strings.Contains(first, "marker-ticket-xyz") && !strings.Contains(first, "Marker Ticket") {
		t.Errorf("first LLMRequest does not appear to contain the board snapshot "+
			"(looked for the marker ticket); got: %q", first)
	}
}

// TestHandleEvent_TranscriptWindowIsLast20MessagesOldestFirst pins 06 §3.2
// (D2): Recent is asked for exactly TranscriptWindow (20) messages, and the
// oldest 5 of a 25-message history are dropped from what reaches the model.
func TestHandleEvent_TranscriptWindowIsLast20MessagesOldestFirst(t *testing.T) {
	const total = 25
	messages := make([]brain.Message, 0, total)
	for i := range total {
		messages = append(messages, brain.Message{
			Role: brain.RoleUser,
			Text: fmt.Sprintf("history-marker-%02d", i),
			At:   time.Now().Add(time.Duration(i) * time.Minute),
		})
	}
	fc := &fakeConvo{messages: messages}
	llm := &scriptedLLM{responses: []brain.LLMResponse{endTurn("ok")}}

	svc := newTestService(&fakeBoard{}, &fakeSay{}, fc, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(11, "hi")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	if len(fc.recentNs) != 1 {
		t.Fatalf("ConversationReader.Recent was called %d times, want exactly 1", len(fc.recentNs))
	}
	if fc.recentNs[0] != brain.TranscriptWindow {
		t.Fatalf("Recent(n) was called with n=%d, want TranscriptWindow=%d", fc.recentNs[0], brain.TranscriptWindow)
	}

	llm.requireCalled(t, 1)
	first := requestText(llm.requestAt(t, 0))

	// The oldest 5 (indices 0-4) must be dropped; the 21st-oldest (index 5,
	// the first message inside the last-20 window) and the newest
	// (index 24) must both be present.
	if strings.Contains(first, "history-marker-00") {
		t.Errorf("first LLMRequest contains history-marker-00, which is outside " +
			"the last-20 window and should have been dropped")
	}
	if strings.Contains(first, "history-marker-04") {
		t.Errorf("first LLMRequest contains history-marker-04, which is outside " +
			"the last-20 window and should have been dropped")
	}
	if !strings.Contains(first, "history-marker-05") {
		t.Errorf("first LLMRequest is missing history-marker-05, the oldest message inside the last-20 window")
	}
	if !strings.Contains(first, "history-marker-24") {
		t.Errorf("first LLMRequest is missing history-marker-24, the newest transcript message")
	}
}

// TestHandleEvent_TruncatesLongAgentOutputHeadAndTail pins 06 §3.3: a long
// agent.turn_completed Output is truncated to an ~8k head+tail budget with
// an elision marker in between — the brain judges outcomes, it does not
// re-review diffs. This asserts the truncation *shape* (head kept, tail
// kept, a large middle section dropped, total shrunk well below the raw
// input) without pinning the literal elision-marker text.
func TestHandleEvent_TruncatesLongAgentOutputHeadAndTail(t *testing.T) {
	const headMarker = "HEAD_MARKER_START"
	const tailMarker = "TAIL_MARKER_END"
	const middleMarker = "MIDDLE_ONLY_MARKER_UNIQUE"

	// Build an output far larger than the ~8000-char budget: head marker,
	// then filler, then a middle marker positioned well past what an
	// 8k-head slice would ever reach, then more filler, then a tail marker
	// positioned well before what an 8k-tail slice would start at.
	filler := strings.Repeat("x", 20000)
	output := headMarker + filler + middleMarker + filler + tailMarker

	fb := &fakeBoard{}
	llm := &scriptedLLM{responses: []brain.LLMResponse{endTurn("ok")}}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, llm)

	ev := agentTurnCompletedEvent(12, brain.AgentTurnCompletedPayload{
		TicketID: "t-7", WorkerID: "w-1", IsError: false, Output: output, CostUSD: 0.02,
	})
	if err := svc.HandleEvent(context.Background(), ev); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	llm.requireCalled(t, 1)
	first := requestText(llm.requestAt(t, 0))

	if !strings.Contains(first, headMarker) {
		t.Errorf("first LLMRequest is missing the head marker; truncation must keep the head (06 §3.3)")
	}
	if !strings.Contains(first, tailMarker) {
		t.Errorf("first LLMRequest is missing the tail marker; truncation must keep the tail (06 §3.3)")
	}
	if strings.Contains(first, middleMarker) {
		t.Errorf("first LLMRequest contains the middle-only marker; the ~8k " +
			"head+tail budget should have elided the middle (06 §3.3)")
	}
	if len(first) >= len(output) {
		t.Errorf("first LLMRequest (len %d) is not shorter than the raw agent "+
			"output (len %d); truncation does not appear to have run", len(first), len(output))
	}
}

// TestHandleEvent_RendersNonEmptyStableSystemPrompt pins 06 §3's "the
// system prompt lives in the repo as a Go template" — every round of a pass
// renders the same, non-empty system prompt (prompt.go's RenderSystemPrompt).
// This intentionally does not pin the prompt's literal prose, which is
// business content reviewed on its own terms (06 D7).
func TestHandleEvent_RendersNonEmptyStableSystemPrompt(t *testing.T) {
	llm := &scriptedLLM{responses: []brain.LLMResponse{
		toolUse(newToolCall(t, "s1", brain.ToolSay, brain.SayInput{Text: "one"})),
		toolUse(newToolCall(t, "s2", brain.ToolSay, brain.SayInput{Text: "two"})),
		endTurn(""),
	}}

	svc := newTestService(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, llm)
	if err := svc.HandleEvent(context.Background(), humanMessageEvent(13, "hi")); err != nil {
		t.Fatalf("HandleEvent returned error: %v", err)
	}

	llm.requireCalled(t, 2)
	r0 := llm.requestAt(t, 0)
	r1 := llm.requestAt(t, 1)
	if r0.System == "" {
		t.Fatalf("first LLMRequest.System is empty, want the rendered system prompt (06 §3, D7)")
	}
	if r0.System != r1.System {
		t.Errorf("System prompt changed between rounds of the same pass (%q vs %q); "+
			"it should be rendered once and held fixed", r0.System, r1.System)
	}
}
