package api_test

// Route tests for the signed-in account surface (11 §4 GET /api/me, PUT
// /api/settings, PUT /api/project, POST /api/settings/verify) and the
// dev-only session mint (11 §7 POST /api/dev/session), driven over real
// net/http via httptest against fakeAuth/fakeAccount/fakeDevSession doubles —
// no real GitHub, no Postgres.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// testSessionValue is the session token every "cookie present and resolves"
// test case in this file shares.
const testSessionValue = "sess-1"

// Shared fixture values across this file's test cases, named once so goconst
// doesn't flag the repeated literals.
const (
	testUserID      = "u-1"
	testGHLogin     = "alice"
	testProjectName = "kiln"
	testRepoURL     = "https://github.com/x/y"
	testDevToken1   = "dev-tok-1"
	testSecretName  = "STRIPE_KEY"
)

// sessionCookieFor builds the kiln_session cookie carrying testSessionValue,
// the outgoing request cookie a test sends (not a Set-Cookie response).
func sessionCookieFor() *http.Cookie {
	//nolint:gosec // G124: an outgoing request cookie the test sends, not a Set-Cookie response.
	return &http.Cookie{Name: testSessionCookie, Value: testSessionValue}
}

// newIdentityServer builds a bare *api.Server with EnableIdentity turned on
// over the given fakeAuth/fakeAccount doubles, ready for further Enable*
// calls (e.g. EnableDevSession) before Handler() is taken.
func newIdentityServer(auth *fakeAuth, account *fakeAccount) *api.Server {
	srv := newBareServer()
	srv.EnableIdentity(auth, account)
	return srv
}

// doPut issues a context-ful application/json PUT and fails the test on a
// transport error.
func doPut(t *testing.T, url string, body []byte, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build PUT %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Default to an authenticated call so callers that don't manage cookies
	// (e.g. the push/mode PUT tests, now behind withSession) still pass the
	// session guard; callers that pass explicit cookies override this.
	if len(cookies) == 0 {
		req.AddCookie(authCookie())
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	return resp
}

// doPostWithCookies issues a context-ful application/json POST carrying the
// given cookies and fails the test on a transport error.
func doPostWithCookies(t *testing.T, url string, body []byte, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// asObj asserts m[key] is a JSON object, failing the test with the offending
// value otherwise — the shared, ok-checked type assertion every decoded-body
// field lookup in this file goes through (errcheck: check-type-assertions).
func asObj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%q = %v (%T), want a JSON object", key, m[key], m[key])
	}
	return v
}

func TestMeRequiresSession(t *testing.T) {
	srv := newIdentityServer(&fakeAuth{}, &fakeAccount{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doGetNoAuth(t, ts.URL+"/api/me")
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with no session cookie", resp.StatusCode)
	}
}

func TestMeReturnsAccountView(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID, GitHubLogin: testGHLogin}}
	account := &fakeAccount{me: identity.Me{
		User: identity.User{GitHubLogin: testGHLogin, DisplayName: "Alice", AvatarURL: "https://example.com/a.png"},
		// Project left nil: not yet onboarded.
		Settings: identity.MeSettings{
			AnthropicKey: identity.SecretStatus{Set: false},
			AmikaKey:     identity.SecretStatus{Set: false},
			GitHubToken:  identity.SecretStatus{Set: false},
		},
	}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(sessionCookieFor())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	user := asObj(t, body, "user")
	if user["github_login"] != testGHLogin {
		t.Errorf("user.github_login = %v, want alice", user["github_login"])
	}
	if _, present := body["project"]; present && body["project"] != nil {
		t.Errorf("project = %v, want absent/null", body["project"])
	}
	settings := asObj(t, body, "settings")
	anthropic := asObj(t, settings, "anthropic_api_key")
	if anthropic["set"] != false {
		t.Errorf("settings.anthropic_api_key.set = %v, want false", anthropic["set"])
	}
}

func TestMeWithProjectAndSecrets(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{me: identity.Me{
		User: identity.User{GitHubLogin: testGHLogin},
		Project: &identity.Project{
			Name: testProjectName, RepoURL: testRepoURL,
			AmikaSnapshot: "snap-1", WorkerCount: 5,
		},
		Settings: identity.MeSettings{
			AnthropicKey: identity.SecretStatus{Set: true, Tail: "x4Kd"},
			AmikaKey:     identity.SecretStatus{Set: false},
			GitHubToken:  identity.SecretStatus{Set: false},
		},
	}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(sessionCookieFor())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer closeBody(t, resp)

	rawBody := readBody(t, resp)
	var body map[string]any
	if err := json.Unmarshal([]byte(rawBody), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	project := asObj(t, body, "project")
	if project["name"] != testProjectName || project["repo_url"] != testRepoURL {
		t.Errorf("project = %+v, want name=kiln repo_url=https://github.com/x/y", project)
	}
	if project["worker_count"] != float64(5) {
		t.Errorf("project.worker_count = %v, want 5", project["worker_count"])
	}
	settings := asObj(t, body, "settings")
	anthropic := asObj(t, settings, "anthropic_api_key")
	if anthropic["set"] != true || anthropic["tail"] != "x4Kd" {
		t.Errorf("settings.anthropic_api_key = %+v, want {set:true tail:x4Kd}", anthropic)
	}
	// The serialized secret is presence+fingerprint ONLY: exactly {set, tail},
	// no field that could ever carry a raw secret value.
	if len(anthropic) != 2 {
		t.Errorf("settings.anthropic_api_key has keys %v, want exactly {set, tail}", anthropic)
	}
	if strings.Contains(rawBody, `"value"`) || strings.Contains(rawBody, "sk-") {
		t.Errorf("body carries a raw-secret-shaped field: %s", rawBody)
	}
}

// meToWire serializes a project's Amika secrets as name + value fingerprint
// only — the array shape the dashboard reads, never a raw value (02 §8, D7).
func TestMeSerializesAmikaSecretStatuses(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{me: identity.Me{
		User:    identity.User{GitHubLogin: testGHLogin},
		Project: &identity.Project{Name: testProjectName, RepoURL: testRepoURL, WorkerCount: 3},
		ProjectSecrets: []identity.AmikaSecretStatus{
			{Name: testSecretName, Value: identity.SecretStatus{Set: true, Tail: "3xyz"}},
		},
	}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(sessionCookieFor())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer closeBody(t, resp)

	var body map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	project := asObj(t, body, "project")
	secrets, ok := project["amika_secrets"].([]any)
	if !ok || len(secrets) != 1 {
		t.Fatalf("project.amika_secrets = %v, want exactly one secret", project["amika_secrets"])
	}
	sec, ok := secrets[0].(map[string]any)
	if !ok {
		t.Fatalf("amika_secrets[0] = %v (%T), want an object", secrets[0], secrets[0])
	}
	if sec["name"] != testSecretName {
		t.Errorf("amika_secrets[0].name = %v, want STRIPE_KEY", sec["name"])
	}
	value := asObj(t, sec, "value")
	if value["set"] != true || value["tail"] != "3xyz" {
		t.Errorf("amika_secrets[0].value = %+v, want {set:true tail:3xyz}", value)
	}
	// Presence+fingerprint ONLY: the element is exactly {name, value} and value
	// is exactly {set, tail} — structurally no field can carry the plaintext.
	if len(sec) != 2 || len(value) != 2 {
		t.Errorf("amika_secrets[0] = %+v, want exactly {name, value:{set,tail}}", sec)
	}
}

func TestPutSettings(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{me: identity.Me{User: identity.User{GitHubLogin: testGHLogin}}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := []byte(`{"anthropic_api_key":"sk-x","devin_api_key":"cog-y"}`)
	resp := doPut(t, ts.URL+"/api/settings", body, sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, readBody(t, resp))
	}
	got := account.lastSettingsUpdate()
	if got.AnthropicKey != "sk-x" {
		t.Errorf("SettingsUpdate.AnthropicKey = %q, want sk-x", got.AnthropicKey)
	}
	if got.DevinKey != "cog-y" {
		t.Errorf("SettingsUpdate.DevinKey = %q, want cog-y", got.DevinKey)
	}

	var refreshed map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&refreshed); err != nil {
		t.Fatalf("decode refreshed Me: %v", err)
	}
	user := asObj(t, refreshed, "user")
	if user["github_login"] != testGHLogin {
		t.Errorf("refreshed Me user.github_login = %v, want alice", user["github_login"])
	}
}

func TestPutSettingsBadBody(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doPut(t, ts.URL+"/api/settings", []byte("{not json"), sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed JSON", resp.StatusCode)
	}
}

func TestPutProject(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{me: identity.Me{User: identity.User{GitHubLogin: testGHLogin}}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := []byte(`{"name":"kiln","repo_url":"https://github.com/x/y","worker_count":5}`)
	resp := doPut(t, ts.URL+"/api/project", body, sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, readBody(t, resp))
	}
	got := account.lastProjectUpdate()
	want := identity.ProjectUpdate{Name: testProjectName, RepoURL: testRepoURL, WorkerCount: 5}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectUpdate = %+v, want %+v", got, want)
	}
}

// handlePutProject maps the inbound amika_secrets array onto the domain input:
// a typed value passes through, an omitted value becomes "" (the keep-existing
// signal the service merge honours), 02 §8.
func TestPutProjectMapsAmikaSecrets(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{me: identity.Me{User: identity.User{GitHubLogin: testGHLogin}}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := []byte(`{"name":"kiln","repo_url":"https://github.com/x/y","worker_count":5,` +
		`"amika_secrets":[{"name":"STRIPE_KEY","value":"stripe-secret-1"},{"name":"KEEP_ME"}]}`)
	resp := doPut(t, ts.URL+"/api/project", body, sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, readBody(t, resp))
	}
	got := account.lastProjectUpdate().AmikaSecrets
	want := []identity.AmikaSecretInput{
		{Name: testSecretName, Value: "stripe-secret-1"},
		{Name: "KEEP_ME", Value: ""}, // value omitted → keep-existing signal
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectUpdate.AmikaSecrets = %+v, want %+v", got, want)
	}
}

func TestPutProjectInvalid(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{projectErr: identity.ErrInvalidProject}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := []byte(`{"name":"","repo_url":"","worker_count":0}`)
	resp := doPut(t, ts.URL+"/api/project", body, sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for ErrInvalidProject", resp.StatusCode)
	}
}

func TestVerify(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	account := &fakeAccount{verifyChecks: []identity.CheckResult{
		{Name: "anthropic", Status: "ok", Message: "reachable"},
		{Name: "amika", Status: "skipped", Message: "not configured"},
		{Name: "repo", Status: "failed", Message: "clone failed"},
	}}
	srv := newIdentityServer(auth, account)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doPostWithCookies(t, ts.URL+"/api/settings/verify", nil, sessionCookieFor())
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, readBody(t, resp))
	}
	var body struct {
		Checks []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Checks) != 3 {
		t.Fatalf("len(checks) = %d, want 3", len(body.Checks))
	}
	wantOrder := []string{"anthropic", "amika", "repo"}
	for i, name := range wantOrder {
		if body.Checks[i].Name != name {
			t.Errorf("checks[%d].name = %q, want %q", i, body.Checks[i].Name, name)
		}
	}
	if body.Checks[0].Status != "ok" || body.Checks[1].Status != "skipped" || body.Checks[2].Status != "failed" {
		t.Errorf("checks = %+v, want statuses ok/skipped/failed in order", body.Checks)
	}
}

func TestDevSessionMintsCookie(t *testing.T) {
	auth := &fakeAuth{}
	account := &fakeAccount{}
	devSession := &fakeDevSession{
		signInUser:     identity.User{ID: "u-dev"},
		sessionToken:   testDevToken1,
		sessionExpires: time.Now().Add(24 * time.Hour),
	}
	srv := newIdentityServer(auth, account)
	srv.EnableDevSession(devSession)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/dev/session", []byte(`{"github_login":"e2e-user"}`))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", resp.StatusCode, readBody(t, resp))
	}
	sess := cookieNamed(resp, testSessionCookie)
	if sess == nil || sess.Value != testDevToken1 {
		t.Fatalf("kiln_session cookie = %+v, want value dev-tok-1", sess)
	}
	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Token != testDevToken1 {
		t.Errorf("body.token = %q, want dev-tok-1", body.Token)
	}
	if body.ExpiresAt.IsZero() {
		t.Error("body.expires_at is zero, want the minted expiry")
	}
}

func TestDevSessionAbsentByDefault(t *testing.T) {
	srv := newIdentityServer(&fakeAuth{}, &fakeAccount{}) // no EnableDevSession call
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/dev/session", []byte(`{"github_login":"e2e-user"}`))
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when EnableDevSession was never called", resp.StatusCode)
	}
}
