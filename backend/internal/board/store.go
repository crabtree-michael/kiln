package board

import "context"

// Store is the board's persistence port (03 §9, per the 02 §2 layering).
// Implemented by ./postgres, injected at the composition root
// (backend/cmd/kiln), and consumed only by Service — nothing outside this
// module touches the board tables (03 I8).
//
// Every read and write is scoped to one project (11 §3): projectID is the
// tenant key, threaded as the first parameter after ctx, and no method keeps
// a global (cross-project) read. A valid id belonging to another project
// behaves exactly like a missing one — ErrNotFound.
type Store interface {
	// Tx runs fn as one short READ COMMITTED transaction (03 §6). An error
	// from fn rolls back everything — the ticket change and its outbox
	// appends commit together or not at all (03 I7).
	Tx(ctx context.Context, fn func(Tx) error) error

	// Snapshot reads the project's full board in render order (03 §4
	// GetBoard). Tickets and worker counts cover only the given project.
	Snapshot(ctx context.Context, projectID string) (Snapshot, error)

	// GetTicket reads one of the project's tickets by id, backing the brain's
	// get_ticket tool (06 §4 amended). Returns ErrNotFound if the id does not
	// exist within the project or the ticket has been archived (an archived
	// ticket is gone from every read).
	GetTicket(ctx context.Context, projectID string, id TicketID) (Ticket, error)

	// SetWorkerHealth reconciles the health of every worker in the project in
	// one write: each id in erroredWorkerIDs becomes 'errored', every other of
	// the project's workers becomes 'ok' (03 §5 amended). A full reconcile (not
	// an incremental set) means a recovered or provider-dropped worker flips
	// back to healthy automatically. Called by the agent liveness reconciler
	// through the composition root; a worker marked 'errored' is excluded from
	// FreeWorker and the WorkerFree count, so the pull never binds a ticket to a
	// failing sandbox. Ids not owned by the project are ignored.
	SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error
}

// Tx is the transaction-scoped view of the store. The service works
// lock-then-check (03 §6): lock the target row first, then verify the
// operation's precondition on what the lock returned.
//
// Every method takes the operation's context so the adapter can issue
// context-scoped statements without capturing a context in a struct field
// (the idiomatic Go convention; Store.Tx already owns the transaction's
// lifetime). The context is the same one passed to Store.Tx. Like Store,
// every method is project-scoped (11 §3).
type Tx interface {
	// LockTicket is SELECT … FOR UPDATE on one of the project's tickets.
	// Returns ErrNotFound if the id does not exist within the project.
	// Targeted operations must conflict loudly — no SKIP LOCKED here (03 §6).
	LockTicket(ctx context.Context, projectID string, id TicketID) (Ticket, error)

	// InsertTicket persists a new ticket under the project (CreateTicket) and
	// returns the persisted row. ID and timestamp generation are the
	// adapter's concern (entities.go); the returned Ticket carries whatever
	// the adapter assigned, so the caller never has to re-read.
	InsertTicket(ctx context.Context, projectID string, t Ticket) (Ticket, error)

	// UpdateTicket persists a mutation of a previously locked ticket and
	// returns the persisted row (e.g. with updated_at refreshed by the
	// adapter), so every Service mutation can return an accurate Ticket
	// (03 §4: "every mutation returns the updated Ticket"). A ticket outside
	// the project is ErrNotFound.
	UpdateTicket(ctx context.Context, projectID string, t Ticket) (Ticket, error)

	// NextReadyTicket locks the project's next pullable ticket in pull order
	// — priority DESC, ready_at ASC, id ASC — using FOR UPDATE SKIP LOCKED
	// (03 §5). ok is false when no ready ticket is available in the project.
	NextReadyTicket(ctx context.Context, projectID string) (t Ticket, ok bool, err error)

	// FreeWorker locks one of the project's workers that no active ticket
	// references, using FOR UPDATE SKIP LOCKED (03 §5). ok is false when none
	// is free.
	FreeWorker(ctx context.Context, projectID string) (w Worker, ok bool, err error)

	// AppendOutbox records one emission for the project in this transaction
	// (03 §7, I7).
	AppendOutbox(ctx context.Context, projectID string, e Emission) error
}
