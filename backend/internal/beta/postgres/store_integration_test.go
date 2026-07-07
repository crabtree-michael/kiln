//go:build integration

// Package postgres_test exercises the beta store adapter against a real
// database (mirrors push/postgres's integration test shape). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/beta/postgres/...
//
// kiln_test is shared with other modules, so setup only ever creates beta's own
// table if missing and only ever truncates beta_signups — never DROPs, never
// touches tables it doesn't own.
package postgres_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/beta/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run beta/postgres integration tests")
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
	truncateBetaTables(ctx, t, db)
	return db
}

// ensureMigrationsApplied applies ./migrations, in filename order, only if
// beta's own table doesn't already exist — kiln_test is shared, and other
// modules' tables must never be touched here.
func ensureMigrationsApplied(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'beta_signups'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("check for beta_signups table: %v", err)
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

// truncateBetaTables resets exactly beta's own table so every test starts clean,
// without disturbing other modules sharing kiln_test.
func truncateBetaTables(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE beta_signups RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate beta tables: %v", err)
	}
}

func count(ctx context.Context, t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM beta_signups`).Scan(&n); err != nil {
		t.Fatalf("count beta_signups: %v", err)
	}
	return n
}

func TestSaveRecordsEmail(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, "a@example.com"); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := store.Save(ctx, "b@example.com"); err != nil {
		t.Fatalf("Save b: %v", err)
	}
	if got := count(ctx, t, db); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}
}

func TestSaveIsIdempotentOnEmail(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// Same address twice must not duplicate the row or error (ON CONFLICT DO NOTHING).
	if err := store.Save(ctx, "dup@example.com"); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := store.Save(ctx, "dup@example.com"); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if got := count(ctx, t, db); got != 1 {
		t.Fatalf("idempotent Save produced %d rows, want 1", got)
	}
}
