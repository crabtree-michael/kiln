package agent

// White-box unit tests for Service.SaveWorkerSnapshot — the executor behind
// board's agent.snapshot (05 §4, §6), the saved-sandbox counterpart to Release.
// Same-package so they can inspect the unexported worker cache, which the
// capture drops on the way out (the provider deletes the box it captured).
// These cover the four things the outbox depends on: the capture reaches the
// right sandbox, a redelivery does not capture twice, and the two "nothing to
// capture" facts come back as sentinels rather than as retryable failures.

import (
	"context"
	"errors"
	"sync"
	"testing"
)

var errCatalogUnreadable = errors.New("synthetic list-snapshots failure")

// sandboxRef is the slot's provider-side sandbox handle — what a capture must
// address, as distinct from the board slot id it is reached by.
const sandboxRef = "sandbox-ref-1"

// captureProvider is a resetProvider that also exposes a SandboxCatalog: it
// records every capture and serves a seeded snapshot list, so a test can drive
// both the first capture and the redelivery that must not repeat it.
type captureProvider struct {
	*resetProvider

	mu       sync.Mutex
	seeded   []Snapshot
	captured []SaveSnapshotRequest
	listErr  error
}

func newCaptureProvider(live ...ProviderWorker) *captureProvider {
	return &captureProvider{resetProvider: &resetProvider{live: live}}
}

func (p *captureProvider) ListSnapshots(context.Context) ([]Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listErr != nil {
		return nil, p.listErr
	}
	return append([]Snapshot(nil), p.seeded...), nil
}

func (p *captureProvider) ListDevBoxes(context.Context) ([]DevBox, error) { return nil, nil }

func (p *captureProvider) SaveSnapshot(_ context.Context, req SaveSnapshotRequest) (Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.captured = append(p.captured, req)
	snap := Snapshot{Ref: req.Name, Name: req.Name, Description: req.Description, State: SnapshotCapturing}
	p.seeded = append(p.seeded, snap)
	return snap, nil
}

func (p *captureProvider) captures() []SaveSnapshotRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SaveSnapshotRequest(nil), p.captured...)
}

// captureService wires the white-box single-tenant shape these tests want: one
// provider under the default prefix, every other port unused.
func captureService(p Provider) *Service {
	return NewService(nil, staticResolver{p, WorkerNamePrefix}, staticProjects{}, nil, nil, nil, nil)
}

// The capture reaches the sandbox behind the named slot — not some other box —
// and carries the name it was asked for, which is the name the caller derived
// so a redelivery can recognise it.
func TestSaveWorkerSnapshot_CapturesTheSandboxBehindTheSlot(t *testing.T) {
	const slot = "w1"
	worker := ProviderWorker{Name: WorkerName(slot), Ref: sandboxRef}
	other := ProviderWorker{Name: WorkerName("w2"), Ref: "sandbox-ref-2"}
	provider := newCaptureProvider(worker, other)
	svc := captureService(provider)

	snap, err := svc.SaveWorkerSnapshot(context.Background(), "", slot, "proj-20260809-120000", "from ticket t1")
	if err != nil {
		t.Fatalf("SaveWorkerSnapshot: %v", err)
	}
	if snap.Name != "proj-20260809-120000" {
		t.Errorf("captured snapshot name = %q, want the requested name", snap.Name)
	}
	got := provider.captures()
	if len(got) != 1 {
		t.Fatalf("captures = %+v, want exactly one", got)
	}
	if got[0].DevBoxRef != worker.Ref {
		t.Errorf("captured dev box = %q, want the slot's own sandbox %q", got[0].DevBoxRef, worker.Ref)
	}
	if got[0].Name != "proj-20260809-120000" || got[0].Description != "from ticket t1" {
		t.Errorf("capture request = %+v, want the caller's name and description", got[0])
	}
}

// The capture consumes its source — the provider scrubs the box and deletes it —
// so the slot's cached worker must not survive the call, or every later turn on
// that slot would be routed at a sandbox that is being deleted. Dropping it
// hands the slot to the reconciler to re-provision.
func TestSaveWorkerSnapshot_DropsTheCapturedWorkerFromTheCache(t *testing.T) {
	const slot = "w1"
	worker := ProviderWorker{Name: WorkerName(slot), Ref: sandboxRef}
	provider := newCaptureProvider(worker)
	svc := captureService(provider)
	svc.putWorker(worker)

	if _, err := svc.SaveWorkerSnapshot(context.Background(), "", slot, "proj-1", ""); err != nil {
		t.Fatalf("SaveWorkerSnapshot: %v", err)
	}
	if _, ok := svc.slotWorker(WorkerNamePrefix, slot); ok {
		t.Error("the captured worker must be dropped from the cache — the provider deletes the box it captured")
	}
}

// The outbox is at-least-once and the provider API has no idempotency key, so
// the derived name is the guard: a redelivery finds the capture that already ran
// and returns it instead of starting a second one.
func TestSaveWorkerSnapshot_RedeliveryDoesNotCaptureTwice(t *testing.T) {
	const slot, name = "w1", "proj-20260809-120000"
	provider := newCaptureProvider(ProviderWorker{Name: WorkerName(slot), Ref: sandboxRef})
	svc := captureService(provider)

	first, err := svc.SaveWorkerSnapshot(context.Background(), "", slot, name, "")
	if err != nil {
		t.Fatalf("first SaveWorkerSnapshot: %v", err)
	}
	second, err := svc.SaveWorkerSnapshot(context.Background(), "", slot, name, "")
	if err != nil {
		t.Fatalf("redelivered SaveWorkerSnapshot: %v", err)
	}
	if n := len(provider.captures()); n != 1 {
		t.Errorf("captures = %d, want 1 — a redelivery must not re-capture the workspace", n)
	}
	if second.Ref != first.Ref {
		t.Errorf("redelivery returned %q, want the already-captured %q", second.Ref, first.Ref)
	}
}

// A catalog read that fails says nothing about whether the capture ran, and
// losing the workspace is the worse way to be wrong — so the capture proceeds.
func TestSaveWorkerSnapshot_CapturesAnywayWhenTheCatalogCannotBeRead(t *testing.T) {
	provider := newCaptureProvider(ProviderWorker{Name: WorkerName("w1"), Ref: sandboxRef})
	provider.listErr = errCatalogUnreadable
	svc := captureService(provider)

	if _, err := svc.SaveWorkerSnapshot(context.Background(), "", "w1", "proj-1", ""); err != nil {
		t.Fatalf("SaveWorkerSnapshot: %v", err)
	}
	if n := len(provider.captures()); n != 1 {
		t.Errorf("captures = %d, want 1 — an unreadable catalog must not skip the capture", n)
	}
}

// A provider with no snapshot catalog has no workspace image to save. That is a
// fact about the provider, not a transient failure, so it comes back as
// ErrNoCatalog for the caller to report and stop on.
func TestSaveWorkerSnapshot_NoCatalogIsTerminal(t *testing.T) {
	provider := &resetProvider{live: []ProviderWorker{{Name: WorkerName("w1"), Ref: "r1"}}}
	svc := captureService(provider)

	if _, err := svc.SaveWorkerSnapshot(context.Background(), "", "w1", "proj-1", ""); !errors.Is(err, ErrNoCatalog) {
		t.Fatalf("SaveWorkerSnapshot on a catalog-less provider = %v, want ErrNoCatalog", err)
	}
}

// A slot whose sandbox is already gone has nothing left to capture — also
// terminal, and distinctly so, since the two say different things to an operator.
func TestSaveWorkerSnapshot_NoLiveWorkerIsTerminal(t *testing.T) {
	svc := captureService(newCaptureProvider())

	if _, err := svc.SaveWorkerSnapshot(context.Background(), "", "w1", "proj-1", ""); !errors.Is(err, ErrNoLiveWorker) {
		t.Fatalf("SaveWorkerSnapshot with no live sandbox = %v, want ErrNoLiveWorker", err)
	}
}
