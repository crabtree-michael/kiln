package brain

import "strings"

// The brain's action surface as the model is shown it: the tool names, the
// argument struct each call is unmarshalled into, and the Tools table of
// JSON-Schema definitions the LLM port offers every pass. Declarations only —
// nothing here calls a port. The three files it pairs with are tool_dispatch.go
// (name → handler), tool_handlers.go (the handlers) and tool_render.go (how
// their results read back), so adding a tool is one append in each rather than
// five edits scattered through one file.

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
			"use get_ticket for a single ticket's full body. Read-only — issue it in the same round " +
			"as your other opening reads rather than a round of its own.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolGetTicket,
		Description: "Read one ticket in full, including its body, by id. The result's " +
			"\"allowed now\" line lists exactly what its current state accepts — check it rather " +
			"than attempting a change the state will refuse. Read-only — when you already know the " +
			"id (the event names it), ask for it in the same round as your other reads.",
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
			"next one. Read-only — batch it with your other reads in one round.",
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
			"so you can edit or retract one. Read-only — batch it with your other reads in one round.",
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
			"ticket or idle. Read-only — batch it with your other reads in one round.",
		InputSchema: objectSchema([]string{}, map[string]any{}),
	},
	{
		Name: ToolGetAgentUpdates,
		Description: "Read an agent's latest completed output by worker id — use to check " +
			"what a working agent last produced. Read-only — an agent event already names its " +
			"worker, so ask for this in your opening round alongside the board reads instead of " +
			"a round later.",
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
			"commands is reachable. An inspection command shares a round with your other reads " +
			"rather than taking one of its own.",
		InputSchema: objectSchema([]string{fieldCommand}, map[string]any{
			fieldCommand: stringSchema("The shell command to run in the repo clone."),
		}),
	},
}
