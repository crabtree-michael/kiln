//go:build integration

// Package postgres_test exercises the push store adapter against a real
// database (mirrors identity/postgres's integration test shape). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/push/postgres/...
//
// kiln_test is shared with other modules, so setup only ever creates push's own
// tables if missing and only ever truncates push_subscriptions and
// push_user_settings — never DROPs, never touches tables it doesn't own.
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

	"github.com/crabtree-michael/kiln/backend/internal/push"
	"github.com/crabtree-michael/kiln/backend/internal/push/postgres"
)

const (
	userA = "11111111-1111-1111-1111-111111111111"
	userB = "22222222-2222-2222-2222-222222222222"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run push/postgres integration tests")
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
	truncatePushTables(ctx, t, db)
	return db
}

// tableExists reports whether a public-schema table is present.
func tableExists(ctx context.Context, t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1
	)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("check for %s table: %v", name, err)
	}
	return exists
}

// ensureMigrationsApplied applies ./migrations, in filename order, only if
// push's own tables don't already exist — kiln_test is shared, and other
// modules' tables must never be touched here. A database that pre-dates the
// tenancy migration (push_subscriptions present, push_user_settings absent)
// gets only the missing tail applied.
func ensureMigrationsApplied(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	haveSubs := tableExists(ctx, t, db, "push_subscriptions")
	haveUserSettings := tableExists(ctx, t, db, "push_user_settings")
	if haveSubs && haveUserSettings {
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
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		if haveSubs && e.Name() != "0003_user_tenancy.sql" {
			continue // pre-tenancy schema already in place; apply only the tail.
		}
		names = append(names, e.Name())
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

// truncatePushTables resets exactly push's own tables so every test starts
// clean, without disturbing other modules sharing kiln_test. The legacy
// push_settings singleton is left alone — it is unread after 11 phase 2.
func truncatePushTables(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE push_subscriptions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate push_subscriptions: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE push_user_settings`); err != nil {
		t.Fatalf("truncate push_user_settings: %v", err)
	}
}

func TestModeDefaultsToBlockedAndRoundTrips(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// No push_user_settings row for the user yet: the default, not an error.
	got, err := store.Mode(ctx, userA)
	if err != nil {
		t.Fatalf("Mode (default): %v", err)
	}
	if got != push.ModeBlocked {
		t.Errorf("default mode = %q, want %q", got, push.ModeBlocked)
	}

	if err := store.SetMode(ctx, userA, push.ModeAll); err != nil {
		t.Fatalf("SetMode all: %v", err)
	}
	got, err = store.Mode(ctx, userA)
	if err != nil {
		t.Fatalf("Mode (after set): %v", err)
	}
	if got != push.ModeAll {
		t.Errorf("mode after SetMode(all) = %q, want %q", got, push.ModeAll)
	}

	// SetMode upserts: writing again for the same user updates the row.
	if err := store.SetMode(ctx, userA, push.ModeBlocked); err != nil {
		t.Fatalf("SetMode blocked: %v", err)
	}
	got, err = store.Mode(ctx, userA)
	if err != nil {
		t.Fatalf("Mode (after second set): %v", err)
	}
	if got != push.ModeBlocked {
		t.Errorf("mode after SetMode(blocked) = %q, want %q", got, push.ModeBlocked)
	}
}

func TestModeIsPerUser(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.SetMode(ctx, userA, push.ModeAll); err != nil {
		t.Fatalf("SetMode userA: %v", err)
	}

	// User B never set a mode: still the default, unaffected by user A.
	got, err := store.Mode(ctx, userB)
	if err != nil {
		t.Fatalf("Mode userB: %v", err)
	}
	if got != push.ModeBlocked {
		t.Errorf("userB mode = %q, want default %q", got, push.ModeBlocked)
	}
	got, err = store.Mode(ctx, userA)
	if err != nil {
		t.Fatalf("Mode userA: %v", err)
	}
	if got != push.ModeAll {
		t.Errorf("userA mode = %q, want %q", got, push.ModeAll)
	}
}

func sub(endpoint string) push.Subscription {
	return push.Subscription{Endpoint: endpoint, P256dh: "pub-" + endpoint, Auth: "auth-" + endpoint}
}

func TestSaveThenList(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, userA, sub("https://push.example/a")); err != nil {
		t.Fatalf("Save a: %v", err)
	}
	if err := store.Save(ctx, userA, sub("https://push.example/b")); err != nil {
		t.Fatalf("Save b: %v", err)
	}

	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d subscriptions, want 2", len(got))
	}
	if got[0].ID == 0 || got[0].CreatedAt.IsZero() {
		t.Errorf("store did not assign ID/CreatedAt: %+v", got[0])
	}
}

func TestListIsolatesUsers(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, userA, sub("https://push.example/a")); err != nil {
		t.Fatalf("Save userA: %v", err)
	}
	if err := store.Save(ctx, userB, sub("https://push.example/b")); err != nil {
		t.Fatalf("Save userB: %v", err)
	}

	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA: %v", err)
	}
	if len(got) != 1 || got[0].Endpoint != "https://push.example/a" {
		t.Fatalf("List(userA) = %+v, want only userA's subscription", got)
	}
	got, err = store.List(ctx, userB)
	if err != nil {
		t.Fatalf("List userB: %v", err)
	}
	if len(got) != 1 || got[0].Endpoint != "https://push.example/b" {
		t.Fatalf("List(userB) = %+v, want only userB's subscription", got)
	}
}

func TestSaveUpsertsOnEndpoint(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, userA, push.Subscription{Endpoint: "https://push.example/x", P256dh: "old", Auth: "old"}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	// Re-subscribe from the same browser with rotated keys — must update, not duplicate.
	if err := store.Save(ctx, userA, push.Subscription{Endpoint: "https://push.example/x", P256dh: "new", Auth: "new"}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("upsert produced %d rows, want 1", len(got))
	}
	if got[0].P256dh != "new" || got[0].Auth != "new" {
		t.Errorf("upsert did not refresh keys: %+v", got[0])
	}
}

func TestSaveUpsertReassignsUser(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// The same browser endpoint re-subscribing under a different signed-in user
	// moves to that user (endpoint is globally unique; last write wins).
	if err := store.Save(ctx, userA, sub("https://push.example/shared")); err != nil {
		t.Fatalf("Save as userA: %v", err)
	}
	if err := store.Save(ctx, userB, sub("https://push.example/shared")); err != nil {
		t.Fatalf("Save as userB: %v", err)
	}

	gotA, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA: %v", err)
	}
	if len(gotA) != 0 {
		t.Fatalf("List(userA) = %+v, want empty after endpoint moved to userB", gotA)
	}
	gotB, err := store.List(ctx, userB)
	if err != nil {
		t.Fatalf("List userB: %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("List(userB) returned %d rows, want 1", len(gotB))
	}
}

func TestDeleteByEndpoint(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, userA, sub("https://push.example/gone")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.DeleteByEndpoint(ctx, "https://push.example/gone"); err != nil {
		t.Fatalf("DeleteByEndpoint: %v", err)
	}
	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List returned %d after delete, want 0", len(got))
	}
	// Deleting an absent endpoint is a no-op, not an error.
	if err := store.DeleteByEndpoint(ctx, "https://push.example/never"); err != nil {
		t.Errorf("DeleteByEndpoint on absent row: %v", err)
	}
}

func TestDeleteUserEndpointIsOwnerScoped(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if err := store.Save(ctx, userA, sub("https://push.example/a")); err != nil {
		t.Fatalf("Save userA: %v", err)
	}
	if err := store.Save(ctx, userB, sub("https://push.example/b")); err != nil {
		t.Fatalf("Save userB: %v", err)
	}

	// userB cannot delete userA's endpoint: scoped by owner, so this is a no-op.
	if err := store.DeleteUserEndpoint(ctx, userB, "https://push.example/a"); err != nil {
		t.Fatalf("DeleteUserEndpoint (cross-user): %v", err)
	}
	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("userA lost a subscription to a cross-user delete: %+v", got)
	}

	// The owner deleting their own endpoint drops exactly that row.
	if err := store.DeleteUserEndpoint(ctx, userA, "https://push.example/a"); err != nil {
		t.Fatalf("DeleteUserEndpoint (owner): %v", err)
	}
	got, err = store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA after owner delete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("owner delete left %d rows, want 0", len(got))
	}
	// Deleting an absent endpoint is a no-op, not an error.
	if err := store.DeleteUserEndpoint(ctx, userA, "https://push.example/never"); err != nil {
		t.Errorf("DeleteUserEndpoint on absent row: %v", err)
	}
}

// TestTouchForegroundStampsAndClears exercises the presence-lease write side
// (02 §10 push dedup): a fresh subscription has a nil lease, visible=true stamps
// it (server clock, reflected in List), and visible=false clears it back to nil.
func TestTouchForegroundStampsAndClears(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	const endpoint = "https://push.example/device"
	if err := store.Save(ctx, userA, sub(endpoint)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A device that has never reported presence carries a nil lease → send.
	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List (initial): %v", err)
	}
	if len(got) != 1 || got[0].LastSeenForegroundAt != nil {
		t.Fatalf("fresh subscription has non-nil lease: %+v", got)
	}

	// visible=true stamps the server clock.
	if err := store.TouchForeground(ctx, userA, endpoint, true); err != nil {
		t.Fatalf("TouchForeground(true): %v", err)
	}
	got, err = store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List (after stamp): %v", err)
	}
	if len(got) != 1 || got[0].LastSeenForegroundAt == nil {
		t.Fatalf("visible=true did not stamp the lease: %+v", got)
	}

	// visible=false clears it back to nil (the clean leave-beacon path).
	if err := store.TouchForeground(ctx, userA, endpoint, false); err != nil {
		t.Fatalf("TouchForeground(false): %v", err)
	}
	got, err = store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List (after clear): %v", err)
	}
	if len(got) != 1 || got[0].LastSeenForegroundAt != nil {
		t.Fatalf("visible=false did not clear the lease: %+v", got)
	}
}

// TestTouchForegroundIsOwnerScoped: a presence report for an endpoint owned by
// another user (or unknown) is a no-op — it stamps nothing and errors nothing,
// so a racing/foreign beacon can never affect a device it doesn't own (fail-open).
func TestTouchForegroundIsOwnerScoped(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	const endpoint = "https://push.example/owned-by-a"
	if err := store.Save(ctx, userA, sub(endpoint)); err != nil {
		t.Fatalf("Save userA: %v", err)
	}

	// userB tries to stamp userA's device: scoped by user_id, so it changes nothing.
	if err := store.TouchForeground(ctx, userB, endpoint, true); err != nil {
		t.Fatalf("TouchForeground (cross-user): %v", err)
	}
	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA: %v", err)
	}
	if len(got) != 1 || got[0].LastSeenForegroundAt != nil {
		t.Fatalf("cross-user TouchForeground stamped userA's device: %+v", got)
	}

	// An unknown endpoint is likewise a no-op, not an error.
	if err := store.TouchForeground(ctx, userA, "https://push.example/never", true); err != nil {
		t.Errorf("TouchForeground on absent endpoint: %v", err)
	}
}

// TestDeleteUserEndpointLeavesOtherDevices is the reported cross-device scenario:
// one user with two devices (Phone A and Phone B, both under the single v1 user).
// Disabling notifications on Phone B deletes only Phone B's endpoint — Phone A's
// subscription must survive, so its notifications are never "reset" by activity
// on another device.
func TestDeleteUserEndpointLeavesOtherDevices(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	const phoneA = "https://push.example/phone-a"
	const phoneB = "https://push.example/phone-b"
	if err := store.Save(ctx, userA, sub(phoneA)); err != nil {
		t.Fatalf("Save phoneA: %v", err)
	}
	if err := store.Save(ctx, userA, sub(phoneB)); err != nil {
		t.Fatalf("Save phoneB: %v", err)
	}

	// Phone B disables notifications: it removes its own endpoint only.
	if err := store.DeleteUserEndpoint(ctx, userA, phoneB); err != nil {
		t.Fatalf("DeleteUserEndpoint phoneB: %v", err)
	}

	got, err := store.List(ctx, userA)
	if err != nil {
		t.Fatalf("List userA: %v", err)
	}
	if len(got) != 1 || got[0].Endpoint != phoneA {
		t.Fatalf("after disabling phoneB, List(userA) = %+v, want only phoneA", got)
	}
}
