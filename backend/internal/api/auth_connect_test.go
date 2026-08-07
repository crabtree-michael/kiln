package api_test

// The single GitHub flow (11 §2, amended 2026-08-03 and by the GitHub App
// migration). What these cover is what the unification is worth:
// /auth/github/connect is the only start route, the callback has no second
// completion to dispatch to, and a flow that installs nothing still signs the
// user in instead of stranding them outside the app. Plus the App's second
// callback shape — an installation arriving with no code, from GitHub's own
// Apps page.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

const (
	// testInstallationID is the installation GitHub reports on the callback.
	testInstallationID = int64(90210)
	// testAuthorizeURL is the flow's first leg — GitHub's user-authorization
	// screen — and testInstallURL its second, the repository chooser the
	// callback redirects to when the account has installed nothing. Kept apart
	// so a Location header names which leg it is unambiguously.
	testAuthorizeURL = "https://github.com/login/oauth/authorize"
	testInstallURL   = "https://github.com/apps/kiln/installations/new"
	// testInstallCookie mirrors the unexported api.installPromptCookie: the
	// one-hop marker that keeps a declined install from looping.
	testInstallCookie = "kiln_gh_install"
)

func TestAuthConnectRedirectsToTheAuthorizeFlow(t *testing.T) {
	// That the flow starts on the App's authorize screen — the one GitHub
	// answers with a code every time, rather than the install page it dead-ends
	// for anyone who has already installed — is the identity service's contract
	// (ConnectURL); what this asserts is that the route drives that port and
	// mints CSRF state doing it.
	auth := &fakeAuth{connectURL: testAuthorizeURL}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+testConnectPath)
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
	// One nonce, one URL carrying it, once. A cached copy of this response would
	// send a later sign-in to whatever entry point was current when it was
	// stored, holding a nonce the callback no longer accepts — and this entry
	// point has now moved twice.
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
}

// Where the flow ENDS is decided when it starts, because by the time the
// callback runs the only thing left of the request that began it is the state
// nonce. So the destination rides in the nonce: GitHub echoes it back verbatim,
// and the callback has already proved it matches the cookie before reading it.
//
// The dashboard's own affordances ask for it, so connecting a repo from a
// settings form comes back to that form. Everything else — the landing page's
// "Sign in", the session gate — leaves it off and lands in the app.
func TestAuthConnectCarriesTheReturnDestinationInState(t *testing.T) {
	cases := []struct {
		name        string
		query       string
		toDashboard bool
	}{
		{name: "plain sign-in ends in the app", query: "", toDashboard: false},
		{name: "next=dashboard comes back to the dashboard", query: "?next=dashboard", toDashboard: true},
		// Asking for GitHub's chooser is something only the dashboard does, so
		// it needs no second parameter to say where it came from.
		{name: "setup implies it", query: "?setup=1", toDashboard: true},
		// Not a destination this route knows, so it is not one it acts on.
		{name: "an unknown next is ignored", query: "?next=https://evil.example", toDashboard: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newAuthTestServer(&fakeAuth{connectURL: testAuthorizeURL, installURL: testInstallURL})
			defer ts.Close()

			resp := doAuthRequest(t, http.MethodGet, ts.URL+testConnectPath+tc.query)
			defer closeBody(t, resp)

			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", resp.StatusCode)
			}
			sc := cookieNamed(resp, testStateCookie)
			if sc == nil {
				t.Fatal("no kiln_oauth_state cookie set")
			}
			if got := strings.HasSuffix(sc.Value, testDashboardStateSuffix); got != tc.toDashboard {
				t.Errorf("state %q carries the dashboard marker = %t, want %t", sc.Value, got, tc.toDashboard)
			}
			// Marker or not, it is still the nonce the callback compares.
			loc, err := resp.Location()
			if err != nil {
				t.Fatalf("Location: %v", err)
			}
			if got := loc.Query().Get("state"); got != sc.Value {
				t.Errorf("redirect state = %q, cookie = %q; want identical", got, sc.Value)
			}
		})
	}
}

// The install hop is a detour, not a new journey: someone who set out from the
// dashboard and had to stop at GitHub's chooser on the way still meant to end up
// back on the dashboard. The re-minted state carries the same marker, which is
// the only thing that survives the hop to say so.
func TestInstallHopKeepsTheReturnDestination(t *testing.T) {
	ts := newAuthTestServer(installlessAuth())
	defer ts.Close()

	state := testStateValue + testDashboardStateSuffix
	resp := doAuthRequest(t, http.MethodGet, callbackURL(ts.URL, state, 0),
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: state})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 on to the install screen", resp.StatusCode)
	}
	minted := lastCookieNamed(resp, testStateCookie)
	if minted == nil || minted.MaxAge <= 0 {
		t.Fatalf("kiln_oauth_state = %+v, want a fresh cookie for the install hop", minted)
	}
	if !strings.HasSuffix(minted.Value, testDashboardStateSuffix) {
		t.Errorf("re-minted state = %q, want it still marked for the dashboard", minted.Value)
	}
}

// The callback has ONE completion. The old split carried the intent on a
// `connect:` state prefix, which meant a state token's shape decided whether a
// repo credential got stored; nothing decides that any more, so a callback
// completing an arbitrarily-shaped state still runs the storing path.
func TestAuthCallbackAlwaysCompletesTheOneFlow(t *testing.T) {
	for _, state := range []string{testStateValue, "connect:st-1"} {
		t.Run("state="+state, func(t *testing.T) {
			auth := &fakeAuth{
				completeConnectUser: identity.User{ID: testUserID},
				sessionToken:        testSessionToken,
				sessionExpires:      time.Now().Add(time.Hour),
			}
			ts := newAuthTestServer(auth)
			defer ts.Close()

			resp := doAuthRequest(t, http.MethodGet, callbackURL(ts.URL, state, testInstallationID),
				//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
				&http.Cookie{Name: testStateCookie, Value: state})
			defer closeBody(t, resp)

			if got := auth.completeConnectCallCount(); got != 1 {
				t.Errorf("CompleteConnect called %d times, want 1", got)
			}
			// The installation_id query parameter is the whole point of the App
			// callback: dropping it would sign the user in with no repo access
			// and no sign that anything went wrong.
			if got := auth.lastCompleteConnectInstallation(); got != testInstallationID {
				t.Errorf("installation_id = %d, want %d", got, testInstallationID)
			}
			// Connecting signs the authorizing account in, so the session and
			// the installation always belong to the same GitHub account.
			if cookieNamed(resp, testSessionCookie) == nil {
				t.Error("no session cookie set — the flow must also establish the session")
			}
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status = %d, want 302", resp.StatusCode)
			}
		})
	}
}

// A malformed installation_id must not take the sign-in down with it: GitHub
// sent it, Kiln cannot fix it, and the service already treats 0 as "nothing
// installed" — which produces the explain-and-retry page below rather than a 500.
func TestAuthCallbackTreatsAnUnparseableInstallationAsAbsent(t *testing.T) {
	auth := &fakeAuth{
		completeConnectUser: identity.User{ID: testUserID},
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(time.Hour),
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet,
		ts.URL+"/auth/github/callback?code="+testAuthCode+"&installation_id=not-a-number&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if got := auth.lastCompleteConnectInstallation(); got != 0 {
		t.Errorf("installation_id = %d, want 0 for an unparseable value", got)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 — a bad parameter is not a broken sign-in", resp.StatusCode)
	}
}

// A user who authorized but has nothing installed is missing the repository
// half, and the only screen that can grant it is GitHub's chooser — so the
// callback signs them in and sends them there. That second leg is the whole
// reason sign-in can start on the authorize screen: the install page is reached
// when there is genuinely an installation to create, and not before.
func TestAuthCallbackWithoutInstallationSendsOnToTheChooser(t *testing.T) {
	auth := installlessAuth()
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, callbackURL(ts.URL, testStateValue, 0),
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 on to the install screen", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), testInstallURL) {
		t.Errorf("Location = %q, want the install screen %q", loc, testInstallURL)
	}
	// The session comes first: the account authenticated, so they are signed in
	// before they leave, and a user who abandons the install screen still lands
	// inside the app rather than back at a sign-in wall.
	sess := cookieNamed(resp, testSessionCookie)
	if sess == nil || sess.Value != testSessionToken {
		t.Fatalf("kiln_session cookie = %+v, want one minted before the install hop", sess)
	}
	// The install screen's own callback is CSRF-checked like any other, so the
	// hop re-mints state — and it must be the value the browser keeps, which is
	// the last write of that cookie name, not the eager clear above it.
	state := lastCookieNamed(resp, testStateCookie)
	if state == nil || state.Value == "" || state.MaxAge <= 0 {
		t.Fatalf("kiln_oauth_state = %+v, want a fresh cookie for the install hop", state)
	}
	if got := loc.Query().Get("state"); got != state.Value {
		t.Errorf("redirect state = %q, cookie = %q; want identical", got, state.Value)
	}
	if cookieNamed(resp, testInstallCookie) == nil {
		t.Error("no one-hop marker set — a declined install would loop forever")
	}
}

// The bound on that hop. Declining the install screen looks exactly like never
// having seen it — same callback, same missing installation — so the marker
// cookie is what tells the two apart. Coming back still empty means the user
// said no, and the honest answer is the page that says what is missing and
// offers the retry deliberately, not a third trip to the same screen.
func TestAuthCallbackStopsExplainingAfterOneInstallHop(t *testing.T) {
	auth := installlessAuth()
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, callbackURL(ts.URL, testStateValue, 0),
		//nolint:gosec // G124: outgoing request cookies the test sends, not Set-Cookie responses.
		&http.Cookie{Name: testStateCookie, Value: testStateValue},
		//nolint:gosec // G124: as above.
		&http.Cookie{Name: testInstallCookie, Value: "1"})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "install") {
		t.Errorf("page does not say what is missing: %q", body)
	}
	if !strings.Contains(body, testConnectPath) {
		t.Errorf("page offers no retry link: %q", body)
	}
	if !strings.Contains(body, "/dashboard") {
		t.Errorf("page offers no way into the app: %q", body)
	}
	sess := cookieNamed(resp, testSessionCookie)
	if sess == nil || sess.Value != testSessionToken {
		t.Fatalf("kiln_session cookie = %+v, want one minted despite the declined install", sess)
	}
	// The marker is spent — the retry link on that page starts a clean flow,
	// hop included, rather than landing straight back on this page.
	if marker := cookieNamed(resp, testInstallCookie); marker == nil || marker.MaxAge >= 0 {
		t.Errorf("one-hop marker = %+v, want it cleared for the next attempt", marker)
	}
}

// A connected user asking to change accounts or repositories wants the screen
// the sign-in route deliberately skips: GitHub's chooser, which for them renders
// as the configure page. `?setup=1` is that request, on the same route — the one
// variation, and one whose failure modes are both harmless.
func TestAuthConnectWithSetupTargetsTheChooser(t *testing.T) {
	auth := &fakeAuth{
		connectURL: testAuthorizeURL,
		installURL: testInstallURL,
	}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	setupURL := ts.URL + testConnectPath + "?setup=1"
	resp := doAuthRequest(t, http.MethodGet, setupURL)
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), testInstallURL) {
		t.Errorf("Location = %q, want the install screen %q", loc, testInstallURL)
	}
	// Still CSRF-stated: the chooser comes back through the same callback.
	sc := cookieNamed(resp, testStateCookie)
	if sc == nil || loc.Query().Get("state") != sc.Value {
		t.Errorf("state cookie = %+v, redirect state = %q; want identical", sc, loc.Query().Get("state"))
	}
}

// installlessAuth is the double for "GitHub authenticated an account that has
// installed the App nowhere": a populated user WITH ErrInstallationRequired,
// which is the shape CompleteConnect returns so the caller can sign them in and
// still owe them the repository half.
func installlessAuth() *fakeAuth {
	return &fakeAuth{
		completeConnectUser: identity.User{ID: testUserID},
		completeConnectErr:  identity.ErrInstallationRequired,
		sessionToken:        testSessionToken,
		sessionExpires:      time.Now().Add(time.Hour),
		installURL:          testInstallURL,
	}
}

// The App's second callback shape: someone installs Kiln from GitHub's own Apps
// page, so there is an installation and no code. There was no authorize step,
// hence no state cookie to check — the existing session is what authenticates
// the request, and the installation is attached to it.
func TestAuthCallbackAttachesABareInstallationToTheSession(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	ts := newAuthTestServerFor(auth, accountWithProject())
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet,
		ts.URL+"/auth/github/callback?installation_id="+
			strconv.FormatInt(testInstallationID, 10)+"&setup_action=install",
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testSessionCookie, Value: testSessionToken})
	defer closeBody(t, resp)

	got, ok := auth.lastAttach()
	if !ok {
		t.Fatal("AttachInstallation was never called")
	}
	if got.userID != testUserID || got.installationID != testInstallationID {
		t.Errorf("attach = %+v, want the session's user and installation %d", got, testInstallationID)
	}
	if auth.completeConnectCallCount() != 0 {
		t.Error("there is no code to exchange — CompleteConnect must not run")
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 into the app", resp.StatusCode)
	}
	// This shape carries no state, so there is no stated destination — and the
	// default is the app, same as any other completed sign-in.
	if loc := resp.Header.Get("Location"); loc != testAppPath {
		t.Errorf("Location = %q, want %s", loc, testAppPath)
	}
}

// The same shape with nobody signed in: there is no user to attach the
// installation to, so the only honest move is the front door — where the normal
// flow picks the very same installation back up.
func TestAuthCallbackBareInstallationWithoutSessionGoesToConnect(t *testing.T) {
	auth := &fakeAuth{resolveErr: identity.ErrNoSession}
	ts := newAuthTestServer(auth)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet,
		ts.URL+"/auth/github/callback?installation_id="+strconv.FormatInt(testInstallationID, 10))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc.Path != testConnectPath {
		t.Errorf("Location = %q, want the connect route", loc)
	}
	if _, ok := auth.lastAttach(); ok {
		t.Error("nothing may be attached when there is no session to attach it to")
	}
}

// Neither a code nor an installation is the one genuinely malformed callback.
func TestAuthCallbackWithNeitherCodeNorInstallationIsABadRequest(t *testing.T) {
	ts := newAuthTestServer(&fakeAuth{})
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?state="+testStateValue)
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// callbackURL builds the App callback GitHub sends, with installation_id
// omitted entirely when it is 0 (the "user authorized, nothing installed" case,
// which is an ABSENT parameter rather than a zero one). The code is always
// testAuthCode: nothing here reads it, since the exchange is a fake — what
// varies between these cases is the state and the installation.
func callbackURL(base, state string, installationID int64) string {
	u := base + "/auth/github/callback?code=" + testAuthCode + "&state=" + state
	if installationID != 0 {
		u += "&installation_id=" + strconv.FormatInt(installationID, 10)
	}
	return u
}
