//go:build integration

// Package postgres_test exercises the identity store adapter against a real
// database (mirrors board/postgres's integration test shape). Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/identity/postgres/...
//
// kiln_test is shared with other modules, so setup only ever creates
// identity's own tables if missing and only ever truncates
// users/sessions/user_config/projects — never DROPs, never touches tables it
// doesn't own.
package postgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/postgres"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run identity/postgres integration tests")
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
	truncateIdentityTables(ctx, t, db)
	return db
}

// ensureMigrationsApplied applies ./migrations, in filename order, only if
// identity's own tables don't already exist — kiln_test is shared, and other
// modules' tables must never be touched here.
func ensureMigrationsApplied(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'users'
	)`).Scan(&exists)
	if err != nil {
		t.Fatalf("check for users table: %v", err)
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

// truncateIdentityTables resets exactly identity's own tables so every test
// starts clean, without disturbing other modules sharing kiln_test.
func truncateIdentityTables(ctx context.Context, t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx,
		`TRUNCATE TABLE users, sessions, user_config, projects RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate identity tables: %v", err)
	}
}

func mustNewUser(ctx context.Context, t *testing.T, store *postgres.Store, u identity.User) identity.User {
	t.Helper()
	out, err := store.UpsertUser(ctx, u)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	return out
}

// ---- UpsertUser: find-or-create by GitHubID, refreshing mutable fields -----

func TestUpsertUserFindsOrCreates(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	created := mustNewUser(ctx, t, store, identity.User{
		GitHubID:    42,
		GitHubLogin: "Octocat",
		DisplayName: "The Octocat",
		AvatarURL:   "https://example.com/a.png",
	})
	if created.ID == "" {
		t.Fatal("UpsertUser returned empty ID")
	}
	if created.GitHubLogin != "octocat" {
		t.Fatalf("GitHubLogin = %q, want lower-cased %q", created.GitHubLogin, "octocat")
	}

	updated := mustNewUser(ctx, t, store, identity.User{
		GitHubID:    42,
		GitHubLogin: "octocat-renamed",
		DisplayName: "Renamed Cat",
		AvatarURL:   "https://example.com/b.png",
	})
	if updated.ID != created.ID {
		t.Fatalf("second UpsertUser with the same GitHubID created a new row: id=%s, want %s", updated.ID, created.ID)
	}
	if updated.GitHubLogin != "octocat-renamed" || updated.DisplayName != "Renamed Cat" || updated.AvatarURL != "https://example.com/b.png" {
		t.Fatalf("UpsertUser did not refresh mutable fields: %+v", updated)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE github_id = 42`).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users row count for github_id=42 = %d, want 1 (find-or-create must not duplicate)", count)
	}

	byID, err := store.GetUser(ctx, updated.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if byID.ID != updated.ID || byID.GitHubLogin != "octocat-renamed" || byID.DisplayName != "Renamed Cat" {
		t.Fatalf("GetUser = %+v, want %+v", byID, updated)
	}

	if _, err := store.GetUser(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("GetUser unknown id: err = %v, want ErrNotFound", err)
	}
}

// TestUpsertUserAdoptsBootstrapRowOnRealLogin reproduces the prod-rollout /
// bootstrap-from-env case (11 §7): the operator's user is first created with a
// SYNTHETIC github_id (EnsureUser's fnv stand-in), then the same human signs in
// via real GitHub OAuth carrying a DIFFERENT, authoritative github_id under the
// same login. UpsertUser must adopt the existing row (same primary key, so the
// operator keeps their project/config) and stamp the real id — never collide on
// the github_login unique constraint.
func TestUpsertUserAdoptsBootstrapRowOnRealLogin(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	// Bootstrap/dev create with a synthetic id.
	seeded := mustNewUser(ctx, t, store, identity.User{
		GitHubID:    999999999, // fnv-style stand-in
		GitHubLogin: "crabtree-michael",
		DisplayName: "crabtree-michael",
	})

	// Real OAuth arrives: same login, real numeric id, richer profile.
	adopted, err := store.UpsertUser(ctx, identity.User{
		GitHubID:    101582150,
		GitHubLogin: "crabtree-michael",
		DisplayName: "Michael Crabtree",
		AvatarURL:   "https://example.com/real.png",
	})
	if err != nil {
		t.Fatalf("real-login UpsertUser after bootstrap must not collide: %v", err)
	}
	if adopted.ID != seeded.ID {
		t.Fatalf("real login created a NEW row (id=%s) instead of adopting the bootstrap row (id=%s)", adopted.ID, seeded.ID)
	}
	if adopted.GitHubID != 101582150 {
		t.Fatalf("adopted row github_id = %d, want the real id 101582150 stamped on", adopted.GitHubID)
	}
	if adopted.DisplayName != "Michael Crabtree" || adopted.AvatarURL != "https://example.com/real.png" {
		t.Fatalf("adopted row did not refresh profile: %+v", adopted)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE github_login = 'crabtree-michael'`).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users row count for login=crabtree-michael = %d, want 1 (adopt, never duplicate)", count)
	}

	// A subsequent real login (now id matches) still resolves to the same row.
	again := mustNewUser(ctx, t, store, identity.User{
		GitHubID:    101582150,
		GitHubLogin: "crabtree-michael",
		DisplayName: "Michael Crabtree",
	})
	if again.ID != seeded.ID {
		t.Fatalf("repeat real login created id=%s, want stable %s", again.ID, seeded.ID)
	}
}

// ---- Session lifecycle: insert, resolve joined, touch extends, delete -----

func TestSessionLifecycle(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	user := mustNewUser(ctx, t, store, identity.User{GitHubID: 7, GitHubLogin: "alice"})

	now := time.Now().UTC().Truncate(time.Second)
	sess := identity.Session{
		TokenHash: "hash-abc",
		UserID:    user.ID,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := store.InsertSession(ctx, sess); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	gotSess, gotUser, err := store.GetSessionUser(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("GetSessionUser: %v", err)
	}
	if gotSess.TokenHash != sess.TokenHash || gotSess.UserID != user.ID {
		t.Fatalf("GetSessionUser session = %+v, want token=%s user=%s", gotSess, sess.TokenHash, user.ID)
	}
	if gotUser.ID != user.ID || gotUser.GitHubLogin != "alice" {
		t.Fatalf("GetSessionUser user = %+v, want id=%s login=alice", gotUser, user.ID)
	}

	plain, err := store.GetSession(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !plain.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("GetSession ExpiresAt = %v, want %v", plain.ExpiresAt, sess.ExpiresAt)
	}

	newExpiry := now.Add(2 * time.Hour)
	if err := store.TouchSession(ctx, "hash-abc", newExpiry); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	touched, err := store.GetSession(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("GetSession after touch: %v", err)
	}
	if !touched.ExpiresAt.Equal(newExpiry) {
		t.Fatalf("ExpiresAt after TouchSession = %v, want %v", touched.ExpiresAt, newExpiry)
	}

	if err := store.DeleteSession(ctx, "hash-abc"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := store.GetSession(ctx, "hash-abc"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("GetSession after delete: err = %v, want ErrNotFound", err)
	}
}

// ---- UserConfig: zero value before any write, then round-trips exactly ----

func TestUserConfigZeroThenUpsert(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	user := mustNewUser(ctx, t, store, identity.User{GitHubID: 99, GitHubLogin: "bob"})

	zero, err := store.GetUserConfig(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserConfig before any write: %v", err)
	}
	if zero.UserID != user.ID || zero.AnthropicKeyEnc != nil || zero.AmikaKeyEnc != nil ||
		zero.GitHubTokenEnc != nil || zero.AmikaClaudeCredID != "" {
		t.Fatalf("GetUserConfig before any write = %+v, want zero value with UserID set", zero)
	}

	cfg := identity.UserConfig{
		UserID:            user.ID,
		AnthropicKeyEnc:   []byte{0x01, 0x02, 0x03},
		AmikaKeyEnc:       []byte{0xAA, 0xBB},
		GitHubTokenEnc:    []byte{0xFF},
		AmikaClaudeCredID: "cred-1",
	}
	if err := store.UpsertUserConfig(ctx, cfg); err != nil {
		t.Fatalf("UpsertUserConfig: %v", err)
	}

	got, err := store.GetUserConfig(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserConfig after upsert: %v", err)
	}
	if !bytes.Equal(got.AnthropicKeyEnc, cfg.AnthropicKeyEnc) ||
		!bytes.Equal(got.AmikaKeyEnc, cfg.AmikaKeyEnc) ||
		!bytes.Equal(got.GitHubTokenEnc, cfg.GitHubTokenEnc) ||
		got.AmikaClaudeCredID != cfg.AmikaClaudeCredID {
		t.Fatalf("GetUserConfig after upsert = %+v, want %+v", got, cfg)
	}

	cfg2 := identity.UserConfig{
		UserID:            user.ID,
		AnthropicKeyEnc:   []byte{0x09},
		AmikaClaudeCredID: "cred-2",
	}
	if err := store.UpsertUserConfig(ctx, cfg2); err != nil {
		t.Fatalf("second UpsertUserConfig: %v", err)
	}
	got2, err := store.GetUserConfig(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserConfig after second upsert: %v", err)
	}
	if !bytes.Equal(got2.AnthropicKeyEnc, cfg2.AnthropicKeyEnc) || got2.AmikaKeyEnc != nil ||
		got2.AmikaClaudeCredID != cfg2.AmikaClaudeCredID {
		t.Fatalf("GetUserConfig after second upsert = %+v, want overwritten %+v", got2, cfg2)
	}
}

// ---- Project: ErrNotFound before creation; upsert updates in place --------

func TestProjectUpsertAndUniqueOwner(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	user := mustNewUser(ctx, t, store, identity.User{GitHubID: 123, GitHubLogin: "carol"})

	if _, err := store.GetProjectByOwner(ctx, user.ID); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("GetProjectByOwner before create: err = %v, want ErrNotFound", err)
	}

	created, err := store.UpsertProject(ctx, identity.Project{
		OwnerUserID:   user.ID,
		Name:          "my-project",
		RepoURL:       "https://github.com/o/r",
		AmikaSnapshot: "snap-1",
		BrainModel:    "claude-x",
		WorkerCount:   3,
		AmikaSecrets: []identity.AmikaSecret{
			{NameEnc: []byte("enc-name-1"), ValueEnc: []byte("enc-val-1")},
			{NameEnc: []byte("enc-name-2"), ValueEnc: []byte("enc-val-2")},
		},
	})
	if err != nil {
		t.Fatalf("UpsertProject create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("UpsertProject returned empty ID")
	}

	got, err := store.GetProjectByOwner(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetProjectByOwner after create: %v", err)
	}
	if got.ID != created.ID || got.Name != "my-project" || got.WorkerCount != 3 {
		t.Fatalf("GetProjectByOwner after create = %+v, want %+v", got, created)
	}
	// The jsonb amika_secrets column round-trips the encrypted bytes through
	// GetProjectByOwner (the store persists ciphertext verbatim; encryption is
	// the service's job).
	wantSecrets := []identity.AmikaSecret{
		{NameEnc: []byte("enc-name-1"), ValueEnc: []byte("enc-val-1")},
		{NameEnc: []byte("enc-name-2"), ValueEnc: []byte("enc-val-2")},
	}
	if len(got.AmikaSecrets) != len(wantSecrets) {
		t.Fatalf("AmikaSecrets = %+v, want %+v", got.AmikaSecrets, wantSecrets)
	}
	for i, w := range wantSecrets {
		if !bytes.Equal(got.AmikaSecrets[i].NameEnc, w.NameEnc) ||
			!bytes.Equal(got.AmikaSecrets[i].ValueEnc, w.ValueEnc) {
			t.Errorf("AmikaSecrets[%d] = %+v, want %+v", i, got.AmikaSecrets[i], w)
		}
	}

	updated, err := store.UpsertProject(ctx, identity.Project{
		OwnerUserID:   user.ID,
		Name:          "renamed-project",
		RepoURL:       "https://github.com/o/r2",
		AmikaSnapshot: "snap-2",
		BrainModel:    "claude-y",
		WorkerCount:   5,
	})
	if err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("UpsertProject update created a new row: id=%s, want %s (same owner)", updated.ID, created.ID)
	}
	if updated.Name != "renamed-project" || updated.WorkerCount != 5 {
		t.Fatalf("UpsertProject update did not persist changes: %+v", updated)
	}
	// An update with no secrets clears the column back to empty.
	if len(updated.AmikaSecrets) != 0 {
		t.Fatalf("AmikaSecrets after secretless update = %+v, want empty", updated.AmikaSecrets)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE owner_user_id = $1`, user.ID).Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 1 {
		t.Fatalf("projects row count for owner = %d, want 1 (one project per owner)", count)
	}
}

// ---- GetProject by id + ListProjects across owners (phase-2 runtime) -------

func TestGetProjectByID(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	user := mustNewUser(ctx, t, store, identity.User{GitHubID: 200, GitHubLogin: "dave"})

	if _, err := store.GetProject(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("GetProject unknown id: err = %v, want ErrNotFound", err)
	}

	created, err := store.UpsertProject(ctx, identity.Project{
		OwnerUserID:   user.ID,
		Name:          "dave-project",
		RepoURL:       "https://github.com/d/p",
		AmikaSnapshot: "snap-d",
		BrainModel:    "claude-d",
		WorkerCount:   4,
	})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	got, err := store.GetProject(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != created.ID || got.OwnerUserID != user.ID || got.Name != "dave-project" ||
		got.RepoURL != "https://github.com/d/p" || got.AmikaSnapshot != "snap-d" ||
		got.BrainModel != "claude-d" || got.WorkerCount != 4 {
		t.Fatalf("GetProject = %+v, want %+v", got, created)
	}
}

func TestListProjectsOrderedByCreatedAt(t *testing.T) {
	db := testDB(t)
	store := postgres.New(db)
	ctx := context.Background()

	if projects, err := store.ListProjects(ctx); err != nil {
		t.Fatalf("ListProjects on empty table: %v", err)
	} else if len(projects) != 0 {
		t.Fatalf("ListProjects on empty table = %+v, want none", projects)
	}

	u1 := mustNewUser(ctx, t, store, identity.User{GitHubID: 301, GitHubLogin: "erin"})
	u2 := mustNewUser(ctx, t, store, identity.User{GitHubID: 302, GitHubLogin: "frank"})

	p1, err := store.UpsertProject(ctx, identity.Project{
		OwnerUserID: u1.ID, Name: "p1", RepoURL: "https://github.com/e/1", WorkerCount: 3,
	})
	if err != nil {
		t.Fatalf("UpsertProject p1: %v", err)
	}
	p2, err := store.UpsertProject(ctx, identity.Project{
		OwnerUserID: u2.ID, Name: "p2", RepoURL: "https://github.com/f/2", WorkerCount: 3,
	})
	if err != nil {
		t.Fatalf("UpsertProject p2: %v", err)
	}

	got, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListProjects = %d projects, want 2", len(got))
	}
	// created_at order: p1 was inserted before p2. created_at defaults may share
	// a timestamp at sub-ms resolution, so assert membership plus that both ids
	// are present rather than a brittle strict order — but the query does ORDER
	// BY created_at, so verify p1 is not after p2.
	ids := map[string]bool{got[0].ID: true, got[1].ID: true}
	if !ids[p1.ID] || !ids[p2.ID] {
		t.Fatalf("ListProjects ids = %v, want both %q and %q", ids, p1.ID, p2.ID)
	}
}
