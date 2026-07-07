package steward

import (
	"context"
	"time"
)

// PokeRecord is one row of the steward_pokes table: the fact that a Working
// ticket's stalled agent was poked, and when. Exactly one row per ticket while
// it is Working — a ticket is poked at most once per Working episode, and the
// row is deleted when the ticket leaves Working (a clean slate) or is escalated
// to Blocked. PokedAt is the post-poke clock: a re-stall is measured from it,
// not from the agent's last activity, which makes the escalation decision
// race-proof against the brief window before the poke's own turn is recorded.
// ProjectID records which project the ticket belonged to when poked — the
// sweep correlates each record to the project it is currently sweeping through
// it. Empty for legacy rows written before tenancy (NULL project_id).
type PokeRecord struct {
	ProjectID string
	TicketID  string
	WorkerID  string
	PokedAt   time.Time
}

// Store is the module's persistence port over its one table, steward_pokes.
// Adapter-layer state, not board state (the board stays the single source of
// truth for ticket state, 03 I8) — this only remembers which stalls have been
// poked so a second stall can be told from a first. Implemented by ./postgres,
// injected at the composition root.
type Store interface {
	// List returns every current poke record, across all projects — ticket ids
	// are globally unique, so the service scopes records by their ProjectID.
	List(ctx context.Context) ([]PokeRecord, error)
	// Upsert records (or refreshes) that ticketID's stalled agent on workerID
	// in projectID was poked at pokedAt. Keyed by ticket_id.
	Upsert(ctx context.Context, projectID, ticketID, workerID string, pokedAt time.Time) error
	// Delete removes ticketID's poke record, if any. Idempotent.
	Delete(ctx context.Context, ticketID string) error
}
