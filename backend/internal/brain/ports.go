package brain

import (
	"context"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// BoardAPI is the brain's port onto the Board API's mutation operations
// (03 §4) — everything the write tools need except the read path (split out
// as BoardReader below) and RunPull, which is never a brain tool (03 I6).
// Method signatures match board.Service's exactly, so *board.Service
// satisfies this port directly at the composition root with no adapter (see
// the compile-time assertion below); board's typed errors (ErrNotFound,
// ErrInvalidTransition) surface through these calls and are fed back to the
// model verbatim as tool results (06 §6, §8).
//
// After the CRUD consolidation (06 §4 amended), the single update_ticket tool
// is a *facade* over ShapeTicket / RequestApproval / MarkReady / MarkBlocked /
// AcceptToDone — those verbs are no longer separate tools, but the port methods
// remain so the facade can route each patch field to the right typed operation
// and preserve every precondition.
type BoardAPI interface {
	// CreateTicket → tool create_ticket (06 §4).
	CreateTicket(ctx context.Context, title, body string) (board.Ticket, error)
	// ShapeTicket → update_ticket title/body/priority.
	ShapeTicket(ctx context.Context, id board.TicketID, patch board.ShapePatch) (board.Ticket, error)
	// MarkReady → update_ticket state="ready".
	MarkReady(ctx context.Context, id board.TicketID) (board.Ticket, error)
	// SendToAgent → tool send_to_agent. Destructive when the instruction
	// would discard in-flight work — confirm-before-destructive is
	// prompt-level, not mechanical (06 §7); this port has no notion of it.
	SendToAgent(ctx context.Context, id board.TicketID, instruction string) (board.Ticket, error)
	// MarkBlocked → update_ticket state="blocked" (reason required).
	MarkBlocked(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error)
	// AcceptToDone → update_ticket state="done". Always destructive — releases
	// and recycles the worker (06 §7).
	AcceptToDone(ctx context.Context, id board.TicketID) (board.Ticket, error)
	// ArchiveTicket → tool delete_ticket (06 §4 amended). Soft-deletes a
	// non-active ticket; an active (working/blocked) ticket is refused with a
	// typed board error, fed back verbatim (06 §6, §8).
	ArchiveTicket(ctx context.Context, id board.TicketID) (board.Ticket, error)
	// RequestApproval → update_ticket approval_requested=true (08 §5). Sets
	// approval_requested on a Shaping ticket so it surfaces as a proposal card;
	// the gate is at the brain's discretion (08 §5, §9 D5). Precondition
	// state==shaping surfaces as a typed board error, fed back verbatim (06 §6, §8).
	RequestApproval(ctx context.Context, id board.TicketID) (board.Ticket, error)
}

// NotificationStore is the brain's port onto the runtime's notification feed
// (08 §7): brain-authored update/preview cards. Satisfied structurally by
// *runtime.Service at the composition root (no adapter) — brain cannot import
// runtime (see doc.go's no-runtime-import rule), so there is no compile-time
// assertion here; INTEGRATION passes rtSvc for this port. Both ops append
// feed.updated transactionally inside the runtime (08 §7), making the runtime
// a second outbox writer.
type NotificationStore interface {
	// PostNotification → tool post_update (08 §7). kind is "update", or
	// "preview" when image_url is set; ticketID/imageURL are optional.
	PostNotification(ctx context.Context, kind, body string, ticketID, imageURL *string) error
	// RetractNotification → tool retract_update (08 §7). Stamps retracted_at so
	// the card drops from the feed.
	RetractNotification(ctx context.Context, id int64) error
	// EditNotification → tool edit_update (06 §4 amended, 08 §7). Amends a
	// still-active card's kind/body/image in place; kind is "preview" when an
	// image is set, else "update". Appends feed.updated transactionally.
	EditNotification(ctx context.Context, id int64, kind, body string, imageURL *string) error
}

// FeedReader is the brain's read port onto the active feed (06 §4 amended,
// 08 §7): list the update/preview cards the brain may still edit or retract, so
// it knows their ids. Provider-neutral neutral Update shapes out; satisfied by a
// cmd/kiln adapter over *runtime.Service (brain cannot import runtime, so no
// assertion here — the same pattern as AgentInspector). The feed is pulled on
// demand via list_updates rather than injected, keeping per-pass context lean.
type FeedReader interface {
	// ListUpdates → tool list_updates. Active (neither seen nor retracted)
	// cards, newest-first.
	ListUpdates(ctx context.Context) ([]Update, error)
}

// BoardReader is the brain's port onto the board's read path (03 §4
// GetBoard). Called once per pass to build fresh context (06 §3, D3) — it is
// never exposed to the model as a tool: the snapshot is injected, not
// requested, so the model can't skip reading state and no round-trip is
// spent on it.
type BoardReader interface {
	// GetBoard → tool list_tickets (06 §4 amended). The full board snapshot,
	// rendered compactly (no bodies) as the model's ticket roster. No longer
	// injected every pass — the model pulls it on demand, so a pass that does
	// not need board state spends no tokens on it.
	GetBoard(ctx context.Context) (board.Snapshot, error)
	// GetTicket → tool get_ticket (06 §4 amended). One ticket in full, including
	// its body; ErrNotFound for a missing or archived id, fed back verbatim.
	GetTicket(ctx context.Context, id board.TicketID) (board.Ticket, error)
}

// Say is the brain's port onto the runtime's Say path (07 §3, §6): append
// the kiln transcript row, then push a say SSE event. Every user-visible
// reply goes through this — including the dead-letter system-error message
// (06 §8) — and it is also tool #7 in the tool set (06 §4). A duplicate Say
// on crash-replay is accepted as benign (06 §6).
type Say interface {
	Say(ctx context.Context, text string) error
}

// ConversationReader is the brain's port onto the persisted transcript
// (07 §3): Recent(n) serves the last-n-messages block of a pass's context
// (06 §3.2), oldest first. Owned and persisted by the runtime module; the
// brain holds no copy of its own.
type ConversationReader interface {
	Recent(ctx context.Context, n int) ([]Message, error)
}

// AgentInspector is the brain's read seam into the agent runtime (05 §2, 06 §4
// amended): list running agents and read one agent's latest completed output.
// Provider-neutral — worker ids in, neutral status/output out. Best-effort: a
// read failure becomes a tool error the pass loop absorbs (06 §5), never a pass
// failure. Satisfied structurally by a cmd/kiln adapter over *agent.Service;
// brain cannot import internal/agent, so there is no assertion here.
type AgentInspector interface {
	// ListAgents → tool list_agents.
	ListAgents(ctx context.Context) ([]AgentInfo, error)
	// GetAgentUpdates → tool get_agent_updates, keyed by board worker id.
	GetAgentUpdates(ctx context.Context, workerID string) (AgentUpdate, error)
}

// RepoShell is the brain's read-oriented window into the real project
// repository (a maintained local clone). Provider-neutral — a shell command
// string in, a neutral RepoResult out. Best-effort: a Run error or an
// Unavailable result becomes a tool result the pass loop absorbs (06 §5),
// never a pass failure; a non-zero exit is a normal RepoResult, not an error.
// Satisfied structurally by a cmd/kiln adapter over *repo.Shell; brain cannot
// import internal/repo, so there is no compile-time assertion here.
type RepoShell interface {
	// Run → tool bash: run command in the clone, return its combined output.
	Run(ctx context.Context, command string) (RepoResult, error)
	// VerifyOnMain fetches origin and reports whether sha is a real commit on
	// origin/main. Gates update_ticket state="done" (06 §7 amended) under the
	// "main" merge-gate mode: a ticket cannot be accepted unless its named commit
	// is verifiably merged to main.
	VerifyOnMain(ctx context.Context, sha string) (RepoVerify, error)
	// VerifyInPR reports whether sha is associated with a pull request (open or
	// merged). Gates update_ticket state="done" under the "pr" merge-gate mode:
	// the work only needs to exist in a PR, not to have landed on main.
	VerifyInPR(ctx context.Context, sha string) (RepoVerify, error)
}

// Under multi-tenancy (11 §3) *board.Service's methods take a leading
// projectID, so it no longer satisfies these project-neutral ports directly;
// the composition root (cmd/kiln) supplies thin per-project adapters that close
// over the resolved projectID and inject it into each board call. The brain
// stays tenancy-unaware — a pass is always scoped to exactly one project by the
// adapters it was constructed with — so no compile-time assertion belongs here.
