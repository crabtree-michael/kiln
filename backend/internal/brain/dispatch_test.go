package brain_test

// Dispatch pins the tool -> port-method mapping (06 §4) and the "never
// crash the pass" contract (06 §5, §8): every one of the seven tools routes
// to the right port call with correctly-parsed arguments, and a port error
// (or a malformed/unknown tool call) comes back as a ToolResult with IsError
// set and the error text verbatim — never a Go error, never a panic.

import (
	"context"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// TestToolSet_IsExactlyTenToolsInFixedOrder pins the tool set (06 §4, amended
// by 08 §5/§7): no pull tool, no board-read tool — exactly these ten, in this
// order (order matters for prompt-cache friendliness and golden fixtures,
// 06 §4/§9). The last three are 08's feed tools.
func TestToolSet_IsExactlyTenToolsInFixedOrder(t *testing.T) {
	want := []brain.ToolName{
		brain.ToolCreateTicket,
		brain.ToolShapeTicket,
		brain.ToolMarkReady,
		brain.ToolSendToAgent,
		brain.ToolMarkBlocked,
		brain.ToolAcceptToDone,
		brain.ToolSay,
		brain.ToolRequestApproval,
		brain.ToolPostUpdate,
		brain.ToolRetractUpdate,
	}
	if len(brain.Tools) != len(want) {
		t.Fatalf("len(Tools) = %d, want %d (%v)", len(brain.Tools), len(want), want)
	}
	for i, name := range want {
		if brain.Tools[i].Name != name {
			t.Errorf("Tools[%d].Name = %q, want %q", i, brain.Tools[i].Name, name)
		}
	}
}

// TestSystemPrompt_HasFeedToolGuidance pins that the shipped prompt keeps
// the 08 §5/§7 feed-tool guidance. It asserts tool-name presence, not
// literal prose (06 D7).
func TestSystemPrompt_HasFeedToolGuidance(t *testing.T) {
	got, err := brain.RenderSystemPrompt(brain.PromptData{Role: "Kiln"})
	if err != nil {
		t.Fatalf("RenderSystemPrompt: %v", err)
	}
	for _, tool := range []string{"request_approval", "post_update", "retract_update"} {
		if !strings.Contains(got, tool) {
			t.Errorf("system prompt is missing the 08 feed-tool guidance for %s:\n%s", tool, got)
		}
	}
}

// TestDispatch_RoutesEachToolToItsPortMethod is the golden tool -> port
// mapping table (06 §4).
func TestDispatch_RoutesEachToolToItsPortMethod(t *testing.T) {
	priority := 7
	newTitle := "new title"
	newBody := "new body"

	cases := []struct {
		name       string
		call       func(t *testing.T) brain.ToolCall
		wantMethod string
		wantArgs   []any
	}{
		{
			name: "create_ticket",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c1", brain.ToolCreateTicket, brain.CreateTicketInput{Title: "T", Body: "B"})
			},
			wantMethod: "CreateTicket",
			wantArgs:   []any{"T", "B"},
		},
		{
			name: "shape_ticket",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c2", brain.ToolShapeTicket, brain.ShapeTicketInput{
					ID: ticketT1, Title: &newTitle, Body: &newBody, Priority: &priority,
				})
			},
			wantMethod: "ShapeTicket",
			wantArgs: []any{board.TicketID(ticketT1), board.ShapePatch{
				Title: &newTitle, Body: &newBody, Priority: &priority,
			}},
		},
		{
			name: opMarkReady,
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c3", brain.ToolMarkReady, brain.MarkReadyInput{ID: "t-2"})
			},
			wantMethod: methodMarkReady,
			wantArgs:   []any{board.TicketID("t-2")},
		},
		{
			name: "send_to_agent",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c4", brain.ToolSendToAgent, brain.SendToAgentInput{ID: "t-3", Instruction: "keep going"})
			},
			wantMethod: "SendToAgent",
			wantArgs:   []any{board.TicketID("t-3"), "keep going"},
		},
		{
			name: "mark_blocked",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c5", brain.ToolMarkBlocked, brain.MarkBlockedInput{ID: "t-4", Reason: "need a decision"})
			},
			wantMethod: "MarkBlocked",
			wantArgs:   []any{board.TicketID("t-4"), "need a decision"},
		},
		{
			name: "accept_to_done",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c6", brain.ToolAcceptToDone, brain.AcceptToDoneInput{ID: "t-5"})
			},
			wantMethod: "AcceptToDone",
			wantArgs:   []any{board.TicketID("t-5")},
		},
		{
			name: "say",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c7", brain.ToolSay, brain.SayInput{Text: "hello"})
			},
			wantMethod: "Say",
		},
		{
			name: "request_approval",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c8", brain.ToolRequestApproval, brain.RequestApprovalInput{Ticket: "t-6"})
			},
			wantMethod: "RequestApproval",
			wantArgs:   []any{board.TicketID("t-6")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeBoard{}
			fs := &fakeSay{}
			svc := newTestService(fb, fs, &fakeConvo{}, &scriptedLLM{})

			call := tc.call(t)
			result := svc.Dispatch(context.Background(), call)

			if result.ToolCallID != call.ID {
				t.Errorf("ToolResult.ToolCallID = %q, want %q", result.ToolCallID, call.ID)
			}
			if result.IsError {
				t.Errorf("ToolResult.IsError = true, want false (Content: %q)", result.Content)
			}

			if tc.wantMethod == "Say" {
				if got := fs.said(); len(got) != 1 || got[0] != "hello" {
					t.Errorf("fakeSay.said() = %v, want [\"hello\"]", got)
				}
				return
			}

			calls := fb.recordedCalls()
			if len(calls) != 1 {
				t.Fatalf("fakeBoard recorded %d calls, want 1 (%v)", len(calls), calls)
			}
			if calls[0].Method != tc.wantMethod {
				t.Errorf("recorded method = %q, want %q", calls[0].Method, tc.wantMethod)
			}
			if len(tc.wantArgs) > 0 {
				if len(calls[0].Args) != len(tc.wantArgs) {
					t.Fatalf("recorded args = %#v, want %#v", calls[0].Args, tc.wantArgs)
				}
				for i, want := range tc.wantArgs {
					got := calls[0].Args[i]
					if !argsEqual(got, want) {
						t.Errorf("arg[%d] = %#v, want %#v", i, got, want)
					}
				}
			}
		})
	}
}

// TestDispatch_PostUpdate_RoutesToNotificationStore pins post_update's mapping
// (08 §7): kind is "update" with no image, "preview" when image_url is set,
// and body/ticket/image_url reach NotificationStore.PostNotification verbatim.
func TestDispatch_PostUpdate_RoutesToNotificationStore(t *testing.T) {
	ticket := "t-7"
	img := "https://example.test/preview.png"

	cases := []struct {
		name     string
		input    brain.PostUpdateInput
		wantKind string
	}{
		{
			name:     "update (no image)",
			input:    brain.PostUpdateInput{Body: "build is green", Ticket: &ticket},
			wantKind: "update",
		},
		{
			name:     "preview (image set)",
			input:    brain.PostUpdateInput{Body: "have a look", Ticket: &ticket, ImageURL: &img},
			wantKind: "preview",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := &fakeNotifications{}
			svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

			call := newToolCall(t, "pu-1", brain.ToolPostUpdate, tc.input)
			result := svc.Dispatch(context.Background(), call)

			if result.IsError {
				t.Fatalf("ToolResult.IsError = true, want false (Content: %q)", result.Content)
			}
			posts := fn.posted()
			if len(posts) != 1 {
				t.Fatalf("PostNotification called %d times, want 1", len(posts))
			}
			got := posts[0]
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Body != tc.input.Body {
				t.Errorf("body = %q, want %q", got.Body, tc.input.Body)
			}
			if !ptrStrEq(got.Ticket, tc.input.Ticket) {
				t.Errorf("ticket = %v, want %v", got.Ticket, tc.input.Ticket)
			}
			if !ptrStrEq(got.ImageURL, tc.input.ImageURL) {
				t.Errorf("image_url = %v, want %v", got.ImageURL, tc.input.ImageURL)
			}
		})
	}
}

// TestDispatch_PostUpdate_EmptyBodyRejected pins that post_update with an empty
// body is rejected as a malformed call (06 §8) rather than silently posting a
// bodyless card. "body" is a required field; an empty value — whether the model
// omitted it, sent whitespace, or put its prose under the wrong key (e.g. "text",
// which the sibling say tool uses) — must come back as an error ToolResult and
// must NOT reach NotificationStore.PostNotification. Otherwise the brain is told
// "ok", believes it posted, and the user sees a header + timestamp with no text.
func TestDispatch_PostUpdate_EmptyBodyRejected(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
	}{
		{name: "body omitted", input: []byte(`{"ticket": "t-1"}`)},
		{name: "body empty string", input: []byte(`{"body": ""}`)},
		{name: "body whitespace only", input: []byte(`{"body": "   \n\t"}`)},
		{name: "prose under wrong key", input: []byte(`{"text": "build is green"}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := &fakeNotifications{}
			svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

			call := brain.ToolCall{ID: "pu-empty", Name: brain.ToolPostUpdate, Input: tc.input}
			result := svc.Dispatch(context.Background(), call)

			if !result.IsError {
				t.Fatalf("ToolResult.IsError = false, want true for empty-body post_update")
			}
			if posts := fn.posted(); len(posts) != 0 {
				t.Errorf("empty-body post_update must not reach PostNotification; recorded %v", posts)
			}
		})
	}
}

// TestDispatch_RequiredTextFields_RejectEmpty pins that every required free-text
// tool argument — say's text, create_ticket's title, send_to_agent's
// instruction, mark_blocked's reason — is rejected when omitted, empty, or
// whitespace-only (see requireField). Such a value parses cleanly to "", so it
// is not a JSON error; it must still come back as an error ToolResult and never
// reach the board or Say — the same silent-empty gap that produced bodyless
// update cards. create_ticket's body is intentionally optional (the board allows
// it), so it is not covered here.
func TestDispatch_RequiredTextFields_RejectEmpty(t *testing.T) {
	cases := []struct {
		name  string
		tool  brain.ToolName
		input string
	}{
		{"say text omitted", brain.ToolSay, `{}`},
		{"say text whitespace", brain.ToolSay, `{"text": "  \n"}`},
		{"create_ticket title omitted", brain.ToolCreateTicket, `{"body": "b"}`},
		{"create_ticket title whitespace", brain.ToolCreateTicket, `{"title": " \t", "body": "b"}`},
		{"send_to_agent instruction omitted", brain.ToolSendToAgent, `{"id": "t-1"}`},
		{"send_to_agent instruction empty", brain.ToolSendToAgent, `{"id": "t-1", "instruction": ""}`},
		{"mark_blocked reason omitted", brain.ToolMarkBlocked, `{"id": "t-1"}`},
		{"mark_blocked reason whitespace", brain.ToolMarkBlocked, `{"id": "t-1", "reason": "   "}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeBoard{}
			fs := &fakeSay{}
			svc := newTestService(fb, fs, &fakeConvo{}, &scriptedLLM{})

			call := brain.ToolCall{ID: "req-empty", Name: tc.tool, Input: []byte(tc.input)}
			result := svc.Dispatch(context.Background(), call)

			if !result.IsError {
				t.Fatalf("ToolResult.IsError = false, want true for %s with empty required field", tc.tool)
			}
			if calls := fb.recordedCalls(); len(calls) != 0 {
				t.Errorf("empty required field must not reach the board; recorded %v", calls)
			}
			if said := fs.said(); len(said) != 0 {
				t.Errorf("empty required field must not reach Say; said %v", said)
			}
		})
	}
}

// TestDispatch_RetractUpdate_RoutesToNotificationStore pins retract_update's
// mapping (08 §7): notification_id reaches NotificationStore.RetractNotification.
func TestDispatch_RetractUpdate_RoutesToNotificationStore(t *testing.T) {
	fn := &fakeNotifications{}
	svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "ru-1", brain.ToolRetractUpdate, brain.RetractUpdateInput{NotificationID: 42})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("ToolResult.IsError = true, want false (Content: %q)", result.Content)
	}
	if got := fn.retracted(); len(got) != 1 || got[0] != 42 {
		t.Errorf("retracted() = %v, want [42]", got)
	}
}

// TestDispatch_NotificationErrorFedBack pins that a NotificationStore error
// comes back as an error ToolResult, not a Go error (06 §5/§8 applied to the
// 08 feed tools).
func TestDispatch_NotificationErrorFedBack(t *testing.T) {
	wantErr := board.ErrNotFound // any error value; asserting it round-trips verbatim
	fn := &fakeNotifications{postErr: wantErr}
	svc := newTestServiceN(&fakeBoard{}, &fakeSay{}, fn, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "pu-err", brain.ToolPostUpdate, brain.PostUpdateInput{Body: "x"})
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("ToolResult.IsError = false, want true")
	}
	if result.Content != wantErr.Error() {
		t.Errorf("Content = %q, want verbatim %q", result.Content, wantErr.Error())
	}
}

// argsEqual compares recorded call args loosely enough to handle
// board.ShapePatch's pointer fields (compare pointee values, not addresses).
func argsEqual(got, want any) bool {
	switch w := want.(type) {
	case board.ShapePatch:
		g, ok := got.(board.ShapePatch)
		if !ok {
			return false
		}
		return ptrStrEq(g.Title, w.Title) && ptrStrEq(g.Body, w.Body) && ptrIntEq(g.Priority, w.Priority)
	default:
		return got == want
	}
}

func ptrStrEq(a, b *string) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func ptrIntEq(a, b *int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

// TestDispatch_BoardErrorFedBackVerbatim pins 06 §5/§6/§8: a typed Board API
// error (here board.ErrInvalidTransition, the idempotency-rule trigger, 06
// §6) becomes a ToolResult with IsError=true and Content equal to the
// error's Error() text verbatim — never a Go error return, never summarized.
func TestDispatch_BoardErrorFedBackVerbatim(t *testing.T) {
	wantErr := &board.ErrInvalidTransition{From: board.StateWorking, Attempted: opMarkReady}

	fb := &fakeBoard{
		markReadyFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, wantErr
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "err-1", brain.ToolMarkReady, brain.MarkReadyInput{ID: ticketT1})
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("ToolResult.IsError = false, want true")
	}
	if result.ToolCallID != "err-1" {
		t.Errorf("ToolResult.ToolCallID = %q, want %q", result.ToolCallID, "err-1")
	}
	if result.Content != wantErr.Error() {
		t.Errorf("ToolResult.Content = %q, want verbatim %q", result.Content, wantErr.Error())
	}
}

// TestDispatch_NotFoundErrorFedBackVerbatim covers the other typed board
// error (06 §8: "a typed Board API error ... fed back into the loop").
func TestDispatch_NotFoundErrorFedBackVerbatim(t *testing.T) {
	fb := &fakeBoard{
		acceptToDoneFn: func(ctx context.Context, id board.TicketID) (board.Ticket, error) {
			return board.Ticket{}, board.ErrNotFound
		},
	}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	call := newToolCall(t, "err-2", brain.ToolAcceptToDone, brain.AcceptToDoneInput{ID: "missing"})
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("ToolResult.IsError = false, want true")
	}
	if result.Content != board.ErrNotFound.Error() {
		t.Errorf("ToolResult.Content = %q, want verbatim %q", result.Content, board.ErrNotFound.Error())
	}
}

// TestDispatch_UnknownToolName pins 06 §8's "unknown tool name" failure
// mode: Dispatch must not panic or crash the pass; it returns an error
// ToolResult distinct from the not-implemented scaffold text, and it must
// not have called any port.
func TestDispatch_UnknownToolName(t *testing.T) {
	fb := &fakeBoard{}
	fs := &fakeSay{}
	svc := newTestService(fb, fs, &fakeConvo{}, &scriptedLLM{})

	call := brain.ToolCall{ID: "unk-1", Name: brain.ToolName("delete_universe"), Input: []byte(`{}`)}
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("ToolResult.IsError = false, want true for an unknown tool name")
	}
	if result.ToolCallID != "unk-1" {
		t.Errorf("ToolResult.ToolCallID = %q, want %q", result.ToolCallID, "unk-1")
	}
	if strings.Contains(strings.ToLower(result.Content), "not implemented") {
		t.Errorf("ToolResult.Content = %q: looks like the scaffold's generic "+
			"not-implemented stub, not a real unknown-tool error", result.Content)
	}
	if len(fb.recordedCalls()) != 0 {
		t.Errorf("an unknown tool must not reach any BoardAPI method; recorded %v", fb.recordedCalls())
	}
	if len(fs.said()) != 0 {
		t.Errorf("an unknown tool must not reach Say; recorded %v", fs.said())
	}
}

// TestDispatch_MalformedInput pins the "unparseable tool call" failure mode
// (06 §8): bad JSON for a known tool must not panic; it must come back as an
// error ToolResult, and must not reach the port with zero-value/garbage
// arguments.
func TestDispatch_MalformedInput(t *testing.T) {
	fb := &fakeBoard{}
	svc := newTestService(fb, &fakeSay{}, &fakeConvo{}, &scriptedLLM{})

	// "title" should be a string; this is a type-mismatched, unparseable
	// CreateTicketInput.
	call := brain.ToolCall{ID: "bad-1", Name: brain.ToolCreateTicket, Input: []byte(`{"title": 12345, "body": "B"}`)}
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("ToolResult.IsError = false, want true for malformed tool input")
	}
	if strings.Contains(strings.ToLower(result.Content), "not implemented") {
		t.Errorf("ToolResult.Content = %q: looks like the scaffold's generic "+
			"not-implemented stub, not a real parse error", result.Content)
	}
	if len(fb.recordedCalls()) != 0 {
		t.Errorf("malformed input must not reach BoardAPI.CreateTicket; recorded %v", fb.recordedCalls())
	}
}
