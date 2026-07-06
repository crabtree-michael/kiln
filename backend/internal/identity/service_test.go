package identity_test

// Unit tests for the login/session half of identity.Service (11 §2). These
// exercise Service directly against fakeStore/fakeGitHub (fakes_test.go) —
// no real Postgres or GitHub involved.

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// minTokenLen is the lower bound service_test.go asserts on CreateSession's
// raw token (32 random bytes, base64url-encoded, comes out to 43 chars).
const minTokenLen = 40

// testGHToken is a placeholder GitHub access token shared by tests that don't
// care about its value, only that fakeGitHub echoes it back to FetchUser.
const testGHToken = "gh-access-token"

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeClock is a manually-advanced clock for sliding-session tests.
type fakeClock struct {
	mu  sync.Mutex
	cur time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{cur: start}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur = c.cur.Add(d)
}

func TestCompleteLoginAllowlisted(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token: "gh-token",
		user:  githubapi.GitHubUser{ID: 42, Login: "Crabtree-Michael", Name: "Michael Crabtree"},
	}
	svc := identity.NewService(store, mustCipher(t), gh, []string{"crabtree-michael"})

	u, err := svc.CompleteLogin(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if u.GitHubLogin != "crabtree-michael" {
		t.Fatalf("GitHubLogin = %q, want crabtree-michael", u.GitHubLogin)
	}
	if gh.gotCode != "code-1" {
		t.Fatalf("ExchangeCode got code %q, want code-1", gh.gotCode)
	}
	if gh.gotToken != "gh-token" {
		t.Fatalf("FetchUser got token %q, want gh-token", gh.gotToken)
	}

	u2, err := svc.CompleteLogin(context.Background(), "code-2")
	if err != nil {
		t.Fatalf("CompleteLogin second: %v", err)
	}
	if u2.ID != u.ID {
		t.Fatalf("second login user ID = %q, want %q (find-or-create)", u2.ID, u.ID)
	}
}

func TestCompleteLoginNotAllowlisted(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 7, Login: "nobody"}}
	svc := identity.NewService(store, mustCipher(t), gh, []string{"someone-else"})

	_, err := svc.CompleteLogin(context.Background(), "code")
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
	if n := store.userCount(); n != 0 {
		t.Fatalf("store has %d users, want 0", n)
	}
}

func TestCompleteLoginEmptyAllowlist(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 7, Login: "anybody"}}
	svc := identity.NewService(store, mustCipher(t), gh, nil)

	_, err := svc.CompleteLogin(context.Background(), "code")
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
}

func TestAllowlistCheckedOnEveryLogin(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 9, Login: "temp-user"}}
	svc := identity.NewService(store, mustCipher(t), gh, []string{"temp-user"})

	if _, err := svc.CompleteLogin(context.Background(), "code-1"); err != nil {
		t.Fatalf("first login: %v", err)
	}

	// Rebuild the service with the login removed from the allowlist — the
	// allowlist must be re-checked on every login, not just at signup.
	svc2 := identity.NewService(store, mustCipher(t), gh, []string{})
	_, err := svc2.CompleteLogin(context.Background(), "code-2")
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	clock := newFakeClock(baseTime)
	identity.SetClockForTest(svc, clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 1, GitHubLogin: "u1"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	token, expiresAt, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(token) < minTokenLen {
		t.Fatalf("token len = %d, want >= %d", len(token), minTokenLen)
	}
	wantExpiry := baseTime.Add(30 * 24 * time.Hour)
	if !expiresAt.Equal(wantExpiry) {
		t.Fatalf("expiresAt = %v, want %v", expiresAt, wantExpiry)
	}

	got, gotExpiry, err := svc.ResolveSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("resolved user = %q, want %q", got.ID, u.ID)
	}
	if !gotExpiry.Equal(expiresAt) {
		t.Fatalf("ResolveSession expiry = %v, want unchanged %v (well inside renew-below)", gotExpiry, expiresAt)
	}

	for _, sess := range store.allSessions() {
		if sess.TokenHash == token {
			t.Fatal("store holds the raw token, not just its hash")
		}
	}
}

func TestResolveSessionExpired(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	clock := newFakeClock(baseTime)
	identity.SetClockForTest(svc, clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 2, GitHubLogin: "u2"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	clock.advance(31 * 24 * time.Hour) // beyond the 30d TTL

	_, _, err = svc.ResolveSession(context.Background(), token)
	if !errors.Is(err, identity.ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession", err)
	}
	if sessions := store.allSessions(); len(sessions) != 0 {
		t.Fatalf("expired session was not deleted from store: %v", sessions)
	}
}

func TestResolveSessionSlides(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	clock := newFakeClock(baseTime)
	identity.SetClockForTest(svc, clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 3, GitHubLogin: "u3"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	clock.advance(20 * 24 * time.Hour) // remaining 10d < the 15d renew-below threshold

	_, gotExpiry, err := svc.ResolveSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}

	sessions := store.allSessions()
	if len(sessions) != 1 {
		t.Fatalf("store has %d sessions, want 1", len(sessions))
	}
	wantExpiry := clock.now().Add(30 * 24 * time.Hour)
	if !sessions[0].ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v (slid forward)", sessions[0].ExpiresAt, wantExpiry)
	}
	if !gotExpiry.Equal(wantExpiry) {
		t.Fatalf("ResolveSession returned expiry = %v, want slid %v", gotExpiry, wantExpiry)
	}
}

// TestResolveSessionFreshDoesNotRenew is the negative-renewal case (final
// review deferred finding #5): resolving IMMEDIATELY after CreateSession —
// well outside the 15d renew-below threshold — must NOT touch the session
// row or change its expiry. Without this, a bug that always renews would
// slide every request and this file's positive slide test (above) couldn't
// tell the difference.
func TestResolveSessionFreshDoesNotRenew(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	clock := newFakeClock(baseTime)
	identity.SetClockForTest(svc, clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 30, GitHubLogin: "u30"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, createdExpiry, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, gotExpiry, err := svc.ResolveSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if !gotExpiry.Equal(createdExpiry) {
		t.Fatalf("ResolveSession expiry = %v, want unchanged %v (no renewal this soon)", gotExpiry, createdExpiry)
	}
	if n := store.touchSessionCallCount(); n != 0 {
		t.Fatalf("TouchSession called %d times, want 0 for a fresh session", n)
	}

	sessions := store.allSessions()
	if len(sessions) != 1 || !sessions[0].ExpiresAt.Equal(createdExpiry) {
		t.Fatalf("stored session expiry = %+v, want unchanged %v", sessions, createdExpiry)
	}
}

func TestResolveSessionUnknownOrEmpty(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)

	for _, token := range []string{"", "nope"} {
		if _, _, err := svc.ResolveSession(context.Background(), token); !errors.Is(err, identity.ErrNoSession) {
			t.Fatalf("ResolveSession(%q) err = %v, want ErrNoSession", token, err)
		}
	}
}

func TestLogout(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 4, GitHubLogin: "u4"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := svc.ResolveSession(context.Background(), token); !errors.Is(err, identity.ErrNoSession) {
		t.Fatalf("ResolveSession after logout = %v, want ErrNoSession", err)
	}

	if err := svc.Logout(context.Background(), "unknown-token"); err != nil {
		t.Fatalf("Logout of unknown token must be idempotent-nil, got %v", err)
	}
}

func TestDevSignInBypassesAllowlist(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil) // nil allowlist would reject a real login

	u, err := svc.DevSignIn(context.Background(), "E2E-User")
	if err != nil {
		t.Fatalf("DevSignIn: %v", err)
	}
	if u.GitHubLogin != "e2e-user" {
		t.Fatalf("GitHubLogin = %q, want e2e-user", u.GitHubLogin)
	}
	if u.ID == "" {
		t.Fatal("expected a created user id")
	}

	u2, err := svc.DevSignIn(context.Background(), "e2e-user")
	if err != nil {
		t.Fatalf("DevSignIn second: %v", err)
	}
	if u2.ID != u.ID {
		t.Fatalf("second DevSignIn user ID = %q, want %q (find-or-create)", u2.ID, u.ID)
	}
}

// ---- Me / UpdateSettings / UpsertProject (11 §3-§4) ------------------------

// testProjectName and testProjectRepoURL are shared across the UpsertProject
// tests below (goconst).
const (
	testProjectName    = "kiln"
	testProjectRepoURL = "https://github.com/x/y"
)

func mustDevSignIn(t *testing.T, svc *identity.Service, login string) identity.User {
	t.Helper()
	u, err := svc.DevSignIn(context.Background(), login)
	if err != nil {
		t.Fatalf("DevSignIn: %v", err)
	}
	return u
}

func TestMeEmpty(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "fresh-user")

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Project != nil {
		t.Fatalf("Project = %+v, want nil before onboarding", me.Project)
	}
	for name, got := range map[string]identity.SecretStatus{
		"AnthropicKey": me.Settings.AnthropicKey,
		"AmikaKey":     me.Settings.AmikaKey,
		"GitHubToken":  me.Settings.GitHubToken,
	} {
		if got != (identity.SecretStatus{}) {
			t.Fatalf("%s = %+v, want zero-value SecretStatus", name, got)
		}
	}
	if me.Settings.AmikaBaseURL != "" || me.Settings.AmikaClaudeCredID != "" {
		t.Fatalf("clear fields not empty: %+v", me.Settings)
	}
}

func TestUpdateSettingsWriteAndStatus(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "settings-user")

	err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "sk-ant-abcx4Kd",
		AmikaBaseURL: "https://api.amika.dev",
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if want := (identity.SecretStatus{Set: true, Tail: "x4Kd"}); me.Settings.AnthropicKey != want {
		t.Fatalf("AnthropicKey = %+v, want %+v", me.Settings.AnthropicKey, want)
	}
	if me.Settings.AmikaBaseURL != "https://api.amika.dev" {
		t.Fatalf("AmikaBaseURL = %q, want round-tripped clear value", me.Settings.AmikaBaseURL)
	}
	if me.Settings.AmikaKey != (identity.SecretStatus{}) {
		t.Fatalf("AmikaKey = %+v, want unset", me.Settings.AmikaKey)
	}

	cfg, ok := store.configs[u.ID]
	if !ok {
		t.Fatal("no config row stored")
	}
	if bytes.Contains(cfg.AnthropicKeyEnc, []byte("sk-ant-abcx4Kd")) {
		t.Fatal("stored bytes contain the plaintext secret — must be encrypted")
	}
}

func TestUpdateSettingsPartialMerge(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "partial-user")

	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "sk-ant-firstWXYZ",
	}); err != nil {
		t.Fatalf("UpdateSettings first: %v", err)
	}
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AmikaKey: "amk-999zZ",
	}); err != nil {
		t.Fatalf("UpdateSettings second: %v", err)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if want := (identity.SecretStatus{Set: true, Tail: "WXYZ"}); me.Settings.AnthropicKey != want {
		t.Fatalf("AnthropicKey = %+v, want unchanged %+v", me.Settings.AnthropicKey, want)
	}
	if want := (identity.SecretStatus{Set: true, Tail: "99zZ"}); me.Settings.AmikaKey != want {
		t.Fatalf("AmikaKey = %+v, want %+v", me.Settings.AmikaKey, want)
	}
}

func TestUpdateSettingsOverwrite(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "overwrite-user")

	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "aaaa1111",
	}); err != nil {
		t.Fatalf("UpdateSettings first: %v", err)
	}
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "bbbb2222",
	}); err != nil {
		t.Fatalf("UpdateSettings overwrite: %v", err)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if want := (identity.SecretStatus{Set: true, Tail: "2222"}); me.Settings.AnthropicKey != want {
		t.Fatalf("AnthropicKey = %+v, want %+v", me.Settings.AnthropicKey, want)
	}
}

func TestUpsertProjectCreatesThenUpdates(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "project-user")

	created, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
	})
	if err != nil {
		t.Fatalf("UpsertProject create: %v", err)
	}
	if created.WorkerCount != 3 {
		t.Fatalf("WorkerCount = %d, want defaulted 3", created.WorkerCount)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Project == nil {
		t.Fatal("Project = nil, want non-nil after UpsertProject")
	}

	updated, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:        testProjectName,
		RepoURL:     testProjectRepoURL,
		BrainModel:  "claude-haiku-4-5-20251001",
		WorkerCount: 5,
	})
	if err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("second UpsertProject id = %q, want %q (same project)", updated.ID, created.ID)
	}
	if updated.BrainModel != "claude-haiku-4-5-20251001" || updated.WorkerCount != 5 {
		t.Fatalf("UpsertProject update did not persist fields: %+v", updated)
	}
}

// ---- Verify (11 §4) ---------------------------------------------------

// wantSkippedStatus/wantSkipped are the CheckResult fields every unconfigured
// credential group reports.
const (
	wantSkippedStatus = "skipped"
	wantSkipped       = "not configured"
)

func TestVerifySkipsUnconfigured(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	svc.SetVerifier(&fakeVerifier{})
	u := mustDevSignIn(t, svc, "verify-fresh-user")

	checks, err := svc.Verify(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := []identity.CheckResult{
		{Name: "anthropic", Status: wantSkippedStatus, Message: wantSkipped},
		{Name: "amika", Status: wantSkippedStatus, Message: wantSkipped},
		{Name: "repo", Status: wantSkippedStatus, Message: wantSkipped},
	}
	if len(checks) != len(want) {
		t.Fatalf("checks = %+v, want %d entries", checks, len(want))
	}
	for i, c := range checks {
		if c != want[i] {
			t.Fatalf("checks[%d] = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestVerifyRunsConfigured(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	verifier := &fakeVerifier{}
	svc.SetVerifier(verifier)
	u := mustDevSignIn(t, svc, "verify-configured-user")

	const anthropicKey = "sk-ant-liveKey1"
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: anthropicKey,
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if _, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	checks, err := svc.Verify(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(checks) != 3 {
		t.Fatalf("checks = %+v, want 3 entries", checks)
	}
	if checks[0].Name != "anthropic" || checks[0].Status != "ok" {
		t.Fatalf("anthropic check = %+v, want {Name:anthropic Status:ok}", checks[0])
	}
	if checks[1].Name != "amika" || checks[1].Status != wantSkippedStatus {
		t.Fatalf("amika check = %+v, want skipped (no amika key set)", checks[1])
	}
	if checks[2].Name != "repo" || checks[2].Status != "ok" {
		t.Fatalf("repo check = %+v, want {Name:repo Status:ok}", checks[2])
	}

	if verifier.gotAnthropicKey != anthropicKey {
		t.Fatalf("verifier got anthropic key %q, want the decrypted %q", verifier.gotAnthropicKey, anthropicKey)
	}
	if verifier.gotRepoURL != testProjectRepoURL {
		t.Fatalf("verifier got repo URL %q, want %q", verifier.gotRepoURL, testProjectRepoURL)
	}
}

func TestUpsertProjectValidates(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "invalid-project-user")

	cases := []identity.ProjectUpdate{
		{Name: "", RepoURL: testProjectRepoURL, WorkerCount: 3},
		{Name: testProjectName, RepoURL: "", WorkerCount: 3},
		{Name: testProjectName, RepoURL: testProjectRepoURL, WorkerCount: 11},
	}
	for _, upd := range cases {
		if _, err := svc.UpsertProject(context.Background(), u.ID, upd); !errors.Is(err, identity.ErrInvalidProject) {
			t.Fatalf("UpsertProject(%+v) err = %v, want ErrInvalidProject", upd, err)
		}
	}
}
