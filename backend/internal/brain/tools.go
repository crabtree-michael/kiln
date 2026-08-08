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

// ToolName enumerates the fifteen tools (06 §4 amended — the CRUD
// consolidation) — the brain's entire action surface, organized as clean CRUD
// over the two nouns it owns, tickets and feed updates, plus the agent/repo
// seams:
//
//   - Tickets: create_ticket (C), list_tickets + get_ticket + search_tickets
//     (R), update_ticket (U — one patch folding the old shape/mark_ready/
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
	ToolSearchTickets   ToolName = "search_tickets"
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

// SearchTicketsInput — search_tickets (search.go): keyword lookup across every
// ticket on the board, a page at a time. Query is the words that must all
// appear; Page is 1-based and omitted means the first page.
type SearchTicketsInput struct {
	Query string `json:"query"`
	Page  int    `json:"page,omitempty"`
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
	// DoneCommit is the origin/main commit SHA carrying the ticket's work.
	// Required when State="done": the done path verifies it is on origin/main
	// before accepting the ticket (06 §7 amended, prompt.go). Ignored otherwise.
	DoneCommit *string `json:"done_commit,omitempty"`
	// DependsOn is the complete set of tickets this one must wait for (0013).
	// A nil pointer leaves the list untouched; a present list — including an
	// empty one, which clears it — is the desired end state, so the facade adds
	// what is missing and removes what is no longer named. Whole-list rather
	// than add/remove verbs because that is how a model states it ("this waits
	// for A and B"); a delta API would make it read the current list first just
	// to compute one.
	DependsOn *[]string `json:"depends_on,omitempty"`
}

// DeleteTicketInput — delete_ticket → BoardAPI.ArchiveTicket(id) (06 §4
// amended). Soft-deletes a shaping/ready/blocked/done ticket (a blocked delete
// releases the worker it holds); only a *working* ticket is refused with a typed
// board error.
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
//
// Text is an alias for Body, accepted but not advertised in the schema. The
// sibling say tool takes "text", and the model reaches for that key on ~1 in 5
// post_update calls — every one of which self-corrected a round later, so the
// rejection only ever bought a wasted round-trip
// (docs/brain-optimization-2026-08-05.md §1). Taking both keys lands the update
// on the first try. "body" stays the one name in the schema, so the two names
// never compete for the model's attention.
type PostUpdateInput struct {
	Body     string  `json:"body"`
	Text     string  `json:"text,omitempty"`
	Ticket   *string `json:"ticket,omitempty"`
	ImageURL *string `json:"image_url,omitempty"`
}

// resolvedBody is the update's text across both accepted keys. Body wins when
// both carry text — it is the name the schema asks for.
func (in PostUpdateInput) resolvedBody() string {
	if strings.TrimSpace(in.Body) != "" {
		return in.Body
	}
	return in.Text
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
	schemaTypeArray   = "array"

	fieldTicketID       = "id"
	fieldTitle          = "title"
	fieldBody           = "body"
	fieldTicket         = "ticket"
	fieldPriority       = "priority"
	fieldState          = "state"
	fieldBlockedReason  = "blocked_reason"
	fieldDoneCommit     = "done_commit"
	fieldApproval       = "approval_requested"
	fieldDependsOn      = "depends_on"
	fieldImageURL       = "image_url"
	fieldNotificationID = "notification_id"
	fieldWorkerID       = "worker_id"
	fieldCommand        = "command"
	fieldQuery          = "query"
	fieldPage           = "page"

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

// stringArraySchema is a JSON-Schema array of strings — used for the whole-list
// fields, where the model sends the complete desired set rather than a delta.
func stringArraySchema(description string) map[string]any {
	return map[string]any{
		schemaKeyType:        schemaTypeArray,
		schemaKeyDescription: description,
		"items":              map[string]any{schemaKeyType: schemaTypeString},
	}
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
		Description: "List the tickets on the board with their state, priority and worker — the " +
			"compact roster, without bodies. Each column says what its tickets accept right now " +
			"(\"allowed now\"). Every live ticket is listed; Done shows only its most recent few, " +
			"with the rest reachable through search_tickets. Read the board here before deciding; " +
			"use get_ticket for a single ticket's full body. Read-only.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolGetTicket,
		Description: "Read one ticket in full, including its body, by id. The result's " +
			"\"allowed now\" line lists exactly what its current state accepts — check it rather " +
			"than attempting a change the state will refuse. Read-only.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID: stringSchema("Ticket id."),
		}),
	},
	{
		Name: ToolSearchTickets,
		Description: "Find tickets by keyword across the whole board, including the older Done " +
			"tickets the roster does not list. Matching is case-insensitive on ticket ids, titles " +
			"and bodies, and every word in the query must appear — so add a word to narrow, drop " +
			"one to widen. Results come a few per page, best match first; pass page to read the " +
			"next one. Read-only.",
		InputSchema: objectSchema([]string{fieldQuery}, map[string]any{
			fieldQuery: stringSchema("Words to look for; a ticket matches only if all of them appear."),
			fieldPage:  intSchema("Which page of results to read, 1-based. Omit for the first page."),
		}),
	},
	{
		Name: ToolUpdateTicket,
		Description: "Update a ticket. Edit its title/body/priority, and/or move its state: " +
			"\"ready\" queues a shaping ticket for the pull, \"blocked\" needs a human decision " +
			"(give blocked_reason), \"done\" accepts the result and recycles the worker " +
			"(destructive — the workspace is gone). Marking \"done\" requires done_commit: the " +
			"origin/main commit SHA carrying the ticket's work. The system fetches origin and " +
			"rejects the done unless that commit is on origin/main — so find it first with the " +
			"bash tool (git fetch origin, then git log origin/main). Every shaping ticket is " +
			"already a proposal card; set approval_requested only to nudge the user's attention " +
			"to one (mutually exclusive with state). Fields apply before the state change, so " +
			"one call can revise and queue a ticket. A ticket's state limits which of these it " +
			"takes — a working or blocked ticket's fields cannot be edited and it cannot be " +
			"queued — so read its \"allowed now\" line from get_ticket or list_tickets first. " +
			"Use depends_on to make a ticket wait for other tickets to finish first.",
		InputSchema: objectSchema([]string{fieldTicketID}, map[string]any{
			fieldTicketID:      stringSchema("Ticket id."),
			fieldTitle:         stringSchema("New title, if changing."),
			fieldBody:          stringSchema("New body, if changing."),
			fieldPriority:      intSchema("New priority; higher pulls first."),
			fieldState:         stringSchema("New state: \"ready\", \"blocked\", or \"done\"."),
			fieldBlockedReason: stringSchema("Required when state is \"blocked\": what the user must decide."),
			fieldDoneCommit: stringSchema("Required when state is \"done\": the origin/main commit SHA " +
				"carrying this ticket's work. Verified to be on origin/main before the ticket is accepted. " +
				"Each commit maps to one ticket — a SHA already used to accept another ticket is rejected, " +
				"so use the commit that carries this ticket's own work."),
			fieldApproval: boolSchema("Optional emphasis; every shaping ticket already shows as a proposal card."),
			fieldDependsOn: stringArraySchema("The tickets this one must wait for, as a complete " +
				"list — whatever you pass replaces the current one, and [] clears it. A queued " +
				"ticket whose dependencies are not all done is skipped by the pull: it keeps its " +
				"place in the backlog and takes up no worker. Use this to order work that must " +
				"happen in sequence instead of holding a ticket back by leaving it unqueued. A " +
				"ticket cannot wait on itself, directly or through a chain."),
		}),
	},
	{
		Name: ToolDeleteTicket,
		Description: "Delete (archive) a ticket that should not exist — a mistake or duplicate. " +
			"It disappears from the board but is retained for history. Backlog, blocked, or done " +
			"tickets can be deleted; deleting a blocked ticket releases the worker it holds. A " +
			"working ticket must be resolved first.",
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
		Description: "Run a shell command in a clone of the project repository. Commands already " +
			"run INSIDE the clone — never cd into a path. The clone is not auto-updated, so run " +
			"`git fetch origin` before inspecting origin/main. Use git/gh to find the commit that " +
			"carries a ticket's work on origin/main (its SHA is what update_ticket done_commit " +
			"needs), and rg/grep/find to search the repository. Only an allowlisted set of " +
			"commands is reachable.",
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
