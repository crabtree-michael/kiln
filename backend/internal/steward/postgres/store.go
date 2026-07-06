// Package postgres is the steward module's store adapter over its one table,
// steward_pokes — the module owns this table and the migrations in
// ./migrations. Adapter-layer state (which stalls have been poked), never board
// state (03 I8): the board remains the single source of truth for ticket state.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/steward"
)

// Store implements steward.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ steward.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup by the composition root.
func New(db *sql.DB) *Store { return &Store{db: db} }

// List returns every current poke record. Only the error return is named, so a
// deferred rows.Close failure can fold into it (satisfying errcheck's
// check-blank without a non-error named return), mirroring the board/runtime
// store adapters.
func (s *Store) List(ctx context.Context) (_ []steward.PokeRecord, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ticket_id, worker_id, poked_at FROM steward_pokes`)
	if err != nil {
		return nil, fmt.Errorf("steward/postgres: query poke records: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("steward/postgres: close poke records: %w", cerr)
		}
	}()

	var out []steward.PokeRecord
	for rows.Next() {
		var r steward.PokeRecord
		if serr := rows.Scan(&r.TicketID, &r.WorkerID, &r.PokedAt); serr != nil {
			return nil, fmt.Errorf("steward/postgres: scan poke record: %w", serr)
		}
		out = append(out, r)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("steward/postgres: iterate poke records: %w", rerr)
	}
	return out, nil
}

// Upsert records (or refreshes) a poke, keyed by ticket_id.
func (s *Store) Upsert(ctx context.Context, ticketID, workerID string, pokedAt time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO steward_pokes (ticket_id, worker_id, poked_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (ticket_id)
		 DO UPDATE SET worker_id = EXCLUDED.worker_id, poked_at = EXCLUDED.poked_at`,
		ticketID, workerID, pokedAt); err != nil {
		return fmt.Errorf("steward/postgres: upsert poke record: %w", err)
	}
	return nil
}

// Delete removes a poke record; a missing row is not an error.
func (s *Store) Delete(ctx context.Context, ticketID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM steward_pokes WHERE ticket_id = $1`, ticketID); err != nil {
		return fmt.Errorf("steward/postgres: delete poke record: %w", err)
	}
	return nil
}
