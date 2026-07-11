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
// table), so setup only ever creates the board's own tables if missing and
// only ever truncates tickets/workers/outbox — never DROPs, never touches
// tables it doesn't own.
package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/board/postgres"
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

	ensureMigrationsApplied(ctx, t, db)
	truncateBoardTables(ctx, t, db)
	return db
}

// ensureMigrationsApplied applies ./migrations, in filename order, only if
// the board's own tables don't already exist — kiln_test is shared, and
// other modules' tables (e.g. agent_turns) must never be touched here.
func ensureMigrationsApplied(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'tickets'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("check for tickets table: %v", err)
	}
	if exists {
		return
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "migrations")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no .sql migrations found in %s", dir)
	}

	// Read through an fs.FS rooted at the fixed, repo-local migrations
	// directory — names come from that same directory listing, never from
	// external input.
	migrationsFS := os.DirFS(dir)
	for _, name := range names {
		b, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
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
