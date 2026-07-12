//go:build integration

package main

// Bootstrap-from-env + NOT NULL finalizer against a real database (11 phase 2).
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./cmd/kiln/...
//
// ISOLATION: the shared kiln_test database is used concurrently by sibling
// tasks, and this test's whole point is flipping tenant columns NOT NULL — a
// destructive schema change that would break siblings' in-flight rows. So it
// creates and drops its OWN dedicated database (kiln_test_bootstrap) rather than
// touching kiln_test. Nothing else runs against that database.

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	identitypg "github.com/crabtree-michael/kiln/backend/internal/identity/postgres"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// testCipherKey is a fixed 64-hex-char (32-byte) master key for the test cipher.
const testCipherKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

const bootstrapDBName = "kiln_test_bootstrap"

// testRepoURL is the fixture repo whose basename ("widgets") the seeded project
// name is derived from.
const testRepoURL = "https://github.com/acme/widgets.git"

// closeDB closes a pool in a t.Cleanup, logging (not failing) a close error.
func closeDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Logf("close db: %v", err)
	}
}

// bootstrapTestDB creates a fresh, dedicated database and applies every module's
// migrations into it, returning a pool bound to it. The database is dropped on
// cleanup so reruns start clean.
func bootstrapTestDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the bootstrap integration test")
	}
	ctx := context.Background()

	recreateDB(t, base)
	db, err := sql.Open("postgres", swapDBName(t, base, bootstrapDBName))
	if err != nil {
		t.Fatalf("open %s: %v", bootstrapDBName, err)
	}
	t.Cleanup(func() {
		closeDB(t, db)
		dropDB(t, base)
	})
	if err := applyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// recreateDB drops any leftover dedicated database and creates it fresh.
func recreateDB(t *testing.T, base string) {
	t.Helper()
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatalf("open admin conn: %v", err)
	}
	defer closeDB(t, admin)
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, `DROP DATABASE IF EXISTS `+bootstrapDBName+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop %s: %v", bootstrapDBName, err)
	}
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+bootstrapDBName); err != nil {
		t.Fatalf("create %s: %v", bootstrapDBName, err)
	}
}

// dropDB best-effort removes the dedicated database on cleanup.
func dropDB(t *testing.T, base string) {
	t.Helper()
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Logf("drop cleanup open: %v", err)
		return
	}
	defer closeDB(t, admin)
	if _, err := admin.ExecContext(context.Background(),
		`DROP DATABASE IF EXISTS `+bootstrapDBName+` WITH (FORCE)`); err != nil {
		t.Logf("drop %s: %v", bootstrapDBName, err)
	}
}

// swapDBName rewrites the connection URL's database path.
func swapDBName(t *testing.T, base, name string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// newBootstrapIdentity builds a real identity service over the test database
// with the test cipher (no GitHub client, no allowlist — bootstrap never calls
// either).
func newBootstrapIdentity(t *testing.T, db *sql.DB) *identity.Service {
	t.Helper()
	cipher, err := identity.NewCipher(testCipherKey)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	return identity.NewService(identitypg.New(db), cipher, nil, nil)
}

func TestBootstrapAdoptsAndFinalizes(t *testing.T) {
	ctx := context.Background()
	db := bootstrapTestDB(t)
	idSvc := newBootstrapIdentity(t, db)
	log := discardLogger()

	// A legacy orphan ticket with no tenant column — the row adoption must claim.
	var orphanID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO tickets (id, title) VALUES (gen_random_uuid(), 'orphan') RETURNING id`).
		Scan(&orphanID); err != nil {
		t.Fatalf("seed orphan ticket: %v", err)
	}

	in := bootstrapInput{ //nolint:gosec // test fixtures, not real credentials
		GitHubUser:        "Owner", // mixed case → stored lower-cased
		RepoURL:           testRepoURL,
		AmikaSnapshot:     "snap-1",
		WorkerCount:       25, // out of range → clamped to 10
		AnthropicAPIKey:   "anthropic-env-key",
		AmikaClaudeCredID: "cred-env-1",
	}
	if err := bootstrap(ctx, db, idSvc, in, log); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// User + project seeded from env.
	user, err := idSvc.EnsureUser(ctx, "owner")
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	proj, err := idSvc.ProjectFor(ctx, user.ID)
	if err != nil {
		t.Fatalf("lookup project: %v", err)
	}
	if proj.Name != "widgets" {
		t.Errorf("project name = %q, want widgets", proj.Name)
	}
	if proj.RepoURL != in.RepoURL {
		t.Errorf("project repo_url = %q, want %q", proj.RepoURL, in.RepoURL)
	}
	if proj.WorkerCount != 10 {
		t.Errorf("worker_count = %d, want 10 (clamped)", proj.WorkerCount)
	}

	// Unset config seeded (fingerprint only, never the value).
	me, err := idSvc.Me(ctx, user.ID)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if !me.Settings.AnthropicKey.Set {
		t.Error("anthropic key not seeded")
	}
	if me.Settings.AmikaClaudeCredID != "cred-env-1" {
		t.Errorf("amika cred id = %q, want cred-env-1", me.Settings.AmikaClaudeCredID)
	}

	// Orphan ticket adopted into the project.
	assertTicketProject(t, db, orphanID, proj.ID)

	// Every tenant column flipped NOT NULL.
	for _, tc := range tenantColumns {
		if nullable := columnNullable(t, db, tc.table, tc.column); nullable {
			t.Errorf("%s.%s still nullable after finalize", tc.table, tc.column)
		}
	}

	// Idempotent: a second identical boot is a clean no-op.
	if err := bootstrap(ctx, db, idSvc, in, log); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	assertSingleRow(t, db, "users")
	assertSingleRow(t, db, "projects")
	assertTicketProject(t, db, orphanID, proj.ID)
}

func TestBootstrapPreservesDashboardConfig(t *testing.T) {
	ctx := context.Background()
	db := bootstrapTestDB(t)
	idSvc := newBootstrapIdentity(t, db)
	log := discardLogger()

	// First boot seeds anthropic from env.
	first := bootstrapInput{ //nolint:gosec // test fixtures, not real credentials
		GitHubUser:      "owner",
		RepoURL:         testRepoURL,
		AnthropicAPIKey: "env-key-original",
	}
	if err := bootstrap(ctx, db, idSvc, first, log); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	user, err := idSvc.EnsureUser(ctx, "owner")
	if err != nil {
		t.Fatalf("lookup user: %v", err)
	}

	// A dashboard write changes the anthropic key.
	const dashKey = "dashboard-written-key"
	if err = idSvc.UpdateSettings(ctx, user.ID, identity.SettingsUpdate{AnthropicKey: dashKey}); err != nil {
		t.Fatalf("dashboard update: %v", err)
	}

	// A later boot with a DIFFERENT env value must NOT overwrite the dashboard value.
	second := bootstrapInput{ //nolint:gosec // test fixtures, not real credentials
		GitHubUser:      "owner",
		RepoURL:         testRepoURL,
		AnthropicAPIKey: "env-key-different",
	}
	if err = bootstrap(ctx, db, idSvc, second, log); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	me, err := idSvc.Me(ctx, user.ID)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if got, want := me.Settings.AnthropicKey.Tail, identity.Tail(dashKey); got != want {
		t.Errorf("anthropic tail = %q, want %q (dashboard value should survive)", got, want)
	}
}

func assertTicketProject(t *testing.T, db *sql.DB, ticketID, projectID string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRowContext(context.Background(),
		`SELECT project_id FROM tickets WHERE id = $1`, ticketID).Scan(&got); err != nil {
		t.Fatalf("read ticket project_id: %v", err)
	}
	if !got.Valid || got.String != projectID {
		t.Errorf("ticket project_id = %v, want %s", got, projectID)
	}
}

func columnNullable(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var isNullable string
	if err := db.QueryRowContext(context.Background(), `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&isNullable); err != nil {
		t.Fatalf("inspect %s.%s: %v", table, column, err)
	}
	return isNullable == "YES"
}

func assertSingleRow(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != 1 {
		t.Errorf("%s row count = %d, want 1 (idempotent)", table, n)
	}
}
