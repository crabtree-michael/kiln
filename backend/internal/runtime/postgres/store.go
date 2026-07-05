// Package postgres is the runtime's store adapter over the two queue tables
// (04 §2): the events table it owns outright, and the outbox's
// delivery-state columns (the board owns the outbox emission columns and
// appends the rows — 03 §2.4). Pure adapter; the drain semantics live in the
// runtime worker.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// errUnknownQueue guards the table/query lookups against a queue name outside
// the two the runtime knows (04 §2) — a programming error, surfaced loudly.
var errUnknownQueue = errors.New("runtime/postgres: unknown queue")

// humanMessagePayload is the human.message event payload {text} (07 §4),
// written alongside the user transcript row in one transaction.
type humanMessagePayload struct {
	Text string `json:"text"`
}

// Store implements runtime.Store and runtime.MessageStore over Postgres.
type Store struct {
	db *sql.DB
}

var (
	_ runtime.Store             = (*Store)(nil)
	_ runtime.MessageStore      = (*Store)(nil)
	_ runtime.NotificationStore = (*Store)(nil)
)

// New wraps an open connection pool; migrations are applied separately at
// startup.
func New(db *sql.DB) *Store { return &Store{db: db} }

// queueSQL holds the four delivery-state statements for one queue. The claim
// statement atomically selects the next due row (FOR UPDATE SKIP LOCKED),
// increments its attempts, and pushes next_attempt_at forward by the backoff
// (04 §3 step 1 + D8's schema, min(1s×2^(attempts−1),60s)) in a single
// UPDATE … RETURNING — no separate claim transaction is needed. Pushing the
// due time acts as the visibility timeout: a claimed-but-unmarked row (a
// crash between claim and mark, 04 §5) is not re-claimed until its backoff
// elapses, and the serial worker always marks it well before then. On success
// MarkDone flips status; on failure MarkRetry re-sets the exact due time. The
// kind column differs per queue (events.type vs outbox.topic) but is aliased
// so ClaimNextDue scans uniformly.
//
// power(2, attempts) reads the pre-update attempts (0 for a fresh row), which
// equals 2^(new_attempts−1) — the D8 schedule — capped at 60s by least().
type queueSQL struct {
	claim     string
	markDone  string
	markRetry string
	markDead  string
}

var queueSQLByName = map[runtime.QueueName]queueSQL{
	runtime.QueueEvents: {
		claim: `UPDATE events SET
				attempts = attempts + 1,
				next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
			WHERE id = (
				SELECT id FROM events
				WHERE status = 'pending' AND next_attempt_at <= now()
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, type, payload, attempts, created_at`,
		markDone:  `UPDATE events SET status = 'done', processed_at = now() WHERE id = $1`,
		markRetry: `UPDATE events SET last_error = $2, next_attempt_at = $3 WHERE id = $1`,
		markDead:  `UPDATE events SET status = 'dead', last_error = $2 WHERE id = $1`,
	},
	runtime.QueueOutbox: {
		claim: `UPDATE outbox SET
				attempts = attempts + 1,
				next_attempt_at = now() + least(power(2, attempts)::bigint, 60) * interval '1 second'
			WHERE id = (
				SELECT id FROM outbox
				WHERE status = 'pending' AND next_attempt_at <= now()
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, topic, payload, attempts, created_at`,
		markDone:  `UPDATE outbox SET status = 'done', processed_at = now() WHERE id = $1`,
		markRetry: `UPDATE outbox SET last_error = $2, next_attempt_at = $3 WHERE id = $1`,
		markDead:  `UPDATE outbox SET status = 'dead', last_error = $2 WHERE id = $1`,
	},
}

func sqlFor(q runtime.QueueName) (queueSQL, error) {
	qs, ok := queueSQLByName[q]
	if !ok {
		return queueSQL{}, fmt.Errorf("%w: %q", errUnknownQueue, q)
	}
	return qs, nil
}

// InsertEvent appends one row to the events queue (04 §6 EnqueueEvent) and
// returns its id. The outbox is never written here — the board appends it
// transactionally (03 §7).
func (s *Store) InsertEvent(ctx context.Context, t runtime.EventType, payload []byte) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO events (type, payload) VALUES ($1, $2) RETURNING id`,
		string(t), payload).Scan(&id); err != nil {
		return 0, fmt.Errorf("runtime/postgres: insert event: %w", err)
	}
	return id, nil
}

// ClaimNextDue claims the next due entry in id order, incrementing attempts,
// with FOR UPDATE SKIP LOCKED (04 §3 step 1). ok is false when nothing is due.
func (s *Store) ClaimNextDue(ctx context.Context, q runtime.QueueName) (runtime.Entry, bool, error) {
	qs, err := sqlFor(q)
	if err != nil {
		return runtime.Entry{}, false, err
	}
	var (
		e       runtime.Entry
		payload []byte
	)
	err = s.db.QueryRowContext(ctx, qs.claim).Scan(&e.ID, &e.Kind, &payload, &e.Attempts, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.Entry{}, false, nil
	}
	if err != nil {
		return runtime.Entry{}, false, fmt.Errorf("runtime/postgres: claim next due: %w", err)
	}
	e.Payload = append([]byte(nil), payload...)
	return e, true, nil
}

// MarkDone records success: status done, processed_at now (04 §3).
func (s *Store) MarkDone(ctx context.Context, q runtime.QueueName, id int64) error {
	qs, err := sqlFor(q)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, qs.markDone, id); err != nil {
		return fmt.Errorf("runtime/postgres: mark done: %w", err)
	}
	return nil
}

// MarkRetry records a failed attempt: last_error and the backoff'd
// next_attempt_at; the row stays pending (04 §3).
func (s *Store) MarkRetry(
	ctx context.Context, q runtime.QueueName, id int64, lastError string, nextAttemptAt time.Time,
) error {
	qs, err := sqlFor(q)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, qs.markRetry, id, lastError, nextAttemptAt); err != nil {
		return fmt.Errorf("runtime/postgres: mark retry: %w", err)
	}
	return nil
}

// MarkDead retires the entry after MaxAttempts (04 §3); the caller runs the
// per-topic dead-letter action.
func (s *Store) MarkDead(ctx context.Context, q runtime.QueueName, id int64, lastError string) error {
	qs, err := sqlFor(q)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, qs.markDead, id, lastError); err != nil {
		return fmt.Errorf("runtime/postgres: mark dead: %w", err)
	}
	return nil
}

// AppendUserMessageAndEnqueueEvent implements runtime.MessageStore (07 §3):
// one transaction, INSERT into messages (role='user') and INSERT into
// events (type='human.message', payload={text}) — the transcript and the
// event queue commit together or not at all.
func (s *Store) AppendUserMessageAndEnqueueEvent(ctx context.Context, text string) (int64, int64, error) {
	payload, err := json.Marshal(humanMessagePayload{Text: text})
	if err != nil {
		return 0, 0, fmt.Errorf("runtime/postgres: marshal human message payload: %w", err)
	}
	var messageID, eventID int64
	txErr := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO messages (role, text) VALUES ('user', $1) RETURNING id`, text).
			Scan(&messageID); err != nil {
			return fmt.Errorf("runtime/postgres: insert user message: %w", err)
		}
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO events (type, payload) VALUES ($1, $2) RETURNING id`,
			string(runtime.EventHumanMessage), payload).Scan(&eventID); err != nil {
			return fmt.Errorf("runtime/postgres: enqueue human message event: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return 0, 0, txErr
	}
	return messageID, eventID, nil
}

// AppendKilnMessage implements runtime.MessageStore (07 §3): INSERT into
// messages (role='kiln'), the first half of the Say port.
func (s *Store) AppendKilnMessage(ctx context.Context, text string) (runtime.Message, error) {
	m := runtime.Message{Role: runtime.RoleKiln, Text: text}
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO messages (role, text) VALUES ('kiln', $1) RETURNING id, created_at`, text).
		Scan(&m.ID, &m.CreatedAt); err != nil {
		return runtime.Message{}, fmt.Errorf("runtime/postgres: append kiln message: %w", err)
	}
	return m, nil
}

// Recent implements runtime.MessageStore (07 §3): the last n rows by id
// DESC, returned oldest first.
func (s *Store) Recent(ctx context.Context, n int) (_ []runtime.Message, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, text, created_at FROM (
			SELECT id, role, text, created_at FROM messages ORDER BY id DESC LIMIT $1
		) recent ORDER BY id ASC`, n)
	if err != nil {
		return nil, fmt.Errorf("runtime/postgres: query recent messages: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("runtime/postgres: close recent messages: %w", cerr)
		}
	}()

	var out []runtime.Message
	for rows.Next() {
		var (
			m    runtime.Message
			role string
		)
		if serr := rows.Scan(&m.ID, &role, &m.Text, &m.CreatedAt); serr != nil {
			return nil, fmt.Errorf("runtime/postgres: scan message: %w", serr)
		}
		m.Role = runtime.MessageRole(role)
		out = append(out, m)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("runtime/postgres: iterate messages: %w", rerr)
	}
	return out, nil
}

// enqueueFeedUpdatedTx appends a signal-only feed.updated outbox row (08 §7)
// inside an existing transaction — the shared half of every notification
// mutation, which makes the runtime a second outbox writer so a live feed
// re-render fans out exactly when the feed's contents change.
func enqueueFeedUpdatedTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (topic, payload) VALUES ('feed.updated', '{}')`); err != nil {
		return fmt.Errorf("runtime/postgres: enqueue feed.updated: %w", err)
	}
	return nil
}

// PostNotification implements runtime.NotificationStore (08 §3, §7): INSERT the
// brain-authored row and append a feed.updated outbox row in one transaction.
func (s *Store) PostNotification(
	ctx context.Context, kind, body string, ticketID, imageURL *string,
) (runtime.Notification, error) {
	n := runtime.Notification{
		Kind: runtime.NotificationKind(kind), Body: body, TicketID: ticketID, ImageURL: imageURL,
	}
	txErr := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO notifications (kind, ticket_id, body, image_url)
			 VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
			kind, ticketID, body, imageURL).Scan(&n.ID, &n.CreatedAt); err != nil {
			return fmt.Errorf("runtime/postgres: insert notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx)
	})
	if txErr != nil {
		return runtime.Notification{}, txErr
	}
	return n, nil
}

// RetractNotification implements runtime.NotificationStore (08 §3): stamp
// retracted_at=now() and append feed.updated in one transaction.
func (s *Store) RetractNotification(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET retracted_at = now() WHERE id = $1 AND retracted_at IS NULL`,
			id); err != nil {
			return fmt.Errorf("runtime/postgres: retract notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx)
	})
}

// EditNotification implements runtime.NotificationStore (08 §3 amended): amend
// a still-active row's kind/body/image in place and append feed.updated in one
// transaction. Retracted or seen rows are left untouched (the WHERE guard), so
// editing a drained card is a silent no-op rather than resurfacing it.
func (s *Store) EditNotification(ctx context.Context, id int64, kind, body string, imageURL *string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET kind = $2, body = $3, image_url = $4
			 WHERE id = $1 AND retracted_at IS NULL AND seen_at IS NULL`,
			id, kind, body, imageURL); err != nil {
			return fmt.Errorf("runtime/postgres: edit notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx)
	})
}

// MarkSeen implements runtime.NotificationStore (08 §3): stamp seen_at=now() on
// every still-unseen notification up to the client's high-water id, and append
// feed.updated in one transaction.
func (s *Store) MarkSeen(ctx context.Context, lastID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET seen_at = now() WHERE seen_at IS NULL AND id <= $1`,
			lastID); err != nil {
			return fmt.Errorf("runtime/postgres: mark seen: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx)
	})
}

// UnseenNotifications implements runtime.NotificationStore: the
// neither-seen-nor-retracted rows, newest-first — the brain's active-card view
// (list_updates, 06 §4). The feed reads RecentNotifications instead now that
// history is retained (08 D2′).
func (s *Store) UnseenNotifications(ctx context.Context) ([]runtime.Notification, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, created_at
		 FROM notifications
		 WHERE seen_at IS NULL AND retracted_at IS NULL
		 ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("runtime/postgres: query unseen notifications: %w", err)
	}
	return scanNotifications(rows)
}

// RecentNotifications implements runtime.NotificationStore (08 D2′): the newest
// `limit` unretracted rows (seen AND unseen), newest-first — the feed's first
// page of retained update/preview history. Fetches limit+1 to report whether an
// older page remains (the second return), then trims to limit.
func (s *Store) RecentNotifications(ctx context.Context, limit int) ([]runtime.Notification, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, created_at
		 FROM notifications
		 WHERE retracted_at IS NULL
		 ORDER BY id DESC
		 LIMIT $1`, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("runtime/postgres: query recent notifications: %w", err)
	}
	notes, err := scanNotifications(rows)
	if err != nil {
		return nil, false, err
	}
	return trimPage(notes, limit)
}

// HistoryBefore implements runtime.NotificationStore (08 D2′): one older keyset
// page — up to `limit` unretracted rows with id < before, newest-first. Fetches
// limit+1 to report whether a further page remains.
func (s *Store) HistoryBefore(ctx context.Context, before int64, limit int) ([]runtime.Notification, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, created_at
		 FROM notifications
		 WHERE retracted_at IS NULL AND id < $1
		 ORDER BY id DESC
		 LIMIT $2`, before, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("runtime/postgres: query history notifications: %w", err)
	}
	notes, err := scanNotifications(rows)
	if err != nil {
		return nil, false, err
	}
	return trimPage(notes, limit)
}

// LastSeenID implements runtime.NotificationStore (08 D2′): the greatest id
// among seen notifications — the persistent last-seen divider boundary. Nil
// when nothing has been seen yet.
func (s *Store) LastSeenID(ctx context.Context) (*int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT max(id) FROM notifications WHERE seen_at IS NOT NULL`).Scan(&id); err != nil {
		return nil, fmt.Errorf("runtime/postgres: query last seen id: %w", err)
	}
	if !id.Valid {
		return nil, nil //nolint:nilnil // (nil, nil) is the intended "nothing seen yet" signal (08 D2′), not an error.
	}
	v := id.Int64
	return &v, nil
}

// UnseenCount implements runtime.NotificationStore (08 §2): how many unseen,
// unretracted notifications remain — the header's "N updates" count.
func (s *Store) UnseenCount(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications WHERE seen_at IS NULL AND retracted_at IS NULL`).
		Scan(&n); err != nil {
		return 0, fmt.Errorf("runtime/postgres: query unseen count: %w", err)
	}
	return n, nil
}

// trimPage turns a limit+1 over-fetch into (page, hasMore): if more than `limit`
// rows came back there is a further page, and the sentinel row is dropped.
func trimPage(notes []runtime.Notification, limit int) ([]runtime.Notification, bool, error) {
	if len(notes) > limit {
		return notes[:limit], true, nil
	}
	return notes, false, nil
}

// scanNotifications drains a notifications result set (the shared column list:
// id, kind, ticket_id, body, image_url, created_at) into domain rows, closing
// the rows and folding a close error into the return.
func scanNotifications(rows *sql.Rows) (_ []runtime.Notification, err error) {
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("runtime/postgres: close notifications: %w", cerr)
		}
	}()

	var out []runtime.Notification
	for rows.Next() {
		var (
			n        runtime.Notification
			kind     string
			ticketID sql.NullString
			imageURL sql.NullString
		)
		if serr := rows.Scan(&n.ID, &kind, &ticketID, &n.Body, &imageURL, &n.CreatedAt); serr != nil {
			return nil, fmt.Errorf("runtime/postgres: scan notification: %w", serr)
		}
		n.Kind = runtime.NotificationKind(kind)
		if ticketID.Valid {
			n.TicketID = &ticketID.String
		}
		if imageURL.Valid {
			n.ImageURL = &imageURL.String
		}
		out = append(out, n)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("runtime/postgres: iterate notifications: %w", rerr)
	}
	return out, nil
}

// inTx runs fn inside one transaction, rolling back on error (mirroring the
// board store's pattern, 03 §6). A rollback failure is joined to the causing
// error so neither is lost.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("runtime/postgres: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("runtime/postgres: rollback (after %w): %w", err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runtime/postgres: commit: %w", err)
	}
	return nil
}
