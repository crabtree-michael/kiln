package amika

import (
	"context"
	"net/http"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

const (
	pathSnapshots = "/sandbox-snapshots"
	sbDev         = "sb-dev" // a dev-box id in catalog test fixtures
)

// ListSnapshots maps each snapshot object onto the neutral agent.Snapshot with
// Ref = the fully-qualified `snapshot` name (NOT the id — Ref is what the
// project stores and passes back at create time) and a defensively-classified
// state.
func TestListSnapshotsMapsFields(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSnapshots}: func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer k" {
				t.Errorf("auth header = %q, want Bearer k", got)
			}
			// Amika wraps the list in an {"items":[...]} envelope.
			writeJSON(t, w, http.StatusOK, snapshotList{Items: []snapshotObject{
				{
					ID:                "snapshot-id-1",
					Snapshot:          "org/base:ready",
					State:             "ready",
					Description:       "warm toolchain",
					SourceSandboxName: "dev-box-a",
					CreatedAt:         "2026-07-01T10:00:00Z",
				},
				{ID: "snapshot-id-2", Snapshot: "org/base:wip", State: "capturing"},
				{ID: "snapshot-id-3", Snapshot: "", State: "weird-unknown-state"},
			}})
		},
	})

	got, err := c.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d snapshots, want 3: %+v", len(got), got)
	}
	if got[0].Ref != "org/base:ready" || got[0].State != agent.SnapshotReady {
		t.Errorf("snapshot[0] = %+v, want Ref=org/base:ready State=ready", got[0])
	}
	if got[0].Source != "dev-box-a" || got[0].Description != "warm toolchain" {
		t.Errorf("snapshot[0] source/description = %+v", got[0])
	}
	if got[1].State != agent.SnapshotCapturing {
		t.Errorf("snapshot[1] state = %q, want capturing", got[1].State)
	}
	// Empty snapshot name falls back to the id for Ref; an unrecognised state
	// classifies as unknown (defensive default).
	if got[2].Ref != "snapshot-id-3" || got[2].State != agent.SnapshotUnknown {
		t.Errorf("snapshot[2] = %+v, want Ref=snapshot-id-3 State=unknown", got[2])
	}
}

// An empty snapshot catalog is the {"items":[]} envelope: it decodes to an
// empty, non-error result (not a nil-body / wrong-shape decode error).
func TestListSnapshotsEmpty(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSnapshots}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusOK, snapshotList{Items: []snapshotObject{}})
		},
	})

	got, err := c.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d snapshots, want 0: %+v", len(got), got)
	}
}

// ListDevBoxes returns the sandboxes that are NOT this instance's pooled workers
// (the complement of ListWorkers's prefix match) — the user's dev boxes.
func TestListDevBoxesExcludesPooledWorkers(t *testing.T) {
	const prefix = "kiln-dev-worker-"
	c := newClient(t, Config{APIKey: "k", WorkerPrefix: prefix}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusOK, []sandbox{
				{ID: "sb-worker", Name: prefix + "slot-a", State: stateRunning}, // pooled worker: excluded
				{ID: sbDev, Name: "my-dev-box", State: stateRunning},
				{ID: "sb-dev-stopped", Name: "old-dev-box", State: stateStopped},
			})
		},
	})

	got, err := c.ListDevBoxes(context.Background())
	if err != nil {
		t.Fatalf("ListDevBoxes: %v", err)
	}
	want := []agent.DevBox{
		{Ref: sbDev, Name: "my-dev-box", Status: agent.RunReady},
		{Ref: "sb-dev-stopped", Name: "old-dev-box", Status: agent.RunStopped},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d dev boxes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("devbox[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// SaveSnapshot posts name + the dev box's ref as sandbox_id, and maps the
// response (a still-capturing snapshot) back to the neutral shape.
func TestSaveSnapshotPostsAndMaps(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSnapshots}: func(w http.ResponseWriter, r *http.Request) {
			var body createSnapshotRequest
			decodeBody(t, r, &body)
			if body.Name != "my-base" {
				t.Errorf("name = %q, want my-base", body.Name)
			}
			if body.SandboxID != sbDev {
				t.Errorf("sandbox_id = %q, want sb-dev", body.SandboxID)
			}
			if body.Description != "captured from dev box" {
				t.Errorf("description = %q", body.Description)
			}
			writeJSON(t, w, http.StatusOK, snapshotObject{
				ID: "snapshot-new", Snapshot: "org/my-base:1", State: "capturing",
			})
		},
	})

	got, err := c.SaveSnapshot(context.Background(), agent.SaveSnapshotRequest{
		DevBoxRef:   sbDev,
		Name:        "my-base",
		Description: "captured from dev box",
	})
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if got.Ref != "org/my-base:1" || got.State != agent.SnapshotCapturing {
		t.Errorf("SaveSnapshot = %+v, want Ref=org/my-base:1 State=capturing", got)
	}
}

// SaveSnapshot validates its inputs before touching the network — a blank name
// or dev box ref is a client-side error, not a request.
func TestSaveSnapshotRejectsBlankInput(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{})
	if _, err := c.SaveSnapshot(context.Background(), agent.SaveSnapshotRequest{Name: "x"}); err == nil {
		t.Error("SaveSnapshot with blank DevBoxRef must error")
	}
	if _, err := c.SaveSnapshot(context.Background(), agent.SaveSnapshotRequest{DevBoxRef: "sb"}); err == nil {
		t.Error("SaveSnapshot with blank Name must error")
	}
}

// The adapter satisfies the neutral catalog seam, and CapabilitiesOf still
// reports Snapshots so the core gates the affordance on the capability.
func TestClientImplementsSandboxCatalog(t *testing.T) {
	c := New(Config{APIKey: "k"}, nil)
	if _, ok := agent.SandboxCatalogOf(c); !ok {
		t.Fatal("amika.Client must implement agent.SandboxCatalog")
	}
	if !agent.CapabilitiesOf(c).Snapshots {
		t.Error("amika.Client must report the Snapshots capability")
	}
}
