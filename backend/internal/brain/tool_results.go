package brain

import (
	"fmt"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// The ToolResult constructors every handler ends in, plus the one argument guard
// they share. Two dispositions, and which one a failure gets is the whole
// distinction the pass loop runs on (06 §8): a *malformed* call is an
// argument-shape problem the model can fix by re-issuing it, and counts toward
// the one-re-prompt-then-fail rule; an *error* result is a valid call whose
// precondition failed, fed back in the port's own words and not counted.
// Keeping the two constructors side by side is what keeps that line visible.

// ticketResult turns a Board API call's outcome into a ToolResult. A typed
// error's text is fed back verbatim (06 §6, §8); on success the model gets a
// short confirmation of the resulting state.
func ticketResult(id string, t board.Ticket, err error) ToolResult {
	if err != nil {
		return errorResult(id, err)
	}
	return ToolResult{ToolCallID: id, Content: fmt.Sprintf("ok: ticket %s is now %s", t.ID, t.State)}
}

// errorResult feeds an error back verbatim (06 §6, §8).
func errorResult(id string, err error) ToolResult {
	return ToolResult{ToolCallID: id, Content: err.Error(), IsError: true}
}

// malformedResult reports unparseable tool arguments (06 §8). Distinct
// wording from the Board API's typed errors so the model can tell an
// argument-shape problem from a precondition failure.
func malformedResult(id string, err error) ToolResult {
	return malformedResultMsg(id, err.Error())
}

// malformedResultMsg is malformedResult for a reason that is a plain string
// rather than an error value — used by requireField, whose per-tool/field
// message is composed dynamically and so is not a static sentinel error.
func malformedResultMsg(id, reason string) ToolResult {
	return ToolResult{ToolCallID: id, Content: "invalid tool arguments: " + reason, IsError: true}
}

// requireField guards a required free-text tool argument. An omitted, empty, or
// whitespace-only value parses cleanly to "" (the model dropped the field, sent
// blanks, or used the wrong key — e.g. edit_update's "body" vs say's "text"), so
// json.Unmarshal reports no error, yet passing it through is never valid: an
// empty update or blocker card shows a header with no text, an empty instruction
// wakes an agent with nothing to do. Treated as malformed (06 §8) so the pass
// re-prompts rather than silently succeeding; the message names the tool and
// field so a wrong-key call self-corrects. ok is false when the value is blank,
// in which case the returned ToolResult is the malformed feedback to send back.
func requireField(id string, tool ToolName, field, value string) (ToolResult, bool) {
	if strings.TrimSpace(value) != "" {
		return ToolResult{}, true
	}
	return malformedResultMsg(id, fmt.Sprintf("%s requires a non-empty %q field", tool, field)), false
}
