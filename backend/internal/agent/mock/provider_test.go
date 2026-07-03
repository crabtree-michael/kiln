package mock_test

// The mock Provider must simulate instant lifecycle, scripted turns, and the
// injectable failure modes spec 05 §8 lists — the exact knobs the Service's
// own unit tests script against. These tests pin the mock's contract
// directly, independent of Service.

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
)

func pollUntilTerminal(
	ctx context.Context, t *testing.T, p *mock.Provider, w agent.ProviderWorker, ref agent.TurnRef,
) agent.TurnStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := p.CheckTurn(ctx, w, ref)
		if err != nil {
			t.Fatalf("CheckTurn: %v", err)
		}
		if !status.Running {
			return status
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("turn never reached a terminal state within the test budget")
	return agent.TurnStatus{}
}

func TestInstantLifecycle(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	name := agent.WorkerName("11111111-1111-1111-1111-111111111111")

	w, err := p.CreateWorker(ctx, name)
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	if w.Name != name {
		t.Errorf("CreateWorker name = %q, want %q", w.Name, name)
	}

	ready, err := p.WorkerReady(ctx, w)
	if err != nil || !ready {
		t.Fatalf("mock lifecycle must be instant — WorkerReady immediately true (05 §8), got ready=%v err=%v", ready, err)
	}

	list, err := p.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if !containsWorker(list, name) {
		t.Errorf("ListWorkers must include a just-created worker, got %+v", list)
	}

	if destroyErr := p.DestroyWorker(ctx, w); destroyErr != nil {
		t.Fatalf("DestroyWorker: %v", destroyErr)
	}

	list, err = p.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers after destroy: %v", err)
	}
	if containsWorker(list, name) {
		t.Errorf("a destroyed worker must not still be listed, got %+v", list)
	}

	// An already-absent worker is success (05 §2.3 Provider doc: "absent
	// worker = success").
	if destroyErr := p.DestroyWorker(ctx, w); destroyErr != nil {
		t.Errorf("destroying an already-absent worker must succeed, got %v", destroyErr)
	}
}

func containsWorker(list []agent.ProviderWorker, name string) bool {
	for _, w := range list {
		if w.Name == name {
			return true
		}
	}
	return false
}

func TestScriptedTurnReturnsTheConfiguredResult(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	w, err := p.CreateWorker(ctx, agent.WorkerName("w1"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	p.Script = map[string]mock.ScriptedTurn{
		"deploy the fix": {Output: "deployed", IsError: false, Delay: 5 * time.Millisecond},
	}

	ref, err := p.StartTurn(ctx, w, "", "deploy the fix", true)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	status := pollUntilTerminal(ctx, t, p, w, ref)
	if status.IsError || status.Output != "deployed" {
		t.Errorf("scripted turn result not honored (05 §8): got %+v", status)
	}
}

func TestUnscriptedMessageDefaultsToCannedSuccess(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	w, err := p.CreateWorker(ctx, agent.WorkerName("w2"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	ref, err := p.StartTurn(ctx, w, "", "no script covers this message", true)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	status := pollUntilTerminal(ctx, t, p, w, ref)
	if status.IsError {
		t.Errorf("an unscripted message must default to a canned success after"+
			" DefaultTurnDelay (05 §8), got is_error=true output=%q", status.Output)
	}
}

func TestFailProvisioningMakesCreateWorkerFailTerminally(t *testing.T) {
	p := mock.New()
	p.FailProvisioning = true
	if _, err := p.CreateWorker(context.Background(), agent.WorkerName("w3")); err == nil {
		t.Fatal("FailProvisioning=true must make CreateWorker fail (05 §8)")
	}
}

func TestFailStartTurnsFailsNTimesThenSucceeds(t *testing.T) {
	p := mock.New()
	p.FailStartTurns = 2
	ctx := context.Background()
	w, err := p.CreateWorker(ctx, agent.WorkerName("w4"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	for i := range 2 {
		if _, startErr := p.StartTurn(ctx, w, "", "retry me", true); startErr == nil {
			t.Fatalf("StartTurn attempt %d should still be failing (FailStartTurns=2)", i+1)
		}
	}
	if _, err := p.StartTurn(ctx, w, "", "retry me", true); err != nil {
		t.Fatalf("StartTurn must succeed once FailStartTurns is exhausted (05 §8), got %v", err)
	}
}

func TestDropConversationSurfacesOnContinuation(t *testing.T) {
	p := mock.New()
	ctx := context.Background()
	w, err := p.CreateWorker(ctx, agent.WorkerName("w5"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	ref, err := p.StartTurn(ctx, w, "", "first message", true)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	pollUntilTerminal(ctx, t, p, w, ref)

	p.DropConversation(w.Name)

	if _, startErr := p.StartTurn(ctx, w, ref.Conversation, "continue please", false); startErr == nil {
		t.Error("continuing a dropped conversation must surface an error so the" +
			" Service can fall back to a fresh one (05 §3, §8)")
	}
}
