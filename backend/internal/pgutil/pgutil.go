// Package pgutil provides shared database helpers used by the three postgres
// store adapters (agent, board, runtime). Pure infrastructure — no domain
// logic, no module coupling.
package pgutil

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RowScanner is the shared shape of *sql.Row and *sql.Rows, allowing scan
// helpers to accept either without a type switch.
type RowScanner interface {
	Scan(dest ...any) error
}

// NullString maps "" to SQL NULL so nullable text/uuid columns stay clean.
func NullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// InTx runs fn inside one database transaction, rolling back on error. A
// rollback failure is joined to the causing error so neither is lost.
func InTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("rollback (after %w): %w", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
