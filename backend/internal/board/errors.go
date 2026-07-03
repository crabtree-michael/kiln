package board

import (
	"errors"
	"fmt"
)

// ErrNotFound — the ticket id does not exist (03 §4).
var ErrNotFound = errors.New("board: ticket not found")

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
