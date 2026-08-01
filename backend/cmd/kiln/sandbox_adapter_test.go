package main

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/api"
)

// devBoxID is a dev-box ref reused across the adapter tests.
const devBoxID = "sb-1"

// errResolveBoom is a static resolve failure the propagation test injects.
var errResolveBoom = errors.New("resolve boom")

// fakeProviderResolver satisfies agent.ProviderResolver with a canned provider
// (or a resolve error), so the sandboxCatalogAdapter can be exercised without the
// tenant registry.
type fakeProviderResolver struct {
	provider agent.Provider
	err      error
}

func (f *fakeProviderResolver) For(context.Context, string) (agent.Provider, string, error) {
	if f.err != nil {
		return nil, "", f.err
	}
	return f.provider, "kiln-worker-", nil
}

// bareProvider implements agent.Provider but NOT agent.SandboxCatalog — the
// no-catalog case (a session-only provider). Every method is an inert stub; the
// adapter never calls them, it only asks SandboxCatalogOf.
type bareProvider struct{}

func (bareProvider) ListWorkers(context.Context) ([]agent.ProviderWorker, error) { return nil, nil }
func (bareProvider) CreateWorker(context.Context, string) (agent.ProviderWorker, error) {
	return agent.ProviderWorker{}, nil
}

func (bareProvider) WorkerReady(context.Context, agent.ProviderWorker) (bool, error) {
	return true, nil
}
func (bareProvider) DestroyWorker(context.Context, agent.ProviderWorker) error { return nil }
func (bareProvider) StartTurn(context.Context, agent.ProviderWorker, string, string, bool) (agent.TurnRef, error) {
	return agent.TurnRef{}, nil
}

func (bareProvider) CheckTurn(context.Context, agent.ProviderWorker, agent.TurnRef) (agent.TurnStatus, error) {
	return agent.TurnStatus{}, nil
}

func (bareProvider) ReadLatestOutput(context.Context, agent.ProviderWorker) (agent.TurnOutput, error) {
	return agent.TurnOutput{}, nil
}

var _ agent.Provider = bareProvider{}

// The adapter resolves a catalog-capable provider (the mock) and maps its neutral
// snapshots/dev boxes onto the api's value-copies, and forwards a capture.
func TestSandboxCatalogAdapter_MapsMockProviderCatalog(t *testing.T) {
	provider := mock.New()
	provider.Snapshots = []agent.Snapshot{{Ref: "snap-1", Name: "base", State: agent.SnapshotReady}}
	provider.DevBoxes = []agent.DevBox{{Ref: devBoxID, Name: "dev", Status: agent.RunReady}}
	a := &sandboxCatalogAdapter{resolver: &fakeProviderResolver{provider: provider}}

	snaps, err := a.ListSnapshots(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Ref != "snap-1" || snaps[0].State != "ready" {
		t.Errorf("snapshots = %+v", snaps)
	}

	boxes, err := a.ListDevBoxes(context.Background(), "proj-1")
	if err != nil {
		t.Fatalf("ListDevBoxes: %v", err)
	}
	if len(boxes) != 1 || boxes[0].Ref != devBoxID || boxes[0].Status != "ready" {
		t.Errorf("dev boxes = %+v", boxes)
	}

	saved, err := a.SaveSnapshot(context.Background(), "proj-1", api.SaveSnapshotRequest{
		DevBoxRef: devBoxID, Name: "captured",
	})
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if saved.Name != "captured" || saved.State != "capturing" {
		t.Errorf("saved = %+v, want name=captured state=capturing", saved)
	}
	// The capture landed in the mock's catalog, so a re-list now sees it.
	snaps, err = a.ListSnapshots(context.Background(), "proj-1")
	if err != nil || len(snaps) != 2 {
		t.Errorf("post-capture snapshots = %+v (err %v), want 2", snaps, err)
	}
}

// A provider that exposes no catalog surfaces api.ErrNoSandboxCatalog, which the
// routes turn into a 404.
func TestSandboxCatalogAdapter_NoCatalog_ReturnsSentinel(t *testing.T) {
	a := &sandboxCatalogAdapter{resolver: &fakeProviderResolver{provider: bareProvider{}}}
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"ListSnapshots", func() error { _, err := a.ListSnapshots(context.Background(), "p"); return err }},
		{"ListDevBoxes", func() error { _, err := a.ListDevBoxes(context.Background(), "p"); return err }},
		{"SaveSnapshot", func() error {
			_, err := a.SaveSnapshot(context.Background(), "p", api.SaveSnapshotRequest{DevBoxRef: "x", Name: "y"})
			return err
		}},
	} {
		if err := call.run(); !errors.Is(err, api.ErrNoSandboxCatalog) {
			t.Errorf("%s error = %v, want api.ErrNoSandboxCatalog", call.name, err)
		}
	}
}

// A resolve failure propagates as a plain error (not the no-catalog sentinel), so
// the route reports 502 rather than hiding the picker.
func TestSandboxCatalogAdapter_ResolveError_Propagates(t *testing.T) {
	sentinel := errResolveBoom
	a := &sandboxCatalogAdapter{resolver: &fakeProviderResolver{err: sentinel}}
	_, err := a.ListSnapshots(context.Background(), "p")
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want the resolve error", err)
	}
	if errors.Is(err, api.ErrNoSandboxCatalog) {
		t.Error("a resolve failure must not be reported as no-catalog")
	}
}
