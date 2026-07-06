// Package postgres is push's store adapter (mirrors identity/postgres): the only
// code that touches the push_subscriptions table. It owns the migrations in
// ./migrations and is wired in at the composition root (02 §2, backend/cmd/kiln).
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/crabtree-michael/kiln/backend/internal/push"
)

// Store implements push.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ push.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (mirrors identity/postgres.New).
func New(db *sql.DB) *Store { return &Store{db: db} }

// Save upserts on endpoint: a browser re-subscribing (same endpoint, possibly
// rotated keys) refreshes the keys rather than duplicating the row.
func (s *Store) Save(ctx context.Context, sub push.Subscription) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth)
		VALUES ($1, $2, $3)
		ON CONFLICT (endpoint) DO UPDATE
		  SET p256dh = EXCLUDED.p256dh,
		      auth   = EXCLUDED.auth`,
		sub.Endpoint, sub.P256dh, sub.Auth); err != nil {
		return fmt.Errorf("push/postgres: save subscription: %w", err)
	}
	return nil
}

// List returns every stored subscription; the Sender fans out to all of them
// (single user in v1). Only the error return is named, so it can carry a
// deferred rows.Close failure (the board/postgres idiom).
func (s *Store) List(ctx context.Context) (_ []push.Subscription, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint, p256dh, auth, created_at
		FROM push_subscriptions
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("push/postgres: list subscriptions: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("push/postgres: close subscriptions: %w", cerr)
		}
	}()

	var subs []push.Subscription
	for rows.Next() {
		var sub push.Subscription
		if serr := rows.Scan(&sub.ID, &sub.Endpoint, &sub.P256dh, &sub.Auth, &sub.CreatedAt); serr != nil {
			return nil, fmt.Errorf("push/postgres: scan subscription: %w", serr)
		}
		subs = append(subs, sub)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("push/postgres: iterate subscriptions: %w", rerr)
	}
	return subs, nil
}

// DeleteByEndpoint drops a subscription the push service reported gone (404/410).
func (s *Store) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint); err != nil {
		return fmt.Errorf("push/postgres: delete subscription: %w", err)
	}
	return nil
}

// Mode reads the global notification frequency (the single push_settings row).
// A missing row is treated as the default (push.ModeBlocked) rather than an
// error, so a fresh deployment behaves exactly as before the mode existed.
func (s *Store) Mode(ctx context.Context) (string, error) {
	var mode string
	err := s.db.QueryRowContext(ctx, `SELECT mode FROM push_settings WHERE id = 1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return push.ModeBlocked, nil
	}
	if err != nil {
		return "", fmt.Errorf("push/postgres: read mode: %w", err)
	}
	return mode, nil
}

// SetMode upserts the global notification frequency onto the single-row
// push_settings table (id = 1). Idempotent — writing the same mode twice is a
// no-op change.
func (s *Store) SetMode(ctx context.Context, mode string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO push_settings (id, mode)
		VALUES (1, $1)
		ON CONFLICT (id) DO UPDATE SET mode = EXCLUDED.mode`, mode); err != nil {
		return fmt.Errorf("push/postgres: set mode: %w", err)
	}
	return nil
}
