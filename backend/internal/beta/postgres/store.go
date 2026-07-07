// Package postgres is beta's store adapter (mirrors push/postgres): the only
// code that touches the beta_signups table. It owns the migrations in
// ./migrations and is wired in at the composition root (02 §2, backend/cmd/kiln).
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/crabtree-michael/kiln/backend/internal/beta"
)

// Store implements beta.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ beta.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (mirrors push/postgres.New).
func New(db *sql.DB) *Store { return &Store{db: db} }

// Save inserts the email, treating a repeat as a no-op: a visitor who submits
// the same address twice must not duplicate the row or surface an error, so the
// unique-email conflict is swallowed (mirrors push's upsert idempotence).
func (s *Store) Save(ctx context.Context, email string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO beta_signups (email)
		VALUES ($1)
		ON CONFLICT (email) DO NOTHING`, email); err != nil {
		return fmt.Errorf("beta/postgres: save signup: %w", err)
	}
	return nil
}
