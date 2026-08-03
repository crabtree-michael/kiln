package api_test

// The single GitHub grant (11 §2, amended 2026-08-03). What these cover is what
// the unification is worth: /auth/github/connect is the only start route, the
// callback has no second completion to dispatch to, and a grant that comes back
// short of `repo` still signs the user in instead of stranding them outside the
// app.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

func TestAuthConnectRedirectsToRepoScopedGrant(t *testing.T) {
	// That the grant asks GitHub for `repo` is the identity service's contract
	// (ConnectURL) and the adapter's (AuthorizeURL with ScopeRepo); what this
	// asserts is that the route drives that port and mints CSRF state doing it.
	auth := &fakeAuth{connectURL: "https://github.com/login/oauth/authorize-with-repo-scope"}
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
	if want := auth.ConnectURL(state); loc.String() != want {
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
	// The redirect's state and the cookie match exactly — the constant-time CSRF
	// check at the callback compares these two.
	if state != sc.Value {
		t.Errorf("redirect state = %q, cookie = %q; want identical", state, sc.Value)
	}
}

// The callback has ONE completion. The old split carried the intent on a
// `connect:` state prefix, which meant a state token's shape decided whether a
// repo credential got stored; nothing decides that any more, so a callback
// completing an arbitrarily-shaped state still runs the storing path.
func TestAuthCallbackAlwaysCompletesTheOneGrant(t *testing.T) {
	for _, state := range []string{testStateValue, "connect:st-1"} {
		t.Run("state="+state, func(t *testing.T) {
			auth := &fakeAuth{
				completeConnectUser: identity.User{ID: testUserID},
				sessionToken:        testSessionToken,
				sessionExpires:      time.Now().Add(time.Hour),
			}
			ts := newAuthTestServer(auth)
			defer ts.Close()

			resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+state,
				//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
				&http.Cookie{Name: testStateCookie, Value: state})
			defer closeBody(t, resp)

			if got := auth.completeConnectCallCount(); got != 1 {
				t.Errorf("CompleteConnect called %d times, want 1", got)
			}
			// Connecting signs the authorizing account in, so the session and
			// the stored token always belong to the same GitHub account.
			if cookieNamed(resp, testSessionCookie) == nil {
				t.Error("no session cookie set — the grant must also establish the session")
			}
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", resp.StatusCode)
			}
		})
	}
}

// A grant GitHub narrowed yields no repo credential, but the account did
// authenticate — so the callback signs them in and explains, rather than
// bouncing them back out. Before the flows merged this was the connect-only
// path and a session already existed; now it is the sign-in path too, and
// refusing the session would lock them out of the retry the page offers.
func TestAuthCallbackRepoScopeDeniedStillSignsIn(t *testing.T) {
	auth := &fakeAuth{
		completeConnectUser: identity.User{ID: testUserID},
		completeConnectErr:  identity.ErrRepoScopeNotGranted,
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(time.Hour),
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code=c1&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
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
	if !strings.Contains(body, "/dashboard") {
		t.Errorf("page offers no way into the app: %q", body)
	}
	sess := cookieNamed(resp, testSessionCookie)
	if sess == nil || sess.Value != testSessionToken {
		t.Fatalf("kiln_session cookie = %+v, want one minted despite the narrowed grant", sess)
	}
}
