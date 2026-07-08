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
	"reflect"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// stubAuthenticator is a minimal Authenticator double, local to this
// in-package test file (the api_test package's richer fakeAuth isn't visible
// here across the package boundary).
type stubAuthenticator struct {
	user    identity.User
	expires time.Time
	err     error
}

func (a *stubAuthenticator) LoginURL(string) string { return "" }

func (a *stubAuthenticator) CompleteLogin(context.Context, string) (identity.User, error) {
	return identity.User{}, nil
}

func (a *stubAuthenticator) CreateSession(context.Context, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (a *stubAuthenticator) ResolveSession(context.Context, string) (identity.User, time.Time, error) {
	return a.user, a.expires, a.err
}

func (a *stubAuthenticator) Logout(context.Context, string) error { return nil }

// stubUser and stubToken are the fixed user id and session token the
// withSession/withProject white-box tests share (goconst: named once).
const (
	stubUser  = "u-1"
	stubToken = "good-token"
)

// stubProjects is a minimal ProjectResolver double for the withProject
// white-box tests, local to this in-package file. It records whether it was
// consulted, so a test can assert withSession short-circuits before the resolve.
type stubProjects struct {
	project identity.Project
	err     error
	called  bool
}

func (p *stubProjects) ProjectFor(context.Context, string) (identity.Project, error) {
	p.called = true
	return p.project, p.err
}

// TestWithProject_NoCookie_Returns401 asserts withProject inherits withSession's
// guard: no session cookie is a 401, and the resolver is never consulted.
func TestWithProject_NoCookie_Returns401(t *testing.T) {
	projects := &stubProjects{}
	srv := &Server{auth: &stubAuthenticator{}, projects: projects}
	called := false
	h := srv.withProject(func(http.ResponseWriter, *http.Request, identity.User, identity.Project) { called = true })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/scoped", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 with no session cookie", rec.Code)
	}
	if called {
		t.Error("wrapped handler called despite no session cookie")
	}
	if projects.called {
		t.Error("ProjectFor consulted despite the request never authenticating")
	}
}

// TestWithProject_NoProject_Returns404 asserts an authenticated caller whose
// resolver reports no project (identity.ErrNotFound) is a 404, and the handler
// never runs.
func TestWithProject_NoProject_Returns404(t *testing.T) {
	srv := &Server{
		auth:     &stubAuthenticator{user: identity.User{ID: stubUser}, expires: time.Now().Add(time.Hour)},
		projects: &stubProjects{err: identity.ErrNotFound},
	}
	called := false
	h := srv.withProject(func(http.ResponseWriter, *http.Request, identity.User, identity.Project) { called = true })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/scoped", nil)
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: stubToken})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when the session has no project", rec.Code)
	}
	if called {
		t.Error("wrapped handler called despite no project resolving")
	}
}

// TestWithProject_ValidSession_CallsNextWithUserAndProject asserts the happy
// path hands both the resolved user and project to the wrapped handler.
func TestWithProject_ValidSession_CallsNextWithUserAndProject(t *testing.T) {
	wantUser := identity.User{ID: stubUser, GitHubLogin: "octocat"}
	wantProject := identity.Project{ID: "proj-9", Name: "kiln"}
	srv := &Server{
		auth:     &stubAuthenticator{user: wantUser, expires: time.Now().Add(time.Hour)},
		projects: &stubProjects{project: wantProject},
	}
	var gotUser identity.User
	var gotProject identity.Project
	called := false
	h := srv.withProject(func(_ http.ResponseWriter, _ *http.Request, u identity.User, p identity.Project) {
		called = true
		gotUser = u
		gotProject = p
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/scoped", nil)
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: stubToken})
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called {
		t.Fatal("wrapped handler not called on a valid session with a project")
	}
	if gotUser != wantUser {
		t.Errorf("user = %+v, want %+v", gotUser, wantUser)
	}
	if !reflect.DeepEqual(gotProject, wantProject) {
		t.Errorf("project = %+v, want %+v", gotProject, wantProject)
	}
}

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
	srv := &Server{auth: &stubAuthenticator{user: want, expires: time.Now().Add(30 * 24 * time.Hour)}}
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

// TestWithSession_ValidSession_ReissuesCookie asserts the sliding-session fix
// (final review, Important #1): a successful resolve re-issues the
// kiln_session cookie against ResolveSession's returned expiry, with a
// positive Max-Age, so the browser's cookie lifetime tracks the (possibly
// slid) DB expiry instead of expiring on the original login's fixed clock.
func TestWithSession_ValidSession_ReissuesCookie(t *testing.T) {
	expires := time.Now().Add(30 * 24 * time.Hour)
	srv := &Server{auth: &stubAuthenticator{user: identity.User{ID: "u-1"}, expires: expires}}
	h := srv.withSession(func(http.ResponseWriter, *http.Request, identity.User) {})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/protected", nil)
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "good-token"})
	rec := httptest.NewRecorder()
	h(rec, req)

	resp := rec.Result()
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close response body: %v", cerr)
		}
	}()
	var sess *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			sess = c
		}
	}
	if sess == nil {
		t.Fatal("no kiln_session Set-Cookie on a successful resolve")
	}
	if sess.MaxAge <= 0 {
		t.Errorf("kiln_session Max-Age = %d, want a positive value", sess.MaxAge)
	}
}
