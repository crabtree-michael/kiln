package api_test

// Route tests for the GitHub OAuth + cookie-session routes (11 §2): login,
// callback, and logout, driven over real net/http via httptest against a
// fakeAuth double — no real GitHub, no Postgres.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

var errFakeGitHubDown = errors.New("fakeAuth: synthetic github outage")

// Cookie names under test, mirroring the unexported api.sessionCookie /
// api.stateCookie constants (11 §2) — named here so the string literal isn't
// repeated ad hoc across every test case.
const (
	testStateCookie   = "kiln_oauth_state"
	testSessionCookie = "kiln_session"

	// testStateValue is the state token every "state cookie present and
	// matching the query param" test case shares.
	testStateValue = "st-1"
)

// newAuthTestServer builds a bare server with EnableIdentity turned on over
// the given fakeAuth double.
func newAuthTestServer(auth *fakeAuth) *httptest.Server {
	srv := newBareServer()
	srv.EnableIdentity(auth)
	return httptest.NewServer(srv.Handler())
}

// noFollowClient stops at the first redirect (ErrUseLastResponse) so a 302's
// own Location and Set-Cookie headers are inspectable directly.
func noFollowClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// doAuthRequest issues method+url carrying the given cookies over a
// non-redirecting client, failing the test on a transport error.
func doAuthRequest(t *testing.T, method, url string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noFollowClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// readBody reads and returns a response body as a string, failing the test
// on a read error.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// cookieNamed finds a cookie by name among a response's Set-Cookie headers.
func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestAuthLoginRedirects(t *testing.T) {
	auth := &fakeAuth{loginURL: "https://github.com/login/oauth/authorize"}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/login")
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	state := loc.Query().Get("state")
	if state == "" {
		t.Fatalf("Location %q carries no state query param", loc)
	}
	if want := auth.LoginURL(state); loc.String() != want {
		t.Errorf("Location = %q, want %q", loc.String(), want)
	}

	sc := cookieNamed(resp, testStateCookie)
	if sc == nil {
		t.Fatal("no kiln_oauth_state cookie set")
	}
	if !sc.HttpOnly {
		t.Error("state cookie is not HttpOnly")
	}
	if sc.MaxAge <= 0 || sc.MaxAge > 600 {
		t.Errorf("state cookie MaxAge = %d, want (0, 600]", sc.MaxAge)
	}
	if sc.Value != state {
		t.Errorf("state cookie value = %q, want %q (the redirect's state query param)", sc.Value, state)
	}
}

func TestAuthCallbackSuccess(t *testing.T) {
	expires := time.Now().Add(30 * 24 * time.Hour)
	auth := &fakeAuth{
		completeLoginUser: identity.User{ID: "u-1"},
		sessionToken:      "sess-tok-1",
		sessionExpires:    expires,
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if got := auth.lastCompleteLoginCode(); got != "c1" {
		t.Errorf("CompleteLogin called with %q, want c1", got)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Errorf("Location = %q, want /dashboard", loc)
	}

	sess := cookieNamed(resp, testSessionCookie)
	if sess == nil || sess.Value != "sess-tok-1" {
		t.Fatalf("kiln_session cookie = %+v, want value sess-tok-1", sess)
	}
	if !sess.HttpOnly || sess.SameSite != http.SameSiteLaxMode {
		t.Errorf("kiln_session cookie = %+v, want HttpOnly + SameSite=Lax", sess)
	}

	state := cookieNamed(resp, testStateCookie)
	if state == nil || state.MaxAge > 0 {
		t.Errorf("state cookie = %+v, want cleared (Max-Age <= 0)", state)
	}
}

func TestAuthCallbackStateMismatch(t *testing.T) {
	auth := &fakeAuth{}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state=wrong",
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: "right"})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a state mismatch", resp.StatusCode)
	}
	if n := auth.completeLoginCallCount(); n != 0 {
		t.Errorf("CompleteLogin called %d times, want 0 on state mismatch", n)
	}
}

func TestAuthCallbackMissingStateCookie(t *testing.T) {
	auth := &fakeAuth{}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue)
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 with no state cookie", resp.StatusCode)
	}
	if n := auth.completeLoginCallCount(); n != 0 {
		t.Errorf("CompleteLogin called %d times, want 0 with no state cookie", n)
	}
}

func TestAuthCallbackNotAllowlisted(t *testing.T) {
	auth := &fakeAuth{completeLoginErr: identity.ErrNotAllowed}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when the github login isn't allowlisted", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "invite-only") {
		t.Errorf("body = %q, want it to contain %q", body, "invite-only")
	}
	if sess := cookieNamed(resp, testSessionCookie); sess != nil {
		t.Errorf("kiln_session cookie set = %+v, want none on a rejected login", sess)
	}
}

func TestAuthCallbackGitHubDown(t *testing.T) {
	auth := &fakeAuth{completeLoginErr: errFakeGitHubDown}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when CompleteLogin fails for a reason other than the allowlist", resp.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	t.Run("with session cookie", func(t *testing.T) {
		auth := &fakeAuth{}
		ts := newAuthTestServer(auth)
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodPost, ts.URL+"/auth/logout",
			//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
			&http.Cookie{Name: testSessionCookie, Value: "tok-1"})
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		if got := auth.lastLogoutToken(); got != "tok-1" {
			t.Errorf("Logout called with %q, want tok-1", got)
		}
		if sess := cookieNamed(resp, testSessionCookie); sess == nil || sess.MaxAge > 0 {
			t.Errorf("kiln_session cookie = %+v, want cleared (Max-Age <= 0)", sess)
		}
	})

	t.Run("without session cookie is idempotent", func(t *testing.T) {
		auth := &fakeAuth{}
		ts := newAuthTestServer(auth)
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodPost, ts.URL+"/auth/logout")
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 even with no session cookie", resp.StatusCode)
		}
		if n := auth.logoutCallCount(); n != 0 {
			t.Errorf("Logout called %d times, want 0 with no cookie to log out", n)
		}
	})
}

func TestIdentityRoutesAbsentWhenDisabled(t *testing.T) {
	srv := newBareServer() // no EnableIdentity call
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/login")
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when EnableIdentity was never called", resp.StatusCode)
	}
}
