package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/board"
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

// ToolName enumerates the fourteen tools (06 §4 amended — the CRUD
// consolidation) — the brain's entire action surface, organized as clean CRUD
// over the two nouns it owns, tickets and feed updates, plus the agent/repo
// seams:
//
//   - Tickets: create_ticket (C), list_tickets + get_ticket (R),
//     update_ticket (U — one patch folding the old shape/mark_ready/
//     mark_blocked/accept_to_done/request_approval verbs), delete_ticket (D,
//     soft archive).
//   - Feed updates: post_update (C), list_updates (R), edit_update (U),
//     retract_update (D).
//   - Agent seam: list_agents + get_agent_updates (read-only visibility into
//     the runtime without importing internal/agent).
//   - Cross-cutting: send_to_agent (message a ticket's agent), say (talk to the
//     user), bash (read-oriented repo shell over the RepoShell port).
//
// Not in the set (06 D3, I6): anything that pulls (03 I6 — the pull is a system
// action, never a brain decision) and notify (deferred to 10 — the mechanical
// notify.send from MarkBlocked still emits, log-only in v1). Board state is no
// longer injected; the model pulls it via list_tickets / get_ticket, so a pass
// spends no tokens on board state it does not need (06 §4 amended).
type ToolName string

const (
	ToolCreateTicket    ToolName = "create_ticket"
	ToolListTickets     ToolName = "list_tickets"
	ToolGetTicket       ToolName = "get_ticket"
	ToolUpdateTicket    ToolName = "update_ticket"
	ToolDeleteTicket    ToolName = "delete_ticket"
	ToolSendToAgent     ToolName = "send_to_agent"
	ToolSay             ToolName = "say"
	ToolPostUpdate      ToolName = "post_update"
	ToolListUpdates     ToolName = "list_updates"
	ToolEditUpdate      ToolName = "edit_update"
	ToolRetractUpdate   ToolName = "retract_update"
	ToolListAgents      ToolName = "list_agents"
	ToolGetAgentUpdates ToolName = "get_agent_updates"
	ToolBash            ToolName = "bash"
)

// ToolDef is one tool's schema in the shape the Anthropic tool-use API
// expects: name, description, JSON-Schema input. InputSchema is
// map[string]any rather than an SDK type so this scaffold stays SDK-free
// (see llm.go's Adapter wire-in note); the composition-root adapter
// marshals it into the SDK's tool-param shape unchanged. These are the same
// definitions the golden tests assert the model was offered (06 §4, §9).
type ToolDef struct {
	Name        ToolName
	Description string
	InputSchema map[string]any
}

// CreateTicketInput — create_ticket → BoardAPI.CreateTicket(title, body)
// (06 §4). New work lands in Shaping.
type CreateTicketInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// ListTicketsInput — list_tickets takes no arguments (06 §4 amended). Returns
// the compact board roster (every non-archived ticket, no bodies).
type ListTicketsInput struct{}

// GetTicketInput — get_ticket → BoardReader.GetTicket(id) (06 §4 amended). One
// ticket in full, including its body.
type GetTicketInput struct {
	ID string `json:"id"`
}

// UpdateTicketInput — update_ticket (06 §4 amended): one patch tool folding the
// old shape_ticket / mark_ready / mark_blocked / accept_to_done /
// request_approval verbs. Nil/omitted fields are left unchanged. Field edits
// (title/body/priority) apply first, then approval_requested, then the state
// transition, so a single call can shape-then-ready a ticket. Each field routes
// to the board's own typed operation (dispatch), preserving every precondition.
//
//   - State ∈ {"ready","blocked","done"} — the reachable brain transitions;
//     "blocked" requires BlockedReason. There is no transition *to* shaping.
//   - ApprovalRequested and State are mutually exclusive (approval is a
//     shaping-only flag, 08 §5); setting both is a malformed call.
//
// State="done" is always destructive (recycles the worker, 06 §7); the
// confirm-before-destructive rule lives in the system prompt (prompt.go).
type UpdateTicketInput struct {
	ID                string  `json:"id"`
	Title             *string `json:"title,omitempty"`
	Body              *string `json:"body,omitempty"`
	Priority          *int    `json:"priority,omitempty"`
	State             *string `json:"state,omitempty"`
	BlockedReason     *string `json:"blocked_reason,omitempty"`
	ApprovalRequested *bool   `json:"approval_requested,omitempty"`
}

// DeleteTicketInput — delete_ticket → BoardAPI.ArchiveTicket(id) (06 §4
// amended). Soft-deletes a non-active ticket; an active (working/blocked)
// ticket is refused with a typed board error.
type DeleteTicketInput struct {
	ID string `json:"id"`
}

// SendToAgentInput — send_to_agent → BoardAPI.SendToAgent(id, instruction)
// (06 §4). Resumes a blocked agent or starts a new turn for a working one.
// Destructive when the instruction would discard in-flight work — the
// confirm-before-destructive rule (06 §7) is enforced in the system prompt
// (prompt.go), not here.
type SendToAgentInput struct {
	ID          string `json:"id"`
	Instruction string `json:"instruction"`
}

// SayInput — say → Say.Say(text) (06 §4). Text to the user: appended to the
// transcript, pushed over SSE; 09 will speak it.
type SayInput struct {
	Text string `json:"text"`
}

// PostUpdateInput — post_update → NotificationStore.PostNotification(kind,
// body, ticket?, image_url?) (08 §7). kind is "preview" when ImageURL is set,
// else "update". A feed card worth a glance, not a play-by-play.
type PostUpdateInput struct {
	Body     string  `json:"body"`
	Ticket   *string `json:"ticket,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

// ListUpdatesInput — list_updates takes no arguments (06 §4 amended). Returns
// the active feed cards (id, kind, body, ticket, image) so the model knows
// which notification_id to edit or retract.
type ListUpdatesInput struct{}

// EditUpdateInput — edit_update → NotificationStore.EditNotification(id, kind,
// body, image_url) (06 §4 amended, 08 §7). Amends a still-active card in place.
// kind is derived from ImageURL like post_update ("preview" with an image, else
// "update").
type EditUpdateInput struct {
	NotificationID int64   `json:"notification_id"`
	Body           string  `json:"body"`
	ImageURL       *string `json:"image_url,omitempty"`
}

// RetractUpdateInput — retract_update → NotificationStore.RetractNotification(
// notification_id) (08 §7). Drops an update card that stopped mattering.
type RetractUpdateInput struct {
	NotificationID int64 `json:"notification_id"`
}

// ListAgentsInput — list_agents takes no arguments (06 §4 amended).
type ListAgentsInput struct{}

// GetAgentUpdatesInput — get_agent_updates → AgentInspector.GetAgentUpdates(worker_id).
type GetAgentUpdatesInput struct {
	WorkerID string `json:"worker_id"`
}

// BashInput — bash → RepoShell.Run(command). A shell command string run in the
// project clone against an allowlisted set of binaries (git/gh/rg/…).
type BashInput struct {
	Command string `json:"command"`
}

const (
	schemaKeyType        = "type"
	schemaKeyDescription = "description"
	schemaKeyProperties  = "properties"
	schemaKeyRequired    = "required"

	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeBoolean = "boolean"
	schemaTypeObject  = "object"

	fieldTicketID       = "id"
	fieldTitle          = "title"
	fieldBody           = "body"
	fieldTicket         = "ticket"
	fieldPriority       = "priority"
	fieldState          = "state"
	fieldBlockedReason  = "blocked_reason"
	fieldApproval       = "approval_requested"
	fieldImageURL       = "image_url"
	fieldNotificationID = "notification_id"
	fieldWorkerID       = "worker_id"
	fieldCommand        = "command"

	// notifKindUpdate/notifKindPreview are post_update's / edit_update's two
	// kinds (08 §7): "preview" when an image is attached, "update" otherwise.
	notifKindUpdate  = "update"
	notifKindPreview = "preview"

	// The reachable update_ticket state transitions (06 §4 amended). There is
	// no transition *to* shaping — a ticket starts there.
	stateReady   = "ready"
	stateBlocked = "blocked"
	stateDone    = "done"
)

func stringSchema(description string) map[string]any {
	return map[string]any{schemaKeyType: schemaTypeString, schemaKeyDescription: description}
}

func intSchema(description string) map[string]any {
	return map[string]any{schemaKeyType: schemaTypeInteger, schemaKeyDescription: description}
}

func boolSchema(description string) map[string]any {
	return map[string]any{schemaKeyType: schemaTypeBoolean, schemaKeyDescription: description}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		schemaKeyType:       schemaTypeObject,
		schemaKeyProperties: properties,
		schemaKeyRequired:   required,
	}
}

// Tools is the fixed, ordered tool set exposed to the model every pass
// (06 §4). Order is stable for prompt-cache friendliness and deterministic
// golden-test fixtures.
var Tools = []ToolDef{
	{
		Name:        ToolCreateTicket,
		Description: "Create a new ticket in Shaping.",
		InputSchema: objectSchema([]string{fieldTitle, fieldBody}, map[string]any{
			fieldTitle: stringSchema("Ticket title, non-empty."),
			fieldBody:  stringSchema("Ticket body — the shaped details."),
		}),
	},
	{
		Name: ToolListTickets,
		Description: "List every ticket on the board with its state, priority and worker — the " +
			"compact roster, without bodies. Read the board here before deciding; use " +
			"get_ticket for a single ticket's full body. Read-only.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name:        ToolGetTicket,
		Description: "Read one ticket in full, including its body, by id. Read-only.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
		}),
	},
	{
		Name: ToolUpdateTicket,
		Description: "Update a ticket. Edit its title/body/priority, and/or move its state: " +
			"\"ready\" queues a shaping ticket for the pull, \"blocked\" needs a human decision " +
			"(give blocked_reason), \"done\" accepts the result and recycles the worker " +
			"(destructive — the workspace is gone). Set approval_requested to surface a shaping " +
			"ticket as a proposal card (mutually exclusive with state). Fields apply before the " +
			"state change, so one call can revise and queue a ticket.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID:      stringSchema("Ticket id."),
			fieldTitle:         stringSchema("New title, if changing."),
			fieldBody:          stringSchema("New body, if changing."),
			fieldPriority:      intSchema("New priority; higher pulls first."),
			fieldState:         stringSchema("New state: \"ready\", \"blocked\", or \"done\"."),
			fieldBlockedReason: stringSchema("Required when state is \"blocked\": what the user must decide."),
			fieldApproval:      boolSchema("Set true to surface a shaping ticket as a proposal card awaiting approval."),
		}),
	},
	{
		Name: ToolDeleteTicket,
		Description: "Delete (archive) a ticket that should not exist — a mistake or duplicate. " +
			"It disappears from the board but is retained for history. Only backlog or done " +
			"tickets can be deleted; resolve an in-progress ticket first.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
		}),
	},
	{
		Name: ToolSendToAgent,
		Description: "Send an instruction to the agent working a ticket — resumes a blocked " +
			"ticket or gives a new turn to a working one.",
		InputSchema: objectSchema([]string{fieldTicketID, "instruction"}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
			"instruction": stringSchema("The instruction to send to the agent."),
		}),
	},
	{
		Name:        ToolSay,
		Description: "Say something to the user in the chat.",
		InputSchema: objectSchema([]string{"text"}, map[string]any{
			"text": stringSchema("The text to say."),
		}),
	},
	{
		Name: ToolPostUpdate,
		Description: "Post an update to the user's feed — a card worth a glance, not a " +
			"play-by-play. Attach image_url for an inline preview.",
		InputSchema: objectSchema([]string{fieldBody}, map[string]any{
			fieldBody:     stringSchema("The update text."),
			fieldTicket:   stringSchema("Related ticket id, if any."),
			fieldImageURL: stringSchema("Image URL for an inline preview, if any."),
		}),
	},
	{
		Name: ToolListUpdates,
		Description: "List the active feed updates you have posted — their ids, kinds and text — " +
			"so you can edit or retract one. Read-only.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolEditUpdate,
		Description: "Edit a feed update you already posted — fix its wording or swap its preview " +
			"image — instead of retracting and reposting. Use list_updates to find the id.",
		InputSchema: objectSchema([]string{fieldNotificationID, fieldBody}, map[string]any{
			fieldNotificationID: intSchema("The id of the notification to edit."),
			fieldBody:           stringSchema("The new update text."),
			fieldImageURL:       stringSchema("New image URL for an inline preview, if any."),
		}),
	},
	{
		Name:        ToolRetractUpdate,
		Description: "Retract a previously posted update once it no longer matters.",
		InputSchema: objectSchema([]string{fieldNotificationID}, map[string]any{
			fieldNotificationID: intSchema("The id of the notification to retract."),
		}),
	},
	{
		Name: ToolListAgents,
		Description: "List the running agents (workers) and whether each is working a " +
			"ticket or idle. Read-only.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolGetAgentUpdates,
		Description: "Read an agent's latest completed output by worker id — use to check " +
			"what a working agent last produced. Read-only.",
		InputSchema: objectSchema([]string{fieldWorkerID}, map[string]any{
			fieldWorkerID: stringSchema("Board worker id, from list_agents or the board snapshot."),
		}),
	},
	{
		Name: ToolBash,
		Description: "Run a shell command in a clone of the project repository. A read-oriented " +
			"window into the real repo: use git/gh to verify an agent has pushed its work " +
			"(fetch, then confirm its branch and commits are on the remote) before accepting " +
			"a ticket, and rg/grep/find to search the repository for information. Only an " +
			"allowlisted set of commands is reachable.",
		InputSchema: objectSchema([]string{fieldCommand}, map[string]any{
			fieldCommand: stringSchema("The shell command to run in the repo clone."),
		}),
	},
}

// Dispatch executes one tool call against the injected ports — the
// tool -> port-method mapping (06 §4 amended):
//
//	create_ticket  -> BoardAPI.CreateTicket(title, body)
//	list_tickets   -> BoardReader.GetBoard()                          (compact roster)
//	get_ticket     -> BoardReader.GetTicket(id)                       (full body)
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
func (s *Service) Dispatch(ctx context.Context, call ToolCall) ToolResult {
	res, _ := s.dispatchOne(ctx, call)
	return res
}

// dispatchOne routes one tool call to its handler and logs it as a structured
// board-mutating action (turn_id injected from context): the tool name, an
// args summary + content hash (ticket id lives in the args; args_hash makes a
// duplicated send_to_agent instruction greppable — the 841fb6cc smell), and
// the outcome. It additionally reports whether the call was malformed — an
// unknown tool name or unparseable arguments (06 §8) — which the pass loop
// counts toward its one-re-prompt-then-fail rule.
func (s *Service) dispatchOne(ctx context.Context, call ToolCall) (ToolResult, bool) {
	res, malformed := s.routeTool(ctx, call)
	slog.InfoContext(ctx, "brain.tool",
		"tool", string(call.Name),
		"args", obs.Summary(string(call.Input), toolArgsSummaryBytes),
		"args_hash", obs.Hash(string(call.Input)),
		"is_error", res.IsError,
		"result", obs.Summary(res.Content, toolResultSummaryBytes),
	)
	return res, malformed
}

// routeTool is dispatchOne's flat tool → handler table. A typed Board API error
// is *not* malformed: it is a valid call whose precondition failed, fed back
// verbatim for the model to self-correct (06 §6). The case count, not any
// branching logic, is what trips the complexity metric.
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
	return ToolResult{}, true
}

// updateHasWork reports whether a patch actually changes anything — a field
// edit, an approval request, or a state transition (a bare approval_requested:false
// is not work).
func updateHasWork(in UpdateTicketInput) bool {
	edits := in.Title != nil || in.Body != nil || in.Priority != nil
	return edits || (in.ApprovalRequested != nil && *in.ApprovalRequested) || in.State != nil
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
	if in.ApprovalRequested != nil && *in.ApprovalRequested {
		t, err = s.board.RequestApproval(ctx, tid)
		if err != nil {
			return updateStepError(id, "request approval", applied, err)
		}
		applied = append(applied, "approval_requested")
	}
	if in.State != nil {
		// State is last, so no step reads `applied` after this — it need not be
		// extended here.
		if t, err = s.applyState(ctx, tid, *in.State, deref(in.BlockedReason)); err != nil {
			return updateStepError(id, "state="+*in.State, applied, err)
		}
	}
	return ticketResult(id, t, nil)
}

// applyState routes one state transition to its board operation. The board's
// typed error is returned unwrapped on purpose: applyUpdate feeds it back to the
// model verbatim (06 §6), and wrapping it would corrupt the idempotency signal
// the prompt tells the model to read.
//
//nolint:wrapcheck // board error is fed back verbatim (06 §6), never wrapped.
func (s *Service) applyState(
	ctx context.Context, id board.TicketID, state, blockedReason string,
) (board.Ticket, error) {
	switch state {
	case stateReady:
		return s.board.MarkReady(ctx, id)
	case stateBlocked:
		return s.board.MarkBlocked(ctx, id, blockedReason)
	default: // stateDone — validateUpdate already rejected any other value
		return s.board.AcceptToDone(ctx, id)
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

func (s *Service) doPostUpdate(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in PostUpdateInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// body is required, but an omitted/empty/whitespace-only value parses
	// cleanly to "" and would post a card with a header and timestamp but no
	// text — the brain gets "ok" and believes it posted, while the user sees an
	// empty update (08 §7). See requireField.
	if res, ok := requireField(call.ID, ToolPostUpdate, fieldBody, in.Body); !ok {
		return res, true
	}
	kind := notifKindUpdate
	if in.ImageURL != nil {
		kind = notifKindPreview
	}
	if err := s.notifications.PostNotification(ctx, kind, in.Body, in.Ticket, in.ImageURL); err != nil {
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
// blanks, or used the wrong key — e.g. post_update's "body" vs say's "text"), so
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

// formatAgents renders list_agents' result as one line per worker for the model.
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "no running agents"
	}
	var b strings.Builder
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
