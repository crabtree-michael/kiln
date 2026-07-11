// Package postgres is push's store adapter (mirrors identity/postgres): the only
// code that touches the push_subscriptions and push_user_settings tables. It
// owns the migrations in ./migrations and is wired in at the composition root
// (02 §2, backend/cmd/kiln). The legacy push_settings singleton is left in
// place, unread — boot-time adoption copies its mode to the bootstrap owner's
// push_user_settings row.
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
// rotated keys) refreshes the keys rather than duplicating the row. The
// endpoint is globally unique, so a re-subscribe under a different signed-in
// user also moves the row to that user (last write wins).
func (s *Store) Save(ctx context.Context, userID string, sub push.Subscription) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE
		  SET p256dh  = EXCLUDED.p256dh,
		      auth    = EXCLUDED.auth,
		      user_id = EXCLUDED.user_id`,
		sub.Endpoint, sub.P256dh, sub.Auth, userID); err != nil {
		return fmt.Errorf("push/postgres: save subscription: %w", err)
	}
	return nil
}

// List returns the given user's subscriptions only; the Sender fans out to
// exactly these (per-user routing, 11 phase 2). Pre-adoption rows with a NULL
// user_id are invisible here until boot-time adoption assigns them an owner.
// Only the error return is named, so it can carry a deferred rows.Close
// failure (the board/postgres idiom).
func (s *Store) List(ctx context.Context, userID string) (_ []push.Subscription, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, endpoint, p256dh, auth, created_at
		FROM push_subscriptions
		WHERE user_id = $1
		ORDER BY id`, userID)
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

// DeleteUserEndpoint drops the caller's own subscription for an endpoint — the
// device-disables-notifications path. Scoped by user_id so a caller can only
// remove a row they own; a no-op (no error) when the endpoint is absent or owned
// by someone else, matching DeleteByEndpoint's idempotence.
func (s *Store) DeleteUserEndpoint(ctx context.Context, userID, endpoint string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, userID, endpoint); err != nil {
		return fmt.Errorf("push/postgres: delete user subscription: %w", err)
	}
	return nil
}

// Mode reads the user's notification frequency (their push_user_settings row).
// A missing row is treated as the default (push.ModeBlocked) rather than an
// error, so a user who never set a mode behaves exactly like the pre-mode
// deployment did.
func (s *Store) Mode(ctx context.Context, userID string) (string, error) {
	var mode string
	err := s.db.QueryRowContext(ctx,
		`SELECT mode FROM push_user_settings WHERE user_id = $1`, userID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return push.ModeBlocked, nil
	}
	if err != nil {
		return "", fmt.Errorf("push/postgres: read mode: %w", err)
	}
	return mode, nil
}

// SetMode upserts the user's notification frequency into push_user_settings.
// Idempotent — writing the same mode twice is a no-op change.
func (s *Store) SetMode(ctx context.Context, userID, mode string) error {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO push_user_settings (user_id, mode)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET mode = EXCLUDED.mode`, userID, mode); err != nil {
		return fmt.Errorf("push/postgres: set mode: %w", err)
	}
	return nil
}
