package runtime

import (
	"context"
	"time"
)

// BoardTicket is the subset of a board ticket the feed needs (08 §3): enough
// to render a blocker or proposal card. Defined locally because this module
// never imports internal/board (the same layering rule the board and brain
// modules state in the other direction — see the topic-const comment in
// service.go). The composition root supplies the adapter that reads
// board.GetBoard and fills these.
type BoardTicket struct {
	ID            string
	Title         string
	Body          string
	BlockedReason string
	UpdatedAt     time.Time
}

// BoardView is the runtime's read of current board state for feed assembly
// (08 §3): the blocked tickets (blocker cards), the shaping tickets awaiting
// approval (proposal cards), and the working/blocked counts for the header
// summary. The composition-root adapter maps a board.Snapshot to this:
//   - Blocked      <- snap.Blocked
//   - Proposals    <- snap.Shaping filtered to ApprovalRequested
//   - WorkingCount <- len(snap.Working)
//   - BlockedCount <- len(snap.Blocked)
type BoardView struct {
	Blocked      []BoardTicket
	Proposals    []BoardTicket
	WorkingCount int
	BlockedCount int
	// TicketTitles maps every current ticket id to its title, so a
	// ticket-tagged update/preview note can render the linked ticket's title
	// as its card label (08 §3 — "ticket title for board cards"). Nil/absent
	// entries leave the label empty, which is valid for a note with no ticket.
	TicketTitles map[string]string
}

// BoardReader is the runtime's read-only port onto board state for feed
// assembly (08 §3), scoped to one project's board (11 §3). Implemented by a
// composition-root adapter over *board.Service (this module holds no board
// import).
type BoardReader interface {
	BoardView(ctx context.Context, projectID string) (BoardView, error)
}

// FeedCard is one backlog item on the primary screen (08 §3): a blocker,
// proposal, update, or preview. Runtime domain type; the api package maps it
// to wire.FeedCard. Hybrid-sourced — blockers/proposals derive from board
// state, updates/previews from notification rows — but the client renders one
// ordered list and never knows the difference.
type FeedCard struct {
	Kind           string  // blocker | proposal | update | preview | poke | done
	ID             string  // stable card id: blocker:<tid> | proposal:<tid> | update:<nid>
	Label          string  // card title (ticket title for board cards)
	Body           string  // blocked_reason | shaped summary | note body
	TicketID       *string // set for board-sourced cards and ticket-tagged notes
	ImageURL       *string // set for kind=preview
	NotificationID *int64  // set for update/preview cards (the seen high-water source)
	// GitHubURL/GitHubLabel are set on kind=done cards (08 §7): the link to the
	// landed commit or pull request and its clickable label (abbreviated SHA or
	// "#<number>"), rendered as the card's second line. Nil otherwise.
	GitHubURL   *string
	GitHubLabel *string
	CreatedAt   time.Time
}

// FeedSummary is the server-derived header status (08 §2): the counts the
// client renders the one-line summary from. Runtime domain type; the api
// package maps it to wire.FeedSummary.
type FeedSummary struct {
	BlockerCount int        // unresolved blockers
	UpdateCount  int        // unseen updates/previews
	StreamCount  int        // working + blocked (active tickets)
	Building     int        // working
	Idle         int        // blocked
	LastWordAt   *time.Time // newest notification's created_at, or nil
	// LastSeenNotificationID is the persistent seen high-water mark (08 D2′):
	// the greatest notification id the user has acked. It marks the last-seen
	// divider boundary — update/preview cards with a greater NotificationID are
	// new since last visit; those at or below it are retained history. Nil when
	// nothing has ever been seen.
	LastSeenNotificationID *int64
}

// FeedSnapshot is GET /api/feed's body and the feed SSE event payload — the
// identical absolute shape (08 §3, D2′). Cards are in strict order: unresolved
// blockers, then pending proposals, then updates newest-first (seen and unseen —
// retained history). Cards carries only the NEWEST PAGE of updates;
// HasMoreHistory signals older ones are pageable via FeedHistory. Runtime
// domain type; the api package maps it to wire.FeedSnapshot.
type FeedSnapshot struct {
	Summary FeedSummary
	Cards   []FeedCard
	// HasMoreHistory is true when retained update/preview history exists older
	// than the oldest update card in Cards — page it in via FeedHistory (08 D2′).
	HasMoreHistory bool
}

// ActivityEvent is the ephemeral activity SSE payload (08 §4), never stored.
// thinking brackets a brain pass (On toggles the spinner); toast confirms one
// side-effect board transition (Verb + TicketTitle render the auto-dismissing
// pill, TicketID lets a tap open the ticket). Runtime domain type; the api
// package maps it to wire.ActivityEvent.
type ActivityEvent struct {
	Kind        string // thinking | toast
	On          *bool  // set for kind=thinking
	Verb        string // set for kind=toast: started | nudged | finished | queued
	TicketID    string // set for kind=toast
	TicketTitle string // set for kind=toast
}

// FeedPusher is the runtime's port onto the api SSE hub's feed fan-out (08
// §3): push a fresh absolute FeedSnapshot to the project's connected clients
// (11 §3). Mirrors SayPusher; implemented by the api package's Hub.
type FeedPusher interface {
	PushFeed(ctx context.Context, projectID string, snap FeedSnapshot) error
}

// ActivityPusher is the runtime's port onto the api SSE hub's activity
// fan-out (08 §4): push one ephemeral ActivityEvent to the project's
// connected clients (11 §3). Mirrors SayPusher; implemented by the api
// package's Hub.
type ActivityPusher interface {
	PushActivity(ctx context.Context, projectID string, ev ActivityEvent) error
}
