package testutil

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path"
	"sort"
	"testing"

	"github.com/lib/pq"
)

// ApplyMigrations brings the shared test database up to date with one module's
// embedded migrations, skipping any file already recorded in the
// schema_migrations ledger. It is deliberately the same per-file ledger, under
// the same keys, that the composition root applies at startup
// (cmd/kiln/wiring.go), so the app and the integration suites agree about what
// a database contains.
//
// The ledger is the whole point. The obvious cheaper check — "the module's
// first table exists, so assume its schema is current" — pins a long-lived
// kiln_test to whatever schema it had the day it was created: every migration
// added afterwards is skipped forever, and the gate fails much later with
// `column "keep_sandbox" does not exist` on a database nobody suspects. The
// migrations are not written idempotently (bare CREATE TABLE / ADD COLUMN),
// so only a ledger can decide what still needs applying.
//
// key is the module's stable ledger prefix (its MigrationsKey), and embedded is
// its Migrations FS, still rooted at the "migrations" subdirectory.
func ApplyMigrations(ctx context.Context, t *testing.T, db *sql.DB, key string, embedded fs.FS) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("ensure migration ledger: %v", err)
	}

	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		t.Fatalf("sub migrations %s: %v", key, err)
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read migrations %s: %v", key, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no .sql migrations found for %s", key)
	}
	for _, name := range names {
		applyMigrationFile(ctx, t, db, sub, path.Join(key, name), name)
	}
}

// applyMigrationFile applies one migration unless the ledger already has it,
// recording it on success.
func applyMigrationFile(ctx context.Context, t *testing.T, db *sql.DB, fsys fs.FS, ledgerKey, name string) {
	t.Helper()
	var applied bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = $1)`, ledgerKey).
		Scan(&applied); err != nil {
		t.Fatalf("check migration %s: %v", ledgerKey, err)
	}
	if applied {
		return
	}
	body, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("read migration %s: %v", ledgerKey, err)
	}
	if _, err := db.ExecContext(ctx, string(body)); err != nil {
		// The objects are already there but the ledger doesn't know: this
		// database predates schema_migrations, so its schema is frozen wherever
		// it was created and every later migration would be skipped. Say that,
		// rather than letting a bare "relation already exists" read like a
		// broken migration. Postgres wraps a multi-statement simple query in one
		// implicit transaction, so nothing was half-applied here.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && preLedgerCodes[string(pqErr.Code)] {
			t.Fatalf("migration %s cannot apply because its objects already exist (%s), but the "+
				"schema_migrations ledger has no record of it — this test database predates the "+
				"ledger and its schema is frozen. It holds only test scratch data: reset it with "+
				"`make test-db-reset`.", ledgerKey, pqErr.Code.Name())
		}
		t.Fatalf("apply migration %s: %v", ledgerKey, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations (filename) VALUES ($1)`, ledgerKey); err != nil {
		t.Fatalf("record migration %s: %v", ledgerKey, err)
	}
}

// preLedgerCodes are the SQLSTATEs a migration raises when the objects it
// creates are already present — which, given the ledger said it had not been
// applied, means the database was built before the ledger existed.
//
// Detecting this per file rather than up front matters: the suites do not all
// migrate through this helper (internal/agent and internal/steward drop and
// rebuild their own single table instead), so "the database has tables but no
// ledger rows" is a perfectly normal mid-run state and would be a false alarm.
// A collision on a specific migration is not.
var preLedgerCodes = map[string]bool{
	"42P07": true, // duplicate_table
	"42701": true, // duplicate_column
	"42710": true, // duplicate_object (a constraint)
}
