package brain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// The tool handlers, one per entry in the Tools table (tool_schemas.go) and in
// the same order, reached through routeTool (tool_dispatch.go). Each one
// unmarshals its input struct, guards the arguments the port below it does not,
// calls that port, and hands the outcome to a ToolResult constructor
// (tool_results.go) or a renderer (tool_render.go). update_ticket's handler is
// the exception: it heads a multi-step facade over five board operations, so it
// lives with that machinery in update_ticket.go.
//
// A handler returns (result, malformed): malformed is true only for an
// argument-shape problem (06 §8), which the pass loop counts toward its
// one-re-prompt-then-fail rule. A port's typed error is never malformed — it is
// a valid call whose precondition failed, fed back verbatim (06 §6).

// notifKindUpdate/notifKindPreview are post_update's / edit_update's two
// kinds (08 §7): "preview" when an image is attached, "update" otherwise.
const (
	notifKindUpdate  = "update"
	notifKindPreview = "preview"
)

func (s *Service) doCreateTicket(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in CreateTicketInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// title is required non-empty (board enforces it too, via ErrEmptyTitle, but
	// with an exact "" test that a whitespace-only title slips past). body is
	// intentionally optional at the board, so it is not guarded here.
	if res, ok := requireField(call.ID, ToolCreateTicket, fieldTitle, in.Title); !ok {
		return res, true
	}
	t, err := s.board.CreateTicket(ctx, in.Title, in.Body)
	return ticketResult(call.ID, t, err), false
}

func (s *Service) doListTickets(ctx context.Context, call ToolCall) (ToolResult, bool) {
	snap, err := s.reader.GetBoard(ctx)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatRoster(snap)}, false
}

func (s *Service) doGetTicket(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in GetTicketInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	t, err := s.reader.GetTicket(ctx, board.TicketID(in.ID))
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatTicketDetail(t)}, false
}

// doSearchTickets runs one keyword query over the board snapshot and returns a
// single page of hits (search.go). It reads through the same GetBoard the roster
// uses — non-archived tickets only, so search never resurrects a deleted one —
// and filters in memory: a project's board is small, and a second read path
// would buy nothing a filter does not. A blank query is malformed (06 §8): it
// would otherwise "match" nothing and read as an empty board.
func (s *Service) doSearchTickets(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in SearchTicketsInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	if res, ok := requireField(call.ID, ToolSearchTickets, fieldQuery, in.Query); !ok {
		return res, true
	}
	snap, err := s.reader.GetBoard(ctx)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	// An omitted (0) or nonsensical page is the first page rather than an error:
	// the model asked to search, and refusing over a page number spends a round
	// on nothing.
	page := max(in.Page, 1)
	hits := searchTickets(snap, searchWords(in.Query))
	return ToolResult{ToolCallID: call.ID, Content: formatSearchResults(in.Query, hits, page)}, false
}

func (s *Service) doDeleteTicket(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in DeleteTicketInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	t, err := s.board.ArchiveTicket(ctx, board.TicketID(in.ID))
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: fmt.Sprintf("ok: ticket %s deleted", t.ID)}, false
}

func (s *Service) doSendToAgent(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in SendToAgentInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// An empty instruction would wake the agent with nothing to act on; the
	// board does not guard it, so reject it here (see requireField).
	if res, ok := requireField(call.ID, ToolSendToAgent, "instruction", in.Instruction); !ok {
		return res, true
	}
	t, err := s.board.SendToAgent(ctx, board.TicketID(in.ID), in.Instruction)
	return ticketResult(call.ID, t, err), false
}

func (s *Service) doSay(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in SayInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// An empty message is nothing to show the user; reject it rather than push a
	// blank line into the transcript (see requireField).
	if res, ok := requireField(call.ID, ToolSay, "text", in.Text); !ok {
		return res, true
	}
	if err := s.say.Say(ctx, in.Text); err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: "ok"}, false
}

func (s *Service) doPostUpdate(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in PostUpdateInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// The text is required under one key or the other (see PostUpdateInput),
	// but an omitted/empty/whitespace-only value parses cleanly to "" and would
	// post a card with a header and timestamp but no text — the brain gets "ok"
	// and believes it posted, while the user sees an empty update (08 §7). See
	// requireField.
	body := in.resolvedBody()
	if res, ok := requireField(call.ID, ToolPostUpdate, fieldBody, body); !ok {
		return res, true
	}
	kind := notifKindUpdate
	if in.ImageURL != nil {
		kind = notifKindPreview
	}
	if err := s.notifications.PostNotification(ctx, kind, body, in.Ticket, in.ImageURL); err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: "ok"}, false
}

func (s *Service) doListUpdates(ctx context.Context, call ToolCall) (ToolResult, bool) {
	updates, err := s.feed.ListUpdates(ctx)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatUpdates(updates)}, false
}

func (s *Service) doEditUpdate(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in EditUpdateInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// body is required for the same reason as post_update: an empty edit would
	// blank the card. See requireField.
	if res, ok := requireField(call.ID, ToolEditUpdate, fieldBody, in.Body); !ok {
		return res, true
	}
	kind := notifKindUpdate
	if in.ImageURL != nil {
		kind = notifKindPreview
	}
	if err := s.notifications.EditNotification(ctx, in.NotificationID, kind, in.Body, in.ImageURL); err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: "ok"}, false
}

func (s *Service) doRetractUpdate(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in RetractUpdateInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	if err := s.notifications.RetractNotification(ctx, in.NotificationID); err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: "ok"}, false
}

func (s *Service) doListAgents(ctx context.Context, call ToolCall) (ToolResult, bool) {
	agents, err := s.agents.ListAgents(ctx)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatAgents(agents)}, false
}

func (s *Service) doGetAgentUpdates(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in GetAgentUpdatesInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	u, err := s.agents.GetAgentUpdates(ctx, in.WorkerID)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	return ToolResult{ToolCallID: call.ID, Content: formatUpdate(u)}, false
}

func (s *Service) doBash(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in BashInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// An empty command is nothing to run; reject it like the other required
	// free-text fields (see requireField).
	if res, ok := requireField(call.ID, ToolBash, fieldCommand, in.Command); !ok {
		return res, true
	}
	res, err := s.repo.Run(ctx, in.Command)
	if err != nil {
		return errorResult(call.ID, err), false
	}
	if res.Unavailable {
		return ToolResult{
			ToolCallID: call.ID,
			Content:    "repo inspection unavailable: " + res.Reason,
			IsError:    true,
		}, false
	}
	// A non-zero exit is NOT a tool error — feed the rendered result back as
	// content so the model can read it (same philosophy as the board's typed
	// errors fed back verbatim).
	return ToolResult{ToolCallID: call.ID, Content: formatRepoResult(res)}, false
}
