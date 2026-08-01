package api_test

// Route tests for the project sandbox-selection endpoints (sandbox selection):
// GET/POST /api/snapshots and GET /api/dev-boxes — thin
// decode/delegate/encode against a fake SandboxCatalog, driven over httptest.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

const (
	devBoxRef   = "sb-dev"     // a dev-box ref used across the sandbox route tests
	snapshotRef = "org/base:1" // a snapshot ref used across the sandbox route tests
	snapshotN   = "my-base"    // a snapshot name used across the sandbox route tests
	stateReady  = "ready"      // the ready snapshot/dev-box state in fixtures
)

// fakeSandboxCatalog is api.SandboxCatalog: canned lists and a recorded
// SaveSnapshot call, or an injected error (e.g. api.ErrNoSandboxCatalog for the
// no-catalog 404 path).
type fakeSandboxCatalog struct {
	mu        sync.Mutex
	snapshots []api.Snapshot
	devBoxes  []api.DevBox
	saved     api.SaveSnapshotRequest
	savedPID  string
	err       error
}

func (f *fakeSandboxCatalog) ListSnapshots(_ context.Context, _ string) ([]api.Snapshot, error) {
	return f.snapshots, f.err
}

func (f *fakeSandboxCatalog) ListDevBoxes(_ context.Context, _ string) ([]api.DevBox, error) {
	return f.devBoxes, f.err
}

func (f *fakeSandboxCatalog) SaveSnapshot(
	_ context.Context, projectID string, req api.SaveSnapshotRequest,
) (api.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = req
	f.savedPID = projectID
	if f.err != nil {
		return api.Snapshot{}, f.err
	}
	return api.Snapshot{Ref: "new-snap", Name: req.Name, State: "capturing", Source: req.DevBoxRef}, nil
}

// newSandboxServer builds a session+tenancy-scoped server with the given
// catalog enabled.
func newSandboxServer(catalog api.SandboxCatalog) *httptest.Server {
	srv := enableSession(newBareServer())
	srv.EnableSandboxCatalog(catalog)
	return httptest.NewServer(srv.Handler())
}

func TestListSnapshots_ReturnsMappedList(t *testing.T) {
	captured := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	catalog := &fakeSandboxCatalog{snapshots: []api.Snapshot{
		{Ref: snapshotRef, Name: "base", Description: "warm", Source: "dev-a", State: stateReady, CreatedAt: captured},
	}}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/snapshots")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body wire.SnapshotList
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(body.Snapshots))
	}
	s := body.Snapshots[0]
	if s.Ref != snapshotRef || s.State != wire.SnapshotState(stateReady) || s.Source != "dev-a" {
		t.Errorf("snapshot = %+v", s)
	}
}

// The sandbox routes are dual-mounted (12 §3.2): the id'd form
// /api/projects/{pid}/snapshots resolves the named project through withProjectID
// and serves the same catalog as the bare form.
func TestListSnapshots_IdScopedRoute_ReturnsMappedList(t *testing.T) {
	catalog := &fakeSandboxCatalog{snapshots: []api.Snapshot{
		{Ref: snapshotRef, Name: "base", State: stateReady},
	}}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/projects/"+testProjectID+"/snapshots")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 on the id'd route", resp.StatusCode)
	}
	var body wire.SnapshotList
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Snapshots) != 1 || body.Snapshots[0].Ref != snapshotRef {
		t.Errorf("snapshots = %+v", body.Snapshots)
	}
}

// The id'd capture route scopes the save to the named project.
func TestSaveSnapshot_IdScopedRoute_Returns202(t *testing.T) {
	catalog := &fakeSandboxCatalog{}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/projects/"+testProjectID+"/snapshots", mustJSON(t, wire.SaveSnapshotRequest{
		DevBoxRef: devBoxRef, Name: snapshotN,
	}))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 on the id'd route", resp.StatusCode)
	}
	if catalog.savedPID != testProjectID {
		t.Errorf("save scoped to project %q, want %q", catalog.savedPID, testProjectID)
	}
}

func TestListSnapshots_NoCatalog_Returns404(t *testing.T) {
	ts := newSandboxServer(&fakeSandboxCatalog{err: api.ErrNoSandboxCatalog})
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/snapshots")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a provider with no catalog", resp.StatusCode)
	}
}

func TestListSnapshots_ProviderError_Returns502(t *testing.T) {
	ts := newSandboxServer(&fakeSandboxCatalog{err: errFakeBoardFailed})
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/snapshots")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for a provider read failure", resp.StatusCode)
	}
}

func TestListDevBoxes_ReturnsMappedList(t *testing.T) {
	catalog := &fakeSandboxCatalog{devBoxes: []api.DevBox{
		{Ref: devBoxRef, Name: "my-dev-box", Status: "ready"},
	}}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	resp := doGet(t, ts.URL+"/api/dev-boxes")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body wire.DevBoxList
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.DevBoxes) != 1 || body.DevBoxes[0].Ref != devBoxRef {
		t.Errorf("dev boxes = %+v", body.DevBoxes)
	}
}

func TestSaveSnapshot_CapturesAndReturns202(t *testing.T) {
	catalog := &fakeSandboxCatalog{}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/snapshots", mustJSON(t, wire.SaveSnapshotRequest{
		DevBoxRef: devBoxRef, Name: snapshotN,
	}))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if catalog.saved.DevBoxRef != devBoxRef || catalog.saved.Name != "my-base" {
		t.Errorf("saved request = %+v", catalog.saved)
	}
	if catalog.savedPID != testProjectID {
		t.Errorf("save scoped to project %q, want %q", catalog.savedPID, testProjectID)
	}
	var body wire.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Ref != "new-snap" || body.State != wire.SnapshotState("capturing") {
		t.Errorf("snapshot = %+v", body)
	}
}

func TestSaveSnapshot_MissingFields_Returns400(t *testing.T) {
	catalog := &fakeSandboxCatalog{}
	ts := newSandboxServer(catalog)
	defer ts.Close()

	for _, body := range []wire.SaveSnapshotRequest{
		{DevBoxRef: "", Name: "x"},
		{DevBoxRef: "sb", Name: ""},
	} {
		resp := doPost(t, ts.URL+"/api/snapshots", mustJSON(t, body))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d for %+v, want 400", resp.StatusCode, body)
		}
		closeBody(t, resp)
	}
	if catalog.savedPID != "" {
		t.Error("SaveSnapshot must not be called when validation fails")
	}
}

func TestSandboxRoutes_Unauthenticated_Returns401(t *testing.T) {
	ts := newSandboxServer(&fakeSandboxCatalog{})
	defer ts.Close()

	resp := doGetNoAuth(t, ts.URL+"/api/snapshots")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a session", resp.StatusCode)
	}
}

// When the catalog is never enabled, the routes are absent (404 for the pattern,
// not a handler 404) — a deployment that never wired sandbox selection.
func TestSandboxRoutes_Disabled_NotMounted(t *testing.T) {
	ts := httptest.NewServer(enableSession(newBareServer()).Handler())
	defer ts.Close()

	resp := doPost(t, ts.URL+"/api/snapshots", mustJSON(t, wire.SaveSnapshotRequest{
		DevBoxRef: "sb", Name: "n",
	}))
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when sandbox catalog is not enabled", resp.StatusCode)
	}
}
