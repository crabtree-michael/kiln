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

// Store implements runtime.Store and runtime.MessageStore over Postgres.
type Store struct {
	db *sql.DB
}

var (
	_ runtime.Store        = (*Store)(nil)
	_ runtime.MessageStore = (*Store)(nil)
)

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

// AppendUserMessageAndEnqueueEvent implements runtime.MessageStore (07 §3):
// one transaction, INSERT into messages (role='user') and INSERT into
// events (type='human.message', payload={text}) — the transcript and the
// event queue commit together or not at all.
func (s *Store) AppendUserMessageAndEnqueueEvent(ctx context.Context, text string) (int64, int64, error) {
	return 0, 0, errNotImplemented
}

// AppendKilnMessage implements runtime.MessageStore (07 §3): INSERT into
// messages (role='kiln'), the first half of the Say port.
func (s *Store) AppendKilnMessage(ctx context.Context, text string) (runtime.Message, error) {
	return runtime.Message{}, errNotImplemented
}

// Recent implements runtime.MessageStore (07 §3): the last n rows by id
// DESC, returned oldest first.
func (s *Store) Recent(ctx context.Context, n int) ([]runtime.Message, error) {
	return nil, errNotImplemented
}
