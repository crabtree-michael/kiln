package brain

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

// toolArgsSummaryBytes / toolResultSummaryBytes bound how much of a tool call's
// raw arguments and result text a log line carries. Arguments hold the ticket
// id and (for send_to_agent) the full instruction; the summary keeps the head
// and tail and args_hash gives the exact identity for spotting a redelivered
// stale instruction (ticket 841fb6cc).
const (
	toolArgsSummaryBytes   = 1024
	toolResultSummaryBytes = 512
)

// Dispatch executes one tool call against the injected ports — the
// tool -> port-method mapping (06 §4 amended):
//
//	create_ticket  -> BoardAPI.CreateTicket(title, body)
//	list_tickets   -> BoardReader.GetBoard()                          (compact roster)
//	get_ticket     -> BoardReader.GetTicket(id)                       (full body)
//	search_tickets -> BoardReader.GetBoard(), filtered by keyword     (one page)
//	update_ticket  -> facade: ShapeTicket / RequestApproval / MarkReady /
//	                  MarkBlocked / AcceptToDone, routed per patch field
//	delete_ticket  -> BoardAPI.ArchiveTicket(id)
//	send_to_agent  -> BoardAPI.SendToAgent(id, instruction)
//	say              -> Say.Say(text)
//	post_update      -> NotificationStore.PostNotification(kind, ...) (08 §7)
//	list_updates     -> FeedReader.ListUpdates()                      (08 §7)
//	edit_update      -> NotificationStore.EditNotification(id, ...)   (08 §7)
//	retract_update   -> NotificationStore.RetractNotification(id)     (08 §7)
//	list_agents      -> AgentInspector.ListAgents()
//	get_agent_updates-> AgentInspector.GetAgentUpdates(worker_id)
//	bash             -> RepoShell.Run(command)
//
// Never returns a Go error: a tool failure — bad arguments, a typed Board
// API error, an unknown tool name — becomes a ToolResult with IsError set
// and Content carrying the failure verbatim, fed back into the loop
// (06 §5, §8). The idempotency rule (06 §6) depends on ErrInvalidTransition
// reaching the model exactly this way. See
// docs/specs/06-orchestrator-brain.md §4, §6, §8.
//
// A single call with no pass behind it, so nothing is remembered between two
// Dispatches — the per-pass memo (memo.go) belongs to runPass, and inventing a
// one-call one here would only ever dedupe a call against itself.
func (s *Service) Dispatch(ctx context.Context, call ToolCall) ToolResult {
	res, _ := s.dispatchOne(ctx, nil, call)
	return res
}

// dispatchOne routes one tool call to its handler and logs it as a structured
// board-mutating action (turn_id injected from context): the tool name, an
// args summary + content hash (ticket id lives in the args; args_hash makes a
// duplicated send_to_agent instruction greppable — the 841fb6cc smell), whether
// the pass answered it from memory rather than the port, and the outcome. It
// additionally reports whether the call was malformed — an unknown tool name or
// unparseable arguments (06 §8) — which the pass loop counts toward its
// one-re-prompt-then-fail rule.
//
// `reused` is emitted on every record, not only the suppressed ones, so the
// duplicate-rate query that measured §10 (args_hash repeated within a turn_id)
// can split what the model still emits from what actually cost a port call. The
// record stays honest about what the model asked for: a suppressed call is
// logged, because the call was made.
func (s *Service) dispatchOne(ctx context.Context, memo *passMemo, call ToolCall) (ToolResult, bool) {
	res, malformed, reused := s.routeOrReuse(ctx, memo, call)
	slog.InfoContext(ctx, "brain.tool",
		"tool", string(call.Name),
		"args", obs.Summary(string(call.Input), toolArgsSummaryBytes),
		"args_hash", obs.Hash(string(call.Input)),
		"reused", reused,
		"is_error", res.IsError,
		"result", obs.Summary(res.Content, toolResultSummaryBytes),
	)
	return res, malformed
}

// routeOrReuse answers a call from the pass's memo when it is an exact repeat
// of one already made, and otherwise routes it to its handler and files the
// outcome. A malformed call is never filed: 06 §8's one-re-prompt-then-fail
// rule needs a repeated malformed call to be dispatched and counted as
// malformed again, not suppressed into a clean result.
//
// Returns the result, whether the call was malformed, and whether it was
// answered from memory rather than a port.
func (s *Service) routeOrReuse(ctx context.Context, memo *passMemo, call ToolCall) (ToolResult, bool, bool) {
	if res, ok := memo.reuse(call); ok {
		return res, false, true
	}
	res, malformed := s.routeTool(ctx, call)
	if !malformed {
		memo.record(call, res)
	}
	return res, malformed, false
}

// routeTool is dispatchOne's flat tool → handler table, in the same order as
// the Tools table it mirrors (tool_schemas.go). Every handler lives in
// tool_handlers.go except update_ticket's, which heads the patch facade in
// update_ticket.go. A typed Board API error is *not* malformed: it is a valid
// call whose precondition failed, fed back verbatim for the model to
// self-correct (06 §6). The case count, not any branching logic, is what trips
// the complexity metric.
//
//nolint:cyclop // Flat one-case-per-tool dispatch table (06 §4, 08 §5/§7).
func (s *Service) routeTool(ctx context.Context, call ToolCall) (ToolResult, bool) {
	switch call.Name {
	case ToolCreateTicket:
		return s.doCreateTicket(ctx, call)
	case ToolListTickets:
		return s.doListTickets(ctx, call)
	case ToolGetTicket:
		return s.doGetTicket(ctx, call)
	case ToolSearchTickets:
		return s.doSearchTickets(ctx, call)
	case ToolUpdateTicket:
		return s.doUpdateTicket(ctx, call)
	case ToolDeleteTicket:
		return s.doDeleteTicket(ctx, call)
	case ToolSendToAgent:
		return s.doSendToAgent(ctx, call)
	case ToolSay:
		return s.doSay(ctx, call)
	case ToolPostUpdate:
		return s.doPostUpdate(ctx, call)
	case ToolListUpdates:
		return s.doListUpdates(ctx, call)
	case ToolEditUpdate:
		return s.doEditUpdate(ctx, call)
	case ToolRetractUpdate:
		return s.doRetractUpdate(ctx, call)
	case ToolListAgents:
		return s.doListAgents(ctx, call)
	case ToolGetAgentUpdates:
		return s.doGetAgentUpdates(ctx, call)
	case ToolBash:
		return s.doBash(ctx, call)
	default:
		return ToolResult{
			ToolCallID: call.ID,
			Content:    fmt.Sprintf("unknown tool %q", call.Name),
			IsError:    true,
		}, true
	}
}
