package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// The update_ticket facade (06 §4 amended) — the one tool whose handler is a
// small state machine rather than a single port call, which is why it has a file
// of its own rather than a place in tool_handlers.go. One patch folds the old
// shape_ticket / mark_ready / mark_blocked / accept_to_done / request_approval
// verbs, and this is where it is taken apart again: validate the argument shape,
// then route each present field to the board's own typed operation in a fixed
// order — fields, dependencies, approval, state.
//
// It is a facade, never a bypass. Every field goes through the board operation
// that owns it, so every precondition and every ErrInvalidTransition still
// holds, and the board's error text reaches the model unwrapped (06 §6) — the
// idempotency rule tells it to read that error as "already done, never retry",
// which only works if the wording is the board's own.

// The reachable update_ticket state transitions (06 §4 amended). There is
// no transition *to* shaping — a ticket starts there.
const (
	stateReady   = "ready"
	stateBlocked = "blocked"
	stateDone    = "done"
)

// doUpdateTicket is the update_ticket facade (06 §4 amended): it validates the
// patch, then routes each present field to the board's own typed operation in a
// fixed order — field edits, then approval, then the state transition — so one
// call can revise and queue a ticket. The first typed board error stops the call
// and is fed back verbatim (06 §6); when earlier steps already applied, the
// error names them so the model can re-issue only the remainder. Argument-shape
// problems (bad state, approval+state, blocked without a reason, an empty patch)
// are malformed (06 §8).
func (s *Service) doUpdateTicket(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in UpdateTicketInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	if res, ok := requireField(call.ID, ToolUpdateTicket, fieldTicketID, in.ID); !ok {
		return res, true
	}
	if res, ok := validateUpdate(call.ID, in); !ok {
		return res, true
	}
	return s.applyUpdate(ctx, call.ID, in), false
}

// validateUpdate checks an update_ticket patch's argument shape (06 §8), before
// any board call: approval_requested and state are mutually exclusive, the state
// (if any) must be a reachable transition with a reason when "blocked", and the
// patch must do something. ok is false when the returned ToolResult is the
// malformed feedback to send back.
func validateUpdate(id string, in UpdateTicketInput) (ToolResult, bool) {
	if in.ApprovalRequested != nil && *in.ApprovalRequested && in.State != nil {
		return malformedResultMsg(id, "update_ticket: approval_requested and state are mutually exclusive"), false
	}
	if res, ok := validateUpdateState(id, in); !ok {
		return res, false
	}
	if !updateHasWork(in) {
		return malformedResultMsg(id,
			"update_ticket: nothing to update — set at least one field, approval_requested, or state"), false
	}
	return ToolResult{}, true
}

// validateUpdateState validates the state field alone (a reachable transition,
// with a blocked_reason when moving to blocked). A nil state is valid.
func validateUpdateState(id string, in UpdateTicketInput) (ToolResult, bool) {
	if in.State == nil {
		return ToolResult{}, true
	}
	switch *in.State {
	case stateReady, stateBlocked, stateDone:
	default:
		return malformedResultMsg(id, fmt.Sprintf(
			"update_ticket: state must be %q, %q, or %q", stateReady, stateBlocked, stateDone)), false
	}
	if *in.State == stateBlocked && strings.TrimSpace(deref(in.BlockedReason)) == "" {
		return malformedResultMsg(id, "update_ticket: state=\"blocked\" requires a non-empty blocked_reason"), false
	}
	if *in.State == stateDone {
		sha := strings.TrimSpace(deref(in.DoneCommit))
		if sha == "" {
			return malformedResultMsg(id, "update_ticket: state=\"done\" requires done_commit "+
				"(the origin/main commit SHA carrying this ticket's work)"), false
		}
		if !isHexSHA(sha) {
			return malformedResultMsg(id, "update_ticket: done_commit must be a git commit SHA (7-40 hex chars)"), false
		}
	}
	return ToolResult{}, true
}

// isHexSHA reports whether s is a 7-40 char hex string — a plausible git commit
// SHA (abbreviated or full). Validated before the SHA reaches VerifyOnMain; the
// repo layer runs git via argv so this is defense-in-depth, not the only guard.
func isHexSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		hex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !hex {
			return false
		}
	}
	return true
}

// updateHasWork reports whether a patch actually changes anything — a field
// edit, an approval request, or a state transition (a bare approval_requested:false
// is not work).
func updateHasWork(in UpdateTicketInput) bool {
	edits := in.Title != nil || in.Body != nil || in.Priority != nil
	return edits || (in.ApprovalRequested != nil && *in.ApprovalRequested) ||
		in.State != nil || in.DependsOn != nil
}

// applyUpdate routes a validated patch to the board's typed operations in order
// — field edits, then approval, then state — and returns the final ticket
// result. A step's typed error stops the sequence and is reported with any steps
// that already applied (updateStepError).
func (s *Service) applyUpdate(ctx context.Context, id string, in UpdateTicketInput) ToolResult {
	tid := board.TicketID(in.ID)
	var t board.Ticket
	var err error
	var applied []string

	if in.Title != nil || in.Body != nil || in.Priority != nil {
		t, err = s.board.ShapeTicket(ctx, tid, board.ShapePatch{Title: in.Title, Body: in.Body, Priority: in.Priority})
		if err != nil {
			return updateStepError(id, "edit fields", applied, err)
		}
		applied = append(applied, "fields")
	}
	if step, ok := s.dependencyStep(ctx, id, tid, in, &applied, &t); !ok {
		return step
	}
	if in.ApprovalRequested != nil && *in.ApprovalRequested {
		t, err = s.board.RequestApproval(ctx, tid)
		if err != nil {
			return updateStepError(id, "request approval", applied, err)
		}
		applied = append(applied, "approval_requested")
	}
	if in.State != nil {
		// State is the final step, so it returns the tool result directly (the
		// push gate can short-circuit it) rather than falling through.
		return s.applyStateStep(ctx, id, in, applied)
	}
	return ticketResult(id, t, nil)
}

// dependencyStep runs update_ticket's depends_on step, if the patch carries one.
// It sits before approval and state so a single call can say what a ticket waits
// for and queue it: the edges must exist by the time it is markable ready, or
// the pull could take it in the window between the two.
//
// ok=false means the returned ToolResult is the failure to send back; `applied`
// and `t` are updated in place so the caller's sequence carries on unchanged.
func (s *Service) dependencyStep(
	ctx context.Context, id string, tid board.TicketID,
	in UpdateTicketInput, applied *[]string, t *board.Ticket,
) (ToolResult, bool) {
	if in.DependsOn == nil {
		return ToolResult{}, true
	}
	updated, err := s.reconcileDependencies(ctx, tid, *in.DependsOn)
	if err != nil {
		return updateStepError(id, "set depends_on", *applied, err), false
	}
	*t = updated
	*applied = append(*applied, fieldDependsOn)
	return ToolResult{}, true
}

// reconcileDependencies drives the ticket's dependency list to exactly `want`
// (0013): it reads what the ticket has now, adds what is missing, and removes
// what is no longer named. Whole-list rather than add/remove verbs, because that
// is how the model states it — "this waits for A and B" — and a delta API would
// force it to read the list first just to compute one.
//
// Adds run before removes so a rejected edge (a cycle, a missing ticket) leaves
// the existing list intact rather than half-dismantled: the model's next attempt
// then starts from the state it last saw. Duplicates in `want` are harmless —
// AddDependency is idempotent — and a self-edge is refused by the board, so
// nothing needs filtering here.
func (s *Service) reconcileDependencies(
	ctx context.Context, id board.TicketID, want []string,
) (board.Ticket, error) {
	current, err := s.reader.GetTicket(ctx, id)
	if err != nil {
		//nolint:wrapcheck // board errors reach the model verbatim (06 §6).
		return board.Ticket{}, err
	}
	wanted := dependencySet(want)
	have := dependencySet(ticketDependencyIDs(current))

	// Walk `want` in the order given, not the set: which edge is attempted first
	// decides which refusal the model sees, so it must not depend on map order.
	t := current
	for _, dep := range dedupeDependencies(want) {
		if have[dep] {
			continue
		}
		//nolint:wrapcheck // board errors reach the model verbatim (06 §6).
		if t, err = s.board.AddDependency(ctx, id, dep); err != nil {
			return board.Ticket{}, err
		}
	}
	for _, dep := range current.DependsOn {
		if wanted[dep] {
			continue
		}
		//nolint:wrapcheck // board errors reach the model verbatim (06 §6).
		if t, err = s.board.RemoveDependency(ctx, id, dep); err != nil {
			return board.Ticket{}, err
		}
	}
	return t, nil
}

// dependencySet indexes ticket ids for membership tests, ignoring blanks.
func dependencySet(ids []string) map[board.TicketID]bool {
	set := make(map[board.TicketID]bool, len(ids))
	for _, raw := range ids {
		if id := strings.TrimSpace(raw); id != "" {
			set[board.TicketID(id)] = true
		}
	}
	return set
}

// dedupeDependencies is `ids` in the order given, blanks dropped and repeats
// collapsed — so a model naming the same dependency twice writes one edge.
func dedupeDependencies(ids []string) []board.TicketID {
	seen := make(map[board.TicketID]bool, len(ids))
	out := make([]board.TicketID, 0, len(ids))
	for _, raw := range ids {
		id := board.TicketID(strings.TrimSpace(raw))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// ticketDependencyIDs is a ticket's current dependency list as plain strings,
// so it can share dependencySet with the requested list.
func ticketDependencyIDs(t board.Ticket) []string {
	out := make([]string, 0, len(t.DependsOn))
	for _, d := range t.DependsOn {
		out = append(out, string(d))
	}
	return out
}

// applyStateStep runs update_ticket's final step, the state transition. A done
// is gated: its named commit must be verifiably on origin/main (06 §7 amended)
// before the board accepts it — the refusal is fed back for the model to self-
// correct. `applied` names any earlier field/approval steps for the error path.
func (s *Service) applyStateStep(ctx context.Context, id string, in UpdateTicketInput, applied []string) ToolResult {
	var link board.CompletionLink
	if *in.State == stateDone {
		res, ok, l := s.verifyDone(ctx, id, in)
		if !ok {
			return res
		}
		link = l
	}
	t, err := s.applyState(ctx, board.TicketID(in.ID), *in.State,
		deref(in.BlockedReason), link, strings.TrimSpace(deref(in.DoneCommit)))
	if err != nil {
		return updateStepError(id, "state="+*in.State, applied, err)
	}
	return ticketResult(id, t, nil)
}

// verifyDone gates the done transition on a fresh repository check that the
// named commit satisfies the project's configured merge gate (06 §7): either it
// is on origin/main ("main" mode) or it is associated with a pull request ("pr"
// mode). It dispatches to the mode-specific check; both fail closed and feed a
// refusal back verbatim for the model to self-correct. ok=true means proceed to
// AcceptToDone; the returned CompletionLink names the verified work on GitHub
// (commit or pull request) and is carried onto the completion feed card.
func (s *Service) verifyDone(
	ctx context.Context, callID string, in UpdateTicketInput,
) (ToolResult, bool, board.CompletionLink) {
	sha := strings.TrimSpace(deref(in.DoneCommit))
	if s.cfg.GateMode == GatePR {
		return s.verifyDoneInPR(ctx, callID, sha, in.ID)
	}
	return s.verifyDoneOnMain(ctx, callID, sha, in.ID)
}

// verifyDoneOnMain gates the done on a fresh git check that sha is on
// origin/main. It fails closed: an unavailable repo shell refuses the done
// rather than silently reverting to trust. A refusal is a precondition failure
// fed back verbatim (IsError, not malformed) — the prompt tells the model to
// then message the agent to merge, and block the ticket meanwhile.
func (s *Service) verifyDoneOnMain(
	ctx context.Context, callID, sha, ticketID string,
) (ToolResult, bool, board.CompletionLink) {
	v, err := s.repo.VerifyOnMain(ctx, sha)
	if err != nil {
		return errorResult(callID, err), false, board.CompletionLink{}
	}
	if v.Unavailable {
		return ToolResult{ToolCallID: callID, IsError: true, Content: fmt.Sprintf(
			"cannot mark ticket %s done: repository verification is unavailable (%s), so the "+
				"push to origin/main cannot be confirmed.",
			ticketID, v.Reason)}, false, board.CompletionLink{}
	}
	if !v.OnMain {
		return ToolResult{ToolCallID: callID, IsError: true, Content: fmt.Sprintf(
			"cannot mark ticket %s done: commit %s is not on origin/main (%s). Go back to the agent with send_to_agent "+
				"and have it commit and push this ticket's work onto origin/main; once it lands, mark the ticket done "+
				"with that commit. Do not substitute an unrelated commit that is already on main just to pass this check. "+
				"If it needs a human decision instead, set the ticket blocked.",
			ticketID, sha, v.Reason)}, false, board.CompletionLink{}
	}
	return ToolResult{}, true, board.CompletionLink{URL: v.URL, Label: v.Ref, Summary: v.Summary}
}

// verifyDoneInPR gates the done on a fresh check that sha is associated with a
// pull request (merged or not). Fails closed exactly like verifyDoneOnMain; the
// refusal steers the model to have the agent open a PR, or block the ticket.
func (s *Service) verifyDoneInPR(
	ctx context.Context, callID, sha, ticketID string,
) (ToolResult, bool, board.CompletionLink) {
	v, err := s.repo.VerifyInPR(ctx, sha)
	if err != nil {
		return errorResult(callID, err), false, board.CompletionLink{}
	}
	if v.Unavailable {
		return ToolResult{ToolCallID: callID, IsError: true, Content: fmt.Sprintf(
			"cannot mark ticket %s done: repository verification is unavailable (%s), so it "+
				"cannot be confirmed that the work is in a pull request.",
			ticketID, v.Reason)}, false, board.CompletionLink{}
	}
	if !v.InPR {
		return ToolResult{ToolCallID: callID, IsError: true, Content: fmt.Sprintf(
			"cannot mark ticket %s done: commit %s is not in any pull request (%s). Have the agent open a "+
				"pull request for the work, then accept it — or set the ticket blocked if it needs a decision.",
			ticketID, sha, v.Reason)}, false, board.CompletionLink{}
	}
	return ToolResult{}, true, board.CompletionLink{URL: v.URL, Label: v.Ref, Summary: v.Summary}
}

// applyState routes one state transition to its board operation. The board's
// typed error is returned unwrapped on purpose: applyUpdate feeds it back to the
// model verbatim (06 §6), and wrapping it would corrupt the idempotency signal
// the prompt tells the model to read.
//
//nolint:wrapcheck // board error is fed back verbatim (06 §6), never wrapped.
func (s *Service) applyState(
	ctx context.Context, id board.TicketID, state, blockedReason string,
	link board.CompletionLink, doneCommit string,
) (board.Ticket, error) {
	switch state {
	case stateReady:
		return s.board.MarkReady(ctx, id)
	case stateBlocked:
		return s.board.MarkBlocked(ctx, id, blockedReason)
	default: // stateDone — validateUpdate already rejected any other value
		return s.board.AcceptToDone(ctx, id, link, doneCommit)
	}
}

// updateStepError reports a failed update_ticket step, naming any steps that
// already applied so the model can re-issue only the remainder (06 §6). Fed back
// verbatim; not malformed (the arguments were valid, a precondition failed).
func updateStepError(id, step string, applied []string, err error) ToolResult {
	msg := err.Error()
	if len(applied) > 0 {
		msg = fmt.Sprintf("applied %s, then failed to %s: %s", strings.Join(applied, "+"), step, err.Error())
	}
	return ToolResult{ToolCallID: id, Content: msg, IsError: true}
}

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
