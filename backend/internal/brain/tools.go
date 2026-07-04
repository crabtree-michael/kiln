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

// ToolName enumerates the ten tools (06 §4, amended by 08 §5/§7) — the brain's
// entire action surface. Not in the set (06 §4, D3, I6): anything that pulls
// (03 I6, the pull is a system action, never a brain decision), notify
// (deferred to 10 — the mechanical notify.send from MarkBlocked still emits,
// log-only in v1), and any board-read (the snapshot is already in context).
// 08 adds the three feed tools: request_approval, post_update, retract_update.
type ToolName string

const (
	ToolCreateTicket    ToolName = "create_ticket"
	ToolShapeTicket     ToolName = "shape_ticket"
	ToolMarkReady       ToolName = "mark_ready"
	ToolSendToAgent     ToolName = "send_to_agent"
	ToolMarkBlocked     ToolName = "mark_blocked"
	ToolAcceptToDone    ToolName = "accept_to_done"
	ToolSay             ToolName = "say"
	ToolRequestApproval ToolName = "request_approval"
	ToolPostUpdate      ToolName = "post_update"
	ToolRetractUpdate   ToolName = "retract_update"
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

// ShapeTicketInput — shape_ticket → BoardAPI.ShapeTicket(id, patch) (06 §4).
// Nil/omitted fields are left unchanged; reprioritize is shape (03 §4's
// "Reprioritize is not a separate operation").
type ShapeTicketInput struct {
	ID       string  `json:"id"`
	Title    *string `json:"title,omitempty"`
	Body     *string `json:"body,omitempty"`
	Priority *int    `json:"priority,omitempty"`
}

// MarkReadyInput — mark_ready → BoardAPI.MarkReady(id) (06 §4). Makes the
// ticket pullable; the pull itself is never a tool (03 I6).
type MarkReadyInput struct {
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

// MarkBlockedInput — mark_blocked → BoardAPI.MarkBlocked(id, reason)
// (06 §4). Turn ended, human decision genuinely needed.
type MarkBlockedInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// AcceptToDoneInput — accept_to_done → BoardAPI.AcceptToDone(id) (06 §4).
// Always destructive (06 §7): releases and recycles the worker.
type AcceptToDoneInput struct {
	ID string `json:"id"`
}

// SayInput — say → Say.Say(text) (06 §4). Text to the user: appended to the
// transcript, pushed over SSE; 09 will speak it.
type SayInput struct {
	Text string `json:"text"`
}

// RequestApprovalInput — request_approval → BoardAPI.RequestApproval(id)
// (08 §5). Sets approval_requested on a Shaping ticket so it surfaces as a
// proposal card. Used at the brain's discretion for complex decisions;
// routine work goes straight to mark_ready.
type RequestApprovalInput struct {
	Ticket string `json:"ticket"`
}

// PostUpdateInput — post_update → NotificationStore.PostNotification(kind,
// body, ticket?, image_url?) (08 §7). kind is "preview" when ImageURL is set,
// else "update". A feed card worth a glance, not a play-by-play.
type PostUpdateInput struct {
	Body     string  `json:"body"`
	Ticket   *string `json:"ticket,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

// RetractUpdateInput — retract_update → NotificationStore.RetractNotification(
// notification_id) (08 §7). Drops an update card that stopped mattering.
type RetractUpdateInput struct {
	NotificationID int64 `json:"notification_id"`
}

const (
	schemaKeyType        = "type"
	schemaKeyDescription = "description"
	schemaKeyProperties  = "properties"
	schemaKeyRequired    = "required"

	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"

	fieldTicketID       = "id"
	fieldTitle          = "title"
	fieldBody           = "body"
	fieldTicket         = "ticket"
	fieldImageURL       = "image_url"
	fieldNotificationID = "notification_id"

	// notifKindUpdate/notifKindPreview are post_update's two kinds (08 §7):
	// "preview" when an image is attached, "update" otherwise.
	notifKindUpdate  = "update"
	notifKindPreview = "preview"
)

func stringSchema(description string) map[string]any {
	return map[string]any{schemaKeyType: schemaTypeString, schemaKeyDescription: description}
}

func intSchema(description string) map[string]any {
	return map[string]any{schemaKeyType: schemaTypeInteger, schemaKeyDescription: description}
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
		Name: ToolShapeTicket,
		Description: "Update a ticket's title, body, and/or priority while it is in Shaping " +
			"or Ready. Reprioritizing is done by shaping.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
			fieldTitle:    stringSchema("New title, if changing."),
			fieldBody:     stringSchema("New body, if changing."),
			"priority":    intSchema("New priority; higher pulls first."),
		}),
	},
	{
		Name:        ToolMarkReady,
		Description: "Mark a shaping ticket ready, making it pullable. Does not pull it — the pull is automatic.",
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
		Name:        ToolMarkBlocked,
		Description: "Mark a working ticket blocked because a human decision is genuinely needed.",
		InputSchema: objectSchema([]string{fieldTicketID, "reason"}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
			"reason":      stringSchema("What the user must decide."),
		}),
	},
	{
		Name: ToolAcceptToDone,
		Description: "Accept a ticket's result as done, releasing and recycling its worker. " +
			"Destructive — the workspace is gone.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
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
		Name: ToolRequestApproval,
		Description: "Ask the user to approve a Shaping ticket before it runs — it surfaces " +
			"as a proposal card. Use for tickets embedding a complex or consequential " +
			"technical decision; routine work goes straight to mark_ready.",
		InputSchema: objectSchema([]string{fieldTicket}, map[string]any{
			fieldTicket: stringSchema("Ticket id."),
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
		Name:        ToolRetractUpdate,
		Description: "Retract a previously posted update once it no longer matters.",
		InputSchema: objectSchema([]string{fieldNotificationID}, map[string]any{
			fieldNotificationID: intSchema("The id of the notification to retract."),
		}),
	},
}

// Dispatch executes one tool call against the injected ports — the
// tool -> port-method mapping (06 §4):
//
//	create_ticket  -> BoardAPI.CreateTicket(title, body)
//	shape_ticket   -> BoardAPI.ShapeTicket(id, patch)
//	mark_ready     -> BoardAPI.MarkReady(id)
//	send_to_agent  -> BoardAPI.SendToAgent(id, instruction)
//	mark_blocked   -> BoardAPI.MarkBlocked(id, reason)
//	accept_to_done   -> BoardAPI.AcceptToDone(id)
//	say              -> Say.Say(text)
//	request_approval -> BoardAPI.RequestApproval(id)                  (08 §5)
//	post_update      -> NotificationStore.PostNotification(kind, ...) (08 §7)
//	retract_update   -> NotificationStore.RetractNotification(id)     (08 §7)
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
	case ToolShapeTicket:
		return s.doShapeTicket(ctx, call)
	case ToolMarkReady:
		return s.doMarkReady(ctx, call)
	case ToolSendToAgent:
		return s.doSendToAgent(ctx, call)
	case ToolMarkBlocked:
		return s.doMarkBlocked(ctx, call)
	case ToolAcceptToDone:
		return s.doAcceptToDone(ctx, call)
	case ToolSay:
		return s.doSay(ctx, call)
	case ToolRequestApproval:
		return s.doRequestApproval(ctx, call)
	case ToolPostUpdate:
		return s.doPostUpdate(ctx, call)
	case ToolRetractUpdate:
		return s.doRetractUpdate(ctx, call)
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

func (s *Service) doShapeTicket(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in ShapeTicketInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	patch := board.ShapePatch{Title: in.Title, Body: in.Body, Priority: in.Priority}
	t, err := s.board.ShapeTicket(ctx, board.TicketID(in.ID), patch)
	return ticketResult(call.ID, t, err), false
}

func (s *Service) doMarkReady(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in MarkReadyInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	t, err := s.board.MarkReady(ctx, board.TicketID(in.ID))
	return ticketResult(call.ID, t, err), false
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

func (s *Service) doMarkBlocked(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in MarkBlockedInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	// reason is the whole point of a blocker card — the decision the user must
	// make. An empty one is a "needs you" card with nothing to act on. The board
	// stores it verbatim without guarding, so reject it here (see requireField).
	if res, ok := requireField(call.ID, ToolMarkBlocked, "reason", in.Reason); !ok {
		return res, true
	}
	t, err := s.board.MarkBlocked(ctx, board.TicketID(in.ID), in.Reason)
	return ticketResult(call.ID, t, err), false
}

func (s *Service) doAcceptToDone(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in AcceptToDoneInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	t, err := s.board.AcceptToDone(ctx, board.TicketID(in.ID))
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

func (s *Service) doRequestApproval(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in RequestApprovalInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
	}
	t, err := s.board.RequestApproval(ctx, board.TicketID(in.Ticket))
	return ticketResult(call.ID, t, err), false
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
