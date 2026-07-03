package brain

import (
	"context"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// BoardAPI is the brain's port onto six of the Board API's operations
// (03 §4) — everything the seven tools need except the read path (split out
// as BoardReader below) and RunPull, which is never a brain tool (03 I6).
// Method signatures match board.Service's exactly, so *board.Service
// satisfies this port directly at the composition root with no adapter (see
// the compile-time assertion below); board's typed errors (ErrNotFound,
// ErrInvalidTransition) surface through these calls and are fed back to the
// model verbatim as tool results (06 §6, §8).
type BoardAPI interface {
	// CreateTicket → tool create_ticket (06 §4).
	CreateTicket(ctx context.Context, title, body string) (board.Ticket, error)
	// ShapeTicket → tool shape_ticket.
	ShapeTicket(ctx context.Context, id board.TicketID, patch board.ShapePatch) (board.Ticket, error)
	// MarkReady → tool mark_ready.
	MarkReady(ctx context.Context, id board.TicketID) (board.Ticket, error)
	// SendToAgent → tool send_to_agent. Destructive when the instruction
	// would discard in-flight work — confirm-before-destructive is
	// prompt-level, not mechanical (06 §7); this port has no notion of it.
	SendToAgent(ctx context.Context, id board.TicketID, instruction string) (board.Ticket, error)
	// MarkBlocked → tool mark_blocked.
	MarkBlocked(ctx context.Context, id board.TicketID, reason string) (board.Ticket, error)
	// AcceptToDone → tool accept_to_done. Always destructive — releases and
	// recycles the worker (06 §7).
	AcceptToDone(ctx context.Context, id board.TicketID) (board.Ticket, error)
}

// BoardReader is the brain's port onto the board's read path (03 §4
// GetBoard). Called once per pass to build fresh context (06 §3, D3) — it is
// never exposed to the model as a tool: the snapshot is injected, not
// requested, so the model can't skip reading state and no round-trip is
// spent on it.
type BoardReader interface {
	GetBoard(ctx context.Context) (board.Snapshot, error)
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

// Compile-time assertions: *board.Service is the Board API's only
// implementation in v1 and satisfies both ports here with no adapter
// (see doc.go and ports.go's BoardAPI comment).
var (
	_ BoardAPI    = (*board.Service)(nil)
	_ BoardReader = (*board.Service)(nil)
)
