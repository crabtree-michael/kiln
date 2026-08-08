//go:build integration

// Package postgres_test exercises the beta store adapter against a real
// database (mirrors push/postgres's integration test shape). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/beta/postgres/...
//
// kiln_test is shared with other modules, so setup only ever applies beta's own
// migrations and only ever truncates beta_signups — never DROPs, never touches
// tables it doesn't own.
package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/beta/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
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

	testutil.ApplyMigrations(ctx, t, db, postgres.MigrationsKey, postgres.Migrations)
	truncateBetaTables(ctx, t, db)
	return db
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

func TestSaveRecordsGitHubLogin(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, "octocat"); err != nil {
		t.Fatalf("Save octocat: %v", err)
	}
	if err := store.Save(ctx, "hubot"); err != nil {
		t.Fatalf("Save hubot: %v", err)
	}
	if got := count(ctx, t, db); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}

	var login string
	if err := db.QueryRowContext(ctx,
		`SELECT github_login FROM beta_signups ORDER BY id LIMIT 1`).Scan(&login); err != nil {
		t.Fatalf("read back github_login: %v", err)
	}
	if login != "octocat" {
		t.Fatalf("github_login = %q, want octocat", login)
	}
}

func TestSaveIsIdempotentOnGitHubLogin(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// The same login twice must not duplicate the row or error (ON CONFLICT DO
	// NOTHING) — a rejected user retrying sign-in walks this path every time.
	if err := store.Save(ctx, "dupuser"); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := store.Save(ctx, "dupuser"); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if got := count(ctx, t, db); got != 1 {
		t.Fatalf("idempotent Save produced %d rows, want 1", got)
	}
}

// The 0002 migration keeps the retired form's emails rather than dropping them,
// so an email-only row must still be insertable (nullable login, CHECK satisfied
// by the address) and must not collide with other email-only rows on the UNIQUE
// login constraint — Postgres allows repeated NULLs there, and this pins it.
func TestHistoricalEmailRowsSurviveTheRekey(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	for _, email := range []string{"old-a@example.com", "old-b@example.com"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO beta_signups (email) VALUES ($1)`, email); err != nil {
			t.Fatalf("insert legacy row %s: %v", email, err)
		}
	}
	if got := count(ctx, t, db); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}

	// And a row with neither identifier is rejected by the CHECK.
	if _, err := db.ExecContext(ctx, `INSERT INTO beta_signups (id) VALUES (DEFAULT)`); err == nil {
		t.Fatal("insert with neither email nor github_login succeeded, want CHECK violation")
	}
}
