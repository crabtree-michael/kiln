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

// TestToolSet_IsExactlyFourteenToolsInFixedOrder pins the CRUD-consolidated tool
// set (06 §4 amended): clean CRUD over tickets and feed updates, plus the agent
// read seam, send/say, and bash — exactly these fourteen, in this order (order
// matters for prompt-cache friendliness and golden fixtures, 06 §4/§9). No pull
// tool; board reads are now tools (list_tickets/get_ticket), not injection.
func TestToolSet_IsExactlyFourteenToolsInFixedOrder(t *testing.T) {
	want := []brain.ToolName{
		brain.ToolCreateTicket,
		brain.ToolListTickets,
		brain.ToolGetTicket,
		brain.ToolUpdateTicket,
		brain.ToolDeleteTicket,
		brain.ToolSendToAgent,
		brain.ToolSay,
		brain.ToolPostUpdate,
		brain.ToolListUpdates,
		brain.ToolEditUpdate,
		brain.ToolRetractUpdate,
		brain.ToolListAgents,
		brain.ToolGetAgentUpdates,
		brain.ToolBash,
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

// TestSystemPrompt_HasToolGuidance pins that the shipped prompt names the CRUD
// and feed tools it guides the model to use. It asserts tool-name presence, not
// literal prose (06 D7). The agent read tools (list_agents, get_agent_updates)
// are intentionally NOT asserted here — they are self-describing via their tool
// schemas, not the prompt prose.
func TestSystemPrompt_HasToolGuidance(t *testing.T) {
	got, err := brain.RenderSystemPrompt(brain.PromptData{Role: "Kiln"})
	if err != nil {
		t.Fatalf("RenderSystemPrompt: %v", err)
	}
	tools := []string{
		"list_tickets", "get_ticket", "update_ticket", "delete_ticket",
		"post_update", "edit_update", "retract_update", "bash",
	}
	for _, tool := range tools {
		if !strings.Contains(got, tool) {
			t.Errorf("system prompt is missing tool guidance for %s:\n%s", tool, got)
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
			name: "get_ticket",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c2", brain.ToolGetTicket, brain.GetTicketInput{ID: ticketT1})
			},
			wantMethod: "GetTicket",
			wantArgs:   []any{board.TicketID(ticketT1)},
		},
		{
			name: "update_ticket fields -> ShapeTicket",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c3", brain.ToolUpdateTicket, brain.UpdateTicketInput{
					ID: ticketT1, Title: &newTitle, Body: &newBody, Priority: &priority,
				})
			},
			wantMethod: methodShapeTicket,
			wantArgs: []any{board.TicketID(ticketT1), board.ShapePatch{
				Title: &newTitle, Body: &newBody, Priority: &priority,
			}},
		},
		{
			name: "update_ticket state=ready -> MarkReady",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c4", brain.ToolUpdateTicket, brain.UpdateTicketInput{
					ID: "t-2", State: new("ready"),
				})
			},
			wantMethod: methodMarkReady,
			wantArgs:   []any{board.TicketID("t-2")},
		},
		{
			name: "update_ticket state=blocked -> MarkBlocked",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c5", brain.ToolUpdateTicket, brain.UpdateTicketInput{
					ID: "t-4", State: new("blocked"), BlockedReason: new("need a decision"),
				})
			},
			wantMethod: "MarkBlocked",
			wantArgs:   []any{board.TicketID("t-4"), "need a decision"},
		},
		{
			name: "update_ticket state=done -> AcceptToDone",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c6", brain.ToolUpdateTicket, brain.UpdateTicketInput{
					ID: "t-5", State: new("done"), DoneCommit: new("a1b2c3d"),
				})
			},
			// The shared fakeRepo (below) verifies OnMain, so the done proceeds.
			wantMethod: methodAcceptToDone,
			wantArgs:   []any{board.TicketID("t-5")},
		},
		{
			name: "update_ticket approval_requested -> RequestApproval",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c7", brain.ToolUpdateTicket, brain.UpdateTicketInput{
					ID: "t-6", ApprovalRequested: new(true),
				})
			},
			wantMethod: "RequestApproval",
			wantArgs:   []any{board.TicketID("t-6")},
		},
		{
			name: "delete_ticket -> ArchiveTicket",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c8", brain.ToolDeleteTicket, brain.DeleteTicketInput{ID: ticketT7})
			},
			wantMethod: "ArchiveTicket",
			wantArgs:   []any{board.TicketID(ticketT7)},
		},
		{
			name: "send_to_agent",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c9", brain.ToolSendToAgent, brain.SendToAgentInput{ID: "t-3", Instruction: "keep going"})
			},
			wantMethod: "SendToAgent",
			wantArgs:   []any{board.TicketID("t-3"), "keep going"},
		},
		{
			name: "say",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c10", brain.ToolSay, brain.SayInput{Text: sayHello})
			},
			wantMethod: "Say",
		},
		{
			name: "bash",
			call: func(t *testing.T) brain.ToolCall {
				t.Helper()
				return newToolCall(t, "c11", brain.ToolBash, brain.BashInput{Command: "git fetch"})
			},
			wantMethod: "Run",
			wantArgs:   []any{"git fetch"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb := &fakeBoard{}
			fs := &fakeSay{}
			// OnMain=true so the state=done case clears the push gate; other cases
			// never call VerifyOnMain.
			fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
			svc := newTestServiceR(fb, fs, &fakeConvo{}, fr, &scriptedLLM{})

			call := tc.call(t)
			result := svc.Dispatch(context.Background(), call)

			if result.ToolCallID != call.ID {
				t.Errorf("ToolResult.ToolCallID = %q, want %q", result.ToolCallID, call.ID)
			}
			if result.IsError {
				t.Errorf("ToolResult.IsError = true, want false (Content: %q)", result.Content)
			}

			if tc.wantMethod == "Say" {
				if got := fs.said(); len(got) != 1 || got[0] != sayHello {
					t.Errorf("fakeSay.said() = %v, want [\"hello\"]", got)
				}
				return
			}

			if tc.wantMethod == "Run" {
				if fr.gotCommand != tc.wantArgs[0] {
					t.Errorf("RepoShell.Run command = %q, want %q", fr.gotCommand, tc.wantArgs[0])
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
	ticket := ticketT7
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
// instruction, update_ticket's blocked_reason when moving to blocked — is
// rejected when omitted, empty, or whitespace-only (requireField / validateUpdate).
// Such a value parses cleanly to "", so it is not a JSON error; it must still
// come back as an error ToolResult and never reach the board or Say — the same
// silent-empty gap that produced bodyless update cards. create_ticket's body is
// intentionally optional (the board allows it), so it is not covered here.
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
		{"update_ticket blocked reason omitted", brain.ToolUpdateTicket, `{"id": "t-1", "state": "blocked"}`},
		{
			"update_ticket blocked reason whitespace", brain.ToolUpdateTicket,
			`{"id": "t-1", "state": "blocked", "blocked_reason": "   "}`,
		},
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

// TestDispatch_ListAgents_RoutesToInspector pins list_agents' mapping to
// AgentInspector.ListAgents (06 §4 amended).
func TestDispatch_ListAgents_RoutesToInspector(t *testing.T) {
	fi := &fakeInspector{list: []brain.AgentInfo{
		{WorkerID: workerW1, TicketID: "tkt-1", Status: brain.AgentBuilding},
		{WorkerID: "w-2", Status: brain.AgentIdle},
	}}
	svc := newTestServiceI(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fi, &scriptedLLM{})

	call := newToolCall(t, "la-1", brain.ToolListAgents, brain.ListAgentsInput{})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("IsError = true, want false (%q)", result.Content)
	}
	if !strings.Contains(result.Content, workerW1) || !strings.Contains(result.Content, "tkt-1") ||
		!strings.Contains(result.Content, "w-2") {
		t.Errorf("Content = %q, want both workers", result.Content)
	}
}

// TestDispatch_GetAgentUpdates_RoutesToInspector pins get_agent_updates'
// mapping to AgentInspector.GetAgentUpdates(worker_id) (06 §4 amended).
func TestDispatch_GetAgentUpdates_RoutesToInspector(t *testing.T) {
	fi := &fakeInspector{update: brain.AgentUpdate{
		WorkerID: workerW1, Status: brain.AgentBuilding, LatestOutput: "all done",
	}}
	svc := newTestServiceI(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fi, &scriptedLLM{})

	call := newToolCall(t, "gu-1", brain.ToolGetAgentUpdates, brain.GetAgentUpdatesInput{WorkerID: workerW1})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("IsError = true, want false (%q)", result.Content)
	}
	if fi.gotWorkerID != workerW1 {
		t.Errorf("GetAgentUpdates worker = %q, want w-1", fi.gotWorkerID)
	}
	if !strings.Contains(result.Content, "all done") {
		t.Errorf("Content = %q, want the latest output", result.Content)
	}
}

// TestDispatch_Bash_RoutesToRepoShell pins bash's mapping to RepoShell.Run:
// the command reaches the port, and the RepoResult renders into the tool
// result — the exit-code header and the output both appear, and a non-zero
// exit is fed back as normal (non-error) content, not a tool error.
func TestDispatch_Bash_RoutesToRepoShell(t *testing.T) {
	fr := &fakeRepo{result: brain.RepoResult{
		Output:   "abc123 fix the thing",
		ExitCode: 1,
	}}
	svc := newTestServiceR(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})

	call := newToolCall(t, "bash-1", brain.ToolBash,
		brain.BashInput{Command: "git log --oneline origin/feature"})
	result := svc.Dispatch(context.Background(), call)

	if result.IsError {
		t.Fatalf("IsError = true, want false — a non-zero exit is normal content (%q)", result.Content)
	}
	if fr.gotCommand != "git log --oneline origin/feature" {
		t.Errorf("Run command = %q, want the bash input verbatim", fr.gotCommand)
	}
	if !strings.Contains(result.Content, "exit 1") {
		t.Errorf("Content = %q, want the exit-code header", result.Content)
	}
	if !strings.Contains(result.Content, "abc123 fix the thing") {
		t.Errorf("Content = %q, want the command output", result.Content)
	}
}

// TestDispatch_Bash_UnavailableIsError pins that an Unavailable RepoResult (the
// clone could not be set up) renders as an error tool result naming the reason,
// rather than erroring the pass.
func TestDispatch_Bash_UnavailableIsError(t *testing.T) {
	fr := &fakeRepo{result: brain.RepoResult{Unavailable: true, Reason: "repo not configured"}}
	svc := newTestServiceR(&fakeBoard{}, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})

	call := newToolCall(t, "bash-2", brain.ToolBash, brain.BashInput{Command: "git status"})
	result := svc.Dispatch(context.Background(), call)

	if !result.IsError {
		t.Fatalf("IsError = false, want true for an unavailable repo shell")
	}
	if !strings.Contains(result.Content, "repo not configured") {
		t.Errorf("Content = %q, want the unavailable reason", result.Content)
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

	call := newToolCall(t, "err-1", brain.ToolUpdateTicket, brain.UpdateTicketInput{ID: ticketT1, State: new("ready")})
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
	// OnMain=true so the done clears the push gate and reaches the board, where the
	// not-found error is what this test pins.
	fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
	svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})

	call := newToolCall(t, "err-2", brain.ToolUpdateTicket,
		brain.UpdateTicketInput{ID: "missing", State: new("done"), DoneCommit: new("abc1234")})
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

// TestUpdateTicketDoneGate covers the push gate on state="done": the brain must
// supply a done_commit SHA that VerifyOnMain confirms is on origin/main before
// the board accepts the ticket (06 §7 amended, prompt.go). A missing/invalid
// SHA is malformed; an unverified SHA (not on main, or repo unavailable) is
// refused without touching the board.
func TestUpdateTicketDoneGate(t *testing.T) {
	const sha = "a1b2c3d4e5f6"
	done := func(t *testing.T, id string, commit *string) brain.ToolCall {
		t.Helper()
		return newToolCall(t, "cd", brain.ToolUpdateTicket,
			brain.UpdateTicketInput{ID: id, State: new("done"), DoneCommit: commit})
	}
	acceptedToDone := func(fb *fakeBoard) bool {
		for _, c := range fb.recordedCalls() {
			if c.Method == methodAcceptToDone {
				return true
			}
		}
		return false
	}

	t.Run("missing done_commit is malformed, board untouched", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
		svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})
		res := svc.Dispatch(context.Background(), done(t, "t-1", nil))
		if !res.IsError {
			t.Fatalf("expected IsError for missing done_commit; got %+v", res)
		}
		if len(fb.recordedCalls()) != 0 {
			t.Fatalf("board must not be touched; recorded %v", fb.recordedCalls())
		}
		if fr.gotSHA != "" {
			t.Errorf("VerifyOnMain must not run on a malformed call; got sha %q", fr.gotSHA)
		}
	})

	t.Run("non-hex done_commit is malformed", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
		svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})
		res := svc.Dispatch(context.Background(), done(t, "t-1", new("not a sha!")))
		if !res.IsError {
			t.Fatalf("expected IsError for non-hex done_commit; got %+v", res)
		}
		if len(fb.recordedCalls()) != 0 {
			t.Fatalf("board must not be touched; recorded %v", fb.recordedCalls())
		}
	})

	t.Run("commit not on origin/main is refused, board untouched", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{OnMain: false, Reason: "not an ancestor of origin/main"}}
		svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})
		res := svc.Dispatch(context.Background(), done(t, "t-1", new(sha)))
		if !res.IsError {
			t.Fatalf("expected IsError when commit is not on main; got %+v", res)
		}
		if fr.gotSHA != sha {
			t.Errorf("VerifyOnMain sha = %q, want %q", fr.gotSHA, sha)
		}
		if acceptedToDone(fb) {
			t.Fatalf("AcceptToDone must NOT be called when commit is not on main")
		}
	})

	t.Run("repo shell unavailable fails closed", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{Unavailable: true, Reason: "repo not configured"}}
		svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})
		res := svc.Dispatch(context.Background(), done(t, "t-1", new(sha)))
		if !res.IsError {
			t.Fatalf("expected IsError (fail closed) when repo unavailable; got %+v", res)
		}
		if acceptedToDone(fb) {
			t.Fatalf("AcceptToDone must NOT be called when verification is unavailable")
		}
	})

	t.Run("verified commit on origin/main accepts to done", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{OnMain: true}}
		svc := newTestServiceR(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{})
		res := svc.Dispatch(context.Background(), done(t, "t-9", new(sha)))
		if res.IsError {
			t.Fatalf("expected success for a verified on-main commit; got error %q", res.Content)
		}
		if fr.gotSHA != sha {
			t.Errorf("VerifyOnMain sha = %q, want %q", fr.gotSHA, sha)
		}
		calls := fb.recordedCalls()
		if len(calls) != 1 || calls[0].Method != methodAcceptToDone {
			t.Fatalf("expected a single AcceptToDone; got %v", calls)
		}
	})

	t.Run("pr gate: work not in a PR is refused, board untouched", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{InPR: false, Reason: "not associated with any pull request"}}
		svc := newTestServiceRGate(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{}, brain.GatePR)
		res := svc.Dispatch(context.Background(), done(t, "t-1", new(sha)))
		if !res.IsError {
			t.Fatalf("expected IsError when work is not in a PR; got %+v", res)
		}
		if fr.gotSHA != sha {
			t.Errorf("VerifyInPR sha = %q, want %q", fr.gotSHA, sha)
		}
		if acceptedToDone(fb) {
			t.Fatalf("AcceptToDone must NOT be called when work is not in a PR")
		}
	})

	t.Run("pr gate: work in a PR accepts to done", func(t *testing.T) {
		fb := &fakeBoard{}
		fr := &fakeRepo{verify: brain.RepoVerify{InPR: true}}
		svc := newTestServiceRGate(fb, &fakeSay{}, &fakeConvo{}, fr, &scriptedLLM{}, brain.GatePR)
		res := svc.Dispatch(context.Background(), done(t, "t-9", new(sha)))
		if res.IsError {
			t.Fatalf("expected success for work in a PR; got error %q", res.Content)
		}
		calls := fb.recordedCalls()
		if len(calls) != 1 || calls[0].Method != methodAcceptToDone {
			t.Fatalf("expected a single AcceptToDone; got %v", calls)
		}
	})
}
