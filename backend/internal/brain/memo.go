package brain

import "fmt"

// A pass re-sends its entire conversation on every round, so every result it
// has already been handed is still in front of the model. Asking for one a
// second time therefore buys nothing and costs a call — and, usually, a whole
// round. 4.1% of all measured tool calls were an exact repeat within a single
// pass, one of which read the same ticket four times
// (docs/brain-optimization-2026-08-08-measured.md §10), and update_ticket still
// fails on a transition the ticket's state does not accept in 10.3% of calls,
// with the failures clustering on single ids because the model re-issues the
// same refused edit rather than backing off (§11).
//
// passMemo is one pass's memory of what it has already asked for, and the
// backstop under the prompt rules that ask the model not to repeat itself. It
// answers two kinds of repeat without touching a port:
//
//   - A read whose answer cannot have changed. Reads take no action, so two
//     identical read calls can only differ if something moved the state between
//     them — and inside a pass only the pass's own actions can, since there is
//     no mid-pass snapshot refresh (06 §5).
//   - A mutation the board already refused. Nothing has moved since the
//     refusal, so the same call gets the same answer.
//
// It is per-pass state held by runPass, never by the stateless Service (06 §9):
// two passes must never see each other's memory, since between them the board
// does move.
type passMemo struct {
	// reads holds the calls whose results this pass may still reuse; only
	// membership matters, since the result itself is already in the
	// conversation (see alreadyRead).
	reads map[string]bool
	// refusals maps a refused call to the board's own wording of the refusal,
	// so a repeat is answered with that error rather than a paraphrase.
	refusals map[string]string
}

func newPassMemo() *passMemo {
	return &passMemo{reads: map[string]bool{}, refusals: map[string]string{}}
}

// readOnlyTools names the tools that only look. Everything else is treated as
// having moved something, which is the conservative direction: a needless
// re-read costs one call, a stale reused one costs a wrong decision.
//
// bash is deliberately absent even though it is a read tool in the prompt's
// sense. The done flow has it run `git fetch origin`, so a shell command is not
// guaranteed to leave the clone as it found it, and its output is time-varying
// in a way board state is not.
var readOnlyTools = map[ToolName]bool{
	ToolListTickets:     true,
	ToolGetTicket:       true,
	ToolSearchTickets:   true,
	ToolListUpdates:     true,
	ToolListAgents:      true,
	ToolGetAgentUpdates: true,
}

// memoKey identifies one exact call: the tool plus its arguments verbatim. That
// is the same identity brain.tool's args_hash logs, and the identity the
// duplicate measurement counted by — so what this suppresses is exactly what
// that query counts. Two calls that mean the same thing but serialize
// differently are, deliberately, not the same call: guessing at JSON
// equivalence would risk suppressing a call that was not a repeat.
func memoKey(call ToolCall) string {
	return string(call.Name) + "\x00" + string(call.Input)
}

// reuse answers a call this pass has already made, or reports ok=false when the
// call is new and must go to its port. Safe on a nil memo — the exported
// Dispatch has no pass behind it and passes one.
func (m *passMemo) reuse(call ToolCall) (ToolResult, bool) {
	if m == nil {
		return ToolResult{}, false
	}
	key := memoKey(call)
	if readOnlyTools[call.Name] {
		if m.reads[key] {
			return ToolResult{ToolCallID: call.ID, Content: alreadyRead(call.Name)}, true
		}
		return ToolResult{}, false
	}
	if refusal, refused := m.refusals[key]; refused {
		return ToolResult{ToolCallID: call.ID, IsError: true, Content: alreadyRefused(call.Name, refusal)}, true
	}
	return ToolResult{}, false
}

// record files what a call did, so a repeat of it can be answered from memory.
//
// Only calls that actually reached a port belong here: a malformed call is
// excluded by the caller, because 06 §8's one-re-prompt-then-fail rule needs
// the second malformed call to be dispatched and counted, not swallowed.
//
// The two invalidations are what keep the memory honest. A mutating call may
// have changed anything a read saw, so it drops every remembered read. A
// *successful* mutating call additionally drops every remembered refusal: the
// board has moved, so a call it refused before might now be allowed, and
// refusing it from memory would be inventing a precondition failure.
func (m *passMemo) record(call ToolCall, res ToolResult) {
	if m == nil {
		return
	}
	if readOnlyTools[call.Name] {
		// A failed read is not remembered: unlike a refused mutation, it says
		// nothing about the state, and the next attempt may well succeed.
		if !res.IsError {
			m.reads[memoKey(call)] = true
		}
		return
	}
	clear(m.reads)
	if res.IsError {
		m.refusals[memoKey(call)] = res.Content
		return
	}
	clear(m.refusals)
}

// alreadyRead is what a repeated read gets instead of a second trip to its
// port. It deliberately does not repeat the result: the pass re-sends its whole
// conversation every round, so the first result is already in front of the
// model, and a second copy would grow every remaining round's prefix — the very
// cost this is trying to avoid — to say what it has already been told.
func alreadyRead(name ToolName) string {
	return fmt.Sprintf("not re-read: this pass already called %s with these exact arguments, and that "+
		"result is already in front of you — reuse it. Nothing has changed it since: within one turn "+
		"the board moves only when you move it. Read again only after an action of yours changes what "+
		"this reports.", name)
}

// alreadyRefused is what a re-issued mutation gets when the board already
// refused that exact call this pass and nothing has succeeded since.
//
// The refusal is repeated verbatim rather than replaced. It is the board's own
// wording, and the idempotency rule (06 §6) is written against reading it; the
// note in front of it says only that this is the same call, and — like the
// refusal itself (allowedActions, tools.go) — names no alternative, since
// offering one at this moment invites the retry the rule forbids.
func alreadyRefused(name ToolName, refusal string) string {
	return fmt.Sprintf("not retried: this pass already called %s with these exact arguments and it was "+
		"refused, and nothing has changed since, so it would be refused again. The refusal, "+
		"unchanged:\n%s", name, refusal)
}
