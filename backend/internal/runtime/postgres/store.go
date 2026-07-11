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

	"github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/pgutil"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// nullableUUID maps the runtime's string-typed project id onto the nullable
// uuid column (11 §3): "" (no project — a pre-adoption caller) becomes SQL
// NULL rather than an invalid-uuid cast error.
func nullableUUID(projectID string) any {
	if projectID == "" {
		return nil
	}
	return projectID
}

// nullableText maps an optional string column: "" becomes SQL NULL so an absent
// value reads back as a nil pointer (github_url/github_label on non-linked cards)
// rather than an empty string.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullStringPtr maps a nullable text column read back as sql.NullString onto the
// domain's *string: SQL NULL becomes nil, any value a fresh pointer (copied off
// the loop-local NullString so successive rows never alias the same address).
func nullStringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

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
// elapses, and the dispatcher always marks it well before then. On success
// MarkDone flips status; on failure MarkRetry re-sets the exact due time. The
// kind column differs per queue (events.type vs outbox.topic) but is aliased
// so ClaimNextDue scans uniformly.
//
// $1 is the dispatcher's busy project set (11 §3): `project_id <> ALL($1)`
// skips every project with an in-flight entry, which is what makes
// per-project serialization hold across the claim. With an empty array the
// predicate is vacuously true, so an idle dispatcher claims anything due. A
// NULL project_id (legacy pre-adoption row) compares to nothing, so it is
// claimable only while the busy set is empty — see ClaimNextDue.
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
				  AND project_id <> ALL($1::uuid[])
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, project_id, type, payload, attempts, created_at`,
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
				  AND project_id <> ALL($1::uuid[])
				ORDER BY id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			RETURNING id, project_id, topic, payload, attempts, created_at`,
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

// InsertEvent appends one row to the events queue (04 §6 EnqueueEvent),
// stamped with its tenant project (11 §3), and returns its id. The outbox is
// never written here — the board appends it transactionally (03 §7).
//
// A non-zero idempotencyKey is stamped on the row and deduped against the
// partial unique index (architecture audit 3.1): a redelivery collapses onto
// the first insert via ON CONFLICT DO NOTHING and returns id 0, so a replayed
// agent completion never enqueues a second brain pass. A zero key is stored as
// NULL, which the partial index ignores — the row always inserts.
func (s *Store) InsertEvent(
	ctx context.Context, projectID string, t runtime.EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO events (project_id, type, payload, idempotency_key) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		 RETURNING id`,
		nullableUUID(projectID), string(t), payload, nullableKey(idempotencyKey)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		// Duplicate delivery: the event is already enqueued (architecture audit
		// 3.1). Report success with no new id so the caller treats it as a no-op.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("runtime/postgres: insert event: %w", err)
	}
	return id, nil
}

// nullableKey renders a zero idempotency key as SQL NULL (no dedup) and any
// non-zero key as itself — the real outbox ids the events dedup index keys on
// are bigserial, so never zero.
func nullableKey(key int64) any {
	if key == 0 {
		return nil
	}
	return key
}

// nullPseudoProjectUUID stands in for the empty ProjectID ("" — a legacy row
// whose project_id is NULL) inside the busy uuid[] array, which cannot carry
// "". It is the all-zero uuid, which no real (v4, random) project id ever is.
// Because `NULL <> ALL(non-empty-array)` is not true in SQL, ANY non-empty
// busy set already excludes all NULL-project rows from a claim — so keeping a
// placeholder in the array (instead of dropping the "" member) preserves
// exactly that: while a legacy NULL-project entry is in flight, no second one
// can be claimed. Legacy rows therefore serialize as one pseudo-project and
// are only claimed when the dispatcher is otherwise idle; boot-time adoption
// backfills project_id, so this is a transitional case, not steady state.
const nullPseudoProjectUUID = "00000000-0000-0000-0000-000000000000"

// ClaimNextDue claims the next due entry in id order, incrementing attempts,
// with FOR UPDATE SKIP LOCKED (04 §3 step 1), skipping every project in busy
// (11 §3 — the dispatcher's per-project serialization; empty busy claims
// anything due). ok is false when nothing is due.
func (s *Store) ClaimNextDue(ctx context.Context, q runtime.QueueName, busy []string) (runtime.Entry, bool, error) {
	qs, err := sqlFor(q)
	if err != nil {
		return runtime.Entry{}, false, err
	}
	busyUUIDs := make([]string, len(busy))
	for i, p := range busy {
		if p == "" {
			p = nullPseudoProjectUUID
		}
		busyUUIDs[i] = p
	}
	var (
		e         runtime.Entry
		projectID sql.NullString
		payload   []byte
	)
	err = s.db.QueryRowContext(ctx, qs.claim, pq.Array(busyUUIDs)).
		Scan(&e.ID, &projectID, &e.Kind, &payload, &e.Attempts, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.Entry{}, false, nil
	}
	if err != nil {
		return runtime.Entry{}, false, fmt.Errorf("runtime/postgres: claim next due: %w", err)
	}
	e.ProjectID = projectID.String // "" for a legacy NULL-project row
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
// event queue commit together or not at all, both stamped with the project
// (11 §3).
func (s *Store) AppendUserMessageAndEnqueueEvent(ctx context.Context, projectID, text string) (int64, int64, error) {
	payload, err := json.Marshal(humanMessagePayload{Text: text})
	if err != nil {
		return 0, 0, fmt.Errorf("runtime/postgres: marshal human message payload: %w", err)
	}
	var messageID, eventID int64
	txErr := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO messages (project_id, role, text) VALUES ($1, 'user', $2) RETURNING id`,
			nullableUUID(projectID), text).
			Scan(&messageID); err != nil {
			return fmt.Errorf("runtime/postgres: insert user message: %w", err)
		}
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO events (project_id, type, payload) VALUES ($1, $2, $3) RETURNING id`,
			nullableUUID(projectID), string(runtime.EventHumanMessage), payload).Scan(&eventID); err != nil {
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
// messages (role='kiln'), stamped with the project (11 §3), the first half of
// the Say port.
func (s *Store) AppendKilnMessage(ctx context.Context, projectID, text string) (runtime.Message, error) {
	m := runtime.Message{Role: runtime.RoleKiln, Text: text}
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO messages (project_id, role, text) VALUES ($1, 'kiln', $2) RETURNING id, created_at`,
		nullableUUID(projectID), text).
		Scan(&m.ID, &m.CreatedAt); err != nil {
		return runtime.Message{}, fmt.Errorf("runtime/postgres: append kiln message: %w", err)
	}
	return m, nil
}

// Recent implements runtime.MessageStore (07 §3): the project's last n rows
// by id DESC, returned oldest first. The project_id predicate is the tenant
// wall (11 §3) — another project's transcript can never leak in.
func (s *Store) Recent(ctx context.Context, projectID string, n int) (_ []runtime.Message, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role, text, created_at FROM (
			SELECT id, role, text, created_at FROM messages
			WHERE project_id = $2 ORDER BY id DESC LIMIT $1
		) recent ORDER BY id ASC`, n, nullableUUID(projectID))
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
// re-render fans out exactly when the feed's contents change. The row carries
// the mutating project's id (11 §3) so the executor re-renders and pushes
// exactly that project's feed.
func enqueueFeedUpdatedTx(ctx context.Context, tx *sql.Tx, projectID string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox (project_id, topic, payload) VALUES ($1, 'feed.updated', '{}')`,
		nullableUUID(projectID)); err != nil {
		return fmt.Errorf("runtime/postgres: enqueue feed.updated: %w", err)
	}
	return nil
}

// PostNotification implements runtime.NotificationStore (08 §3, §7): INSERT the
// brain-authored row and append a feed.updated outbox row in one transaction,
// both stamped with the project (11 §3).
func (s *Store) PostNotification(
	ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
) (runtime.Notification, error) {
	n := runtime.Notification{
		Kind: runtime.NotificationKind(kind), Body: body, TicketID: ticketID, ImageURL: imageURL,
	}
	txErr := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO notifications (project_id, kind, ticket_id, body, image_url)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
			nullableUUID(projectID), kind, ticketID, body, imageURL).Scan(&n.ID, &n.CreatedAt); err != nil {
			return fmt.Errorf("runtime/postgres: insert notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
	if txErr != nil {
		return runtime.Notification{}, txErr
	}
	return n, nil
}

// PostCompletionCard implements runtime.NotificationStore (08 §7): insert the
// mechanical "done" card keyed on the outbox entry id and append feed.updated —
// both in one transaction, and only when the insert actually happened. The
// partial unique index on idempotency_key makes a redelivery a no-op
// (ON CONFLICT DO NOTHING), so we skip the feed.updated fan-out on a duplicate.
func (s *Store) PostCompletionCard(
	ctx context.Context, projectID string, key int64, ticketID, body, githubURL, githubLabel, workSummary string,
) (bool, error) {
	var posted bool
	txErr := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO notifications
			   (project_id, kind, ticket_id, body, github_url, github_label, work_summary, idempotency_key)
			 VALUES ($1, 'done', $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
			nullableUUID(projectID), ticketID, body, nullableText(githubURL), nullableText(githubLabel),
			nullableText(workSummary), key)
		if err != nil {
			return fmt.Errorf("runtime/postgres: insert completion card: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("runtime/postgres: completion card rows affected: %w", err)
		}
		if n == 0 {
			return nil // duplicate delivery: card already posted, nothing to fan out
		}
		posted = true
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
	if txErr != nil {
		return false, txErr
	}
	return posted, nil
}

// RetractNotification implements runtime.NotificationStore (08 §3): stamp
// retracted_at=now() and append feed.updated in one transaction. The
// project_id predicate makes a cross-tenant id a silent no-op (11 §3).
func (s *Store) RetractNotification(ctx context.Context, projectID string, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET retracted_at = now()
			 WHERE id = $1 AND project_id = $2 AND retracted_at IS NULL`,
			id, nullableUUID(projectID)); err != nil {
			return fmt.Errorf("runtime/postgres: retract notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
}

// RetractAllNotifications implements runtime.NotificationStore (08 §3,
// clear-all): stamp retracted_at=now() on every still-active row of the
// project and append feed.updated in one transaction.
func (s *Store) RetractAllNotifications(ctx context.Context, projectID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET retracted_at = now()
			 WHERE project_id = $1 AND retracted_at IS NULL`,
			nullableUUID(projectID)); err != nil {
			return fmt.Errorf("runtime/postgres: retract all notifications: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
}

// EditNotification implements runtime.NotificationStore (08 §3 amended): amend
// a still-active row's kind/body/image in place and append feed.updated in one
// transaction. Retracted or seen rows — and rows of another project (11 §3) —
// are left untouched (the WHERE guard), so editing a drained or foreign card
// is a silent no-op rather than resurfacing it.
func (s *Store) EditNotification(
	ctx context.Context, projectID string, id int64, kind, body string, imageURL *string,
) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET kind = $2, body = $3, image_url = $4
			 WHERE id = $1 AND project_id = $5 AND retracted_at IS NULL AND seen_at IS NULL`,
			id, kind, body, imageURL, nullableUUID(projectID)); err != nil {
			return fmt.Errorf("runtime/postgres: edit notification: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
}

// MarkSeen implements runtime.NotificationStore (08 §3): stamp seen_at=now() on
// every still-unseen notification of the project up to the client's high-water
// id, and append feed.updated in one transaction.
func (s *Store) MarkSeen(ctx context.Context, projectID string, lastID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET seen_at = now()
			 WHERE project_id = $2 AND seen_at IS NULL AND id <= $1`,
			lastID, nullableUUID(projectID)); err != nil {
			return fmt.Errorf("runtime/postgres: mark seen: %w", err)
		}
		return enqueueFeedUpdatedTx(ctx, tx, projectID)
	})
}

// UnseenNotifications implements runtime.NotificationStore: the
// neither-seen-nor-retracted rows, newest-first — the brain's active-card view
// (list_updates, 06 §4). Mechanical poke and done cards are excluded
// (kind NOT IN ('poke','done')) — they are posted by the steward/runtime, not
// the brain's to edit or retract. The feed reads RecentNotifications instead now
// that history is retained (08 D2′).
func (s *Store) UnseenNotifications(ctx context.Context, projectID string) ([]runtime.Notification, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, github_url, github_label, work_summary, created_at
		 FROM notifications
		 WHERE project_id = $1 AND seen_at IS NULL AND retracted_at IS NULL AND kind NOT IN ('poke', 'done')
		 ORDER BY id DESC`, nullableUUID(projectID))
	if err != nil {
		return nil, fmt.Errorf("runtime/postgres: query unseen notifications: %w", err)
	}
	return scanNotifications(rows)
}

// RecentNotifications implements runtime.NotificationStore (08 D2′): the newest
// `limit` unretracted rows (seen AND unseen), newest-first — the feed's first
// page of retained update/preview history. Fetches limit+1 to report whether an
// older page remains (the second return), then trims to limit.
func (s *Store) RecentNotifications(
	ctx context.Context, projectID string, limit int,
) ([]runtime.Notification, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, github_url, github_label, work_summary, created_at
		 FROM notifications
		 WHERE project_id = $2 AND retracted_at IS NULL
		 ORDER BY id DESC
		 LIMIT $1`, limit+1, nullableUUID(projectID))
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
func (s *Store) HistoryBefore(
	ctx context.Context, projectID string, before int64, limit int,
) ([]runtime.Notification, bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, ticket_id, body, image_url, github_url, github_label, work_summary, created_at
		 FROM notifications
		 WHERE project_id = $3 AND retracted_at IS NULL AND id < $1
		 ORDER BY id DESC
		 LIMIT $2`, before, limit+1, nullableUUID(projectID))
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
func (s *Store) LastSeenID(ctx context.Context, projectID string) (*int64, error) {
	var id sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT max(id) FROM notifications WHERE project_id = $1 AND seen_at IS NOT NULL`,
		nullableUUID(projectID)).Scan(&id); err != nil {
		return nil, fmt.Errorf("runtime/postgres: query last seen id: %w", err)
	}
	if !id.Valid {
		return nil, nil //nolint:nilnil // (nil, nil) is the intended "nothing seen yet" signal (08 D2′), not an error.
	}
	v := id.Int64
	return &v, nil
}

// UnseenCount implements runtime.NotificationStore (08 §2): how many unseen,
// unretracted notifications remain — the header's "N updates" count. Mechanical
// poke and done cards are excluded (kind NOT IN ('poke','done')): they are feed
// entries, not "updates" the user is being counted as owing a look.
func (s *Store) UnseenCount(ctx context.Context, projectID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications
		 WHERE project_id = $1 AND seen_at IS NULL AND retracted_at IS NULL AND kind NOT IN ('poke', 'done')`,
		nullableUUID(projectID)).
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
// id, kind, ticket_id, body, image_url, github_url, github_label, work_summary,
// created_at) into domain rows, closing the rows and folding a close error into
// the return.
func scanNotifications(rows *sql.Rows) (_ []runtime.Notification, err error) {
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("runtime/postgres: close notifications: %w", cerr)
		}
	}()

	var out []runtime.Notification
	for rows.Next() {
		var (
			n           runtime.Notification
			kind        string
			ticketID    sql.NullString
			imageURL    sql.NullString
			githubURL   sql.NullString
			githubLabel sql.NullString
			workSummary sql.NullString
		)
		if serr := rows.Scan(
			&n.ID, &kind, &ticketID, &n.Body, &imageURL, &githubURL, &githubLabel, &workSummary, &n.CreatedAt,
		); serr != nil {
			return nil, fmt.Errorf("runtime/postgres: scan notification: %w", serr)
		}
		n.Kind = runtime.NotificationKind(kind)
		n.TicketID = nullStringPtr(ticketID)
		n.ImageURL = nullStringPtr(imageURL)
		n.GitHubURL = nullStringPtr(githubURL)
		n.GitHubLabel = nullStringPtr(githubLabel)
		n.WorkSummary = nullStringPtr(workSummary)
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
	if err := pgutil.InTx(ctx, s.db, nil, fn); err != nil {
		return fmt.Errorf("runtime/postgres: tx: %w", err)
	}
	return nil
}
