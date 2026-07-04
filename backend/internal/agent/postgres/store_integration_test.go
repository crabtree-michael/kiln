//go:build integration

// Package postgres_test exercises the Store adapter against a real database
// (05 §10): idempotent dedupe, phase/provider-handle persistence, and the
// non-terminal working set are durability promises a fake can't stand in
// for. Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/agent/postgres/...
//
// Each test drops and re-applies the module's migrations (in filename
// order) against agent_turns before running, so reruns are clean without a
// shared migration runner.
package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run agent/postgres integration tests")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Logf("close db: %v", closeErr)
		}
	})
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS agent_turns`); err != nil {
		t.Fatalf("drop agent_turns: %v", err)
	}
	applyMigrations(ctx, t, db)
	return db
}

func applyMigrations(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
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

	for _, name := range names {
		// The migrations directory is fixed and repo-local (this package's
		// own ./migrations), never derived from external input.
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func baseTurn(key int64, workerID string) agent.Turn {
	return agent.Turn{
		IdempotencyKey: key,
		Kind:           agent.KindSend,
		TicketID:       "22222222-2222-2222-2222-222222222222",
		WorkerID:       workerID,
		Message:        "do the thing",
		Phase:          agent.PhaseRecorded,
	}
}

func TestRecord_DedupesByIdempotencyKey(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()
	workerID := "11111111-1111-1111-1111-111111111111"

	created, err := store.Record(ctx, baseTurn(1, workerID))
	if err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if !created {
		t.Fatal("first Record with a fresh idempotency key must report created=true")
	}

	created, err = store.Record(ctx, baseTurn(1, workerID))
	if err != nil {
		t.Fatalf("duplicate Record must be a silent success, got error: %v", err)
	}
	if created {
		t.Fatal("duplicate Record with a seen idempotency key must report created=false — no second turn (05 §7)")
	}

	var count int
	countQuery := `SELECT count(*) FROM agent_turns WHERE idempotency_key = 1`
	if err := db.QueryRowContext(ctx, countQuery).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("want exactly 1 row for idempotency_key=1, got %d — a repeated outbox"+
			" id must never create a second turn (05 §7, D2)", count)
	}
}

func TestRecord_DifferentKeysCreateDistinctRows(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()
	workerID := "22222222-2222-2222-2222-222222222222"

	for _, key := range []int64{101, 102, 103} {
		created, err := store.Record(ctx, baseTurn(key, workerID))
		if err != nil {
			t.Fatalf("Record(%d): %v", key, err)
		}
		if !created {
			t.Fatalf("Record(%d) with a never-seen key must report created=true", key)
		}
	}
}

func TestListNonTerminal_ExcludesDoneButIncludesFailed(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()
	workerID := "33333333-3333-3333-3333-333333333333"

	recorded := baseTurn(10, workerID)
	if _, err := store.Record(ctx, recorded); err != nil {
		t.Fatalf("Record recorded: %v", err)
	}

	failed := baseTurn(11, workerID)
	if _, err := store.Record(ctx, failed); err != nil {
		t.Fatalf("Record failed (pre-update): %v", err)
	}
	failed.Phase = agent.PhaseFailed
	if err := store.Update(ctx, failed); err != nil {
		t.Fatalf("Update -> failed: %v", err)
	}

	done := baseTurn(12, workerID)
	if _, err := store.Record(ctx, done); err != nil {
		t.Fatalf("Record done (pre-update): %v", err)
	}
	done.Phase = agent.PhaseDone
	if err := store.Update(ctx, done); err != nil {
		t.Fatalf("Update -> done: %v", err)
	}

	rows, err := store.ListNonTerminal(ctx)
	if err != nil {
		t.Fatalf("ListNonTerminal: %v", err)
	}
	seen := map[int64]agent.Phase{}
	for _, r := range rows {
		seen[r.IdempotencyKey] = r.Phase
	}

	if _, ok := seen[10]; !ok {
		t.Error("ListNonTerminal must include a recorded row (05 §5, §7)")
	}
	if _, ok := seen[11]; !ok {
		t.Error("ListNonTerminal must include a failed row — Terminal() is done-only," +
			" failed still owes its error event (05 §5)")
	}
	if _, ok := seen[12]; ok {
		t.Error("ListNonTerminal must exclude done rows — the poller's working set is" +
			" phase <> done (05 §5, migrations/0001_agent_turns.sql agent_turns_open)")
	}
}

func TestUpdate_PersistsPhaseAndProviderHandles(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()
	workerID := "44444444-4444-4444-4444-444444444444"

	turn := baseTurn(20, workerID)
	if _, err := store.Record(ctx, turn); err != nil {
		t.Fatalf("Record: %v", err)
	}

	turn.Phase = agent.PhaseTurnStarted
	turn.ProviderWorker = "sandbox-abc"
	turn.ProviderTurn = &agent.TurnRef{Conversation: "session-1", Turn: "job-1"}
	turn.Attempts = 2
	turn.LastError = "transient: 502 upstream"
	if err := store.Update(ctx, turn); err != nil {
		t.Fatalf("Update: %v", err)
	}

	rows, err := store.ListNonTerminal(ctx)
	if err != nil {
		t.Fatalf("ListNonTerminal: %v", err)
	}
	var got *agent.Turn
	for i := range rows {
		if rows[i].IdempotencyKey == 20 {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("updated turn not found via ListNonTerminal")
	}

	if got.Phase != agent.PhaseTurnStarted {
		t.Errorf("phase not persisted: got %v, want %v", got.Phase, agent.PhaseTurnStarted)
	}
	if got.ProviderWorker != "sandbox-abc" {
		t.Errorf("provider_worker not persisted: got %q", got.ProviderWorker)
	}
	if got.ProviderTurn == nil || got.ProviderTurn.Conversation != "session-1" || got.ProviderTurn.Turn != "job-1" {
		t.Errorf("provider_turn (jsonb) not round-tripped: got %+v", got.ProviderTurn)
	}
	if got.Attempts != 2 {
		t.Errorf("attempts not persisted: got %d, want 2", got.Attempts)
	}
	if got.LastError != "transient: 502 upstream" {
		t.Errorf("last_error not persisted: got %q", got.LastError)
	}
}

func TestLatestForWorker_NoRowsReturnsFalse(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	_, found, err := store.LatestForWorker(ctx, "55555555-5555-5555-5555-555555555555")
	if err != nil {
		t.Fatalf("LatestForWorker on an untouched worker: %v", err)
	}
	if found {
		t.Fatal("LatestForWorker must report found=false when no operation has ever touched this worker")
	}
}

func TestLatestForWorker_ReturnsNewestRowByIdempotencyKey(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()
	workerID := "66666666-6666-6666-6666-666666666666"

	older := baseTurn(30, workerID)
	if _, err := store.Record(ctx, older); err != nil {
		t.Fatalf("Record older: %v", err)
	}

	newer := agent.Turn{
		IdempotencyKey: 31,
		Kind:           agent.KindRelease,
		WorkerID:       workerID,
		Phase:          agent.PhaseRecorded,
	}
	if _, err := store.Record(ctx, newer); err != nil {
		t.Fatalf("Record newer (release): %v", err)
	}

	latest, found, err := store.LatestForWorker(ctx, workerID)
	if err != nil {
		t.Fatalf("LatestForWorker: %v", err)
	}
	if !found {
		t.Fatal("LatestForWorker must find a row once one has been recorded")
	}
	if latest.IdempotencyKey != 31 {
		t.Errorf("LatestForWorker must return the newest operation (highest idempotency"+
			" key = newest outbox id), got key=%d, want 31", latest.IdempotencyKey)
	}
	if latest.Kind != agent.KindRelease {
		t.Errorf("expected the release row to be latest (05 §2.1, §3: a release row means"+
			" the next Send starts fresh), got kind=%v", latest.Kind)
	}
}
