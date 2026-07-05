package agent_test

// Startup + periodic reconciliation (05 §4): adopt every provider worker
// matching a board slot, create only for slots with no live worker, destroy
// orphaned kiln-worker-* entries matching no slot. These tests use a
// hand-rolled Provider (not the mock) because they need to pre-seed provider
// state — worker adoption before Run ever calls CreateWorker — which
// mock.Provider's documented knobs (Script/FailProvisioning/FailStartTurns)
// have no affordance for (see report: contract gap).

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

type reconcileProvider struct {
	mu        sync.Mutex
	workers   []agent.ProviderWorker
	created   []string
	destroyed []string
}

func newReconcileProvider(seed ...agent.ProviderWorker) *reconcileProvider {
	return &reconcileProvider{workers: append([]agent.ProviderWorker(nil), seed...)}
}

func (p *reconcileProvider) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]agent.ProviderWorker(nil), p.workers...), nil
}

func (p *reconcileProvider) CreateWorker(ctx context.Context, name string) (agent.ProviderWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.created = append(p.created, name)
	w := agent.ProviderWorker{Name: name, Ref: name}
	p.workers = append(p.workers, w)
	return w, nil
}

func (p *reconcileProvider) WorkerReady(ctx context.Context, w agent.ProviderWorker) (bool, error) {
	return true, nil
}

func (p *reconcileProvider) DestroyWorker(ctx context.Context, w agent.ProviderWorker) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.destroyed = append(p.destroyed, w.Name)
	kept := p.workers[:0]
	for _, ww := range p.workers {
		if ww.Name != w.Name {
			kept = append(kept, ww)
		}
	}
	p.workers = kept
	return nil
}

func (p *reconcileProvider) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	return agent.TurnRef{Conversation: "c", Turn: "t"}, nil
}

func (p *reconcileProvider) CheckTurn(
	ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef,
) (agent.TurnStatus, error) {
	return agent.TurnStatus{Running: false, Output: "ok", IsError: false, CostUSD: 0}, nil
}

func (p *reconcileProvider) ReadLatestOutput(ctx context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	return agent.TurnOutput{}, nil
}

func (p *reconcileProvider) wasCreated(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.created, name)
}

func (p *reconcileProvider) wasDestroyed(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Contains(p.destroyed, name)
}

const (
	reconcileWorkerA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	reconcileWorkerB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

func TestRun_ReconcileCreatesOnlyMissingWorkers(t *testing.T) {
	provider := newReconcileProvider() // no live workers at all
	slots := &fakeSlots{ids: []string{reconcileWorkerA, reconcileWorkerB}}
	clock := testutil.NewFakeClock()
	svc := agent.NewService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool {
		return provider.wasCreated(agent.WorkerName(reconcileWorkerA)) &&
			provider.wasCreated(agent.WorkerName(reconcileWorkerB))
	})
}

func TestRun_ReconcileAdoptsExistingWorkersWithoutRecreating(t *testing.T) {
	existingName := agent.WorkerName(reconcileWorkerA)
	provider := newReconcileProvider(agent.ProviderWorker{Name: existingName, Ref: "already-provisioned"})
	slots := &fakeSlots{ids: []string{reconcileWorkerA, reconcileWorkerB}}
	clock := testutil.NewFakeClock()
	svc := agent.NewService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	// The slot with no live worker (B) must get one.
	testutil.Eventually(t, func() bool { return provider.wasCreated(agent.WorkerName(reconcileWorkerB)) })

	// Give a few more reconcile sweeps a chance to run, then confirm the
	// already-live worker (A) was never recreated — adopt-first (05 §4).
	time.Sleep(150 * time.Millisecond)
	if provider.wasCreated(existingName) {
		t.Errorf("adopt-first must not recreate a slot that already has a live worker"+
			" (05 §4), but CreateWorker(%q) was called", existingName)
	}
}

func TestRun_ReconcileDestroysOrphanedWorkers(t *testing.T) {
	validName := agent.WorkerName(reconcileWorkerA)
	orphanName := agent.WorkerNamePrefix + "no-such-slot"
	provider := newReconcileProvider(
		agent.ProviderWorker{Name: validName, Ref: "valid"},
		agent.ProviderWorker{Name: orphanName, Ref: "orphan"},
	)
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	svc := agent.NewService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return provider.wasDestroyed(orphanName) })

	if provider.wasDestroyed(validName) {
		t.Errorf("reconciliation must only destroy kiln-worker-* entries matching no board"+
			" slot (05 §4), but destroyed the still-valid %q", validName)
	}
}
