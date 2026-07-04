package agent_test

// Send/Release must record intent in agent_turns and return — never block
// on the provider, and never create a second turn for a repeated outbox id
// (05 §2.1, §7, D2). These tests never call Service.Run: they pin down the
// port-call contract in isolation from the machine that later advances it.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// blockingProvider hangs on every method until closeBlock is closed (never,
// in these tests) — any Send/Release that actually calls the provider
// synchronously will hang past the timeout below, proving record-and-return
// was violated.
type blockingProvider struct {
	block chan struct{}
	calls atomic.Int64
}

func newBlockingProvider() *blockingProvider {
	return &blockingProvider{block: make(chan struct{})}
}

func (p *blockingProvider) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	p.calls.Add(1)
	<-p.block
	return nil, nil
}

func (p *blockingProvider) CreateWorker(ctx context.Context, name string) (agent.ProviderWorker, error) {
	p.calls.Add(1)
	<-p.block
	return agent.ProviderWorker{}, nil
}

func (p *blockingProvider) WorkerReady(ctx context.Context, w agent.ProviderWorker) (bool, error) {
	p.calls.Add(1)
	<-p.block
	return false, nil
}

func (p *blockingProvider) DestroyWorker(ctx context.Context, w agent.ProviderWorker) error {
	p.calls.Add(1)
	<-p.block
	return nil
}

func (p *blockingProvider) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	p.calls.Add(1)
	<-p.block
	return agent.TurnRef{}, nil
}

func (p *blockingProvider) CheckTurn(
	ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef,
) (agent.TurnStatus, error) {
	p.calls.Add(1)
	<-p.block
	return agent.TurnStatus{}, nil
}

func (p *blockingProvider) ReadLatestOutput(ctx context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	p.calls.Add(1)
	<-p.block
	return agent.TurnOutput{}, nil
}

func (p *blockingProvider) callCount() int64 { return p.calls.Load() }

const nonBlockingBudget = 200 * time.Millisecond

func sendPayload(t *testing.T, ticketID, workerID, message string) []byte {
	t.Helper()
	b, err := json.Marshal(agent.SendPayload{TicketID: ticketID, WorkerID: workerID, Message: message})
	if err != nil {
		t.Fatalf("marshal SendPayload: %v", err)
	}
	return b
}

func releasePayload(t *testing.T, workerID string) []byte {
	t.Helper()
	b, err := json.Marshal(agent.ReleasePayload{WorkerID: workerID})
	if err != nil {
		t.Fatalf("marshal ReleasePayload: %v", err)
	}
	return b
}

func TestSend_DoesNotBlockOnTheProvider(t *testing.T) {
	provider := newBlockingProvider()
	svc := agent.NewService(newFakeStore(), provider, &fakeEvents{}, &fakeSlots{}, newFakeClock())

	done := make(chan error, 1)
	go func() {
		done <- svc.Send(context.Background(), 1, sendPayload(t, "ticket-1", "worker-1", "hello"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send returned an error: %v", err)
		}
	case <-time.After(nonBlockingBudget):
		t.Fatal("Send blocked past its budget — record-and-return must never wait on the provider (05 §2.1, D2)")
	}

	if n := provider.callCount(); n != 0 {
		t.Fatalf("Send must not call the Provider at all — it only records intent;"+
			" the machine advances it (05 D2). Provider was called %d time(s)", n)
	}
}

func TestRelease_DoesNotBlockOnTheProvider(t *testing.T) {
	provider := newBlockingProvider()
	svc := agent.NewService(newFakeStore(), provider, &fakeEvents{}, &fakeSlots{}, newFakeClock())

	done := make(chan error, 1)
	go func() {
		done <- svc.Release(context.Background(), 1, releasePayload(t, "worker-1"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Release returned an error: %v", err)
		}
	case <-time.After(nonBlockingBudget):
		t.Fatal("Release blocked past its budget — record-and-return must never wait on the provider (05 §2.1, §4, D2)")
	}

	if n := provider.callCount(); n != 0 {
		t.Fatalf("Release must not call the Provider synchronously — destroy+recreate is"+
			" the machine's job (05 §4). Provider was called %d time(s)", n)
	}
}

func TestSend_RecordsTheDecodedPayload(t *testing.T) {
	store := newFakeStore()
	svc := agent.NewService(store, newBlockingProvider(), &fakeEvents{}, &fakeSlots{}, newFakeClock())

	if err := svc.Send(context.Background(), 7, sendPayload(t, "ticket-9", "worker-9", "fix the flaky test")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	row, ok := store.get(7)
	if !ok {
		t.Fatal("Send must record a Turn in the store keyed by the outbox id (05 §2.1, §7)")
	}
	if row.Kind != agent.KindSend {
		t.Errorf("recorded Kind = %q, want %q", row.Kind, agent.KindSend)
	}
	if row.TicketID != "ticket-9" || row.WorkerID != "worker-9" || row.Message != "fix the flaky test" {
		t.Errorf("recorded Turn does not match the decoded SendPayload: %+v", row)
	}
	if row.Phase.Terminal() {
		t.Errorf("a freshly recorded Send must not already be terminal, got phase=%v", row.Phase)
	}
}

func TestRelease_RecordsTheDecodedPayload(t *testing.T) {
	store := newFakeStore()
	svc := agent.NewService(store, newBlockingProvider(), &fakeEvents{}, &fakeSlots{}, newFakeClock())

	if err := svc.Release(context.Background(), 8, releasePayload(t, "worker-9")); err != nil {
		t.Fatalf("Release: %v", err)
	}

	row, ok := store.get(8)
	if !ok {
		t.Fatal("Release must record a Turn in the store keyed by the outbox id (05 §2.1, §7)")
	}
	if row.Kind != agent.KindRelease {
		t.Errorf("recorded Kind = %q, want %q", row.Kind, agent.KindRelease)
	}
	if row.WorkerID != "worker-9" {
		t.Errorf("recorded WorkerID = %q, want %q", row.WorkerID, "worker-9")
	}
	if row.TicketID != "" {
		t.Errorf("a release operation has no ticket, got TicketID=%q", row.TicketID)
	}
}

func TestSend_DuplicateIdempotencyKeyIsSilentSuccessWithNoSecondTurn(t *testing.T) {
	store := newFakeStore()
	svc := agent.NewService(store, newBlockingProvider(), &fakeEvents{}, &fakeSlots{}, newFakeClock())

	payload := sendPayload(t, "ticket-1", "worker-1", "hello")
	if err := svc.Send(context.Background(), 42, payload); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := svc.Send(context.Background(), 42, payload); err != nil {
		t.Fatalf("a repeated outbox id must be a silent success (04 §3), got error: %v", err)
	}

	if n := store.count(); n != 1 {
		t.Fatalf("a duplicate Send with the same idempotency key must not create a second turn, got %d rows", n)
	}
	if n := store.recordCallCount(42); n != 2 {
		t.Fatalf("Store.Record must still be consulted on the duplicate call"+
			" (that's the dedupe check itself), got %d calls", n)
	}
}

func TestRelease_DuplicateIdempotencyKeyIsSilentSuccessWithNoSecondTurn(t *testing.T) {
	store := newFakeStore()
	svc := agent.NewService(store, newBlockingProvider(), &fakeEvents{}, &fakeSlots{}, newFakeClock())

	payload := releasePayload(t, "worker-1")
	if err := svc.Release(context.Background(), 43, payload); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := svc.Release(context.Background(), 43, payload); err != nil {
		t.Fatalf("a repeated outbox id must be a silent success (04 §3), got error: %v", err)
	}

	if n := store.count(); n != 1 {
		t.Fatalf("a duplicate Release with the same idempotency key must not create a second turn, got %d rows", n)
	}
}
