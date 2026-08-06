package identity_test

// Unit tests for the login/session half of identity.Service (11 §2). These
// exercise Service directly against fakeStore/fakeGitHub (fakes_test.go) —
// no real Postgres or GitHub involved.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// repoCheckName is the fixed name of Verify's repo check (11 §4). It is the
// check that probes GitHub reachability — unrelated to any OAuth scope, which
// is a vocabulary the GitHub App migration retired.
const repoCheckName = "repo"

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

func TestCompleteConnectAllowlisted(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token: "gh-token",
		user:  githubapi.GitHubUser{ID: 42, Login: "Crabtree-Michael", Name: "Michael Crabtree"},
	}
	svc := newTestService(t, store, gh, []string{testAllowedLogin})

	u, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if u.GitHubLogin != testAllowedLogin {
		t.Fatalf("GitHubLogin = %q, want %s", u.GitHubLogin, testAllowedLogin)
	}
	if gh.gotCode != "code-1" {
		t.Fatalf("ExchangeCode got code %q, want code-1", gh.gotCode)
	}
	if gh.gotToken != "gh-token" {
		t.Fatalf("FetchUser got token %q, want gh-token", gh.gotToken)
	}

	u2, err := svc.CompleteConnect(context.Background(), "code-2", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect second: %v", err)
	}
	if u2.ID != u.ID {
		t.Fatalf("second login user ID = %q, want %q (find-or-create)", u2.ID, u.ID)
	}
}

func TestCompleteConnectNotAllowlisted(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 7, Login: "nobody"}}
	svc := newTestService(t, store, gh, []string{"someone-else"})

	_, err := svc.CompleteConnect(context.Background(), "code", connectInstallation)
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
	if n := store.userCount(); n != 0 {
		t.Fatalf("store has %d users, want 0", n)
	}
}

func TestCompleteConnectEmptyAllowlist(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 7, Login: "anybody"}}
	svc := newTestService(t, store, gh, nil)

	_, err := svc.CompleteConnect(context.Background(), "code", connectInstallation)
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
}

func TestAllowlistCheckedOnEveryLogin(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 9, Login: "temp-user"}}
	svc := newTestService(t, store, gh, []string{"temp-user"})

	if _, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation); err != nil {
		t.Fatalf("first login: %v", err)
	}

	// Rebuild the service with the login removed from the allowlist — the
	// allowlist must be re-checked on every login, not just at signup.
	svc2 := newTestService(t, store, gh, []string{})
	_, err := svc2.CompleteConnect(context.Background(), "code-2", connectInstallation)
	if !errors.Is(err, identity.ErrNotAllowed) {
		t.Fatalf("err = %v, want ErrNotAllowed", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)

	for _, token := range []string{"", "nope"} {
		if _, _, err := svc.ResolveSession(context.Background(), token); !errors.Is(err, identity.ErrNoSession) {
			t.Fatalf("ResolveSession(%q) err = %v, want ErrNoSession", token, err)
		}
	}
}

func TestLogout(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)

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
	svc := newTestService(t, store, &fakeGitHub{}, nil) // nil allowlist would reject a real login

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
	// testAllowedLogin is the allowlisted GitHub login the CompleteLogin tests
	// share (goconst).
	testAllowedLogin = "crabtree-michael"
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "fresh-user")

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if len(me.Projects) != 0 {
		t.Fatalf("Projects = %+v, want empty before onboarding", me.Projects)
	}
	for name, got := range map[string]identity.SecretStatus{
		"AnthropicKey": me.Settings.AnthropicKey,
		"AmikaKey":     me.Settings.AmikaKey,
		"DevinKey":     me.Settings.DevinKey,
		"GitHubToken":  me.Settings.GitHubToken,
	} {
		if got != (identity.SecretStatus{}) {
			t.Fatalf("%s = %+v, want zero-value SecretStatus", name, got)
		}
	}
	if me.Settings.AmikaClaudeCredID != "" {
		t.Fatalf("clear fields not empty: %+v", me.Settings)
	}
}

func TestUpdateSettingsWriteAndStatus(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "settings-user")

	err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: "sk-ant-abcx4Kd",
		DevinKey:     "cog-secretV1nE",
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
	if want := (identity.SecretStatus{Set: true, Tail: "V1nE"}); me.Settings.DevinKey != want {
		t.Fatalf("DevinKey = %+v, want %+v", me.Settings.DevinKey, want)
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
	if bytes.Contains(cfg.DevinKeyEnc, []byte("cog-secretV1nE")) {
		t.Fatal("stored bytes contain the plaintext devin secret — must be encrypted")
	}
}

func TestUpdateSettingsPartialMerge(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
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
	if len(me.Projects) != 1 {
		t.Fatalf("Projects = %+v, want exactly one after UpsertProject", me.Projects)
	}

	updated, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:        testProjectName,
		RepoURL:     testProjectRepoURL,
		WorkerCount: 5,
	})
	if err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("second UpsertProject id = %q, want %q (same project)", updated.ID, created.ID)
	}
	if updated.WorkerCount != 5 {
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	svc.SetVerifier(&fakeVerifier{})
	u := mustDevSignIn(t, svc, "verify-fresh-user")

	checks, err := svc.Verify(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	want := []identity.CheckResult{
		{Name: "anthropic", Status: wantSkippedStatus, Message: wantSkipped},
		{Name: "amika", Status: wantSkippedStatus, Message: wantSkipped},
		{Name: "devin", Status: wantSkippedStatus, Message: wantSkipped},
		{Name: repoCheckName, Status: wantSkippedStatus, Message: wantSkipped},
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
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	verifier := &fakeVerifier{}
	svc.SetVerifier(verifier)
	u := mustDevSignIn(t, svc, "verify-configured-user")

	const anthropicKey = "sk-ant-liveKey1"
	const devinKey = "cog-liveKey1"
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		AnthropicKey: anthropicKey,
		DevinKey:     devinKey,
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
	if len(checks) != 4 {
		t.Fatalf("checks = %+v, want 4 entries", checks)
	}
	if checks[0].Name != "anthropic" || checks[0].Status != "ok" {
		t.Fatalf("anthropic check = %+v, want {Name:anthropic Status:ok}", checks[0])
	}
	if checks[1].Name != "amika" || checks[1].Status != wantSkippedStatus {
		t.Fatalf("amika check = %+v, want skipped (no amika key set)", checks[1])
	}
	if checks[2].Name != "devin" || checks[2].Status != "ok" {
		t.Fatalf("devin check = %+v, want {Name:devin Status:ok}", checks[2])
	}
	if checks[3].Name != repoCheckName || checks[3].Status != "ok" {
		t.Fatalf("repo check = %+v, want {Name:repo Status:ok}", checks[3])
	}

	if verifier.gotAnthropicKey != anthropicKey {
		t.Fatalf("verifier got anthropic key %q, want the decrypted %q", verifier.gotAnthropicKey, anthropicKey)
	}
	if verifier.gotDevinKey != devinKey {
		t.Fatalf("verifier got devin key %q, want the decrypted %q", verifier.gotDevinKey, devinKey)
	}
	if verifier.gotRepoURL != testProjectRepoURL {
		t.Fatalf("verifier got repo URL %q, want %q", verifier.gotRepoURL, testProjectRepoURL)
	}
}

func TestUpsertProjectValidates(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "invalid-project-user")

	cases := []identity.ProjectUpdate{
		{Name: "", RepoURL: testProjectRepoURL, WorkerCount: 3},
		{Name: testProjectName, RepoURL: "", WorkerCount: 3},
		{Name: testProjectName, RepoURL: testProjectRepoURL, WorkerCount: 11},
		{Name: testProjectName, RepoURL: testProjectRepoURL, WorkerCount: 3, MergeGateMode: "sometimes"},
	}
	for _, upd := range cases {
		if _, err := svc.UpsertProject(context.Background(), u.ID, upd); !errors.Is(err, identity.ErrInvalidProject) {
			t.Fatalf("UpsertProject(%+v) err = %v, want ErrInvalidProject", upd, err)
		}
	}
}

// TestUpsertProjectMergeGateMode covers the gate-mode knob (06 §7): an omitted
// mode defaults to "main" (the prior behavior), and "pr" round-trips.
func TestUpsertProjectMergeGateMode(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "gate-mode-user")

	created, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
	})
	if err != nil {
		t.Fatalf("UpsertProject create: %v", err)
	}
	if created.MergeGateMode != identity.MergeGateMain {
		t.Fatalf("MergeGateMode = %q, want defaulted %q", created.MergeGateMode, identity.MergeGateMain)
	}

	updated, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:          testProjectName,
		RepoURL:       testProjectRepoURL,
		MergeGateMode: identity.MergeGatePR,
	})
	if err != nil {
		t.Fatalf("UpsertProject update: %v", err)
	}
	if updated.MergeGateMode != identity.MergeGatePR {
		t.Fatalf("MergeGateMode = %q, want %q", updated.MergeGateMode, identity.MergeGatePR)
	}
}

// Fixed secret names/values reused across the round-trip assertions.
const (
	amikaSecretName1  = "STRIPE_KEY"
	amikaSecretName2  = "OPENAI_KEY"
	amikaSecretValue1 = "stripe-secret-value"
	amikaSecretValue2 = "openai-secret-value"
)

// A project's Amika secrets (02 §8) are stored encrypted (name and value),
// trimmed, decrypted into Me's fingerprint view and RuntimeConfig's plaintext,
// carry forward on an empty-value re-upsert, and clear on an empty list.
func TestUpsertProjectAmikaSecretsRoundTrip(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "secrets-user")

	p, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
		AmikaSecrets: []identity.AmikaSecretInput{
			{Name: " STRIPE_KEY ", Value: amikaSecretValue1},
			{Name: amikaSecretName2, Value: amikaSecretValue2},
		},
	})
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// At rest: ciphertext only, never the plaintext name or value.
	if len(p.AmikaSecrets) != 2 {
		t.Fatalf("stored AmikaSecrets = %+v, want 2", p.AmikaSecrets)
	}
	for i, sec := range p.AmikaSecrets {
		if len(sec.NameEnc) == 0 || len(sec.ValueEnc) == 0 {
			t.Fatalf("AmikaSecrets[%d] not encrypted: %+v", i, sec)
		}
		if string(sec.NameEnc) == amikaSecretName1 || string(sec.ValueEnc) == amikaSecretValue1 {
			t.Fatalf("AmikaSecrets[%d] stored in the clear: %+v", i, sec)
		}
	}

	// Me: decrypted, trimmed names + value fingerprints — never the value.
	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	wantStatus := []identity.AmikaSecretStatus{
		{Name: amikaSecretName1, Value: identity.SecretStatus{Set: true, Tail: identity.Tail(amikaSecretValue1)}},
		{Name: amikaSecretName2, Value: identity.SecretStatus{Set: true, Tail: identity.Tail(amikaSecretValue2)}},
	}
	if len(me.Projects) != 1 {
		t.Fatalf("Me().Projects = %+v, want exactly one", me.Projects)
	}
	gotSecrets := me.Projects[0].Secrets
	if len(gotSecrets) != len(wantStatus) {
		t.Fatalf("Me().Projects[0].Secrets = %+v, want %+v", gotSecrets, wantStatus)
	}
	for i, w := range wantStatus {
		if gotSecrets[i] != w {
			t.Errorf("Secrets[%d] = %+v, want %+v", i, gotSecrets[i], w)
		}
	}

	// RuntimeConfig: plaintext name/value for in-process sandbox injection.
	wantValues := []identity.AmikaSecretValue{
		{Name: amikaSecretName1, Value: amikaSecretValue1},
		{Name: amikaSecretName2, Value: amikaSecretValue2},
	}
	assertRuntimeSecrets := func(when string) {
		t.Helper()
		rc, rerr := svc.RuntimeConfig(context.Background(), p.ID)
		if rerr != nil {
			t.Fatalf("RuntimeConfig (%s): %v", when, rerr)
		}
		if len(rc.AmikaSecrets) != len(wantValues) {
			t.Fatalf("RuntimeConfig.AmikaSecrets (%s) = %+v, want %+v", when, rc.AmikaSecrets, wantValues)
		}
		for i, w := range wantValues {
			if rc.AmikaSecrets[i] != w {
				t.Errorf("RuntimeConfig.AmikaSecrets[%d] (%s) = %+v, want %+v", i, when, rc.AmikaSecrets[i], w)
			}
		}
	}
	assertRuntimeSecrets("initial")

	// Re-upsert keeping the same names with EMPTY values carries the stored
	// (encrypted) values forward (the write-only merge).
	if _, keepErr := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
		AmikaSecrets: []identity.AmikaSecretInput{
			{Name: amikaSecretName1},
			{Name: amikaSecretName2},
		},
	}); keepErr != nil {
		t.Fatalf("UpsertProject keep: %v", keepErr)
	}
	assertRuntimeSecrets("after empty-value re-upsert")

	// Re-upsert with no secrets clears them (wholesale upsert).
	cleared, err := svc.UpsertProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name:    testProjectName,
		RepoURL: testProjectRepoURL,
	})
	if err != nil {
		t.Fatalf("UpsertProject clear: %v", err)
	}
	if len(cleared.AmikaSecrets) != 0 {
		t.Fatalf("AmikaSecrets after clear = %+v, want empty", cleared.AmikaSecrets)
	}
}

// Invalid secret lists are the client's fault (ErrInvalidProject): a blank name,
// duplicate env-var names, a brand-new secret with no value, or over the cap.
func TestUpsertProjectAmikaSecretsValidates(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "bad-secrets-user")

	tooMany := make([]identity.AmikaSecretInput, 51)
	for i := range tooMany {
		tooMany[i] = identity.AmikaSecretInput{Name: fmt.Sprintf("NAME_%d", i), Value: "v"}
	}
	cases := map[string][]identity.AmikaSecretInput{
		"blank name":          {{Name: "  ", Value: "v"}},
		"duplicate name":      {{Name: "DUP", Value: "a"}, {Name: "DUP", Value: "b"}},
		"new secret no value": {{Name: "NAME", Value: ""}},
		"over cap":            tooMany,
	}
	for label, secrets := range cases {
		upd := identity.ProjectUpdate{Name: testProjectName, RepoURL: testProjectRepoURL, AmikaSecrets: secrets}
		if _, err := svc.UpsertProject(context.Background(), u.ID, upd); !errors.Is(err, identity.ErrInvalidProject) {
			t.Fatalf("UpsertProject(%s) err = %v, want ErrInvalidProject", label, err)
		}
	}
}

// ---- GitHub connection / repo picker --------------------------------------

// Signing in IS connecting (11 §2, amended 2026-08-03 and by the GitHub App
// migration): there is one flow, it ends on GitHub's repository chooser, and
// completing it leaves a usable repo credential behind. This is the regression
// test for the split it replaced — a scopeless sign-in that produced a signed-in
// user the repo picker couldn't serve, and a second route the UI had to
// remember to send them to.
func TestSignInGrantsRepoAccess(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token: "gho_fresh",
		user:  githubapi.GitHubUser{ID: 42, Login: testAllowedLogin},
	}
	svc := newTestService(t, store, gh, []string{testAllowedLogin})

	u, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if !me.Settings.GitHubToken.Set {
		t.Error("signing in stored no github credential; the single grant must leave one")
	}
	if got := me.Settings.GitHub.Status; got != identity.GitHubConnected {
		t.Errorf("status = %q, want %q straight after signing in", got, identity.GitHubConnected)
	}
	// And the repo picker serves the account with no second authorization.
	if _, err := svc.ListGitHubRepos(context.Background(), u.ID); err != nil {
		t.Errorf("ListGitHubRepos: %v, want a served list straight after sign-in", err)
	}
}

// Re-running the flow re-authorizes: the newer user token replaces the stored
// one, which is how "Switch account" changes the connected account or regains a
// revoked grant.
func TestCompleteConnectRefreshesStoredToken(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{token: "gho_old", user: githubapi.GitHubUser{ID: 42, Login: "u"}}
	svc := newTestService(t, store, gh, []string{"u"})

	u, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("first CompleteConnect: %v", err)
	}
	gh.token = "gho_new"
	if _, err := svc.CompleteConnect(context.Background(), "code-2", connectInstallation); err != nil {
		t.Fatalf("second CompleteConnect: %v", err)
	}

	if _, err := svc.ListGitHubRepos(context.Background(), u.ID); err != nil {
		t.Fatalf("ListGitHubRepos: %v", err)
	}
	if gh.gotReposToken != "gho_new" {
		t.Errorf("ListRepos got token %q, want the re-authorized gho_new", gh.gotReposToken)
	}
}

func TestListGitHubReposMapsRepos(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token: testGHToken,
		user:  githubapi.GitHubUser{ID: 42, Login: "u"},
		installationRepos: []githubapi.Repo{
			{FullName: "acme/api", HTMLURL: "https://github.com/acme/api", Private: true},
			{FullName: "u/blog", HTMLURL: "https://github.com/u/blog"},
		},
	}
	svc := newTestService(t, store, gh, []string{"u"})
	u, err := svc.CompleteConnect(context.Background(), "code", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	repos, err := svc.ListGitHubRepos(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListGitHubRepos: %v", err)
	}
	want := []identity.Repo{
		{FullName: "acme/api", URL: "https://github.com/acme/api", Private: true},
		{FullName: "u/blog", URL: "https://github.com/u/blog", Private: false},
	}
	if len(repos) != len(want) {
		t.Fatalf("got %d repos, want %d", len(repos), len(want))
	}
	for i := range want {
		if repos[i] != want[i] {
			t.Errorf("repos[%d] = %+v, want %+v", i, repos[i], want[i])
		}
	}
}

// A user who has never connected (including every session minted before Kiln
// asked for the repo scope) gets the not-connected sentinel, not an error —
// the dashboard turns that into the "Connect GitHub account" prompt.
func TestListGitHubReposWithoutCredential(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	u := mustDevSignIn(t, svc, "someone")

	_, err := svc.ListGitHubRepos(context.Background(), u.ID)
	if !errors.Is(err, identity.ErrGitHubNotConnected) {
		t.Fatalf("err = %v, want ErrGitHubNotConnected", err)
	}
}

// A stored-but-rejected token (revoked, or carrying no repo scope) is the same
// user-facing state as never having connected: re-authorize.
func TestListGitHubReposRejectedTokenIsNotConnected(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token:    testGHToken,
		user:     githubapi.GitHubUser{ID: 42, Login: "u"},
		reposErr: githubapi.ErrUnauthorized,
	}
	svc := newTestService(t, store, gh, []string{"u"})
	u, err := svc.CompleteConnect(context.Background(), "code", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	_, err = svc.ListGitHubRepos(context.Background(), u.ID)
	if !errors.Is(err, identity.ErrGitHubNotConnected) {
		t.Fatalf("err = %v, want ErrGitHubNotConnected", err)
	}
}

// A GitHub outage is NOT a re-authorize prompt — it must stay a real error so
// the api reports a transport failure instead of telling the user to reconnect.
func TestListGitHubReposTransportErrorPropagates(t *testing.T) {
	store := newFakeStore()
	gh := &fakeGitHub{
		token:    testGHToken,
		user:     githubapi.GitHubUser{ID: 42, Login: "u"},
		reposErr: githubapi.ErrListRepos,
	}
	svc := newTestService(t, store, gh, []string{"u"})
	u, err := svc.CompleteConnect(context.Background(), "code", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	_, err = svc.ListGitHubRepos(context.Background(), u.ID)
	if err == nil {
		t.Fatal("expected an error on a GitHub outage, got nil")
	}
	if errors.Is(err, identity.ErrGitHubNotConnected) {
		t.Fatalf("err = %v, want it NOT to read as not-connected", err)
	}
}

// The keyless seam: with a dev connection configured, a dev-minted session is
// "connected" for the repo picker exactly as a real login would be — and
// through the same installation-scoped path, so the keyless lane exercises the
// App credential model rather than the raw-token fallback beside it.
func TestDevSignInStoresConfiguredGitHubConnection(t *testing.T) {
	const devInstallation = int64(4242)
	store := newFakeStore()
	gh := &fakeGitHub{installationRepos: []githubapi.Repo{{FullName: "keyless/demo"}}}
	svc := newTestService(t, store, gh, nil)
	svc.SetDevGitHubConnection(devInstallation, "mock-github-token")

	u := mustDevSignIn(t, svc, "keyless-user")

	if got := settingsOf(t, svc, u.ID).GitHub.Status; got != identity.GitHubConnected {
		t.Errorf("status = %q, want %q — the dev connection carries an installation",
			got, identity.GitHubConnected)
	}
	repos, err := svc.ListGitHubRepos(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListGitHubRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "keyless/demo" {
		t.Errorf("repos = %+v, want the mock listing", repos)
	}
	if gh.gotInstallationReposID != devInstallation {
		t.Errorf("listed installation %d, want the dev %d", gh.gotInstallationReposID, devInstallation)
	}
	if gh.gotReposToken != "mock-github-token" {
		t.Errorf("listed with token %q, want the configured dev token", gh.gotReposToken)
	}
}

// The dev credential is written ONCE, not on every mint. A credential write
// invalidates the owner's projects, and invalidation closes the tenant bundle —
// so a repeat mint used to tear down another spec's in-flight agent turn (specs
// share one dev login). Signing in again must be side-effect-free.
func TestDevSignInWritesTheDevTokenOnlyOnce(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)
	svc.SetDevGitHubConnection(4242, "mock-github-token")
	var invalidated int
	svc.SetInvalidator(func(string) { invalidated++ })

	u := mustDevSignIn(t, svc, "keyless-user")
	if _, err := svc.CreateProject(context.Background(), u.ID, identity.ProjectUpdate{
		Name: testProjectName, RepoURL: testProjectRepoURL,
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	invalidated = 0 // ignore the create's own invalidation

	for range 3 {
		mustDevSignIn(t, svc, "keyless-user")
	}

	if invalidated != 0 {
		t.Errorf("repeat DevSignIn invalidated the project %d times, want 0", invalidated)
	}
}

// Without that opt-in — every real deployment — DevSignIn must not write a
// credential at all: a real-service e2e run's stored GitHub token would
// otherwise be clobbered by a synthetic one.
func TestDevSignInLeavesGitHubTokenAloneByDefault(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(t, store, &fakeGitHub{}, nil)

	u := mustDevSignIn(t, svc, "someone")
	if err := svc.UpdateSettings(context.Background(), u.ID, identity.SettingsUpdate{
		GitHubToken: "real-token",
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Signing in again must not overwrite the real credential.
	if _, err := svc.DevSignIn(context.Background(), "someone"); err != nil {
		t.Fatalf("second DevSignIn: %v", err)
	}
	me, err := svc.Me(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Settings.GitHubToken.Tail != identity.Tail("real-token") {
		t.Errorf("stored credential tail = %q, want the real token's", me.Settings.GitHubToken.Tail)
	}
}
