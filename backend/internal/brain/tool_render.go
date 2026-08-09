package brain

import (
	"fmt"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// What a read tool's result looks like to the model — the renderers behind
// list_tickets, get_ticket, list_updates, list_agents, get_agent_updates and
// bash, plus the "allowed now" line the two ticket reads share with the roster
// (service.go's renderBoard).
//
// These are prompt surface, not plumbing: every string here is text the model
// reads and acts on, so a change to one is a behaviour change that rides the
// golden-test gate (06 D7) exactly as a change to prompt.go does. That is why
// they sit apart from the ToolResult constructors in tool_results.go, which are
// pure mechanics.

// allowedPrefix opens the line get_ticket and list_tickets carry to say what a
// ticket's current state accepts. One wording across both reads, so the model
// learns to look for it in either.
const allowedPrefix = "allowed now"

// toolPhrasing maps the board's state-gated operations onto the tool call the
// model would actually make, written in its own argument vocabulary rather than
// the board's method names. An operation with no brain tool behind it — the
// sandbox controls, which are the user's buttons in the ticket sheet, not the
// orchestrator's — is absent and drops out of the rendered line.
var toolPhrasing = map[board.Operation]string{
	board.OpShapeTicket:     "update_ticket title/body/priority",
	board.OpRequestApproval: "update_ticket approval_requested=true",
	board.OpMarkReady:       `update_ticket state="ready"`,
	board.OpSendToAgent:     "send_to_agent",
	board.OpMarkBlocked:     `update_ticket state="blocked"`,
	board.OpAcceptToDone:    `update_ticket state="done"`,
	board.OpArchiveTicket:   "delete_ticket",
}

// allowedActions renders the calls a ticket in this state accepts right now,
// from the board's own precondition table (board.State.AllowedOps) — so the
// model can check before it acts instead of learning the constraint from a
// failed update_ticket (docs/brain-optimization-2026-08-05.md §2).
//
// This is the *preventive* half only. The refusal itself is deliberately
// untouched: a transition that fails still comes back as the board's
// ErrInvalidTransition verbatim, with nothing appended, because the idempotency
// rule (06 §6) reads that error as "already done, never retry" and a hint about
// what else is available would invite exactly the retry it forbids.
func allowedActions(state board.State) string {
	ops := state.AllowedOps()
	phrasings := make([]string, 0, len(ops))
	for _, op := range ops {
		if phrasing, ok := toolPhrasing[op]; ok {
			phrasings = append(phrasings, phrasing)
		}
	}
	if len(phrasings) == 0 {
		// Unreachable for the five real states — every one of them accepts at
		// least delete_ticket — so this is only what an unrecognized state renders
		// as: claim no affordance rather than a wrong one.
		return "nothing"
	}
	return strings.Join(phrasings, ", ")
}

// joinTicketIDs renders a dependency list for the model, comma-separated.
func joinTicketIDs(ids []board.TicketID) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return strings.Join(out, ", ")
}

// formatRoster renders list_tickets' result — the compact board roster, no
// bodies (06 §4 amended). Reuses renderBoard (service.go), the same compact
// per-column layout that was injected before board reads became a tool.
func formatRoster(snap board.Snapshot) string {
	var b strings.Builder
	renderBoard(&b, snap)
	return b.String()
}

// formatTicketDetail renders get_ticket's result — one ticket in full, including
// its body (06 §4 amended).
func formatTicketDetail(t board.Ticket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ticket %s: %q\nstate=%s priority=%d", t.ID, t.Title, t.State, t.Priority)
	if t.WorkerID != nil {
		fmt.Fprintf(&b, " worker=%s", *t.WorkerID)
	}
	if t.ApprovalRequested {
		b.WriteString(" approval_requested=true")
	}
	// The user's per-ticket sandbox option. Surfaced so the brain doesn't tell them
	// the workspace is gone after accept_to_done when they asked to keep it.
	if t.KeepSandbox {
		b.WriteString(" keep_sandbox=true")
	}
	// Dependencies, and — for a queued ticket — whether they are why it has not
	// started. Without this line the model reads a Ready ticket sitting still and
	// concludes the pull is stuck (or re-queues it, which changes nothing).
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, "\ndepends_on: %s (%d not done)", joinTicketIDs(t.DependsOn), t.UnmetDependencies)
		if t.WaitingOnDependencies() {
			b.WriteString("\nwaiting: queued, but held until those are done — this is expected, not a stall")
		}
	}
	fmt.Fprintf(&b, "\n%s: %s", allowedPrefix, allowedActions(t.State))
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, "\n%s: %s", fieldDependsOn, formatDependsOn(t))
	}
	if t.BlockedReason != nil {
		fmt.Fprintf(&b, "\nblocked_reason: %s", *t.BlockedReason)
	}
	b.WriteString("\nbody:\n")
	if t.Body == "" {
		b.WriteString("(empty)")
	} else {
		b.WriteString(t.Body)
	}
	return b.String()
}

// formatDependsOn renders a ticket's dependencies for the model, naming how many
// are still outstanding when any are. Spelling out "waiting" matters: the model's
// job is to explain why a queued ticket is not moving, and a bare id list reads
// like metadata rather than a reason the pull is passing it over.
func formatDependsOn(t board.Ticket) string {
	ids := make([]string, 0, len(t.DependsOn))
	for _, d := range t.DependsOn {
		ids = append(ids, string(d))
	}
	list := strings.Join(ids, ", ")
	if t.UnmetDependencies == 0 {
		return list + " (all done)"
	}
	if t.WaitingOnDependencies() {
		return fmt.Sprintf("%s — waiting on %d, so the pull skips this ticket until they are done",
			list, t.UnmetDependencies)
	}
	return fmt.Sprintf("%s (%d not done)", list, t.UnmetDependencies)
}

// formatUpdates renders list_updates' result — one line per active feed card
// (06 §4 amended), leading with the id the model uses for edit_update /
// retract_update.
func formatUpdates(updates []Update) string {
	if len(updates) == 0 {
		return "no active updates"
	}
	var b strings.Builder
	for i, u := range updates {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "update %d [%s]", u.ID, u.Kind)
		if u.TicketID != "" {
			fmt.Fprintf(&b, " (ticket %s)", u.TicketID)
		}
		if u.ImageURL != "" {
			fmt.Fprintf(&b, " image=%s", u.ImageURL)
		}
		fmt.Fprintf(&b, ": %s", u.Body)
	}
	return b.String()
}

// sandboxHealthWarning is the leading line list_agents emits when any worker is
// errored. It names the errored count AND the healthy capacity that remains, so
// the brain keeps work flowing up to the available sandboxes rather than
// halting the whole board on the first failure (formatAgents). Args in order:
// errored, total, healthy, healthy.
const sandboxHealthWarning = "SANDBOX HEALTH: %d of %d workers are errored — this is an infrastructure " +
	"failure, not the tickets. The other %d worker(s) are healthy and can still run work: keep tickets " +
	"flowing, but do not let the number of Ready + Working tickets exceed %d, since anything the pull " +
	"binds to an errored sandbox stalls instead of running. The rest will flow once the sandboxes recover."

// formatAgents renders list_agents' result as one line per worker for the
// model, led by a health-warning line when any worker is in a terminal errored
// state. The warning tells the brain the failure is infrastructure, not the
// ticket, and names how many sandboxes are still healthy — so it starts as many
// tickets as there is available capacity instead of stopping the board, while
// not overloading the dead sandboxes (the user sees the same failure as a
// permanent error band on the dock). Provider-agnostic: keyed off the neutral
// AgentErrored status, no MECA/Amika specifics.
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "no running agents"
	}
	failing := 0
	for _, a := range agents {
		if a.Status == AgentErrored {
			failing++
		}
	}
	var b strings.Builder
	if failing > 0 {
		healthy := len(agents) - failing
		fmt.Fprintf(&b, sandboxHealthWarning+"\n", failing, len(agents), healthy, healthy)
	}
	for i, a := range agents {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "worker %s — %s", a.WorkerID, a.Status)
		if a.TicketID != "" {
			fmt.Fprintf(&b, " (ticket %s)", a.TicketID)
		}
	}
	return b.String()
}

// formatUpdate renders get_agent_updates' result for the model.
func formatUpdate(u AgentUpdate) string {
	head := fmt.Sprintf("worker %s — %s", u.WorkerID, u.Status)
	if u.IsError {
		head += " (last turn errored)"
	}
	if u.LatestOutput == "" {
		return head + "\nno completed output yet"
	}
	return head + "\nlatest output:\n" + u.LatestOutput
}

// formatRepoResult renders a RepoResult for the model: a header line carrying
// the exit code (plus timed-out / truncated flags when set) followed by the
// command's combined output.
func formatRepoResult(res RepoResult) string {
	head := fmt.Sprintf("exit %d", res.ExitCode)
	if res.TimedOut {
		head += " (timed out)"
	}
	if res.Truncated {
		head += " (output truncated)"
	}
	return head + "\n" + res.Output
}
