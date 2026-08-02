package api_test

// The repo-scoped "Connect GitHub" route pair (integrations redesign). Both
// grants share GitHub's single registered callback, so what these cover is the
// discrimination between them: /auth/github/connect must mark its state, and
// the callback must pick its completion off the SERVER-SET cookie — never off
// the attacker-controllable query param.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// connectStateValue is a state token carrying the connect prefix, i.e. what
// handleAuthConnect would have minted.
const connectStateValue = "connect:st-1"

func TestAuthConnectRedirectsToRepoScopedGrant(t *testing.T) {
	// Two distinct bases so the assertion proves WHICH port the route used.
	// That the connect port asks GitHub for `repo` is the identity service's
	// contract (ConnectURL) and the adapter's (AuthorizeURL with ScopeRepo).
	auth := &fakeAuth{
		loginURL:   "https://github.com/login/oauth/authorize",
		connectURL: "https://github.com/login/oauth/authorize-with-repo-scope",
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/connect")
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
	// It is the CONNECT grant that was started, not the plain sign-in one.
	if want := auth.ConnectURL(state); loc.String() != want {
		t.Errorf("Location = %q, want %q", loc.String(), want)
	}
	if strings.HasPrefix(loc.String(), auth.LoginURL(state)) {
		t.Errorf("Location = %q, want the connect grant, not the sign-in one", loc)
	}

	sc := cookieNamed(resp, testStateCookie)
	if sc == nil {
		t.Fatal("no kiln_oauth_state cookie set")
	}
	if !strings.HasPrefix(sc.Value, "connect:") {
		t.Errorf("state cookie = %q, want the connect: prefix", sc.Value)
	}
	if !sc.HttpOnly {
		t.Error("state cookie is not HttpOnly")
	}
	// The redirect's state and the cookie still match exactly, so the existing
	// constant-time CSRF check covers the prefixed form unchanged.
	if state != sc.Value {
		t.Errorf("redirect state = %q, cookie = %q; want identical", state, sc.Value)
	}
}

func TestAuthCallbackConnectStateStoresCredential(t *testing.T) {
	auth := &fakeAuth{
		completeConnectUser: identity.User{ID: testUserID},
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(time.Hour),
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+connectStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: connectStateValue})
	defer closeBody(t, resp)

	// The connect completion ran — and the sign-in one did NOT.
	if got := auth.completeConnectCallCount(); got != 1 {
		t.Errorf("CompleteConnect called %d times, want 1", got)
	}
	if got := auth.completeLoginCallCount(); got != 0 {
		t.Errorf("CompleteLogin called %d times, want 0 on a connect callback", got)
	}
	// Connecting also signs the authorizing account in, so the session and the
	// stored token always belong to the same GitHub account.
	if cookieNamed(resp, testSessionCookie) == nil {
		t.Error("no session cookie set — connect must also establish the session")
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
}

// The intent must come off the server-set cookie, never the query param:
// otherwise anyone could hand-craft a callback URL and drive the token-storing
// path. Here the cookie is a plain sign-in state, so even though the URL claims
// the connect prefix, the CSRF equality check rejects it outright.
func TestAuthCallbackRejectsForgedConnectState(t *testing.T) {
	auth := &fakeAuth{completeLoginUser: identity.User{ID: testUserID}}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+connectStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on a state mismatch", resp.StatusCode)
	}
	if got := auth.completeConnectCallCount(); got != 0 {
		t.Errorf("CompleteConnect called %d times, want 0 — the state never matched", got)
	}
}

// A plain sign-in callback keeps its old behaviour exactly: no repo credential
// is stored just because someone logged in.
func TestAuthCallbackPlainStateStillSignsIn(t *testing.T) {
	auth := &fakeAuth{
		completeLoginUser: identity.User{ID: testUserID},
		sessionToken:      "sess-tok-1",
		sessionExpires:    time.Now().Add(time.Hour),
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if got := auth.completeLoginCallCount(); got != 1 {
		t.Errorf("CompleteLogin called %d times, want 1", got)
	}
	if got := auth.completeConnectCallCount(); got != 0 {
		t.Errorf("CompleteConnect called %d times, want 0 on a sign-in callback", got)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
}

// A grant GitHub narrowed is refused by the service; the callback has to turn
// that into a page that explains what happened, not a generic 502.
func TestAuthCallbackRepoScopeDenied(t *testing.T) {
	auth := &fakeAuth{completeConnectErr: identity.ErrRepoScopeNotGranted}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+connectStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: connectStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "repo") {
		t.Errorf("page does not name the missing permission: %q", body)
	}
	if !strings.Contains(body, "/auth/github/connect") {
		t.Errorf("page offers no retry link: %q", body)
	}
	// Nothing was stored, so no session should be minted off a failed connect.
	if cookieNamed(resp, testSessionCookie) != nil {
		t.Error("a denied grant must not mint a session")
	}
}
