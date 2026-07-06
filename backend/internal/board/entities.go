package board

import "time"

// State is a ticket's single stored position field (03 §2.1). Column and zone
// are derived render groupings, never stored (03 D1).
type State string

const (
	StateShaping State = "shaping" // Backlog · Shaping
	StateReady   State = "ready"   // Backlog · Ready — eligible for the pull
	StateWorking State = "working" // Developing · Working — worker bound
	StateBlocked State = "blocked" // Developing · Blocked — worker held
	StateDone    State = "done"    // Done — worker released
)

// Active reports whether the state binds a worker (03 I3).
func (s State) Active() bool { return s == StateWorking || s == StateBlocked }

// TicketID and WorkerID are uuids; generation is the store adapter's concern.
type (
	TicketID string
	WorkerID string
)

// Ticket per 03 §2.2.
type Ticket struct {
	ID            TicketID
	Title         string     // required, non-empty
	Body          string     // the shaped details; grows during Shaping
	State         State      // one of the five State values (03 I1)
	Priority      int        // backlog ordering for the pull; higher pulls first
	WorkerID      *WorkerID  // non-nil iff State is working/blocked (03 I3)
	BlockedReason *string    // non-nil iff State is blocked (03 I4)
	ReadyAt       *time.Time // set by MarkReady; pull tie-breaker (03 §5)
	// ApprovalRequested marks a shaping ticket the brain has surfaced for the
	// user to approve (08 §5 proposal card). True only while shaping — the DB
	// CHECK ties it to state='shaping'; MarkReady clears it (08 §B).
	ApprovalRequested bool
	// ArchivedAt is set when the ticket has been archived (soft-deleted) — the
	// brain's delete_ticket (06 §4 amended). An archived ticket is invisible to
	// every read path (Snapshot, GetTicket) and to the pull, and every targeted
	// operation treats it as ErrNotFound; the row is retained for history.
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// StateChangedAt is when the ticket last entered its current State (03
	// §2.2). Unlike UpdatedAt it advances only on a real state transition, never
	// on a same-state mutation such as a Working→Working nudge (SendToAgent), so
	// it is the true "time in status" clock the client renders.
	StateChangedAt time.Time
}

// Worker is a capacity slot, not a live resource handle (03 §2.3): the WIP
// cap is the row count, and free vs busy is derived — busy iff an active
// ticket references it (03 D2). There is no status column, and no provider
// detail — the agent-runtime module (05) owns the worker↔provider mapping.
type Worker struct {
	ID        WorkerID
	CreatedAt time.Time
}

// Snapshot is GetBoard's result (03 §4): every ticket grouped in render
// order, plus capacity. Snapshots are absolute; the client never receives
// deltas (03 D7). The wire shape lives in /schema.
type Snapshot struct {
	Shaping []Ticket // priority desc, then created_at asc
	Ready   []Ticket // exact pull order (03 §5), so the user sees what pulls next
	Blocked []Ticket // stacked above Working within Developing
	Working []Ticket
	Done    []Ticket

	WorkerTotal int
	WorkerFree  int
}
