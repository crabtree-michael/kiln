// Package postgres is the board's store adapter (03 §9): the only code that
// touches the board tables. It owns the migrations in ./migrations and is
// wired in at the composition root (02 §2, backend/cmd/kiln). Pure adapter —
// every rule lives in the board service; every query here realizes a
// documented contract (lock-then-check, SKIP LOCKED in the pull picks,
// transactional outbox appends).
package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// errNotImplemented marks scaffold stubs; see docs/specs/03-board-mechanics.md.
var errNotImplemented = errors.New("board/postgres: not implemented (scaffold)")

// Store implements board.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ board.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (tooling TBD — 02 §14).
func New(db *sql.DB) *Store { return &Store{db: db} }

// Tx runs fn in one short READ COMMITTED transaction (03 §6).
func (s *Store) Tx(ctx context.Context, fn func(board.Tx) error) error {
	return errNotImplemented
}

// Snapshot reads the full board in render order (03 §4).
func (s *Store) Snapshot(ctx context.Context) (board.Snapshot, error) {
	return board.Snapshot{}, errNotImplemented
}

// tx is the transaction-scoped adapter behind board.Tx.
type tx struct {
	sqltx *sql.Tx
}

var _ board.Tx = (*tx)(nil)

func (t *tx) LockTicket(id board.TicketID) (board.Ticket, error) {
	return board.Ticket{}, errNotImplemented
}

func (t *tx) InsertTicket(tk board.Ticket) error {
	return errNotImplemented
}

func (t *tx) UpdateTicket(tk board.Ticket) error {
	return errNotImplemented
}

func (t *tx) NextReadyTicket() (board.Ticket, bool, error) {
	return board.Ticket{}, false, errNotImplemented
}

func (t *tx) FreeSandbox() (board.Sandbox, bool, error) {
	return board.Sandbox{}, false, errNotImplemented
}

func (t *tx) AppendOutbox(e board.Emission) error {
	return errNotImplemented
}
