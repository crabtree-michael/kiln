package board

import "slices"

// Operation names a Board API operation whose precondition is a check on the
// ticket's current State (03 §4). The values are exactly the names
// ErrInvalidTransition reports in Attempted, so "what this state allows" and
// "what the board refused" are one vocabulary. (mutate's snake_case `op` label
// is a separate naming — it identifies the board.transition log record, not the
// precondition.)
type Operation string

const (
	OpShapeTicket     Operation = "ShapeTicket"
	OpRequestApproval Operation = "RequestApproval"
	OpMarkReady       Operation = "MarkReady"
	OpSendToAgent     Operation = "SendToAgent"
	OpMarkBlocked     Operation = "MarkBlocked"
	OpAcceptToDone    Operation = "AcceptToDone"
	OpArchiveTicket   Operation = "ArchiveTicket"
	OpKillSandbox     Operation = "KillSandbox"
	OpReassignSandbox Operation = "ReassignSandbox"
)

// stateGatedOperations is every Operation in the order AllowedOps reports them
// — the order a ticket's life runs: shape it, queue it, work it, finish it,
// drop it, and the manual sandbox controls last.
var stateGatedOperations = []Operation{
	OpShapeTicket,
	OpRequestApproval,
	OpMarkReady,
	OpSendToAgent,
	OpMarkBlocked,
	OpAcceptToDone,
	OpArchiveTicket,
	OpKillSandbox,
	OpReassignSandbox,
}

// statePreconditions is the single table of which states each operation accepts
// (03 §4). It is the only copy: the operations' own guards read it (guardState,
// guardBoundWorker) and AllowedOps derives the reverse view from it, so a caller
// can never be told an operation is available that the operation itself would
// then refuse.
//
// State is the *whole* precondition for ShapeTicket, RequestApproval, MarkReady,
// MarkBlocked and ArchiveTicket. The four worker-bound operations additionally
// need the ticket's binding present (guardBoundWorker) — an invariant of the
// active states (03 I3), not an independent choice — ReassignSandbox needs a
// free slot to move to (ErrNoFreeWorker), and AcceptToDone refuses a commit
// already spent on another ticket (ErrCommitAlreadyUsed). Those are not state
// questions, so an operation this table permits can still fail.
var statePreconditions = map[Operation][]State{
	OpShapeTicket:     {StateShaping, StateReady},
	OpRequestApproval: {StateShaping},
	OpMarkReady:       {StateShaping},
	OpSendToAgent:     {StateWorking, StateBlocked},
	OpMarkBlocked:     {StateWorking},
	OpAcceptToDone:    {StateWorking, StateBlocked},
	// Only a *working* ticket is refused: it has a live agent mid-turn. A blocked
	// one is stalled on a human by definition, so it can go directly.
	OpArchiveTicket:   {StateShaping, StateReady, StateBlocked, StateDone},
	OpKillSandbox:     {StateWorking, StateBlocked},
	OpReassignSandbox: {StateWorking, StateBlocked},
}

// PermittedFrom reports whether from satisfies op's state precondition. An
// operation with no entry in the table permits nothing.
func (o Operation) PermittedFrom(from State) bool {
	return slices.Contains(statePreconditions[o], from)
}

// AllowedOps returns the operations state s permits, in stateGatedOperations
// order — the reverse view of statePreconditions. It answers "what can be done
// to a ticket sitting here" without trying it and collecting an
// ErrInvalidTransition.
//
// The brain renders this onto get_ticket / list_tickets so the model checks
// what a state permits instead of discovering it by failed call — 39% of
// update_ticket calls were failing, most of them guesses at an unavailable
// transition (docs/brain-optimization-2026-08-05.md §2). Non-state
// preconditions still apply; see statePreconditions.
func (s State) AllowedOps() []Operation {
	allowed := make([]Operation, 0, len(stateGatedOperations))
	for _, op := range stateGatedOperations {
		if op.PermittedFrom(s) {
			allowed = append(allowed, op)
		}
	}
	return allowed
}

// guardState is an operation's state precondition as a check: nil when from
// satisfies it, the typed refusal (03 D8) when it does not.
func guardState(op Operation, from State) error {
	if op.PermittedFrom(from) {
		return nil
	}
	return &ErrInvalidTransition{From: from, Attempted: string(op)}
}

// guardBoundWorker is guardState for the operations that also need the ticket's
// worker binding in hand. 03 I3 guarantees an active ticket has one, so the nil
// check is the belt-and-braces that keeps a violated invariant a typed error
// rather than a panic — it refuses with the same ErrInvalidTransition, since
// from the caller's side the ticket is simply not in a state that can be sent
// to, accepted, or re-slotted.
func guardBoundWorker(op Operation, t *Ticket) error {
	if err := guardState(op, t.State); err != nil {
		return err
	}
	if t.WorkerID == nil {
		return &ErrInvalidTransition{From: t.State, Attempted: string(op)}
	}
	return nil
}
