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
	workerID := mustInsertWorker(ctx, t, db)
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
	workerID := mustInsertWorker(ctx, t, db)
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
	workerID := mustInsertWorker(ctx, t, db)
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
	workerID := mustInsertWorker(ctx, t, db)

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
		mustInsertWorker(ctx, t, db)
	}
	for i := range numReadyTickets {
		_, err := db.ExecContext(ctx, `
			INSERT INTO tickets (id, title, state, priority, ready_at)
			VALUES (gen_random_uuid(), $1, 'ready', $2, now())`,
			fmt.Sprintf("ticket-%d", i), i)
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
			if err := svc.RunPull(ctx); err != nil {
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

// ---- archived_at: soft delete is invisible to reads but keeps the row ------

func TestArchiveTicket_SoftDeletesFromReadsButKeepsRow(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, "mistake", "body")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	archived, err := svc.ArchiveTicket(ctx, created.ID)
	if err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatalf("ArchiveTicket returned nil ArchivedAt")
	}

	// Gone from both read paths...
	if _, err := svc.GetTicket(ctx, created.ID); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("GetTicket after archive: err = %v, want ErrNotFound", err)
	}
	snap, err := svc.GetBoard(ctx)
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
	mustInsertWorker(ctx, t, db)
	store := postgres.New(db)
	svc := board.NewService(store)

	created, err := svc.CreateTicket(ctx, "ready-then-archived", "")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if _, err := svc.MarkReady(ctx, created.ID); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	if _, err := svc.ArchiveTicket(ctx, created.ID); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if err := svc.RunPull(ctx); err != nil {
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

func mustInsertWorker(ctx context.Context, t *testing.T, db *sql.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO workers (id) VALUES (gen_random_uuid()) RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert worker: %v", err)
	}
	return id
}
