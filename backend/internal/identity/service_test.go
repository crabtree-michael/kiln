package identity_test

// Unit tests for the login/session half of identity.Service (11 §2). These
// exercise Service directly against fakeStore/fakeGitHub (fakes_test.go) —
// no real Postgres or GitHub involved.

import (
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
	svc.SetClock(clock.now)

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

	got, err := svc.ResolveSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("resolved user = %q, want %q", got.ID, u.ID)
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
	svc.SetClock(clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 2, GitHubLogin: "u2"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	clock.advance(31 * 24 * time.Hour) // beyond the 30d TTL

	_, err = svc.ResolveSession(context.Background(), token)
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
	svc.SetClock(clock.now)

	u, err := store.UpsertUser(context.Background(), identity.User{GitHubID: 3, GitHubLogin: "u3"})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _, err := svc.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	clock.advance(20 * 24 * time.Hour) // remaining 10d < the 15d renew-below threshold

	if _, err := svc.ResolveSession(context.Background(), token); err != nil {
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
}

func TestResolveSessionUnknownOrEmpty(t *testing.T) {
	store := newFakeStore()
	svc := identity.NewService(store, mustCipher(t), &fakeGitHub{}, nil)

	for _, token := range []string{"", "nope"} {
		if _, err := svc.ResolveSession(context.Background(), token); !errors.Is(err, identity.ErrNoSession) {
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
	if _, err := svc.ResolveSession(context.Background(), token); !errors.Is(err, identity.ErrNoSession) {
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
