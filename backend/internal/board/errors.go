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

// ErrNoFreeWorker — a dev SeedTicket asked for a working/blocked ticket but no
// worker slot is free to bind (03 I3). Dev-seam only; the real pull never fails
// this way (it simply waits for capacity).
var ErrNoFreeWorker = errors.New("board: no free worker to bind for seed")

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
