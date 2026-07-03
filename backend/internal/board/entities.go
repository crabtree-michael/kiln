package board

import "time"

// State is a ticket's single stored position field (03 §2.1). Column and zone
// are derived render groupings, never stored (03 D1).
type State string

const (
	StateShaping State = "shaping" // Backlog · Shaping
	StateReady   State = "ready"   // Backlog · Ready — eligible for the pull
	StateWorking State = "working" // Developing · Working — sandbox bound
	StateBlocked State = "blocked" // Developing · Blocked — sandbox held
	StateDone    State = "done"    // Done — sandbox released
)

// Active reports whether the state binds a sandbox (03 I3).
func (s State) Active() bool { return s == StateWorking || s == StateBlocked }

// TicketID and SandboxID are uuids; generation is the store adapter's concern.
type (
	TicketID  string
	SandboxID string
)

// Ticket per 03 §2.2.
type Ticket struct {
	ID            TicketID
	Title         string     // required, non-empty
	Body          string     // the shaped details; grows during Shaping
	State         State      // one of the five State values (03 I1)
	Priority      int        // backlog ordering for the pull; higher pulls first
	SandboxID     *SandboxID // non-nil iff State is working/blocked (03 I3)
	BlockedReason *string    // non-nil iff State is blocked (03 I4)
	ReadyAt       *time.Time // set by MarkReady; pull tie-breaker (03 §5)
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Sandbox is a capacity slot, not a live resource handle (03 §2.3): the WIP
// cap is the row count, and free vs busy is derived — busy iff an active
// ticket references it (03 D2). There is no status column.
type Sandbox struct {
	ID        SandboxID
	AmikaRef  *string // Amika's opaque sandbox identifier once known (02 §8)
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

	SandboxTotal int
	SandboxFree  int
}
