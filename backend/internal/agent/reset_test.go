package agent

// White-box unit tests for Service.Reset (the developer "fresh session" reset):
// it destroys every live kiln-worker-* sandbox, leaves non-kiln sandboxes
// alone, keeps going past a per-worker destroy failure, and empties the
// in-memory worker cache. Same-package so it can inspect the unexported cache;
// Reset touches only the provider and the cache, so the other ports are nil.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

var errSyntheticDestroy = errors.New("synthetic destroy failure")

// resetProvider is a minimal Provider that records destroy calls and can be
// told to fail one worker's destroy.
type resetProvider struct {
	mu        sync.Mutex
	live      []ProviderWorker
	destroyed []string
	failOn    string // DestroyWorker of this name returns an error
}

func (p *resetProvider) ListWorkers(context.Context) ([]ProviderWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ProviderWorker(nil), p.live...), nil
}

func (p *resetProvider) CreateWorker(_ context.Context, name string) (ProviderWorker, error) {
	return ProviderWorker{Name: name}, nil
}

func (p *resetProvider) WorkerReady(context.Context, ProviderWorker) (bool, error) { return true, nil }

func (p *resetProvider) DestroyWorker(_ context.Context, w ProviderWorker) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w.Name == p.failOn {
		return errSyntheticDestroy
	}
	p.destroyed = append(p.destroyed, w.Name)
	return nil
}

func (p *resetProvider) StartTurn(context.Context, ProviderWorker, string, string, bool) (TurnRef, error) {
	return TurnRef{}, nil
}

func (p *resetProvider) CheckTurn(context.Context, ProviderWorker, TurnRef) (TurnStatus, error) {
	return TurnStatus{}, nil
}

func (p *resetProvider) ReadLatestOutput(context.Context, ProviderWorker) (TurnOutput, error) {
	return TurnOutput{}, nil
}

func (s *Service) cacheSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.workers)
}

func TestReset_DestroysKilnWorkersAndClearsCache(t *testing.T) {
	kilnA, kilnB := WorkerName("aaaa"), WorkerName("bbbb")
	provider := &resetProvider{live: []ProviderWorker{
		{Name: kilnA, Ref: "ra"},
		{Name: kilnB, Ref: "rb"},
		{Name: "unrelated-sandbox", Ref: "ru"}, // not kiln-worker-*: must be left alone
	}}
	svc := NewService(nil, provider, nil, nil, nil)
	svc.putWorker(ProviderWorker{Name: kilnA, Ref: "ra"})
	svc.putWorker(ProviderWorker{Name: kilnB, Ref: "rb"})

	if err := svc.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if !slices.Contains(provider.destroyed, kilnA) || !slices.Contains(provider.destroyed, kilnB) {
		t.Errorf("both kiln workers should be destroyed, got %v", provider.destroyed)
	}
	if slices.Contains(provider.destroyed, "unrelated-sandbox") {
		t.Errorf("non-kiln sandbox must not be destroyed, got %v", provider.destroyed)
	}
	if n := svc.cacheSize(); n != 0 {
		t.Errorf("worker cache should be empty after reset, still has %d", n)
	}
}

func TestReset_ContinuesPastDestroyError(t *testing.T) {
	kilnA, kilnB := WorkerName("aaaa"), WorkerName("bbbb")
	provider := &resetProvider{
		live:   []ProviderWorker{{Name: kilnA}, {Name: kilnB}},
		failOn: kilnA,
	}
	svc := NewService(nil, provider, nil, nil, nil)

	if err := svc.Reset(context.Background()); err != nil {
		t.Fatalf("Reset should be best-effort, got err: %v", err)
	}
	if !slices.Contains(provider.destroyed, kilnB) {
		t.Errorf("kilnB should still be destroyed despite kilnA failing, got %v", provider.destroyed)
	}
}
