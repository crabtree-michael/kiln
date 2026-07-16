package api_test

// Handler tests for the multi-project collection routes (12 §3.1): the id'd
// create/update/delete/verify endpoints, driven over httptest against the
// fakeAccount/fakeProjectDeleter doubles. The cross-tenant ownership boundary is
// proven end-to-end in tenancy_integration_test.go; these assert the handler
// wiring — status codes, id passthrough, and error mapping.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// newProjectServer builds a server with identity + tenancy enabled over the
// given account/deleter doubles, ready to serve the project collection routes.
func newProjectServer(account *fakeAccount, del *fakeProjectDeleter) *httptest.Server {
	srv := newBareServer()
	srv.EnableIdentity(&fakeAuth{resolveUser: identity.User{ID: testUserID}}, account)
	srv.EnableTenancy(&fakeProjects{project: identity.Project{ID: testProjectID}}, del)
	return httptest.NewServer(srv.Handler())
}

func TestCreateProjectReturns201WithID(t *testing.T) {
	account := &fakeAccount{projectResult: identity.Project{ID: "proj-new", Name: testProjectName}}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	body := []byte(`{"name":"kiln","repo_url":"https://github.com/x/y"}`)
	resp := doPostWithCookies(t, ts.URL+"/api/projects", body, authCookie())
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/projects status = %d, want 201", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["id"] != "proj-new" {
		t.Errorf("created project id = %v, want proj-new", out["id"])
	}
}

func TestCreateProjectInvalidReturns400(t *testing.T) {
	account := &fakeAccount{projectErr: identity.ErrInvalidProject}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	resp := doPostWithCookies(t, ts.URL+"/api/projects", []byte(`{"name":"","repo_url":""}`), authCookie())
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /api/projects (invalid) status = %d, want 400", resp.StatusCode)
	}
}

func TestUpdateProjectNotOwnedReturns404(t *testing.T) {
	account := &fakeAccount{projectErr: identity.ErrNotFound}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	body := []byte(`{"name":"kiln","repo_url":"https://github.com/x/y"}`)
	resp := doPut(t, ts.URL+"/api/projects/foreign-id", body, authCookie())
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT /api/projects/{foreign} status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateProjectPassesID(t *testing.T) {
	account := &fakeAccount{projectResult: identity.Project{ID: "proj-9", Name: testProjectName}}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	body := []byte(`{"name":"kiln","repo_url":"https://github.com/x/y"}`)
	resp := doPut(t, ts.URL+"/api/projects/proj-9", body, authCookie())
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/projects/proj-9 status = %d, want 200", resp.StatusCode)
	}
	if got := account.lastProjectID(); got != "proj-9" {
		t.Errorf("UpdateProject saw id %q, want proj-9", got)
	}
}

func TestDeleteProjectReturns204AndCalls(t *testing.T) {
	deleter := &fakeProjectDeleter{}
	ts := newProjectServer(&fakeAccount{}, deleter)
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, ts.URL+"/api/projects/proj-7", nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /api/projects/proj-7 status = %d, want 204", resp.StatusCode)
	}
	if deleter.calls != 1 || deleter.projectID != "proj-7" || deleter.userID != testUserID {
		t.Errorf("deleter got (user=%q project=%q calls=%d), want (u-1, proj-7, 1)",
			deleter.userID, deleter.projectID, deleter.calls)
	}
}

func TestDeleteProjectNotOwnedReturns404(t *testing.T) {
	deleter := &fakeProjectDeleter{err: identity.ErrNotFound}
	ts := newProjectServer(&fakeAccount{}, deleter)
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, ts.URL+"/api/projects/foreign", nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE /api/projects/{foreign} status = %d, want 404", resp.StatusCode)
	}
}

func TestListProjectsReturnsArray(t *testing.T) {
	account := &fakeAccount{projectViews: []identity.ProjectView{
		{Project: identity.Project{ID: "p-1", Name: "one"}},
		{Project: identity.Project{ID: "p-2", Name: "two"}},
	}}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/projects", nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	req.AddCookie(authCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/projects status = %d, want 200", resp.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[0]["id"] != "p-1" || out[1]["id"] != "p-2" {
		t.Errorf("projects = %+v, want [p-1 p-2]", out)
	}
}

func TestVerifyProjectPassesID(t *testing.T) {
	account := &fakeAccount{verifyChecks: []identity.CheckResult{{Status: "ok"}}}
	ts := newProjectServer(account, &fakeProjectDeleter{})
	defer ts.Close()

	resp := doPostWithCookies(t, ts.URL+"/api/projects/proj-5/verify", nil, authCookie())
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/projects/proj-5/verify status = %d, want 200", resp.StatusCode)
	}
	if got := account.lastProjectID(); got != "proj-5" {
		t.Errorf("VerifyProject saw id %q, want proj-5", got)
	}
}
