package brain

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// ToolName enumerates the seven tools (06 §4) — the brain's entire action
// surface. Not in the set (06 §4, D3, I6): anything that pulls (03 I6, the
// pull is a system action, never a brain decision), notify (deferred to 10 —
// the mechanical notify.send from MarkBlocked still emits, log-only in v1),
// and any board-read (the snapshot is already in context).
type ToolName string

const (
	ToolCreateTicket ToolName = "create_ticket"
	ToolShapeTicket  ToolName = "shape_ticket"
	ToolMarkReady    ToolName = "mark_ready"
	ToolSendToAgent  ToolName = "send_to_agent"
	ToolMarkBlocked  ToolName = "mark_blocked"
	ToolAcceptToDone ToolName = "accept_to_done"
	ToolSay          ToolName = "say"
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

const (
	schemaKeyType        = "type"
	schemaKeyDescription = "description"
	schemaKeyProperties  = "properties"
	schemaKeyRequired    = "required"

	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"

	fieldTicketID = "id"
	fieldTitle    = "title"
	fieldBody     = "body"
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
}

// Dispatch executes one tool call against the injected ports — the
// tool -> port-method mapping (06 §4):
//
//	create_ticket  -> BoardAPI.CreateTicket(title, body)
//	shape_ticket   -> BoardAPI.ShapeTicket(id, patch)
//	mark_ready     -> BoardAPI.MarkReady(id)
//	send_to_agent  -> BoardAPI.SendToAgent(id, instruction)
//	mark_blocked   -> BoardAPI.MarkBlocked(id, reason)
//	accept_to_done -> BoardAPI.AcceptToDone(id)
//	say            -> Say.Say(text)
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

// dispatchOne is Dispatch's core, additionally reporting whether the call was
// malformed — an unknown tool name or unparseable arguments (06 §8) — which
// the pass loop counts toward its one-re-prompt-then-fail rule. A typed Board
// API error is *not* malformed: it is a valid call whose precondition failed,
// fed back verbatim for the model to self-correct (06 §6).
func (s *Service) dispatchOne(ctx context.Context, call ToolCall) (ToolResult, bool) {
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
	t, err := s.board.SendToAgent(ctx, board.TicketID(in.ID), in.Instruction)
	return ticketResult(call.ID, t, err), false
}

func (s *Service) doMarkBlocked(ctx context.Context, call ToolCall) (ToolResult, bool) {
	var in MarkBlockedInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return malformedResult(call.ID, err), true
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
	if err := s.say.Say(ctx, in.Text); err != nil {
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
	return ToolResult{ToolCallID: id, Content: fmt.Sprintf("invalid tool arguments: %v", err), IsError: true}
}
