package api_test

// API tenancy tests (11 phase 2): the whole app surface is now behind a
// session, every app route is scoped to the caller's project, and dev tools +
// reset are project-scoped too. These assert the three guarantees:
//   - every gated route rejects an unauthenticated (cookieless) caller with 401;
//   - a project-scoped route with an authenticated-but-not-onboarded caller is
//     a 404 {"error":"no project configured"};
//   - the project id a session resolves to is exactly the id that reaches the
//     ports — a session never drives another project's state.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// gatedRoute is one method+path pair the tenancy tests exercise.
type gatedRoute struct {
	method string
	path   string
}

// withProjectRoutes is every route wrapped in withProject (project-scoped):
// board/message(s)/feed(all)/accept/stream plus the dev tools and reset.
var withProjectRoutes = []gatedRoute{
	{http.MethodGet, "/api/stream"},
	{http.MethodGet, "/api/board"},
	{http.MethodPost, "/api/message"},
	{http.MethodGet, "/api/messages"},
	{http.MethodGet, "/api/feed"},
	{http.MethodGet, "/api/feed/history"},
	{http.MethodPost, "/api/feed/seen"},
	{http.MethodPost, "/api/feed/dismiss-all"},
	{http.MethodPost, "/api/feed/1/dismiss"},
	{http.MethodPost, "/api/tickets/t-1/accept"},
	{http.MethodPost, "/api/dev/tickets"},
	{http.MethodPost, "/api/dev/notifications"},
	{http.MethodPost, "/api/dev/reset"},
}

// withSessionRoutes is every route wrapped in withSession (user-scoped, no
// project): the voice token mint and the push registration surface.
var withSessionRoutes = []gatedRoute{
	{http.MethodPost, "/api/voice/token"},
	{http.MethodPost, "/api/push/subscribe"},
	{http.MethodGet, "/api/push/key"},
	{http.MethodGet, "/api/push/mode"},
	{http.MethodPut, "/api/push/mode"},
}

// newFullServer builds a server with the entire gated surface mounted —
// identity + tenancy + push + all three dev routes — over the given
// authenticator and project resolver, so a single server exercises every
// guarded route at once.
func newFullServer(auth api.Authenticator, projects api.ProjectResolver) *httptest.Server {
	boards := &fakeBoardReader{}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	srv.EnableIdentity(auth, &fakeAccount{})
	srv.EnableTenancy(projects, &fakeProjectDeleter{})
	srv.EnablePush(&fakePushRegistrar{}, "BPUB")
	srv.EnableDevTickets(&fakeSeeder{})
	srv.EnableDevNotifications(&fakeNotificationPoster{})
	srv.EnableReset(&fakeResetter{})
	return httptest.NewServer(srv.Handler())
}

// doGatedRoute issues method+url with the given cookies and returns the status.
func doGatedRoute(t *testing.T, url, method string, cookies ...*http.Cookie) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	closeBody(t, resp)
	return resp.StatusCode
}

// TestGatedRoutes_RejectMissingSessionWith401 covers the first guarantee: with
// no session cookie, EVERY gated route — project-scoped and user-scoped alike —
// is a 401, before any port is touched. Only /auth/*, /healthz, /api/dev/session
// and the SPA fallback are public, and none of those are in these tables.
func TestGatedRoutes_RejectMissingSessionWith401(t *testing.T) {
	// A resolver/auth that WOULD succeed, to prove the 401 comes from the
	// missing cookie and not from a resolve failure.
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	projects := &fakeProjects{project: identity.Project{ID: testProjectID}}
	ts := newFullServer(auth, projects)
	defer ts.Close()

	for _, r := range append(append([]gatedRoute{}, withProjectRoutes...), withSessionRoutes...) {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			if code := doGatedRoute(t, ts.URL+r.path, r.method); code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 for %s %s without a session cookie", code, r.method, r.path)
			}
		})
	}
}

// TestProjectScopedRoutes_Return404WithoutProject covers the second guarantee:
// an authenticated caller whose session resolves no project (ProjectFor returns
// identity.ErrNotFound — not yet onboarded) gets a 404 on every project-scoped
// route. The user-scoped routes never consult the resolver, so they are not
// asserted here.
func TestProjectScopedRoutes_Return404WithoutProject(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	projects := &fakeProjects{err: identity.ErrNotFound}
	ts := newFullServer(auth, projects)
	defer ts.Close()

	for _, r := range withProjectRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			if code := doGatedRoute(t, ts.URL+r.path, r.method, authCookie()); code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for %s %s when the session has no project", code, r.method, r.path)
			}
		})
	}
}

// TestNoProjectBodyIsJSONError asserts the 404 no-project body shape the client
// keys onboarding off of: {"error":"no project configured"}.
func TestNoProjectBodyIsJSONError(t *testing.T) {
	auth := &fakeAuth{resolveUser: identity.User{ID: testUserID}}
	ts := newFullServer(auth, &fakeProjects{err: identity.ErrNotFound})
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/board", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/board: %v", err)
	}
	defer closeBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "no project configured" {
		t.Errorf("body = %v, want {\"error\":\"no project configured\"}", body)
	}
}

// TestProjectScoping_PassesResolvedProjectToPorts covers the third guarantee:
// the project a session resolves to is exactly the id that reaches the ports —
// project A's session drives only project A's board, never another project's.
func TestProjectScoping_PassesResolvedProjectToPorts(t *testing.T) {
	const projectA = "proj-A-only"
	boards := &fakeBoardReader{}
	poster := &fakeMessagePoster{}
	srv := api.NewServer(
		boards, poster, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	projects := &fakeProjects{project: identity.Project{ID: projectA}}
	srv.EnableIdentity(&fakeAuth{resolveUser: identity.User{ID: testUserID}}, &fakeAccount{})
	srv.EnableTenancy(projects, &fakeProjectDeleter{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// GET /api/board → the board reader must be asked for exactly projectA.
	resp := doGet(t, ts.URL+"/api/board")
	closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/board status = %d, want 200", resp.StatusCode)
	}
	if got := boards.projectID(); got != projectA {
		t.Errorf("BoardReader.GetBoard scoped to %q, want %q (the session's resolved project)", got, projectA)
	}

	// POST /api/message → the poster must be scoped to the same project.
	mresp := doPost(t, ts.URL+"/api/message", mustJSON(t, map[string]string{"text": "scoped message"}))
	closeBody(t, mresp)
	if mresp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/message status = %d, want 202", mresp.StatusCode)
	}
	if got := poster.projectID(); got != projectA {
		t.Errorf("MessagePoster.PostMessage scoped to %q, want %q", got, projectA)
	}

	// And the resolver was consulted with the session's user id.
	if ids := projects.resolvedUserIDs(); len(ids) == 0 || ids[0] != testUserID {
		t.Errorf("ProjectFor resolved for %v, want the session user %q", ids, testUserID)
	}
}

// TestProjectScoping_IgnoresClientSuppliedProjectID pins the structural
// isolation boundary (audit §3.2): the api resolves a project ONLY through the
// owner-scoped ProjectFor (its single ProjectResolver port), so a client cannot
// steer a request at another tenant by smuggling a project id into the query or
// body. The owner-DISCOVERING resolvers (identity.GetProject/RuntimeConfig) are
// never reachable from a handler — this test is the regression guard for that.
func TestProjectScoping_IgnoresClientSuppliedProjectID(t *testing.T) {
	const (
		resolved = "proj-owned-by-session"
		attacker = "proj-of-another-tenant"
	)
	boards := &fakeBoardReader{}
	poster := &fakeMessagePoster{}
	srv := api.NewServer(
		boards, poster, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	srv.EnableIdentity(&fakeAuth{resolveUser: identity.User{ID: testUserID}}, &fakeAccount{})
	srv.EnableTenancy(&fakeProjects{project: identity.Project{ID: resolved}}, &fakeProjectDeleter{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A crafted project_id in the query string must be ignored: the board reader
	// still sees only the session-resolved project.
	resp := doGet(t, ts.URL+"/api/board?project_id="+attacker)
	closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/board status = %d, want 200", resp.StatusCode)
	}
	if got := boards.projectID(); got != resolved {
		t.Errorf("GetBoard scoped to %q, want %q — a client project_id must not cross tenants", got, resolved)
	}

	// Same for a project_id smuggled into the message body.
	body := mustJSON(t, map[string]string{"text": "hi", "project_id": attacker})
	mresp := doPost(t, ts.URL+"/api/message", body)
	closeBody(t, mresp)
	if mresp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /api/message status = %d, want 202", mresp.StatusCode)
	}
	if got := poster.projectID(); got != resolved {
		t.Errorf("PostMessage scoped to %q, want %q — a body project_id must not cross tenants", got, resolved)
	}
}
