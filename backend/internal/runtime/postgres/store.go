// Package postgres is the runtime's store adapter over the two queue tables
// (04 §2): the events table it owns outright, and the outbox's
// delivery-state columns (the board owns the outbox emission columns and
// appends the rows — 03 §2.4). Pure adapter; the drain semantics live in the
// runtime worker.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// errNotImplemented marks scaffold stubs; see docs/specs/04-runtime-and-api.md.
var errNotImplemented = errors.New("runtime/postgres: not implemented (scaffold)")

// Store implements runtime.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ runtime.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup.
func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) InsertEvent(ctx context.Context, t runtime.EventType, payload []byte) (int64, error) {
	return 0, errNotImplemented
}

func (s *Store) ClaimNextDue(ctx context.Context, q runtime.QueueName) (runtime.Entry, bool, error) {
	return runtime.Entry{}, false, errNotImplemented
}

func (s *Store) MarkDone(ctx context.Context, q runtime.QueueName, id int64) error {
	return errNotImplemented
}

func (s *Store) MarkRetry(
	ctx context.Context, q runtime.QueueName, id int64, lastError string, nextAttemptAt time.Time,
) error {
	return errNotImplemented
}

func (s *Store) MarkDead(ctx context.Context, q runtime.QueueName, id int64, lastError string) error {
	return errNotImplemented
}
