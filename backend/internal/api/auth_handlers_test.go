package api_test

// Route tests for the GitHub OAuth + cookie-session routes (11 §2): the
// callback and logout, driven over real net/http via httptest against a
// fakeAuth double — no real GitHub, no Postgres. The single grant's own
// start route lives in auth_connect_test.go.

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

	// testAuthCode is the authorization code every callback case carries. The
	// exchange behind it is a fake, so its value is never the thing under test.
	testAuthCode = "c1"

	// testSessionToken is the session token fakeAuth mints on a successful
	// callback.
	testSessionToken = "sess-tok-1"

	// testConnectPath is THE connect route — the single entry point every
	// GitHub affordance leads to, and where a callback with nobody signed in
	// gets sent back to.
	testConnectPath = "/auth/github/connect"

	// testAppPath is where a completed sign-in lands, and testDashboardPath
	// where the two exceptions do — a user with no project (onboarding lives
	// there) and one who set out from the dashboard.
	testAppPath       = "/app"
	testDashboardPath = "/dashboard"

	// testDashboardStateSuffix mirrors the unexported api.dashboardStateSuffix:
	// the marker a state nonce carries to name the second of those.
	testDashboardStateSuffix = ".dashboard"
)

// newAuthTestServer builds a bare server with EnableIdentity turned on over
// the given fakeAuth double and an account with no projects — the first-visit
// user, whose sign-in ends on onboarding.
func newAuthTestServer(auth *fakeAuth) *httptest.Server {
	return newAuthTestServerFor(auth, &fakeAccount{})
}

// newAuthTestServerFor is the same over a chosen account double, which is what
// decides where a completed sign-in lands.
func newAuthTestServerFor(auth *fakeAuth, account *fakeAccount) *httptest.Server {
	srv := newBareServer()
	srv.EnableIdentity(auth, account)
	return httptest.NewServer(srv.Handler())
}

// accountWithProject is the ordinary returning user: one project, so the app has
// a board to open onto.
func accountWithProject() *fakeAccount {
	return &fakeAccount{projectViews: []identity.ProjectView{{Project: identity.Project{ID: testProjectID}}}}
}

// signedInAuth is the double for a sign-in that completes cleanly.
func signedInAuth() *fakeAuth {
	return &fakeAuth{
		completeConnectUser: identity.User{ID: "u-1"},
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(30 * 24 * time.Hour),
	}
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

// lastCookieNamed returns the LAST Set-Cookie of a name — the one the browser
// ends up holding when a response writes the same name twice. The install hop
// does exactly that: the callback clears the spent state cookie before any
// branch runs, then writes a fresh one for the trip to GitHub.
func lastCookieNamed(resp *http.Response, name string) *http.Cookie {
	var last *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == name {
			last = c
		}
	}
	return last
}

// The old scopeless sign-in URL is now a plain redirect into the single grant
// (11 §2, amended 2026-08-03). It survives only for bookmarks and in-flight
// browsers, so all that matters is that it lands on the one flow rather than
// 404ing someone mid-sign-in.
func TestAuthLoginPathRedirectsToConnect(t *testing.T) {
	ts := newAuthTestServer(&fakeAuth{connectURL: testAuthorizeURL})
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/login")
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != testConnectPath {
		t.Errorf("Location = %q, want /auth/github/connect", loc)
	}
	// It only forwards — the state is minted by the route it forwards to, so a
	// user who takes this path can't end up with a stale one.
	if sc := cookieNamed(resp, testStateCookie); sc != nil {
		t.Errorf("state cookie = %+v, want none from the deprecated alias", sc)
	}
}

// A completed sign-in lands IN the app, which is what the person who clicked
// "Sign in" asked for.
//
// It used to land on the dashboard unconditionally, and that read as a
// laptop-only bug because an installed web app hides it: a home-screen app
// relaunches at its start_url and DefaultRoute takes it to /app regardless of
// where the callback pointed. A browser tab simply stayed on the settings
// screen — the last remaining way to "sign in and end up on a settings page"
// after the entry point stopped stranding returning users on github.com.
func TestAuthCallbackSuccess(t *testing.T) {
	auth := signedInAuth()
	ts := newAuthTestServerFor(auth, accountWithProject())
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if got := auth.lastCompleteConnectCode(); got != testAuthCode {
		t.Errorf("CompleteConnect called with %q, want %s", got, testAuthCode)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != testAppPath {
		t.Errorf("Location = %q, want %s", loc, testAppPath)
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

// Two sign-ins still finish on the dashboard, for opposite reasons: one user has
// nothing for the app to show and needs onboarding, the other set out from the
// dashboard and asked to be put back. A third case is neither — a listing that
// fails says nothing about which user this is, so it takes the destination that
// works for both.
func TestAuthCallbackLandsOnTheDashboardWhenThatIsTheRightScreen(t *testing.T) {
	cases := []struct {
		name    string
		state   string
		account *fakeAccount
	}{
		{
			name:    "no project yet, so onboarding is what comes next",
			state:   testStateValue,
			account: &fakeAccount{},
		},
		{
			name:    "started from the dashboard, so it goes back there",
			state:   testStateValue + testDashboardStateSuffix,
			account: accountWithProject(),
		},
		{
			name:    "the project listing failed, so it takes the safe screen",
			state:   testStateValue,
			account: &fakeAccount{projectErr: errFakeGitHubDown},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newAuthTestServerFor(signedInAuth(), tc.account)
			defer ts.Close()

			resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+tc.state,
				//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
				&http.Cookie{Name: testStateCookie, Value: tc.state})
			defer closeBody(t, resp)

			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != testDashboardPath {
				t.Errorf("Location = %q, want %s", loc, testDashboardPath)
			}
		})
	}
}

func TestAuthCallbackStateMismatch(t *testing.T) {
	auth := &fakeAuth{}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state=wrong",
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: "right"})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a state mismatch", resp.StatusCode)
	}
	if n := auth.completeConnectCallCount(); n != 0 {
		t.Errorf("CompleteConnect called %d times, want 0 on state mismatch", n)
	}
}

func TestAuthCallbackMissingStateCookie(t *testing.T) {
	auth := &fakeAuth{}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue)
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 with no state cookie", resp.StatusCode)
	}
	if n := auth.completeConnectCallCount(); n != 0 {
		t.Errorf("CompleteConnect called %d times, want 0 with no state cookie", n)
	}
}

func TestAuthCallbackNotAllowlisted(t *testing.T) {
	auth := &fakeAuth{completeConnectErr: identity.ErrNotAllowed}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
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
	auth := &fakeAuth{completeConnectErr: errFakeGitHubDown}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when CompleteConnect fails for a reason other than the allowlist", resp.StatusCode)
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

	for _, path := range []string{testConnectPath, "/auth/github/login"} {
		resp := doAuthRequest(t, http.MethodGet, ts.URL+path)
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404 when EnableIdentity was never called", path, resp.StatusCode)
		}
	}
}
