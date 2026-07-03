// Package postgres is the agent module's store adapter over its one table,
// agent_turns (05 §7) — the module owns this table and the migrations in
// ./migrations; it is adapter-layer state, never board state (03 I8). Pure
// adapter: the machine's rules live in the agent service.
package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// errNotImplemented marks scaffold stubs; see docs/specs/05-agent-runtime.md.
var errNotImplemented = errors.New("agent/postgres: not implemented (scaffold)")

// Store implements agent.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ agent.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (tooling TBD — 02 §14).
func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Record(ctx context.Context, t agent.Turn) (bool, error) {
	return false, errNotImplemented
}

func (s *Store) ListNonTerminal(ctx context.Context) ([]agent.Turn, error) {
	return nil, errNotImplemented
}

func (s *Store) Update(ctx context.Context, t agent.Turn) error {
	return errNotImplemented
}

func (s *Store) LatestForWorker(ctx context.Context, workerID string) (agent.Turn, bool, error) {
	return agent.Turn{}, false, errNotImplemented
}
