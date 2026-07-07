//go:build integration

// Package postgres_test exercises the Store adapter against a real database,
// focused on the tenant column: Upsert writes project_id (empty → NULL) and
// List returns it (NULL → empty). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/steward/postgres/...
//
// Each test drops and re-applies the module's migrations (in filename order)
// against steward_pokes before running, so reruns are clean without a shared
// migration runner.
package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/steward"
	"github.com/crabtree-michael/kiln/backend/internal/steward/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run steward/postgres integration tests")
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

	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS steward_pokes`); err != nil {
		t.Fatalf("drop steward_pokes: %v", err)
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
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.ExecContext(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func TestStore_UpsertWritesProjectIDAndListReturnsIt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	store := postgres.New(db)

	const projectID = "11111111-1111-1111-1111-111111111111"
	pokedAt := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	if err := store.Upsert(ctx, projectID, "t1", "w1", pokedAt); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Empty projectID must land as NULL, not fail the uuid cast.
	if err := store.Upsert(ctx, "", "t2", "w2", pokedAt); err != nil {
		t.Fatalf("upsert with empty project: %v", err)
	}

	recs, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byTicket := map[string]steward.PokeRecord{}
	for _, r := range recs {
		byTicket[r.TicketID] = r
	}
	if got := byTicket["t1"].ProjectID; got != projectID {
		t.Fatalf("t1 project_id = %q, want %q", got, projectID)
	}
	if got := byTicket["t2"].ProjectID; got != "" {
		t.Fatalf("t2 project_id = %q, want empty (NULL row)", got)
	}

	// Re-upsert must refresh project_id on conflict.
	const otherProject = "22222222-2222-2222-2222-222222222222"
	if uerr := store.Upsert(ctx, otherProject, "t1", "w1", pokedAt.Add(time.Minute)); uerr != nil {
		t.Fatalf("re-upsert: %v", uerr)
	}
	recs, err = store.List(ctx)
	if err != nil {
		t.Fatalf("list after re-upsert: %v", err)
	}
	for _, r := range recs {
		if r.TicketID == "t1" && r.ProjectID != otherProject {
			t.Fatalf("t1 project_id after re-upsert = %q, want %q", r.ProjectID, otherProject)
		}
	}
}
