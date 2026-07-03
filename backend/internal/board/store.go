package board

import "context"

// Store is the board's persistence port (03 §9, per the 02 §2 layering).
// Implemented by ./postgres, injected at the composition root
// (backend/cmd/kiln), and consumed only by Service — nothing outside this
// module touches the board tables (03 I8).
type Store interface {
	// Tx runs fn as one short READ COMMITTED transaction (03 §6). An error
	// from fn rolls back everything — the ticket change and its outbox
	// appends commit together or not at all (03 I7).
	Tx(ctx context.Context, fn func(Tx) error) error

	// Snapshot reads the full board in render order (03 §4 GetBoard).
	Snapshot(ctx context.Context) (Snapshot, error)
}

// Tx is the transaction-scoped view of the store. The service works
// lock-then-check (03 §6): lock the target row first, then verify the
// operation's precondition on what the lock returned.
type Tx interface {
	// LockTicket is SELECT … FOR UPDATE on one ticket. Returns ErrNotFound
	// if the id does not exist. Targeted operations must conflict loudly —
	// no SKIP LOCKED here (03 §6).
	LockTicket(id TicketID) (Ticket, error)

	// InsertTicket persists a new ticket (CreateTicket).
	InsertTicket(t Ticket) error

	// UpdateTicket persists a mutation of a previously locked ticket.
	UpdateTicket(t Ticket) error

	// NextReadyTicket locks the next pullable ticket in pull order —
	// priority DESC, ready_at ASC, id ASC — using FOR UPDATE SKIP LOCKED
	// (03 §5). ok is false when no ready ticket is available.
	NextReadyTicket() (t Ticket, ok bool, err error)

	// FreeWorker locks a worker that no active ticket references, using
	// FOR UPDATE SKIP LOCKED (03 §5). ok is false when none is free.
	FreeWorker() (w Worker, ok bool, err error)

	// AppendOutbox records one emission in this transaction (03 §7, I7).
	AppendOutbox(e Emission) error
}
