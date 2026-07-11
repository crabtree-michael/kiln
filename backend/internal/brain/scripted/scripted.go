// Package scripted is the fixture-driven brain.LLM selected by
// KILN_BRAIN_MODE=scripted — the keyless end-to-end counterpart to the coding
// agent's AGENT_MODE=mock (05 §8). It replaces only the model's judgment: given
// a rendered LLMRequest it plays back a canned tool-call sequence, so the whole
// orchestration loop — the real bounded tool loop (06 §5), dispatch, the board
// state machine, the transactional outbox, and the event path — runs with no
// Anthropic key. It exists so the /tests e2e suite can drive the live loop
// deterministically in CI (design docs/keyless-e2e-tests-design.md §3.1).
//
// It is STATELESS: every response is derived from the request alone (which rule
// matches the round-1 context, and which round we are on = the number of
// assistant turns already in the conversation). Nothing is remembered between
// calls, so one instance is safe across concurrent per-project brains and its
// output is identical on replay.
//
// Runtime-generated ids (a freshly created ticket's id, the ticket named by an
// agent.turn_completed event) are not known when a fixture is authored, so a
// tool input may contain the placeholder ${ticket}; Do substitutes it with the
// ticket id it recovers from the tool results and event text already in the
// request. That is what lets a two-step "create then mark ready" fixture work.
package scripted

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/brain"
)

// Script is a fixture: an ordered list of rules. The first rule whose match
// fires against a pass's round-1 context drives that pass.
type Script struct {
	Rules []Rule `json:"rules"`
}

// Rule maps a matched pass to a fixed sequence of rounds. Rounds[i] is the
// response for the i-th LLM round of that pass (round 0 is the first call, made
// against the freshly rendered context; later rounds see the prior rounds' tool
// results). A pass that runs past the scripted rounds gets StopEndTurn, exactly
// like the golden-test scriptedLLM's under-scripted fallback (06 §9), so an
// incomplete fixture fails loudly (a ticket that never moves) rather than
// hanging.
type Rule struct {
	When   Match   `json:"when"`
	Rounds []Round `json:"rounds"`
}

// Match selects a rule. Every substring in Contains must appear in the pass's
// round-1 context (the transcript + rendered event, service.go's
// renderContext) for the rule to fire. An empty Contains matches any pass — use
// it only as a trailing catch-all.
type Match struct {
	Contains []string `json:"contains"`
}

// Round is one canned LLM response. Tools present ⇒ a StopToolUse round that
// calls them in order; Tools empty ⇒ a StopEndTurn round carrying Text (the
// pass ends). Text may accompany a tool round too (assistant prose alongside
// the calls).
type Round struct {
	Text  string     `json:"text,omitempty"`
	Tools []ToolStep `json:"tools,omitempty"`
}

// ToolStep is one tool call in a round. Input is the raw JSON arguments object,
// passed to the brain's dispatch verbatim after ${ticket} substitution — so it
// must match the tool's input schema (tools.go), e.g. create_ticket wants
// {"title","body"} and update_ticket wants {"id","state",…}.
type ToolStep struct {
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ticketPlaceholder is substituted in a tool input with the ticket id recovered
// from the request (see recoverTicketID). It lets a fixture reference a ticket
// whose id the board only mints at create time.
const ticketPlaceholder = "${ticket}"

// ticketIDPatterns recover a ticket id from the text already in a request, most
// recent winning (recoverTicketID scans in order and keeps the last match):
//   - a create_ticket / update_ticket result — "ok: ticket t-1 is now shaping"
//   - an agent.turn_completed event — "agent.turn_completed on ticket t-1 (…"
//
// Both are produced by internal/brain (ticketResult, renderEvent); this package
// keys off that rendered text rather than importing board/runtime types.
var ticketIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ticket (\S+) is now`),
	regexp.MustCompile(`on ticket (\S+)`),
}

// LLM is the scripted brain.LLM. Construct it with New (in-memory Script) or
// Load (a fixture file).
type LLM struct {
	rules []Rule
}

var _ brain.LLM = (*LLM)(nil)

// New returns a scripted LLM over an in-memory script (used by tests).
func New(s Script) *LLM { return &LLM{rules: s.Rules} }

// Load reads a JSON fixture from path and returns the scripted LLM. It fails
// fast — the composition root loads the fixture once at startup, so a bad path
// or malformed fixture stops the process rather than surfacing as a dead brain
// on the first event.
func Load(path string) (*LLM, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied fixture path, dev/e2e only.
	if err != nil {
		return nil, fmt.Errorf("scripted: read fixture %q: %w", path, err)
	}
	var s Script
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("scripted: parse fixture %q: %w", path, err)
	}
	return New(s), nil
}

// Do plays the scripted response for one round (brain.LLM). It finds the rule
// matching this pass's round-1 context, indexes into its rounds by how many
// assistant turns the conversation already holds, and returns that round —
// substituting ${ticket} in any tool input from the ids in the request. No
// match, or a pass that runs past its script, ends the turn.
func (l *LLM) Do(_ context.Context, req brain.LLMRequest) (brain.LLMResponse, error) {
	rule := l.match(req)
	if rule == nil {
		return brain.LLMResponse{StopReason: brain.StopEndTurn}, nil
	}
	round := countAssistantTurns(req.Messages)
	if round >= len(rule.Rounds) {
		return brain.LLMResponse{StopReason: brain.StopEndTurn}, nil
	}
	r := rule.Rounds[round]
	if len(r.Tools) == 0 {
		return brain.LLMResponse{StopReason: brain.StopEndTurn, Text: r.Text}, nil
	}

	ticketID := recoverTicketID(req)
	calls := make([]brain.ToolCall, 0, len(r.Tools))
	for i, step := range r.Tools {
		calls = append(calls, brain.ToolCall{
			ID:    fmt.Sprintf("call-%d-%d", round, i),
			Name:  brain.ToolName(step.Name),
			Input: substitute(step.Input, ticketID),
		})
	}
	return brain.LLMResponse{StopReason: brain.StopToolUse, Text: r.Text, Calls: calls}, nil
}

// match returns the first rule whose Contains substrings all appear in the
// pass's anchor text (the round-1 user context, stable across every round of a
// pass), or nil when none fires.
func (l *LLM) match(req brain.LLMRequest) *Rule {
	anchor := anchorText(req)
	for i := range l.rules {
		if containsAll(anchor, l.rules[i].When.Contains) {
			return &l.rules[i]
		}
	}
	return nil
}

// anchorText is the round-1 user context (renderContext's output): the first
// user message's Text. It is present in every round's request (the loop only
// appends after it), so matching on it is stable for the whole pass.
func anchorText(req brain.LLMRequest) string {
	for _, m := range req.Messages {
		if m.Role == brain.LLMRoleUser && m.Text != "" {
			return m.Text
		}
	}
	return ""
}

// countAssistantTurns is the current round index: the number of assistant
// messages already in the conversation (runPass appends one per completed
// round), so round 0 is the first call.
func countAssistantTurns(msgs []brain.LLMMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Role == brain.LLMRoleAssistant {
			n++
		}
	}
	return n
}

// recoverTicketID scans every text the request carries — user context, assistant
// prose, and tool-result contents — for a ticket id, returning the last match
// (the most recent tool result wins over the older event text). Empty when none.
func recoverTicketID(req brain.LLMRequest) string {
	id := ""
	consider := func(s string) {
		for _, re := range ticketIDPatterns {
			if m := re.FindAllStringSubmatch(s, -1); len(m) > 0 {
				id = m[len(m)-1][1]
			}
		}
	}
	for _, m := range req.Messages {
		if m.Text != "" {
			consider(m.Text)
		}
		for _, r := range m.Results {
			consider(r.Content)
		}
	}
	return id
}

// substitute replaces ${ticket} in a raw tool input with id. A no-op when the
// input has no placeholder or no id was recovered, so inputs that hardcode an
// id (or need none) pass through untouched.
func substitute(input json.RawMessage, id string) json.RawMessage {
	if id == "" || !strings.Contains(string(input), ticketPlaceholder) {
		return input
	}
	return json.RawMessage(strings.ReplaceAll(string(input), ticketPlaceholder, id))
}

// containsAll reports whether every substring in subs appears in s. An empty
// subs matches (a catch-all rule).
func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
