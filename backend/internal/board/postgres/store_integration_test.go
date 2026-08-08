//go:build integration

// Package postgres_test exercises the board store adapter against a real
// database (03 §9's "integration tests run against real Postgres: constraint
// backstops... and a concurrency test hammering RunPull from parallel
// goroutines to prove no double-binding"). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/board/postgres/...
//
// kiln_test is shared with other modules (e.g. internal/agent's agent_turns
// table), so setup only ever applies the board's own migrations (through the
// shared schema_migrations ledger) and only ever truncates
// tickets/workers/outbox — never DROPs, never touches tables it doesn't own.
package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/board/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// Fixed tenant ids (11 §3): operations run under projA; projB exists to
// prove the SQL project predicates keep tenants invisible to each other.
const (
	projA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	projB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run board/postgres integration tests")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("close db: %v", closeErr)
		}
	})
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	testutil.ApplyMigrations(ctx, t, db, postgres.MigrationsKey, postgres.Migrations)
	truncateBoardTables(ctx, t, db)
	return db
}

// truncateBoardTables resets exactly the board's own tables (03 I8) so
// every test starts clean, without disturbing other modules sharing
// kiln_test.
func truncateBoardTables(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `TRUNCATE TABLE outbox, tickets, workers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate board tables: %v", err)
	}
}

// ---- I1: state CHECK constraint ------------------------------------------

func TestCheckConstraint_StateEnum(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state) VALUES (gen_random_uuid(), 'x', 'not-a-real-state')`)
	if err == nil {
		t.Fatal("inserting an unrecognized state must violate the CHECK constraint (03 I1)")
	}
}

// ---- I3: worker_id non-null iff state ∈ {working, blocked} ---------------

func TestCheckConstraint_WorkerIDRequiredWhenActive(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state, worker_id) VALUES (gen_random_uuid(), 'x', 'working', NULL)`)
	if err == nil {
		t.Fatal("working with NULL worker_id must violate the I3 CHECK constraint")
	}
}

func TestCheckConstraint_WorkerIDForbiddenWhenInactive(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	workerID := mustInsertWorker(ctx, t, db, projA)
	_, err := db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state, worker_id) VALUES (gen_random_uuid(), 'x', 'shaping', $1)`, workerID)
	if err == nil {
		t.Fatal("shaping with a non-NULL worker_id must violate the I3 CHECK constraint")
	}
}

// I3 binds only LIVE rows (migration 0011): an ARCHIVED blocked row may hold a
// NULL worker_id — deleting a blocked ticket archives it and releases its worker,
// so the off-board row is blocked with no worker. The same shape without
// archived_at still violates I3 (asserted above).
func TestCheckConstraint_ArchivedBlockedRowMayHoldNoWorker(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, title, state, worker_id, blocked_reason, archived_at)
		VALUES (gen_random_uuid(), 'x', 'blocked', NULL, 'why', now())`)
	if err != nil {
		t.Fatalf("archived blocked row with NULL worker_id must satisfy the live-scoped I3: %v", err)
	}
}

// ---- I4: blocked_reason non-null iff state = blocked ---------------------

func TestCheckConstraint_BlockedReasonRequiredWhenBlocked(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	workerID := mustInsertWorker(ctx, t, db, projA)
	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, title, state, worker_id, blocked_reason)
		VALUES (gen_random_uuid(), 'x', 'blocked', $1, NULL)`, workerID)
	if err == nil {
		t.Fatal("blocked with NULL blocked_reason must violate the I4 CHECK constraint")
	}
}

func TestCheckConstraint_BlockedReasonForbiddenWhenNotBlocked(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	workerID := mustInsertWorker(ctx, t, db, projA)
	_, err := db.ExecContext(ctx, `
		INSERT INTO tickets (id, title, state, worker_id, blocked_reason)
		VALUES (gen_random_uuid(), 'x', 'working', $1, 'reason')`, workerID)
	if err == nil {
		t.Fatal("working with a non-NULL blocked_reason must violate the I4 CHECK constraint")
	}
}

// ---- I2: partial unique index one_active_ticket_per_worker ---------------

func TestUniqueIndex_OneActiveTicketPerWorker(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	workerID := mustInsertWorker(ctx, t, db, projA)

	_, err := db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state, worker_id) VALUES (gen_random_uuid(), 'first', 'working', $1)`, workerID)
	if err != nil {
		t.Fatalf("first active ticket for the worker: unexpected error: %v", err)
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state, worker_id) VALUES (gen_random_uuid(), 'second', 'working', $1)`, workerID)
	if err == nil {
		t.Fatal("a second active ticket bound to the same worker must violate one_active_ticket_per_worker (03 I2)")
	}

	// A done ticket referencing the same worker is fine — the index only
	// covers state IN ('working','blocked'); confirms the index is partial,
	// not a blanket unique(worker_id).
	_, err = db.ExecContext(ctx,
		`INSERT INTO tickets (id, title, state, worker_id) VALUES (gen_random_uuid(), 'done-one', 'done', NULL)`)
	if err != nil {
		t.Fatalf("unrelated done ticket insert: unexpected error: %v", err)
	}
}

// ---- Parallel RunPull hammer test: no double-binding under contention ----

// TestParallelRunPull_NoDoubleBind is 03 §9's concurrency test: many
// goroutines calling RunPull against a fixed, smaller pool of workers must
// converge to exactly N bound tickets (N = worker count), each worker bound
// to at most one active ticket (I2), and exactly N agent.send emissions —
// proving FOR UPDATE SKIP LOCKED plus the one_active_ticket_per_worker
// backstop make double-binding impossible under real contention (03 §5,
// §6).
func TestParallelRunPull_NoDoubleBind(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	svc := board.NewService(store)
	ctx := context.Background()

	const numWorkers = 5
	const numReadyTickets = 20

	for range numWorkers {
		mustInsertWorker(ctx, t, db, projA)
	}
	for i := range numReadyTickets {
		_, err := db.ExecContext(ctx, `
			INSERT INTO tickets (id, project_id, title, state, priority, ready_at)
			VALUES (gen_random_uuid(), $1, $2, 'ready', $3, now())`,
			projA, fmt.Sprintf("ticket-%d", i), i)
		if err != nil {
			t.Fatalf("seed ready ticket %d: %v", i, err)
		}
	}

	const numCallers = 10
	var wg sync.WaitGroup
	errs := make(chan error, numCallers)
	for range numCallers {
		wg.Go(func() {
			// Every caller races to drain the whole (ready, free) pair
			// space; RunPull's own loop plus SKIP LOCKED must make this
			// safe with no coordination between callers.
			if err := svc.RunPull(ctx, projA); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent RunPull returned an error: %v", err)
	}

	var workingCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tickets WHERE state = 'working'`).Scan(&workingCount); err != nil {
		t.Fatalf("count working tickets: %v", err)
	}
	if workingCount != numWorkers {
		t.Errorf("working ticket count = %d, want exactly %d (the WIP cap — 03 I2/D2)", workingCount, numWorkers)
	}

	var readyCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tickets WHERE state = 'ready'`).Scan(&readyCount); err != nil {
		t.Fatalf("count ready tickets: %v", err)
	}
	if readyCount != numReadyTickets-numWorkers {
		t.Errorf("remaining ready count = %d, want %d", readyCount, numReadyTickets-numWorkers)
	}

	var distinctWorkers int
	if err := db.QueryRowContext(ctx,
		`SELECT count(DISTINCT worker_id) FROM tickets WHERE state = 'working'`).Scan(&distinctWorkers); err != nil {
		t.Fatalf("count distinct bound workers: %v", err)
	}
	if distinctWorkers != numWorkers {
		t.Errorf("distinct workers bound = %d, want %d — any duplicate means double-binding slipped past"+
			" FOR UPDATE SKIP LOCKED and the I2 index", distinctWorkers, numWorkers)
	}

	var sendCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE topic = 'agent.send'`).Scan(&sendCount); err != nil {
		t.Fatalf("count agent.send emissions: %v", err)
	}
	if sendCount != numWorkers {
		t.Errorf("agent.send emission count = %d, want exactly %d"+
			" (one per binding, no duplicates from repeated/racing RunPull calls)", sendCount, numWorkers)
	}
}

// ---- state_changed_at: the "time in status" clock (0007_state_changed_at) ----

// TestStateChangedAt_OnlyAdvancesOnRealTransition proves the CASE in
// UpdateTicket against real Postgres: state_changed_at moves only when `state`
// actually changes. A Working→Working nudge (SendToAgent) bumps updated_at but
// must leave state_changed_at fixed, so the client's time-in-status subtext
// keeps accumulating through nudges instead of resetting; a Blocked→Working
// resume is a real transition and must advance it.
func TestStateChangedAt_OnlyAdvancesOnRealTransition(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	mustInsertWorker(ctx, t, db, projA)
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, projA, "time-in-status", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := svc.MarkReady(ctx, projA, created.ID); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}
	working, err := svc.GetTicket(ctx, projA, created.ID)
	if err != nil {
		t.Fatalf("GetTicket after pull: %v", err)
	}
	if working.State != board.StateWorking {
		t.Fatalf("state after pull = %q, want working", working.State)
	}
	enteredWorking := working.StateChangedAt

	// A nudge: same state, so state_changed_at must be byte-for-byte unchanged
	// while updated_at moves forward.
	nudged, err := svc.SendToAgent(ctx, projA, created.ID, "keep going")
	if err != nil {
		t.Fatalf("SendToAgent (nudge): %v", err)
	}
	if !nudged.StateChangedAt.Equal(enteredWorking) {
		t.Errorf("nudge moved StateChangedAt: got %v, want unchanged %v", nudged.StateChangedAt, enteredWorking)
	}
	if !nudged.UpdatedAt.After(enteredWorking) {
		t.Errorf("nudge left UpdatedAt = %v, want advanced past %v", nudged.UpdatedAt, enteredWorking)
	}

	// A real transition: block then resume. state_changed_at must advance past
	// when the ticket first entered Working.
	if _, err := svc.MarkBlocked(ctx, projA, created.ID, "needs a decision"); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	resumed, err := svc.SendToAgent(ctx, projA, created.ID, "here's the answer")
	if err != nil {
		t.Fatalf("SendToAgent (resume): %v", err)
	}
	if !resumed.StateChangedAt.After(enteredWorking) {
		t.Errorf("resume left StateChangedAt = %v, want advanced past %v (a real transition restarts the clock)",
			resumed.StateChangedAt, enteredWorking)
	}
}

// ---- archived_at: soft delete is invisible to reads but keeps the row ------

func TestArchiveTicket_SoftDeletesFromReadsButKeepsRow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, projA, "mistake", "body")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	archived, err := svc.ArchiveTicket(ctx, projA, created.ID)
	if err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatalf("ArchiveTicket returned nil ArchivedAt")
	}

	// Gone from both read paths...
	if _, err := svc.GetTicket(ctx, projA, created.ID); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("GetTicket after archive: err = %v, want ErrNotFound", err)
	}
	snap, err := svc.GetBoard(ctx, projA)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(snap.Shaping)+len(snap.Ready)+len(snap.Working)+len(snap.Blocked)+len(snap.Done) != 0 {
		t.Fatalf("archived ticket still visible in snapshot: %+v", snap)
	}

	// ...but the row is retained (soft delete, not hard delete).
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tickets WHERE id = $1 AND archived_at IS NOT NULL`, string(created.ID)).Scan(&count); err != nil {
		t.Fatalf("count archived row: %v", err)
	}
	if count != 1 {
		t.Fatalf("archived row count = %d, want 1 (row retained)", count)
	}
}

// A ready ticket, once archived, is no longer a pull candidate.
func TestArchiveTicket_ArchivedReadyIsNotPulled(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	mustInsertWorker(ctx, t, db, projA)
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, projA, "ready-then-archived", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := svc.MarkReady(ctx, projA, created.ID); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	if _, err := svc.ArchiveTicket(ctx, projA, created.ID); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	var working int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tickets WHERE state = 'working'`).Scan(&working); err != nil {
		t.Fatalf("count working: %v", err)
	}
	if working != 0 {
		t.Fatalf("archived ready ticket was pulled into working (count=%d)", working)
	}
}

// ---- keep_sandbox: the per-ticket sandbox option round-trips, and suppresses
// the release the accept would otherwise emit (migration 0012) ---------------

func TestKeepSandbox_RoundTripsAndSuppressesRelease(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	mustInsertWorker(ctx, t, db, projA)
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, projA, "keep my sandbox", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// The column defaults to false, so an untouched ticket recycles as before.
	if created.KeepSandbox {
		t.Fatal("a new ticket must default to KeepSandbox=false")
	}

	if _, err := svc.SetKeepSandbox(ctx, projA, created.ID, true); err != nil {
		t.Fatalf("SetKeepSandbox: %v", err)
	}
	// Read back through the real SELECT projection, not the write's own RETURNING.
	reread, err := svc.GetTicket(ctx, projA, created.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if !reread.KeepSandbox {
		t.Fatal("KeepSandbox did not survive the round trip through Postgres")
	}

	// Drive it to done through the real pull, and prove no agent.release landed.
	if _, err := svc.MarkReady(ctx, projA, created.ID); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}
	if _, err := svc.AcceptToDone(ctx, projA, created.ID, board.CompletionLink{}, ""); err != nil {
		t.Fatalf("AcceptToDone: %v", err)
	}

	var releases int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE topic = $1`, string(board.TopicAgentRelease)).Scan(&releases); err != nil {
		t.Fatalf("count agent.release: %v", err)
	}
	if releases != 0 {
		t.Fatalf("agent.release rows = %d, want 0 — a saved sandbox is never recycled", releases)
	}
	// The option itself is retained on the done row (the sandbox is still saved).
	var kept bool
	if err := db.QueryRowContext(ctx,
		`SELECT keep_sandbox FROM tickets WHERE id = $1`, string(created.ID)).Scan(&kept); err != nil {
		t.Fatalf("read keep_sandbox: %v", err)
	}
	if !kept {
		t.Error("keep_sandbox must survive AcceptToDone")
	}
}

func mustInsertWorker(ctx context.Context, t *testing.T, db *sql.DB, projectID string) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO workers (id, project_id) VALUES (gen_random_uuid(), $1) RETURNING id`,
		projectID).Scan(&id); err != nil {
		t.Fatalf("insert worker: %v", err)
	}
	return id
}

// ---- Project isolation (11 §3): the SQL predicates, per query family --------

// Snapshot family: tickets and worker counts are scoped by project_id.
func TestProjectIsolation_SnapshotScoped(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	mustInsertWorker(ctx, t, db, projA)
	mustInsertWorker(ctx, t, db, projB)
	mustInsertWorker(ctx, t, db, projB)
	if _, err := svc.CreateTicket(ctx, projA, "A's ticket", ""); err != nil {
		t.Fatalf("CreateTicket(projA): %v", err)
	}
	if _, err := svc.CreateTicket(ctx, projB, "B's ticket", ""); err != nil {
		t.Fatalf("CreateTicket(projB): %v", err)
	}

	snap, err := svc.GetBoard(ctx, projA)
	if err != nil {
		t.Fatalf("GetBoard(projA): %v", err)
	}
	if len(snap.Shaping) != 1 || snap.Shaping[0].Title != "A's ticket" {
		t.Errorf("projA Shaping = %+v, want exactly A's ticket", snap.Shaping)
	}
	if snap.WorkerTotal != 1 || snap.WorkerFree != 1 {
		t.Errorf("projA WorkerTotal/Free = %d/%d, want 1/1 — B's workers leaked into A's counts",
			snap.WorkerTotal, snap.WorkerFree)
	}
}

// Targeted-read + targeted-mutation families: a valid id from another project
// is ErrNotFound for GetTicket and for the lock-then-check mutation path, and
// the foreign row is left untouched.
func TestProjectIsolation_ForeignTicketIDIsNotFound(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, projB, "B's ticket", "")
	if err != nil {
		t.Fatalf("CreateTicket(projB): %v", err)
	}

	if _, err := svc.GetTicket(ctx, projA, created.ID); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("GetTicket(projA, B's id) error = %v, want ErrNotFound", err)
	}
	if _, err := svc.MarkReady(ctx, projA, created.ID); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("MarkReady(projA, B's id) error = %v, want ErrNotFound", err)
	}

	got, err := svc.GetTicket(ctx, projB, created.ID)
	if err != nil {
		t.Fatalf("GetTicket(projB): %v", err)
	}
	if got.State != board.StateShaping {
		t.Errorf("B's ticket state = %q, want untouched shaping", got.State)
	}
}

// Pull family, ticket side: A's pull must never select B's ready ticket even
// with free A capacity (the NextReadyTicket project predicate).
func TestProjectIsolation_PullIgnoresForeignReadyTicket(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	mustInsertWorker(ctx, t, db, projA)
	bTicket, err := svc.CreateTicket(ctx, projB, "B ready", "")
	if err != nil {
		t.Fatalf("CreateTicket(projB): %v", err)
	}
	if _, err := svc.MarkReady(ctx, projB, bTicket.ID); err != nil {
		t.Fatalf("MarkReady(projB): %v", err)
	}

	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull(projA): %v", err)
	}
	got, err := svc.GetTicket(ctx, projB, bTicket.ID)
	if err != nil {
		t.Fatalf("GetTicket(projB): %v", err)
	}
	if got.State != board.StateReady {
		t.Errorf("B's ready ticket state = %q after RunPull(projA), want still ready", got.State)
	}
}

// Pull family, worker side: A's ready ticket must not bind B's free worker
// (the FreeWorker/lockFreeCandidates project predicate).
func TestProjectIsolation_PullIgnoresForeignFreeWorker(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	mustInsertWorker(ctx, t, db, projB)
	aTicket, err := svc.CreateTicket(ctx, projA, "A ready", "")
	if err != nil {
		t.Fatalf("CreateTicket(projA): %v", err)
	}
	if _, err := svc.MarkReady(ctx, projA, aTicket.ID); err != nil {
		t.Fatalf("MarkReady(projA): %v", err)
	}

	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull(projA): %v", err)
	}
	got, err := svc.GetTicket(ctx, projA, aTicket.ID)
	if err != nil {
		t.Fatalf("GetTicket(projA): %v", err)
	}
	if got.State != board.StateReady {
		t.Errorf("A's ticket state = %q, want still ready — only B has a free worker and it must be invisible to A", got.State)
	}
}

// Worker-reconciliation family: ReconcileWorkers counts and inserts per
// project, and WorkerIDs lists only the project's slots.
func TestProjectIsolation_ReconcileWorkersAndWorkerIDs(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)

	if err := store.ReconcileWorkers(ctx, projA, 3); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 3): %v", err)
	}
	if err := store.ReconcileWorkers(ctx, projB, 2); err != nil {
		t.Fatalf("ReconcileWorkers(projB, 2): %v", err)
	}
	// Idempotent per project: A already has 3, so this must add none.
	if err := store.ReconcileWorkers(ctx, projA, 3); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 3) again: %v", err)
	}

	aIDs, err := store.WorkerIDs(ctx, projA)
	if err != nil {
		t.Fatalf("WorkerIDs(projA): %v", err)
	}
	bIDs, err := store.WorkerIDs(ctx, projB)
	if err != nil {
		t.Fatalf("WorkerIDs(projB): %v", err)
	}
	if len(aIDs) != 3 {
		t.Errorf("WorkerIDs(projA) = %d ids, want 3 (B's rows must not count toward A's cap)", len(aIDs))
	}
	if len(bIDs) != 2 {
		t.Errorf("WorkerIDs(projB) = %d ids, want 2", len(bIDs))
	}
	seen := map[string]bool{}
	for _, id := range aIDs {
		seen[id] = true
	}
	for _, id := range bIDs {
		if seen[id] {
			t.Errorf("worker id %s listed under both projects", id)
		}
	}
}

// Worker-health family (03 §5 amended): an errored worker is excluded from the
// free count and never bound by the pull; SetWorkerHealth reconciles both ways
// and stays project-scoped.

// TestSetWorkerHealth_ErroredWorkerSkippedByPull proves FreeWorker's health
// predicate: with one of two workers errored and two ready tickets, exactly one
// ticket pulls, bound to the healthy worker.
func TestSetWorkerHealth_ErroredWorkerSkippedByPull(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	errored := mustInsertWorker(ctx, t, db, projA)
	healthy := mustInsertWorker(ctx, t, db, projA)
	if err := store.SetWorkerHealth(ctx, projA, []string{errored}); err != nil {
		t.Fatalf("SetWorkerHealth: %v", err)
	}

	ids := make([]board.TicketID, 0, 2)
	for _, title := range []string{"t1", "t2"} {
		tk, err := svc.CreateTicket(ctx, projA, title, "")
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		if _, err := svc.MarkReady(ctx, projA, tk.ID); err != nil {
			t.Fatalf("MarkReady: %v", err)
		}
		ids = append(ids, tk.ID)
	}

	snap, err := svc.GetBoard(ctx, projA)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if snap.WorkerTotal != 2 || snap.WorkerFree != 1 {
		t.Errorf("WorkerTotal/WorkerFree = %d/%d, want 2/1 (errored worker in total, out of free)",
			snap.WorkerTotal, snap.WorkerFree)
	}

	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}

	working, ready := 0, 0
	var boundWorker string
	for _, id := range ids {
		got, gerr := svc.GetTicket(ctx, projA, id)
		if gerr != nil {
			t.Fatalf("GetTicket: %v", gerr)
		}
		switch got.State {
		case board.StateWorking:
			working++
			if got.WorkerID != nil {
				boundWorker = string(*got.WorkerID)
			}
		case board.StateReady:
			ready++
		case board.StateShaping, board.StateBlocked, board.StateDone:
			// never occur for these two just-pulled tickets
		}
	}
	if working != 1 || ready != 1 {
		t.Errorf("after pull: working=%d ready=%d, want 1/1 (only one healthy sandbox)", working, ready)
	}
	if boundWorker != healthy {
		t.Errorf("bound worker = %q, want the healthy worker %q, never the errored %q",
			boundWorker, healthy, errored)
	}
}

// TestSetWorkerHealth_ReconcilesBothWaysAndPerProject proves the full reconcile
// flips a worker errored→healthy and back, and never touches another project's
// rows.
func TestSetWorkerHealth_ReconcilesBothWaysAndPerProject(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	wA := mustInsertWorker(ctx, t, db, projA)
	mustInsertWorker(ctx, t, db, projB) // must stay healthy through a projA reconcile

	freeCount := func(proj string) int {
		t.Helper()
		snap, err := svc.GetBoard(ctx, proj)
		if err != nil {
			t.Fatalf("GetBoard(%s): %v", proj, err)
		}
		return snap.WorkerFree
	}

	if err := store.SetWorkerHealth(ctx, projA, []string{wA}); err != nil {
		t.Fatalf("mark errored: %v", err)
	}
	if got := freeCount(projA); got != 0 {
		t.Errorf("projA WorkerFree = %d after errored, want 0", got)
	}
	if got := freeCount(projB); got != 1 {
		t.Errorf("projB WorkerFree = %d, want 1 — a projA reconcile must not touch projB", got)
	}

	// Recovery: an empty errored set flips projA's worker back to healthy.
	if err := store.SetWorkerHealth(ctx, projA, nil); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got := freeCount(projA); got != 1 {
		t.Errorf("projA WorkerFree = %d after recovery, want 1", got)
	}
}

// ReconcileWorkers shrinks the pool when the configured count drops: lowering n
// deletes the excess free slots (so the spawned-sandbox count follows the
// dashboard setting down), scoped to the project.
func TestReconcileWorkers_ShrinksToLowerCount(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)

	if err := store.ReconcileWorkers(ctx, projA, 5); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 5): %v", err)
	}
	// A neighbour tenant's pool must be untouched by A's shrink.
	if err := store.ReconcileWorkers(ctx, projB, 4); err != nil {
		t.Fatalf("ReconcileWorkers(projB, 4): %v", err)
	}

	if err := store.ReconcileWorkers(ctx, projA, 3); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 3): %v", err)
	}
	aIDs, err := store.WorkerIDs(ctx, projA)
	if err != nil {
		t.Fatalf("WorkerIDs(projA): %v", err)
	}
	if len(aIDs) != 3 {
		t.Errorf("WorkerIDs(projA) = %d ids after shrink to 3, want 3", len(aIDs))
	}
	bIDs, err := store.WorkerIDs(ctx, projB)
	if err != nil {
		t.Fatalf("WorkerIDs(projB): %v", err)
	}
	if len(bIDs) != 4 {
		t.Errorf("WorkerIDs(projB) = %d ids, want 4 (A's shrink must not touch B)", len(bIDs))
	}
}

// ReconcileWorkers never deletes a busy slot: an active ticket references it, so
// I2 / the FK forbid removal. When more slots are busy than the new count, the
// pool floors at the busy set rather than dropping below it or erroring.
func TestReconcileWorkers_ShrinkSpareBusySlots(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)

	if err := store.ReconcileWorkers(ctx, projA, 4); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 4): %v", err)
	}
	ids, err := store.WorkerIDs(ctx, projA)
	if err != nil {
		t.Fatalf("WorkerIDs(projA): %v", err)
	}
	// Bind two of the four slots with active (working) tickets.
	for _, id := range ids[:2] {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO tickets (id, project_id, title, state, worker_id)
			 VALUES (gen_random_uuid(), $1, 'busy', 'working', $2)`, projA, id); err != nil {
			t.Fatalf("bind worker %s: %v", id, err)
		}
	}

	// Ask for 1, but two slots are busy: shrink removes only the two free ones.
	if err := store.ReconcileWorkers(ctx, projA, 1); err != nil {
		t.Fatalf("ReconcileWorkers(projA, 1): %v", err)
	}
	after, err := store.WorkerIDs(ctx, projA)
	if err != nil {
		t.Fatalf("WorkerIDs(projA) after shrink: %v", err)
	}
	if len(after) != 2 {
		t.Errorf("WorkerIDs(projA) = %d ids, want 2 (both busy slots survive; only free ones removed)", len(after))
	}
	surviving := map[string]bool{}
	for _, id := range after {
		surviving[id] = true
	}
	for _, id := range ids[:2] {
		if !surviving[id] {
			t.Errorf("busy worker %s was deleted; a bound slot must never be removed", id)
		}
	}
}

// Outbox family: every emission is stamped with the project that produced it.
func TestProjectIsolation_OutboxRowsCarryProjectID(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	if _, err := svc.CreateTicket(ctx, projA, "A's ticket", ""); err != nil {
		t.Fatalf("CreateTicket(projA): %v", err)
	}

	var total, scoped int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&total); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM outbox WHERE project_id = $1`, projA).Scan(&scoped); err != nil {
		t.Fatalf("count scoped outbox: %v", err)
	}
	if total == 0 {
		t.Fatal("CreateTicket must append outbox emissions")
	}
	if scoped != total {
		t.Errorf("outbox rows with project_id=projA: %d of %d — every append must set the tenant column", scoped, total)
	}
}

// ---- Ticket dependencies (0013) --------------------------------------------
//
// The pull's skip test and the cycle walk are both SQL, so the fake cannot
// stand in for them: these run the real predicates against real rows.

// readyTicket creates a ticket and queues it, returning its id — the shape the
// pull orders over.
func readyTicket(ctx context.Context, t *testing.T, svc *board.Service, projectID, title string) board.TicketID {
	t.Helper()
	tk, err := svc.CreateTicket(ctx, projectID, title, "")
	if err != nil {
		t.Fatalf("CreateTicket(%s): %v", title, err)
	}
	if _, err := svc.MarkReady(ctx, projectID, tk.ID); err != nil {
		t.Fatalf("MarkReady(%s): %v", title, err)
	}
	return tk.ID
}

// The headline against real SQL: the NOT EXISTS in NextReadyTicket holds back a
// ticket whose dependency has not landed, and the pull moves on to the next one
// instead of stalling.
func TestDependencies_PullSkipsUnmetAndTakesTheNextTicket(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)
	mustInsertWorker(ctx, t, db, projA)

	// waiter is queued first, so it is what the single worker would claim if the
	// dependency predicate did nothing.
	waiter := readyTicket(ctx, t, svc, projA, "Use the new column")
	blocker := readyTicket(ctx, t, svc, projA, "Land the migration")
	if _, err := svc.AddDependency(ctx, projA, waiter, blocker); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}
	got, err := svc.GetTicket(ctx, projA, waiter)
	if err != nil {
		t.Fatalf("GetTicket(waiter): %v", err)
	}
	if got.State != board.StateReady {
		t.Errorf("waiter state = %q, want ready — its dependency has not landed", got.State)
	}
	other, err := svc.GetTicket(ctx, projA, blocker)
	if err != nil {
		t.Fatalf("GetTicket(blocker): %v", err)
	}
	if other.State != board.StateWorking {
		t.Errorf("blocker state = %q, want working — the pull must move past the waiting ticket", other.State)
	}
}

// Completing the dependency releases the waiter on the next pull.
func TestDependencies_PullProceedsOnceTheDependencyIsDone(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)
	mustInsertWorker(ctx, t, db, projA)
	mustInsertWorker(ctx, t, db, projA)

	waiter := readyTicket(ctx, t, svc, projA, "Waiter")
	blocker := readyTicket(ctx, t, svc, projA, "Blocker")
	if _, err := svc.AddDependency(ctx, projA, waiter, blocker); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull: %v", err)
	}
	if _, err := svc.AcceptToDone(ctx, projA, blocker, board.CompletionLink{}, ""); err != nil {
		t.Fatalf("AcceptToDone: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull after completion: %v", err)
	}

	got, err := svc.GetTicket(ctx, projA, waiter)
	if err != nil {
		t.Fatalf("GetTicket(waiter): %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("waiter state = %q, want working once its dependency was accepted", got.State)
	}
}

// Deleting the dependency must not strand its dependents: the archived_at
// predicate is what stops a dead edge blocking forever.
func TestDependencies_ArchivedDependencyStopsBlocking(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)
	mustInsertWorker(ctx, t, db, projA)

	waiter := readyTicket(ctx, t, svc, projA, "Waiter")
	doomed, err := svc.CreateTicket(ctx, projA, "Abandoned", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := svc.AddDependency(ctx, projA, waiter, doomed.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull while the dependency lived: %v", err)
	}
	if got, gerr := svc.GetTicket(ctx, projA, waiter); gerr != nil || got.State != board.StateReady {
		t.Fatalf("waiter state = %q (err %v), want ready while the dependency existed", got.State, gerr)
	}

	if _, err := svc.ArchiveTicket(ctx, projA, doomed.ID); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if err := svc.RunPull(ctx, projA); err != nil {
		t.Fatalf("RunPull after archiving: %v", err)
	}
	got, err := svc.GetTicket(ctx, projA, waiter)
	if err != nil {
		t.Fatalf("GetTicket(waiter): %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("waiter state = %q, want working — an archived dependency can never be met", got.State)
	}
	if len(got.DependsOn) != 0 || got.UnmetDependencies != 0 {
		t.Errorf("DependsOn/Unmet = %v/%d, want empty — an archived dependency is gone from reads",
			got.DependsOn, got.UnmetDependencies)
	}
}

// The recursive CTE refuses a ring three tickets long and names the chain.
func TestDependencies_RecursiveWalkRejectsTransitiveCycle(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	a := readyTicket(ctx, t, svc, projA, "A")
	b := readyTicket(ctx, t, svc, projA, "B")
	c := readyTicket(ctx, t, svc, projA, "C")
	if _, err := svc.AddDependency(ctx, projA, a, b); err != nil {
		t.Fatalf("AddDependency(a->b): %v", err)
	}
	if _, err := svc.AddDependency(ctx, projA, b, c); err != nil {
		t.Fatalf("AddDependency(b->c): %v", err)
	}

	_, err := svc.AddDependency(ctx, projA, c, a)
	var cyc *board.ErrCircularDependency
	if !errors.As(err, &cyc) {
		t.Fatalf("AddDependency(c->a) = %v, want *board.ErrCircularDependency", err)
	}
	want := []board.TicketID{a, b, c}
	if len(cyc.Path) != len(want) {
		t.Fatalf("Path = %v, want %v", cyc.Path, want)
	}
	for i := range want {
		if cyc.Path[i] != want[i] {
			t.Fatalf("Path = %v, want %v", cyc.Path, want)
		}
	}
}

// A self-edge is refused by the service, and the CHECK constraint is the
// backstop if it ever were not (03 §8's "database constraints back up every
// invariant even if service code is wrong").
func TestDependencies_SelfEdgeRefusedAndConstrained(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	a := readyTicket(ctx, t, svc, projA, "A")
	var cyc *board.ErrCircularDependency
	if _, err := svc.AddDependency(ctx, projA, a, a); !errors.As(err, &cyc) {
		t.Errorf("AddDependency(a->a) = %v, want *board.ErrCircularDependency", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ticket_dependencies (project_id, ticket_id, depends_on_id) VALUES ($1, $2, $2)`,
		projA, string(a)); err == nil {
		t.Error("a self-dependency must violate ticket_dependency_not_self")
	}
}

// Re-adding an edge is a no-op, not a duplicate-key error: at-least-once callers
// must be safe.
func TestDependencies_AddIsIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	a := readyTicket(ctx, t, svc, projA, "A")
	b := readyTicket(ctx, t, svc, projA, "B")
	if _, err := svc.AddDependency(ctx, projA, a, b); err != nil {
		t.Fatalf("first AddDependency: %v", err)
	}
	got, err := svc.AddDependency(ctx, projA, a, b)
	if err != nil {
		t.Fatalf("second AddDependency: %v", err)
	}
	if len(got.DependsOn) != 1 {
		t.Errorf("DependsOn = %v, want exactly one edge", got.DependsOn)
	}
}

// Tenancy (11 §3): an edge may not reach across projects.
func TestDependencies_CannotCrossProjects(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	mine := readyTicket(ctx, t, svc, projA, "Mine")
	theirs := readyTicket(ctx, t, svc, projB, "Theirs")
	if _, err := svc.AddDependency(ctx, projA, mine, theirs); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("AddDependency across projects = %v, want ErrNotFound", err)
	}
}

// The FK cleans up if a ticket row is ever hard-deleted (the board soft-deletes,
// but nothing should be left pointing at a row that is gone).
func TestDependencies_HardDeleteCascades(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	a := readyTicket(ctx, t, svc, projA, "A")
	b := readyTicket(ctx, t, svc, projA, "B")
	if _, err := svc.AddDependency(ctx, projA, a, b); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM tickets WHERE id = $1`, string(b)); err != nil {
		t.Fatalf("hard delete: %v", err)
	}
	var edges int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM ticket_dependencies WHERE ticket_id = $1`, string(a)).Scan(&edges); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edges != 0 {
		t.Errorf("edges = %d, want 0 — ON DELETE CASCADE should have removed them", edges)
	}
}
