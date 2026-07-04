package brain

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// errUnknownPromptVersion is returned by RenderSystemPrompt for a version
// with no registered template.
var errUnknownPromptVersion = errors.New("brain: unknown prompt version")

// PromptVersion pins one revision of the system prompt template (06 §3, D7):
// prompt changes are behavior changes, so they ride the same review +
// golden-test gate as code. Bump this and add a new entry to promptTemplates
// rather than editing a shipped version in place, so golden tests can pin an
// exact version.
type PromptVersion int

// CurrentPromptVersion is the version a fresh pass renders (prompt.go,
// service.go). Solution phase: bump alongside prose changes.
const CurrentPromptVersion PromptVersion = 1

// PromptData is systemPromptV1's template input. Deliberately thin for now —
// the pass's actual context (board/transcript/event) is a separate user
// message (types.go's PassInput), not part of the system prompt.
type PromptData struct {
	// Role names what the model is being asked to be (01 §1: the project
	// orchestrator). Kept as data rather than hardcoded prose so tests can
	// vary it without touching the template.
	Role string
}

// systemPromptV1 is the versioned system prompt (06 §3): role, the board
// rules the model must respect, the confirmation rule, the idempotency
// rule, and the tool-usage contract. Left minimal/structured here — the
// solution phase writes the full prose per 06 §3 and pins it with golden
// tests (06 §9); this scaffold only fixes the template's shape and the
// sections a correct prompt must contain, each tagged with its owning spec
// section so nothing gets lost when the prose is filled in:
//
//   - Role & board rules: this module cannot pull (03 I6 — the system does);
//     Blocked means waiting on the human.
//   - Confirm-before-destructive (06 §7): a destructive action
//     (accept_to_done; a send_to_agent that would discard in-flight work)
//     taken in response to an *ambiguous or unexpected* instruction must be
//     preceded by a say question that ends the pass; unambiguous commands
//     execute immediately. *** THIS IS WHERE THE §7 PROSE GOES *** — the
//     rule is enforced entirely at the prompt level, never mechanically, so
//     the exact wording here is what the golden tests (06 §9) pin.
//   - Idempotency (06 §6): treat ErrInvalidTransition as already done,
//     verify against the snapshot, and continue — never retry the same call.
//   - Tool-usage contract (06 §5): act, then say a short status when the
//     user should hear something; end the turn when there is nothing left
//     to do.
const systemPromptV1 = `You are {{.Role}}. You turn one event — a message from the
user or a completed agent turn — into concrete actions on a small kanban board,
using only the tools provided.

Every turn you are given the full board snapshot, the recent conversation, and
the triggering event. Reason once over that context and act.

BOARD RULES
- Tickets move Shaping → Ready → Working → Blocked/Done. You never pull a ticket
  into Working yourself: the system pulls Ready tickets automatically when a
  worker is free. Your job on the backlog is to create and shape tickets and to
  mark them ready.
- "Blocked" means a human decision is genuinely required before the agent can
  continue — use mark_blocked only then, with a concrete reason.
- The snapshot you are given is authoritative for this turn; there is no
  board-read tool because you already have the whole board.

CONFIRM BEFORE DESTRUCTIVE ACTIONS
- Destructive actions are: accept_to_done (it releases and recycles the worker —
  the workspace is gone) and any send_to_agent whose instruction would discard
  in-flight work (e.g. "start over").
- If a destructive action is called for by an ambiguous or unexpected
  instruction, do NOT execute it. Instead, say a short question that names the
  consequence and asks the user to confirm, and end your turn. The user's answer
  arrives as the next message, and you execute the confirmed action then.
- If the command is unambiguous (e.g. "accept ticket 3"), execute it immediately.
  Do not ask for confirmation on every accept — that would make the tool unusable.

IDEMPOTENCY
- A turn may be a replay of one that already ran. If a tool returns an
  "invalid transition" error, treat that action as already done: verify against
  the board snapshot and continue. Never retry the same call — the error means
  the state you wanted is already in place.

TOOL-USAGE CONTRACT
- Take the actions the event calls for, reading each tool result before the next
  action. Multi-step work (create → shape → mark ready) is one turn.
- When the user should hear something — a status, a question, an outcome — say it
  with a short, plain-language say. End your turn when there is nothing left to do.
`

// promptTemplates holds every shipped version, keyed by PromptVersion —
// never mutate a shipped entry; add a new key instead (see PromptVersion).
var promptTemplates = map[PromptVersion]*template.Template{
	1: template.Must(template.New("system_v1").Parse(systemPromptV1)),
}

// RenderSystemPrompt renders version v of the system prompt template against
// data (06 §3, D7). Returns an error if v names no known version.
func RenderSystemPrompt(v PromptVersion, data PromptData) (string, error) {
	t, ok := promptTemplates[v]
	if !ok {
		return "", fmt.Errorf("%w: %d", errUnknownPromptVersion, v)
	}
	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("brain: render prompt version %d: %w", v, err)
	}
	return buf.String(), nil
}
