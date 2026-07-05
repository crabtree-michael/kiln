package api

// White-box unit tests for withSession (11 §2): it isn't wired to any route
// yet (Phase 1 adds no protected route of its own — Task 9/10 wrap their new
// handlers with it), so it's exercised directly here rather than through
// Handler()/httptest.NewServer like the rest of the api package's route
// tests (auth_handlers_test.go, package api_test).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// stubAuthenticator is a minimal Authenticator double, local to this
// in-package test file (the api_test package's richer fakeAuth isn't visible
// here across the package boundary).
type stubAuthenticator struct {
	user identity.User
	err  error
}

func (a *stubAuthenticator) LoginURL(string) string { return "" }

func (a *stubAuthenticator) CompleteLogin(context.Context, string) (identity.User, error) {
	return identity.User{}, nil
}

func (a *stubAuthenticator) CreateSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (a *stubAuthenticator) ResolveSession(context.Context, string) (identity.User, error) {
	return a.user, a.err
}

func (a *stubAuthenticator) Logout(context.Context, string) error { return nil }

func TestWithSession_NoCookie_Returns401(t *testing.T) {
	srv := &Server{auth: &stubAuthenticator{}}
	called := false
	h := srv.withSession(func(http.ResponseWriter, *http.Request, identity.User) { called = true })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 with no session cookie", rec.Code)
	}
	if called {
		t.Error("wrapped handler called despite no session cookie")
	}
}

func TestWithSession_InvalidSession_Returns401(t *testing.T) {
	srv := &Server{auth: &stubAuthenticator{err: identity.ErrNoSession}}
	called := false
	h := srv.withSession(func(http.ResponseWriter, *http.Request, identity.User) { called = true })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/protected", nil)
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "bad-token"})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when ResolveSession fails", rec.Code)
	}
	if called {
		t.Error("wrapped handler called despite ResolveSession failing")
	}
}

func TestWithSession_ValidSession_CallsNextWithUser(t *testing.T) {
	want := identity.User{ID: "u-1", GitHubLogin: "octocat"}
	srv := &Server{auth: &stubAuthenticator{user: want}}
	var got identity.User
	called := false
	h := srv.withSession(func(_ http.ResponseWriter, _ *http.Request, u identity.User) {
		called = true
		got = u
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/protected", nil)
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "good-token"})
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called {
		t.Fatal("wrapped handler not called with a valid session")
	}
	if got != want {
		t.Errorf("user passed to wrapped handler = %+v, want %+v", got, want)
	}
}
