package api_test

// Private-beta gate tests: what the GitHub callback does with a login the
// allowlist turns away. There is no beta HTTP endpoint any more — the list is
// written only from the rejection path, so this exercises it through
// /auth/github/callback against a fake registrar (mirrors auth_handlers_test.go's
// shape).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// rejectedLogin is the GitHub login the allowlist turns away in these tests, and
// testPrivateBetaPath mirrors the unexported api.privateBetaPath they land on.
const (
	rejectedLogin       = "outsider"
	testPrivateBetaPath = "/beta/pending"
)

var errFakeBetaFailed = errors.New("fakeBetaRegistrar: synthetic failure")

// fakeBetaRegistrar records the logins it is asked to store; err forces a
// failure path.
type fakeBetaRegistrar struct {
	mu     sync.Mutex
	logins []string
	err    error
}

func (f *fakeBetaRegistrar) Register(_ context.Context, githubLogin string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins = append(f.logins, githubLogin)
	return nil
}

func (f *fakeBetaRegistrar) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logins...)
}

// newRejectingServer wires a callback whose CompleteConnect turns `login` away,
// over the given registrar (nil ⇒ EnableBeta is never called, the deployment
// with no list behind it).
func newRejectingServer(t *testing.T, login string, reg api.BetaRegistrar) *httptest.Server {
	t.Helper()
	srv := newBareServer()
	srv.EnableIdentity(&fakeAuth{completeConnectErr: &identity.NotAllowedError{Login: login}}, &fakeAccount{})
	if reg != nil {
		srv.EnableBeta(reg)
	}
	return httptest.NewServer(srv.Handler())
}

func TestRejectedLoginJoinsThePrivateBetaList(t *testing.T) {
	t.Run("records the login and sends them to the private-beta screen", func(t *testing.T) {
		reg := &fakeBetaRegistrar{}
		ts := newRejectingServer(t, rejectedLogin, reg)
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
			//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
			&http.Cookie{Name: testStateCookie, Value: testStateValue})
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302 to the private-beta screen", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != testPrivateBetaPath {
			t.Errorf("Location = %q, want the private-beta screen", loc)
		}
		if got := reg.recorded(); len(got) != 1 || got[0] != rejectedLogin {
			t.Errorf("recorded %v, want [%s]", got, rejectedLogin)
		}
	})

	// The whole point of the gate: someone turned away must not come out of it
	// holding a session.
	t.Run("mints no session for a login that isn't admitted", func(t *testing.T) {
		ts := newRejectingServer(t, rejectedLogin, &fakeBetaRegistrar{})
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
			//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
			&http.Cookie{Name: testStateCookie, Value: testStateValue})
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		if sess := cookieNamed(resp, testSessionCookie); sess != nil {
			t.Errorf("kiln_session cookie set = %+v, want none on a rejected login", sess)
		}
	})

	// A bookkeeping write must never be what a rejected user sees. They did
	// nothing wrong, and the screen is the same either way.
	t.Run("still shows the screen when the list write fails", func(t *testing.T) {
		ts := newRejectingServer(t, rejectedLogin, &fakeBetaRegistrar{err: errFakeBetaFailed})
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
			//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
			&http.Cookie{Name: testStateCookie, Value: testStateValue})
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302 even when the beta write fails", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != testPrivateBetaPath {
			t.Errorf("Location = %q, want the private-beta screen", loc)
		}
	})

	// A deployment that never called EnableBeta has nowhere to record; the user
	// half of the flow must be unaffected.
	t.Run("survives a deployment with no beta list wired", func(t *testing.T) {
		ts := newRejectingServer(t, rejectedLogin, nil)
		defer ts.Close()

		resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
			//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
			&http.Cookie{Name: testStateCookie, Value: testStateValue})
		defer closeBody(t, resp)

		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302 with no registrar wired", resp.StatusCode)
		}
	})
}

// The allowlist compares lower-cased logins and NotAllowedError carries what it
// compared, so the recorded row matches what an operator would paste into
// KILN_ALLOWED_GITHUB_USERS to admit them.
func TestRecordedLoginIsTheOneTheAllowlistCompared(t *testing.T) {
	reg := &fakeBetaRegistrar{}
	ts := newRejectingServer(t, "mixedcaseuser", reg)
	defer ts.Close()

	resp := doAuthRequest(t, http.MethodGet, ts.URL+"/auth/github/callback?code="+testAuthCode+"&state="+testStateValue,
		//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
		&http.Cookie{Name: testStateCookie, Value: testStateValue})
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := reg.recorded(); len(got) != 1 || got[0] != "mixedcaseuser" {
		t.Errorf("recorded %v, want [mixedcaseuser]", got)
	}
}
