package main

// The saved-sandbox capture executor (agentRuntimeAdapter.Snapshot) — the whole
// of what "Save sandbox when done" does once the board has emitted
// agent.snapshot (05 §4, §6). It spans two modules, so it lives in the
// composition root and is tested here: capture through the agent service, then
// point the project at what came back.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// capturedAt is the emit-time instant the board stamps into the payload; the
// snapshot's name is derived from it, so it is fixed here.
var capturedAt = time.Date(2026, 8, 9, 12, 30, 45, 0, time.UTC)

const (
	capturedName   = "acme-widgets-20260809-123045"
	fallbackName   = "kiln-20260809-123045"
	captureProject = "Acme Widgets"
	captureTicket  = "t-1"
	captureSlug    = "kiln"
	captureWorker  = "w-1"
	captureProjID  = "p-1"
)

// errNoSuchProject is the lookup failure the propagation case injects.
var errNoSuchProject = errors.New("no such project")

// fakeProjects is the two-method slice of identity the executor needs.
type fakeProjects struct {
	mu       sync.Mutex
	project  identity.Project
	getErr   error
	setErr   error
	selected []string // every ref the executor pointed the project at, in order
}

func (f *fakeProjects) GetProject(context.Context, string) (identity.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return identity.Project{}, f.getErr
	}
	return f.project, nil
}

func (f *fakeProjects) SetProjectSnapshot(_ context.Context, _, snapshot string) (identity.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return identity.Project{}, f.setErr
	}
	f.selected = append(f.selected, snapshot)
	f.project.AmikaSnapshot = snapshot
	return f.project, nil
}

func (f *fakeProjects) selections() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.selected...)
}

// snapshotRig wires the executor over a real *agent.Service on the mock
// provider (the catalog-capable one), with one live worker on slot w-1.
func snapshotRig(t *testing.T, projects projectSnapshots) (*agentRuntimeAdapter, *mock.Provider) {
	t.Helper()
	provider := mock.New()
	if _, err := provider.CreateWorker(context.Background(), agent.WorkerName(captureWorker)); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	svc := agent.NewService(nil, &fakeProviderResolver{provider: provider}, nil, nil, nil, nil, nil)
	return &agentRuntimeAdapter{inner: svc, projects: projects}, provider
}

// snapshotEntry is the raw outbox payload the board would have appended.
func snapshotEntry(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(board.SnapshotPayload{TicketID: captureTicket, WorkerID: captureWorker, At: capturedAt})
	if err != nil {
		t.Fatalf("marshal snapshot payload: %v", err)
	}
	return raw
}

// The whole point of the fix: the capture actually happens, it is named
// <project>-<timestamp> off the project's name and the emit-time instant, and
// the project ends up pointed at it. Before this, "Save sandbox when done" only
// suppressed the recycle and nothing was ever written to the catalog.
func TestAgentRuntimeAdapter_SnapshotCapturesAndSelects(t *testing.T) {
	projects := &fakeProjects{project: identity.Project{ID: captureProjID, Name: captureProject}}
	a, provider := snapshotRig(t, projects)

	if err := a.Snapshot(context.Background(), captureProjID, 42, snapshotEntry(t)); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	snaps, err := provider.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("captured snapshots = %+v, want exactly one", snaps)
	}
	if snaps[0].Name != capturedName {
		t.Errorf("snapshot name = %q, want %q (<project>-<timestamp>)", snaps[0].Name, capturedName)
	}
	if got := projects.selections(); len(got) != 1 || got[0] != snaps[0].Ref {
		t.Errorf("project pointed at %v, want the captured snapshot's ref %q", got, snaps[0].Ref)
	}
}

// The outbox is at-least-once, and the name is derived from the payload rather
// than the clock precisely so a redelivery lands on the capture that already
// ran. Re-running the same entry must not leave a second snapshot behind.
func TestAgentRuntimeAdapter_SnapshotRedeliveryCapturesOnce(t *testing.T) {
	projects := &fakeProjects{project: identity.Project{ID: captureProjID, Name: captureProject}}
	a, provider := snapshotRig(t, projects)
	entry := snapshotEntry(t)

	for i := range 2 {
		if err := a.Snapshot(context.Background(), captureProjID, 42, entry); err != nil {
			t.Fatalf("Snapshot delivery %d: %v", i+1, err)
		}
	}

	snaps, err := provider.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("captured snapshots = %d, want 1 — a redelivery must not re-capture", len(snaps))
	}
}

// A slot whose sandbox is already gone has nothing to capture. Retrying cannot
// change that, so the entry is reported and completed rather than burned through
// the retry budget to the same end.
func TestAgentRuntimeAdapter_SnapshotWithNothingToCaptureIsNotRetried(t *testing.T) {
	projects := &fakeProjects{project: identity.Project{ID: captureProjID, Name: captureProject}}
	svc := agent.NewService(nil, &fakeProviderResolver{provider: mock.New()}, nil, nil, nil, nil, nil)
	a := &agentRuntimeAdapter{inner: svc, projects: projects}

	if err := a.Snapshot(context.Background(), captureProjID, 42, snapshotEntry(t)); err != nil {
		t.Fatalf("Snapshot with no live sandbox = %v, want it completed, not retried", err)
	}
	if got := projects.selections(); len(got) != 0 {
		t.Errorf("project pointed at %v, want left alone when nothing was captured", got)
	}
}

// A provider with no snapshot catalog is the same kind of fact — nothing to
// capture, nothing to retry.
func TestAgentRuntimeAdapter_SnapshotWithoutACatalogIsNotRetried(t *testing.T) {
	projects := &fakeProjects{project: identity.Project{ID: captureProjID, Name: captureProject}}
	svc := agent.NewService(nil, &fakeProviderResolver{provider: bareProvider{}}, nil, nil, nil, nil, nil)
	a := &agentRuntimeAdapter{inner: svc, projects: projects}

	if err := a.Snapshot(context.Background(), captureProjID, 42, snapshotEntry(t)); err != nil {
		t.Fatalf("Snapshot on a catalog-less provider = %v, want it completed, not retried", err)
	}
}

// A project that cannot be read is a real failure — the outbox should retry it,
// not swallow it and lose the workspace.
func TestAgentRuntimeAdapter_SnapshotPropagatesAProjectLookupFailure(t *testing.T) {
	a, _ := snapshotRig(t, &fakeProjects{getErr: errNoSuchProject})

	if err := a.Snapshot(context.Background(), captureProjID, 42, snapshotEntry(t)); !errors.Is(err, errNoSuchProject) {
		t.Fatalf("Snapshot with an unreadable project = %v, want the failure propagated for retry", err)
	}
}

// With identity unconfigured there is no project to name the snapshot after or
// to point anywhere. The capture is still what the user asked for, so it goes
// ahead under the fallback stem.
func TestAgentRuntimeAdapter_SnapshotCapturesWithoutIdentity(t *testing.T) {
	a, provider := snapshotRig(t, nil)

	if err := a.Snapshot(context.Background(), captureProjID, 42, snapshotEntry(t)); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	snaps, err := provider.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Name != fallbackName {
		t.Errorf("captured snapshots = %+v, want one named off the fallback stem", snaps)
	}
}

// The name is the capture's idempotency key, so its shape is a contract, not a
// cosmetic: a stable UTC timestamp behind a slug of the project's own name.
func TestSnapshotName(t *testing.T) {
	cases := []struct {
		name    string
		project string
		at      time.Time
		want    string
	}{
		{name: "project and timestamp", project: captureProject, at: capturedAt, want: capturedName},
		{name: "punctuation collapses", project: "  Acme//Widgets!  ", at: capturedAt, want: capturedName},
		{name: "already a slug", project: captureSlug, at: capturedAt, want: fallbackName},
		{name: "no name falls back", project: "", at: capturedAt, want: fallbackName},
		{name: "punctuation-only falls back", project: "***", at: capturedAt, want: fallbackName},
		{
			name:    "non-UTC is normalized",
			project: captureSlug,
			at:      capturedAt.In(time.FixedZone("west", -7*60*60)),
			want:    fallbackName,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := snapshotName(tc.project, tc.at); got != tc.want {
				t.Fatalf("snapshotName(%q, %v) = %q, want %q", tc.project, tc.at, got, tc.want)
			}
		})
	}
}
