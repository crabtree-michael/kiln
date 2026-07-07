// Package postgres is the board's store adapter (03 §9): the only code that
// touches the board tables. It owns the migrations in ./migrations and is
// wired in at the composition root (02 §2, backend/cmd/kiln). Pure adapter —
// every rule lives in the board service; every query here realizes a
// documented contract (lock-then-check, SKIP LOCKED in the pull picks,
// transactional outbox appends). Every statement is project-scoped (11 §3):
// reads carry a project_id predicate, writes set the column, and a valid id
// from another project is indistinguishable from a missing one.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/pgutil"
)

// ticketColumns is the canonical projection for a ticket row, shared by every
// SELECT/RETURNING so scanTicket can read them positionally.
const ticketColumns = `id, title, body, state, priority, worker_id, blocked_reason, ready_at, ` +
	`approval_requested, created_at, updated_at, state_changed_at, archived_at`

// activeTicketExists is the correlated subquery that derives a worker's busy
// state (03 D2): a worker is busy iff an active ticket references it. The
// project correlation keeps the derivation tenant-local (worker ids are
// globally unique, so it never changes the answer for well-formed data — it
// pins the scoping rule that no read crosses a project boundary).
const activeTicketExists = `
	SELECT 1 FROM tickets t
	WHERE t.worker_id = s.id AND t.project_id = s.project_id
	  AND t.state IN ('working','blocked')`

// Store implements board.Store over Postgres.
type Store struct {
	db *sql.DB
}

var _ board.Store = (*Store)(nil)

// New wraps an open connection pool; migrations are applied separately at
// startup (tooling TBD — 02 §14).
func New(db *sql.DB) *Store { return &Store{db: db} }

// Tx runs fn in one short READ COMMITTED transaction (03 §6): the ticket
// change and its outbox appends commit together or roll back together (03 I7).
func (s *Store) Tx(ctx context.Context, fn func(board.Tx) error) error {
	if err := pgutil.InTx(ctx, s.db, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(sqltx *sql.Tx) error {
		return fn(&tx{sqltx: sqltx})
	}); err != nil {
		return fmt.Errorf("board/postgres: tx: %w", err)
	}
	return nil
}

// Snapshot reads the project's full board in render order (03 §4). Grouping
// is derived from state alone (03 D1); each group is ordered per the GetBoard
// contract: Shaping by priority desc then created_at asc, Ready in exact pull
// order (03 §5 / D9), Developing (Blocked, Working) and Done by recency.
// Worker capacity counts cover the project's workers only.
func (s *Store) Snapshot(ctx context.Context, projectID string) (board.Snapshot, error) {
	var snap board.Snapshot
	if err := s.readTickets(ctx, projectID, &snap); err != nil {
		return board.Snapshot{}, err
	}
	sortSnapshot(&snap)

	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM workers WHERE project_id = $1`, projectID).Scan(&snap.WorkerTotal); err != nil {
		return board.Snapshot{}, fmt.Errorf("board/postgres: count workers: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM workers s WHERE s.project_id = $1 AND NOT EXISTS (`+activeTicketExists+`)`,
		projectID).
		Scan(&snap.WorkerFree); err != nil {
		return board.Snapshot{}, fmt.Errorf("board/postgres: count free workers: %w", err)
	}
	return snap, nil
}

// GetTicket reads one of the project's non-archived tickets by id (03 §4
// amended), backing the brain's get_ticket tool. A missing, archived, or
// other-project id is ErrNotFound. A plain read outside any transaction — no
// row lock, since the brain only reads here.
func (s *Store) GetTicket(ctx context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+ticketColumns+` FROM tickets WHERE id = $2 AND project_id = $1 AND archived_at IS NULL`,
		projectID, string(id))
	tk, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return board.Ticket{}, board.ErrNotFound
	}
	return tk, err
}

// ReconcileWorkers brings the project's workers to exactly n rows at startup
// (03 §8): insert-only — rows are added when the project's current count is
// below n, and a row is never deleted while an active ticket could reference
// it (v1 never auto-deletes at all; the FK would refuse it anyway). Called by
// the composition root (backend/cmd/kiln) before the runtime starts driving
// RunPull, using the WIP cap from configuration. Not part of board.Store: no
// Board API operation needs it, so it stays a concrete method on the adapter
// rather than widening the port (03 I8).
func (s *Store) ReconcileWorkers(ctx context.Context, projectID string, n int) error {
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM workers WHERE project_id = $1`, projectID).Scan(&count); err != nil {
		return fmt.Errorf("board/postgres: count workers: %w", err)
	}
	for i := count; i < n; i++ {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO workers (id, project_id) VALUES (gen_random_uuid(), $1)`, projectID); err != nil {
			return fmt.Errorf("board/postgres: insert worker: %w", err)
		}
	}
	return nil
}

// WorkerIDs lists the project's capacity-slot ids (03 §2.3), oldest first.
// Like ReconcileWorkers it is a concrete composition-root helper, not part of
// board.Store: no Board API operation needs it, but the agent-runtime
// reconciler reads the slot ids through its own Slots port (05 §4), which the
// composition root backs with this method (04 §8). Keeping the SQL inside the
// board adapter preserves the module's sole ownership of the workers table
// (03 I8) — nothing outside board issues queries against it.
func (s *Store) WorkerIDs(ctx context.Context, projectID string) (_ []string, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM workers WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("board/postgres: query worker ids: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("board/postgres: close worker ids: %w", cerr)
		}
	}()

	var ids []string
	for rows.Next() {
		var id string
		if serr := rows.Scan(&id); serr != nil {
			return nil, fmt.Errorf("board/postgres: scan worker id: %w", serr)
		}
		ids = append(ids, id)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("board/postgres: iterate worker ids: %w", rerr)
	}
	return ids, nil
}

// readTickets loads the project's every ticket into snap, grouped by state
// alone (03 D1). Only the error return is named, so it can carry a deferred
// rows.Close failure (satisfying errcheck's check-blank without a non-error
// named return).
func (s *Store) readTickets(ctx context.Context, projectID string, snap *board.Snapshot) (err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+ticketColumns+` FROM tickets WHERE project_id = $1 AND archived_at IS NULL`, projectID)
	if err != nil {
		return fmt.Errorf("board/postgres: query tickets: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("board/postgres: close tickets: %w", cerr)
		}
	}()

	for rows.Next() {
		t, serr := scanTicket(rows)
		if serr != nil {
			return serr
		}
		appendByState(snap, t)
	}
	if rerr := rows.Err(); rerr != nil {
		return fmt.Errorf("board/postgres: iterate tickets: %w", rerr)
	}
	return nil
}

// appendByState places a ticket in its derived render group (03 D1): the
// state field alone decides the column/zone; there is no stored grouping.
func appendByState(snap *board.Snapshot, t board.Ticket) {
	switch t.State {
	case board.StateShaping:
		snap.Shaping = append(snap.Shaping, t)
	case board.StateReady:
		snap.Ready = append(snap.Ready, t)
	case board.StateBlocked:
		snap.Blocked = append(snap.Blocked, t)
	case board.StateWorking:
		snap.Working = append(snap.Working, t)
	case board.StateDone:
		snap.Done = append(snap.Done, t)
	}
}

// sortSnapshot orders each derived group per the GetBoard render contract
// (03 §4): Shaping by priority desc then created_at asc; Ready in exact pull
// order (03 §5 / D9); Blocked and Working oldest-first; Done newest-first.
func sortSnapshot(snap *board.Snapshot) {
	sort.SliceStable(snap.Shaping, func(i, j int) bool {
		if snap.Shaping[i].Priority != snap.Shaping[j].Priority {
			return snap.Shaping[i].Priority > snap.Shaping[j].Priority
		}
		return snap.Shaping[i].CreatedAt.Before(snap.Shaping[j].CreatedAt)
	})
	sort.SliceStable(snap.Ready, func(i, j int) bool { return pullLess(snap.Ready[i], snap.Ready[j]) })
	sort.SliceStable(snap.Blocked, func(i, j int) bool {
		return snap.Blocked[i].UpdatedAt.Before(snap.Blocked[j].UpdatedAt)
	})
	sort.SliceStable(snap.Working, func(i, j int) bool {
		return snap.Working[i].UpdatedAt.Before(snap.Working[j].UpdatedAt)
	})
	sort.SliceStable(snap.Done, func(i, j int) bool {
		return snap.Done[i].UpdatedAt.After(snap.Done[j].UpdatedAt)
	})
}

// pullLess is the deterministic pull order (03 D9): priority DESC, ready_at
// ASC, id ASC — the same total order the tickets_ready_pull_order index and
// NextReadyTicket's ORDER BY realize, applied in Go for Snapshot's Ready
// group. No project term: a Snapshot's tickets are per-project by
// construction (readTickets), so the order is already tenant-local.
func pullLess(a, b board.Ticket) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	var at, bt time.Time
	if a.ReadyAt != nil {
		at = *a.ReadyAt
	}
	if b.ReadyAt != nil {
		bt = *b.ReadyAt
	}
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.ID < b.ID
}

// tx is the transaction-scoped adapter behind board.Tx. It holds no context:
// each method receives the operation's context per the board.Tx contract.
type tx struct {
	sqltx *sql.Tx
}

var _ board.Tx = (*tx)(nil)

// LockTicket is SELECT … FOR UPDATE on one of the project's tickets (03 §6):
// targeted, no SKIP LOCKED, so a concurrent claimer conflicts loudly rather
// than skipping. An id from another project is ErrNotFound.
func (t *tx) LockTicket(ctx context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	row := t.sqltx.QueryRowContext(ctx,
		`SELECT `+ticketColumns+` FROM tickets WHERE id = $2 AND project_id = $1 AND archived_at IS NULL FOR UPDATE`,
		projectID, string(id))
	tk, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return board.Ticket{}, board.ErrNotFound
	}
	return tk, err
}

// InsertTicket persists a new ticket under the project; the id is generated
// in the database (gen_random_uuid) and, with created_at/updated_at, returned
// so the caller never re-reads (entities.go).
func (t *tx) InsertTicket(ctx context.Context, projectID string, tk board.Ticket) (board.Ticket, error) {
	row := t.sqltx.QueryRowContext(ctx,
		`INSERT INTO tickets (id, project_id, title, body, state, priority,
		                      worker_id, blocked_reason, ready_at, approval_requested)
		 VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING `+ticketColumns,
		projectID, tk.Title, tk.Body, string(tk.State), tk.Priority,
		workerIDArg(tk.WorkerID), strArg(tk.BlockedReason), timeArg(tk.ReadyAt), tk.ApprovalRequested)
	return scanTicket(row)
}

// UpdateTicket persists a mutation of a previously locked ticket, refreshing
// updated_at, and returns the persisted row (03 §4). state_changed_at advances
// only when the state actually transitions (the CASE compares the locked row's
// old state against the new one), so a same-state mutation — a Working→Working
// nudge included — leaves the "time in status" clock untouched. A ticket
// outside the project is ErrNotFound.
func (t *tx) UpdateTicket(ctx context.Context, projectID string, tk board.Ticket) (board.Ticket, error) {
	row := t.sqltx.QueryRowContext(ctx,
		`UPDATE tickets
		 SET title = $2, body = $3, state = $4, priority = $5,
		     worker_id = $6, blocked_reason = $7, ready_at = $8, approval_requested = $9,
		     archived_at = $10, updated_at = now(),
		     state_changed_at = CASE WHEN tickets.state IS DISTINCT FROM $4
		                             THEN now() ELSE tickets.state_changed_at END
		 WHERE id = $1 AND project_id = $11
		 RETURNING `+ticketColumns,
		string(tk.ID), tk.Title, tk.Body, string(tk.State), tk.Priority,
		workerIDArg(tk.WorkerID), strArg(tk.BlockedReason), timeArg(tk.ReadyAt), tk.ApprovalRequested,
		timeArg(tk.ArchivedAt), projectID)
	out, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return board.Ticket{}, board.ErrNotFound
	}
	return out, err
}

// NextReadyTicket locks the project's next pullable ticket in pull order
// using FOR UPDATE SKIP LOCKED (03 §5); ok is false when none is available in
// the project. The ORDER BY matches the project-scoped
// tickets_ready_pull_order index (0008_project_id.sql).
func (t *tx) NextReadyTicket(ctx context.Context, projectID string) (board.Ticket, bool, error) {
	row := t.sqltx.QueryRowContext(ctx,
		`SELECT `+ticketColumns+` FROM tickets
		 WHERE project_id = $1 AND state = 'ready' AND archived_at IS NULL
		 ORDER BY priority DESC, ready_at ASC, id ASC
		 FOR UPDATE SKIP LOCKED LIMIT 1`, projectID)
	tk, err := scanTicket(row)
	if errors.Is(err, sql.ErrNoRows) {
		return board.Ticket{}, false, nil
	}
	if err != nil {
		return board.Ticket{}, false, err
	}
	return tk, true, nil
}

// FreeWorker locks one of the project's workers no active ticket references,
// using FOR UPDATE SKIP LOCKED (03 §5) so concurrent pulls claim different
// workers; ok is false when none is free.
//
// The freeness of a worker is *derived* from the tickets table (03 D2), not
// stored on the worker row, so a single `NOT EXISTS(...) FOR UPDATE OF s`
// scan is not race-free under READ COMMITTED: the subquery is evaluated on the
// statement snapshot while FOR UPDATE only rechecks the (unmodified) worker
// row, so a caller could lock a worker that a just-committed sibling
// transaction has already bound — the I2 index would then reject the second
// binding at commit (03 §5's backstop) instead of the pull staying quiet. To
// keep the pull quiet under contention, we lock every currently-free worker
// with SKIP LOCKED (skipping any a sibling still holds), then re-verify each
// candidate's freeness in a fresh statement — which sees committed bindings —
// while holding its row lock. Holding the lock plus a committed-view recheck
// makes the claim exclusive: any other binder must lock the same worker row
// (RunPull is the only path that assigns a worker), so it will SKIP ours.
func (t *tx) FreeWorker(ctx context.Context, projectID string) (board.Worker, bool, error) {
	var candidates []workerRow
	if err := t.lockFreeCandidates(ctx, projectID, &candidates); err != nil {
		return board.Worker{}, false, err
	}
	for _, c := range candidates {
		var busy bool
		if err := t.sqltx.QueryRowContext(ctx,
			`SELECT EXISTS (
				SELECT 1 FROM tickets
				WHERE worker_id = $1 AND project_id = $2 AND state IN ('working','blocked'))`,
			c.ID, projectID).Scan(&busy); err != nil {
			return board.Worker{}, false, fmt.Errorf("board/postgres: recheck worker: %w", err)
		}
		if !busy {
			return board.Worker{ID: board.WorkerID(c.ID), CreatedAt: c.CreatedAt}, true, nil
		}
	}
	return board.Worker{}, false, nil
}

// AppendOutbox records one emission for the project in this transaction
// (03 §7, I7). Signal-only topics (pull.evaluate, board.updated) carry a nil
// payload, persisted as an empty JSON object.
func (t *tx) AppendOutbox(ctx context.Context, projectID string, e board.Emission) error {
	payload := []byte("{}")
	if e.Payload != nil {
		b, err := json.Marshal(e.Payload)
		if err != nil {
			return fmt.Errorf("board/postgres: marshal payload: %w", err)
		}
		payload = b
	}
	if _, err := t.sqltx.ExecContext(ctx,
		`INSERT INTO outbox (project_id, topic, payload) VALUES ($1, $2, $3)`,
		projectID, string(e.Topic), payload); err != nil {
		return fmt.Errorf("board/postgres: insert outbox: %w", err)
	}
	return nil
}

// lockFreeCandidates locks every worker of the project that currently looks
// free with FOR UPDATE OF s SKIP LOCKED and returns them for FreeWorker's
// committed-view recheck.
func (t *tx) lockFreeCandidates(ctx context.Context, projectID string, out *[]workerRow) (err error) {
	rows, err := t.sqltx.QueryContext(ctx,
		`SELECT s.id, s.created_at FROM workers s
		 WHERE s.project_id = $1 AND NOT EXISTS (`+activeTicketExists+`)
		 ORDER BY s.id
		 FOR UPDATE OF s SKIP LOCKED`, projectID)
	if err != nil {
		return fmt.Errorf("board/postgres: lock free workers: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("board/postgres: close workers: %w", cerr)
		}
	}()

	for rows.Next() {
		var c workerRow
		if serr := rows.Scan(&c.ID, &c.CreatedAt); serr != nil {
			return fmt.Errorf("board/postgres: scan worker: %w", serr)
		}
		*out = append(*out, c)
	}
	if rerr := rows.Err(); rerr != nil {
		return fmt.Errorf("board/postgres: iterate workers: %w", rerr)
	}
	return nil
}

// workerRow is a locked free-worker candidate.
type workerRow struct {
	ID        string
	CreatedAt time.Time
}

// scanTicket reads one ticket row. A sql.ErrNoRows is returned wrapped so
// callers can still detect it with errors.Is while satisfying wrapcheck.
func scanTicket(r pgutil.RowScanner) (board.Ticket, error) {
	var (
		tk         board.Ticket
		id         string
		state      string
		workerID   sql.NullString
		blocked    sql.NullString
		readyAt    sql.NullTime
		archivedAt sql.NullTime
	)
	if err := r.Scan(&id, &tk.Title, &tk.Body, &state, &tk.Priority,
		&workerID, &blocked, &readyAt, &tk.ApprovalRequested, &tk.CreatedAt, &tk.UpdatedAt,
		&tk.StateChangedAt, &archivedAt); err != nil {
		return board.Ticket{}, fmt.Errorf("board/postgres: scan ticket: %w", err)
	}
	tk.ID = board.TicketID(id)
	tk.State = board.State(state)
	if workerID.Valid {
		w := board.WorkerID(workerID.String)
		tk.WorkerID = &w
	}
	if blocked.Valid {
		reason := blocked.String
		tk.BlockedReason = &reason
	}
	if readyAt.Valid {
		rt := readyAt.Time
		tk.ReadyAt = &rt
	}
	if archivedAt.Valid {
		at := archivedAt.Time
		tk.ArchivedAt = &at
	}
	return tk, nil
}

func workerIDArg(p *board.WorkerID) any {
	if p == nil {
		return nil
	}
	return string(*p)
}

func strArg(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func timeArg(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}
