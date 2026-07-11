package scripted_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/brain/scripted"
)

// userCtx builds the round-1 user message the way service.go's runPass does:
// one user turn carrying the rendered context (transcript + event).
func userCtx(text string) brain.LLMMessage {
	return brain.LLMMessage{Role: brain.LLMRoleUser, Text: text}
}

// assistant + toolResults model a completed round the loop appends before the
// next Do call, so a test can advance the round index and feed a tool result
// (e.g. the create_ticket "ok: ticket t-1 is now shaping" line) back in.
func assistant(calls ...brain.ToolCall) brain.LLMMessage {
	return brain.LLMMessage{Role: brain.LLMRoleAssistant, Calls: calls}
}

func toolResults(contents ...string) brain.LLMMessage {
	res := make([]brain.ToolResult, len(contents))
	for i, c := range contents {
		res[i] = brain.ToolResult{ToolCallID: "call", Content: c}
	}
	return brain.LLMMessage{Role: brain.LLMRoleUser, Results: res}
}

func req(msgs ...brain.LLMMessage) brain.LLMRequest {
	return brain.LLMRequest{Messages: msgs}
}

const (
	toolCreateTicket = "create_ticket"
	toolSay          = "say"
)

func do(t *testing.T, l *scripted.LLM, r brain.LLMRequest) brain.LLMResponse {
	t.Helper()
	resp, err := l.Do(context.Background(), r)
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	return resp
}

// coreLoopScript scripts the flagship keyless journey: a "login form" message
// creates a ticket then marks it ready (two rounds), and a later
// agent.turn_completed says a line.
func coreLoopScript() scripted.Script {
	return scripted.Script{Rules: []scripted.Rule{
		{
			When: scripted.Match{Contains: []string{"The user said", "login form"}},
			Rounds: []scripted.Round{
				{Tools: []scripted.ToolStep{{
					Name:  toolCreateTicket,
					Input: json.RawMessage(`{"title":"Build a login form","body":"wire it up"}`),
				}}},
				{Tools: []scripted.ToolStep{{
					Name:  "update_ticket",
					Input: json.RawMessage(`{"id":"${ticket}","state":"ready"}`),
				}}},
				{Text: "On it — opened a ticket and marked it ready."},
			},
		},
		{
			When:   scripted.Match{Contains: []string{"agent.turn_completed"}},
			Rounds: []scripted.Round{{Text: "The work landed."}},
		},
	}}
}

func TestMatchAndRoundIndexing(t *testing.T) {
	l := scripted.New(coreLoopScript())

	// Round 0: the message matches the login-form rule; expect a create_ticket call.
	r0 := do(t, l, req(userCtx("# Event\nThe user said: build a login form please")))
	if r0.StopReason != brain.StopToolUse {
		t.Fatalf("round 0 stop reason = %q, want tool_use", r0.StopReason)
	}
	if len(r0.Calls) != 1 || r0.Calls[0].Name != toolCreateTicket {
		t.Fatalf("round 0 calls = %+v, want one create_ticket", r0.Calls)
	}

	// Round 1: one assistant turn done + the create result fed back; expect
	// update_ticket with ${ticket} resolved to the created id.
	r1 := do(t, l, req(
		userCtx("# Event\nThe user said: build a login form please"),
		assistant(r0.Calls...),
		toolResults("ok: ticket t-42 is now shaping"),
	))
	if len(r1.Calls) != 1 || r1.Calls[0].Name != "update_ticket" {
		t.Fatalf("round 1 calls = %+v, want one update_ticket", r1.Calls)
	}
	var got struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(r1.Calls[0].Input, &got); err != nil {
		t.Fatalf("round 1 input not valid JSON: %v", err)
	}
	if got.ID != "t-42" {
		t.Errorf("round 1 ticket id = %q, want t-42 (substituted)", got.ID)
	}
	if got.State != "ready" {
		t.Errorf("round 1 state = %q, want ready", got.State)
	}

	// Round 2: two assistant turns done; expect the end-turn text.
	r2 := do(t, l, req(
		userCtx("# Event\nThe user said: build a login form please"),
		assistant(r0.Calls...), toolResults("ok: ticket t-42 is now shaping"),
		assistant(r1.Calls...), toolResults("ok: ticket t-42 is now ready"),
	))
	if r2.StopReason != brain.StopEndTurn {
		t.Fatalf("round 2 stop reason = %q, want end_turn", r2.StopReason)
	}
	if r2.Text == "" || len(r2.Calls) != 0 {
		t.Errorf("round 2 = %+v, want end-turn text and no calls", r2)
	}
}

func TestTicketRecoveredFromEventText(t *testing.T) {
	// A rule that acts on the completion event references ${ticket}; the id is
	// only present in the event text, not in any tool result.
	l := scripted.New(scripted.Script{Rules: []scripted.Rule{{
		When: scripted.Match{Contains: []string{"agent.turn_completed"}},
		Rounds: []scripted.Round{{Tools: []scripted.ToolStep{{
			Name:  "post_update",
			Input: json.RawMessage(`{"body":"done","ticket":"${ticket}"}`),
		}}}},
	}}})

	resp := do(t, l, req(userCtx(
		"# Event\nagent.turn_completed on ticket t-7 (worker w-1, is_error=false, cost $0.01):\nall done",
	)))
	var got struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(resp.Calls[0].Input, &got); err != nil {
		t.Fatalf("input not valid JSON: %v", err)
	}
	if got.Ticket != "t-7" {
		t.Errorf("ticket = %q, want t-7 recovered from event text", got.Ticket)
	}
}

func TestNoMatchEndsTurn(t *testing.T) {
	l := scripted.New(coreLoopScript())
	resp := do(t, l, req(userCtx("# Event\nThe user said: something unrelated")))
	if resp.StopReason != brain.StopEndTurn {
		t.Errorf("stop reason = %q, want end_turn for an unmatched pass", resp.StopReason)
	}
	if len(resp.Calls) != 0 {
		t.Errorf("unmatched pass returned calls %+v, want none", resp.Calls)
	}
}

func TestExhaustedScriptEndsTurn(t *testing.T) {
	// A single-round rule asked for a second round must end the turn, not panic
	// or replay — an over-run pass should stop cleanly.
	l := scripted.New(scripted.Script{Rules: []scripted.Rule{{
		When:   scripted.Match{Contains: []string{"hello"}},
		Rounds: []scripted.Round{{Tools: []scripted.ToolStep{{Name: toolSay, Input: json.RawMessage(`{"text":"hi"}`)}}}},
	}}})
	resp := do(t, l, req(
		userCtx("hello there"),
		assistant(brain.ToolCall{Name: "say"}),
		toolResults("ok"),
	))
	if resp.StopReason != brain.StopEndTurn || len(resp.Calls) != 0 {
		t.Errorf("exhausted script = %+v, want a clean end_turn", resp)
	}
}

func TestSubstituteNoopWhenNoPlaceholder(t *testing.T) {
	// An input without ${ticket} is passed through byte-for-byte even when an id
	// is recoverable from the request.
	l := scripted.New(scripted.Script{Rules: []scripted.Rule{{
		When:   scripted.Match{Contains: []string{"hello"}},
		Rounds: []scripted.Round{{Tools: []scripted.ToolStep{{Name: toolSay, Input: json.RawMessage(`{"text":"fixed"}`)}}}},
	}}})
	resp := do(t, l, req(userCtx("hello, ticket t-9 is now ready")))
	if string(resp.Calls[0].Input) != `{"text":"fixed"}` {
		t.Errorf("input = %s, want it untouched", resp.Calls[0].Input)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.json")
	raw, err := json.Marshal(coreLoopScript())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if werr := os.WriteFile(path, raw, 0o600); werr != nil {
		t.Fatalf("write fixture: %v", werr)
	}

	l, err := scripted.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	resp := do(t, l, req(userCtx("# Event\nThe user said: build a login form")))
	if len(resp.Calls) != 1 || resp.Calls[0].Name != toolCreateTicket {
		t.Errorf("loaded script round 0 = %+v, want create_ticket", resp.Calls)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := scripted.Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("Load of a missing file: want error, got nil")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := scripted.Load(bad); err == nil {
		t.Error("Load of malformed JSON: want error, got nil")
	}
}
