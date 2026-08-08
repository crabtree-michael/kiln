package board

import (
	"errors"
	"fmt"
)

// ErrNotFound — the ticket id does not exist (03 §4).
var ErrNotFound = errors.New("board: ticket not found")

// ErrEmptyTitle — CreateTicket's title precondition failed (03 §4: "title
// non-empty"). A pure input-validation error on a not-yet-created ticket, so
// it is neither an ErrNotFound nor a state-transition failure; it is returned
// before any transaction opens, so a rejected CreateTicket never writes or
// emits (03 I7).
var ErrEmptyTitle = errors.New("board: ticket title must be non-empty")

// ErrNoFreeWorker — an operation needed a free worker slot to bind and there
// was none (03 I3). Two callers raise it, both of which ask for a specific
// binding rather than waiting their turn: a dev SeedTicket planting a
// working/blocked ticket, and ReassignSandbox moving a ticket to a different
// slot. The real pull never fails this way — it simply waits for capacity.
var ErrNoFreeWorker = errors.New("board: no free worker to bind")

// ErrCommitAlreadyUsed — AcceptToDone was given a commit SHA already recorded
// on another ticket in the project (03 §4). One commit maps to at most one
// ticket, so accepting a second ticket with the same SHA is refused rather
// than silently double-assigning the commit. Fed back to the brain verbatim as
// a tool error (06 §6). OtherID names the ticket that already owns the commit.
type ErrCommitAlreadyUsed struct {
	SHA     string   // the commit that is already spent
	OtherID TicketID // the ticket already linked to it
}

func (e *ErrCommitAlreadyUsed) Error() string {
	return fmt.Sprintf("board: commit %s is already linked to ticket %s", e.SHA, e.OtherID)
}

// ErrCircularDependency — AddDependency was asked for an edge that would let a
// ticket wait, directly or through a chain, on itself (0013). Such a cycle is
// unsatisfiable: every ticket in it is skipped by the pull until one of the
// others is done, which can never happen, so the whole ring would sit Ready
// forever with no visible cause. Refused at the point the edge is added, which
// is the only moment the graph is known to be acyclic beforehand.
//
// Path is the existing chain that closes the ring, from DependsOn back to
// Ticket, so the caller (and the brain, which gets this verbatim as a tool
// error — 06 §6) can name the tickets involved rather than just the two ends.
// A self-edge is the degenerate case: Ticket == DependsOn and Path is empty.
type ErrCircularDependency struct {
	Ticket    TicketID   // the ticket the edge would be added to
	DependsOn TicketID   // the dependency that would close the ring
	Path      []TicketID // DependsOn's existing route back to Ticket, if any
}

func (e *ErrCircularDependency) Error() string {
	if e.Ticket == e.DependsOn {
		return fmt.Sprintf("board: ticket %s cannot depend on itself", e.Ticket)
	}
	return fmt.Sprintf("board: ticket %s cannot depend on %s — %s already waits on %s",
		e.Ticket, e.DependsOn, e.DependsOn, e.Ticket)
}

// ErrInvalidTransition — an operation's precondition failed (03 §4). Strict by
// design (03 D8): repeated or illegal transitions are loud typed errors, never
// no-ops, so caller bugs surface immediately.
type ErrInvalidTransition struct {
	From      State  // the ticket's actual state
	Attempted string // the operation that was refused
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("board: cannot %s a ticket in state %q", e.Attempted, e.From)
}
