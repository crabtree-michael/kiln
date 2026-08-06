package api_test

// The canonical-origin redirect (11 §2, canonical.go). The bug it closes is a
// cookie-jar split: a deployment reachable at both its hosting platform's
// hostname and its real domain writes the OAuth state cookie on whichever host
// the user is reading Kiln on, while GitHub delivers the callback to the App's
// one registered callback URL — so state written on one host is read on the
// other and found on neither ("missing oauth state cookie"), and the user is
// left parked on the platform URL.
//
// The three-hop test below is the regression: it walks connect → off-host
// callback → redirected callback, carrying cookies exactly as a browser would,
// and only passes if the state written at hop 1 is the state checked at hop 3.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

const (
	// testCanonicalOrigin is the domain users type; testOffHost is the hosting
	// platform's own hostname, which is where GitHub's callback lands when the
	// App's registered callback URL still names it.
	testCanonicalOrigin = "https://trykiln.dev"
	testCanonicalHost   = "trykiln.dev"
	testOffHost         = "kiln-abc123.onrender.com"

	testCallbackPath = "/auth/github/callback"
)

// newCanonicalServer builds an identity-enabled server pinned to
// testCanonicalOrigin, returning the handler directly: these tests drive it
// in-process so they can set the Host header per request, which is the whole
// variable under test and something an httptest.Server's own address fixes.
func newCanonicalServer(t *testing.T, auth *fakeAuth) http.Handler {
	t.Helper()
	srv := newBareServer()
	srv.EnableIdentity(auth, &fakeAccount{})
	srv.EnableHealthz("v-test", func(context.Context) error { return nil })
	if err := srv.EnableCanonicalHost(testCanonicalOrigin); err != nil {
		t.Fatalf("EnableCanonicalHost(%q): %v", testCanonicalOrigin, err)
	}
	return srv.Handler()
}

// serveOn issues an in-process request for target as though it arrived on host,
// carrying the given cookies.
func serveOn(h http.Handler, host, method, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, nil)
	req.Host = host
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// recordedCookie finds a cookie by name among a recorder's Set-Cookie headers,
// or nil when the response set none of that name.
func recordedCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// mustRecordedCookie is recordedCookie for the cases a test cannot continue
// without — the cookie the next hop has to carry.
func mustRecordedCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	c := recordedCookie(rec, name)
	if c == nil {
		t.Fatalf("no %s cookie was set", name)
	}
	return c
}

// TestSignInSurvivesACallbackOnTheWrongHost is the bug, end to end. The state
// cookie is minted on the canonical host, GitHub returns to the platform host,
// and sign-in must still complete — which it can only do if the callback is
// pulled back onto the host holding the cookie before it is handled.
func TestSignInSurvivesACallbackOnTheWrongHost(t *testing.T) {
	auth := &fakeAuth{
		connectURL:          testAuthorizeURL,
		completeConnectUser: identity.User{ID: testUserID},
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(time.Hour),
	}
	h := newCanonicalServer(t, auth)

	// Hop 1 — the user starts on the domain they typed. The state cookie is
	// written into trykiln.dev's jar and nowhere else.
	start := serveOn(h, testCanonicalHost, http.MethodGet, testConnectPath)
	if start.Code != http.StatusFound {
		t.Fatalf("connect status = %d, want 302", start.Code)
	}
	state := mustRecordedCookie(t, start, testStateCookie)

	// Hop 2 — GitHub delivers the callback to the App's registered callback URL,
	// which is the platform host. The browser sends no cookies: different host,
	// different jar. Today's failure is a 400 here.
	off := serveOn(h, testOffHost, http.MethodGet, testCallbackPath+"?code=c-1&state="+state.Value)
	if off.Code != http.StatusFound {
		t.Fatalf("off-host callback status = %d, want 302 onto the canonical origin", off.Code)
	}
	loc := off.Header().Get("Location")
	want := testCanonicalOrigin + testCallbackPath + "?code=c-1&state=" + state.Value
	if loc != want {
		t.Fatalf("off-host callback Location = %q, want %q", loc, want)
	}
	if n := auth.completeConnectCallCount(); n != 0 {
		// The code must not be spent before the state check clears.
		t.Errorf("CompleteConnect called %d times on the off-host hop, want 0", n)
	}

	// Hop 3 — the browser follows onto trykiln.dev and now DOES send the state
	// cookie, because that is the jar it lives in. The flow completes.
	done := serveOn(h, testCanonicalHost, http.MethodGet, testCallbackPath+"?code=c-1&state="+state.Value, state)
	if done.Code != http.StatusFound {
		t.Fatalf("canonical callback status = %d, want 302; body = %q", done.Code, done.Body.String())
	}
	if got := done.Header().Get("Location"); got != testDashboardPath {
		t.Errorf("canonical callback Location = %q, want %s", got, testDashboardPath)
	}
	if n, code := auth.completeConnectCallCount(), auth.lastCompleteConnectCode(); n != 1 || code != "c-1" {
		t.Errorf("CompleteConnect calls = %d with last code %q, want 1 and \"c-1\"", n, code)
	}
	if sc := recordedCookie(done, testSessionCookie); sc == nil || sc.Value != testSessionToken {
		t.Errorf("session cookie = %+v, want one carrying %q on the canonical host", sc, testSessionToken)
	}
}

// A navigation already on the canonical host is served, not bounced — including
// when the Host header's case differs, since hostnames are case-insensitive and
// a mismatch there would be an infinite redirect.
func TestCanonicalHostServesItsOwnHost(t *testing.T) {
	h := newCanonicalServer(t, &fakeAuth{connectURL: testAuthorizeURL})

	for _, host := range []string{testCanonicalHost, strings.ToUpper(testCanonicalHost)} {
		t.Run("host="+host, func(t *testing.T) {
			rec := serveOn(h, host, http.MethodGet, testConnectPath)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302", rec.Code)
			}
			if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, testAuthorizeURL) {
				t.Errorf("Location = %q, want the GitHub authorize URL — the request was bounced instead of served", loc)
			}
			if recordedCookie(rec, testStateCookie) == nil {
				t.Error("no state cookie: the connect handler never ran")
			}
		})
	}
}

// /healthz is exempt. The hosting platform's health check reaches the process on
// an internal hostname it does not publish, and a 302 away from the probe reads
// as an unhealthy instance — which would take the deployment down rather than
// fix its sign-in.
func TestCanonicalHostExemptsHealthz(t *testing.T) {
	h := newCanonicalServer(t, &fakeAuth{})

	rec := serveOn(h, "10.0.0.7:8080", http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from an off-host health probe; body = %q", rec.Code, rec.Body.String())
	}
}

// Only safe methods are redirected. A browser may drop a redirected POST's body
// or change its method, and every write in this API is issued by a page already
// pulled onto the canonical origin — so a bounce here could only lose a write.
func TestCanonicalHostLeavesWritesAlone(t *testing.T) {
	h := newCanonicalServer(t, &fakeAuth{})

	rec := serveOn(h, testOffHost, http.MethodPost, "/auth/logout")
	if rec.Code == http.StatusFound {
		t.Fatalf("POST was redirected (Location %q); want it served where it arrived", rec.Header().Get("Location"))
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 from logout", rec.Code)
	}
}

// Unpinned is the local/dev and single-host default: no origin, no redirect,
// whatever host the request claims.
func TestWithoutACanonicalHostNothingIsRedirected(t *testing.T) {
	srv := newBareServer()
	srv.EnableIdentity(&fakeAuth{connectURL: testAuthorizeURL}, &fakeAccount{})
	h := srv.Handler()

	rec := serveOn(h, testOffHost, http.MethodGet, testConnectPath)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, testAuthorizeURL) {
		t.Errorf("Location = %q, want the GitHub authorize URL — an unpinned server redirected", loc)
	}
}

// A KILN_PUBLIC_URL that cannot name an origin is rejected at the seam, so the
// composition root can refuse the boot instead of serving the split-jar sign-in
// the setting exists to prevent.
func TestEnableCanonicalHostRejectsANonOrigin(t *testing.T) {
	for _, publicURL := range []string{"", "trykiln.dev", "/dashboard", "ftp://trykiln.dev", "https://"} {
		t.Run("url="+publicURL, func(t *testing.T) {
			if err := newBareServer().EnableCanonicalHost(publicURL); !errors.Is(err, api.ErrPublicURL) {
				t.Errorf("EnableCanonicalHost(%q) error = %v, want ErrPublicURL", publicURL, err)
			}
		})
	}
}

// Surrounding whitespace and a trailing path are tolerated: an operator pasting
// an origin into a hosting dashboard should not have a sign-in outage over it.
func TestEnableCanonicalHostAcceptsAPastedOrigin(t *testing.T) {
	for _, publicURL := range []string{testCanonicalOrigin, "  " + testCanonicalOrigin + "  ", testCanonicalOrigin + "/"} {
		t.Run("url="+publicURL, func(t *testing.T) {
			srv := newBareServer()
			if err := srv.EnableCanonicalHost(publicURL); err != nil {
				t.Fatalf("EnableCanonicalHost(%q): %v", publicURL, err)
			}
			srv.EnableIdentity(&fakeAuth{connectURL: testAuthorizeURL}, &fakeAccount{})

			rec := serveOn(srv.Handler(), testOffHost, http.MethodGet, "/app")
			if got := rec.Header().Get("Location"); got != testCanonicalOrigin+"/app" {
				t.Errorf("Location = %q, want %q", got, testCanonicalOrigin+"/app")
			}
		})
	}
}
