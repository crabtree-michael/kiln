package agent_test

// The turn state machine (05 §5): recorded -> worker_ready -> turn_started ->
// done/failed, advanced only by Run's poller — never inside Send/Release —
// and always ending in exactly one agent.turn_completed event, even when the
// outcome is a mechanical failure (05 §2.2, D3). These tests drive Service
// through Send/Release + Run against the mock Provider and a fake Clock.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

const (
	testWorkerID = "11111111-1111-1111-1111-111111111111"
	testTicketID = "22222222-2222-2222-2222-222222222222"
)

// startTurnCall records one Provider.StartTurn invocation as seen from the
// outside, including what the provider handed back — enough to assert
// first-message-vs-continuation (05 §2.1, §3) without reaching into Service
// internals.
type startTurnCall struct {
	worker          string
	conversationIn  string
	message         string
	fresh           bool
	conversationOut string
	err             error
}

// recordingProvider wraps a real agent.Provider (the mock) and records every
// StartTurn call; everything else delegates untouched via embedding.
type recordingProvider struct {
	agent.Provider

	mu    sync.Mutex
	calls []startTurnCall
}

func (r *recordingProvider) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	ref, err := r.Provider.StartTurn(ctx, w, conversation, message, fresh)
	r.mu.Lock()
	r.calls = append(r.calls, startTurnCall{
		worker: w.Name, conversationIn: conversation, message: message,
		fresh: fresh, conversationOut: ref.Conversation, err: err,
	})
	r.mu.Unlock()
	if err != nil {
		return ref, fmt.Errorf("recordingProvider: %w", err)
	}
	return ref, nil
}

func (r *recordingProvider) startTurnCalls() []startTurnCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]startTurnCall(nil), r.calls...)
}

func newHarness(workerIDs ...string) (*fakeStore, *fakeEvents, *fakeSlots, *testutil.FakeClock) {
	return newFakeStore(), &fakeEvents{}, &fakeSlots{ids: workerIDs}, testutil.NewFakeClock()
}

func TestRun_FreshSendCompletesAndEmitsTurnCompleted(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	sendMsg := sendPayload(t, testTicketID, testWorkerID, "implement the feature")
	if err := svc.Send(context.Background(), 1, sendMsg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed event, got %d: %+v", len(tcs), tcs)
	}
	got := tcs[0]
	if got.TicketID != testTicketID || got.WorkerID != testWorkerID {
		t.Errorf("turn_completed identity mismatch: got %+v", got)
	}
	if got.IsError {
		t.Errorf("a successful turn must not be reported as is_error: %+v", got)
	}

	row, ok := store.get(1)
	if !ok || row.Phase != agent.PhaseDone {
		t.Errorf("the turn must rest at phase=done once its event is enqueued (05 §5), got ok=%v phase=%v", ok, row.Phase)
	}

	calls := provider.startTurnCalls()
	if len(calls) == 0 {
		t.Fatal("Run must have called Provider.StartTurn to advance the machine")
	}
	if !calls[0].fresh {
		t.Errorf("the first Send after a worker is (re)created must start a fresh" +
			" conversation (05 §2.1, §3), got fresh=false")
	}
}

func TestRun_TurnCompletedPayloadCarriesNoProviderHandles(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "ship it")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	raws := events.rawPayloads(agent.EventTurnCompleted)
	if len(raws) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed payload, got %d", len(raws))
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raws[0], &m); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	want := map[string]bool{"ticket_id": true, "worker_id": true, "is_error": true, "output": true, "cost_usd": true}
	if len(m) != len(want) {
		t.Fatalf("turn_completed payload must carry exactly {ticket_id, worker_id, is_error,"+
			" output, cost_usd} — no provider handles (05 §2.2), got keys %v", keysOf(m))
	}
	for k := range m {
		if !want[k] {
			t.Errorf("unexpected key %q leaked into the turn_completed payload — provider"+
				" handles must never appear here (05 §2.2)", k)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRun_ProvisioningFailureStillEmitsErrorTurnCompletedAndReachesDone(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: &mock.Provider{FailProvisioning: true}}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "do the thing")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed event even for a mechanical failure (05 D3), got %d", len(tcs))
	}
	if !tcs[0].IsError {
		t.Errorf("a terminal provisioning failure must be reported as is_error=true (05 §2.2, D3), got %+v", tcs[0])
	}
	if tcs[0].TicketID != testTicketID {
		t.Errorf("failure event must still carry the ticket id, got %+v", tcs[0])
	}

	row, ok := store.get(1)
	if !ok || row.Phase != agent.PhaseDone {
		t.Errorf("failed is not a resting state — the machine must still land at done once"+
			" its error event fires (05 §5: failed -> done), got ok=%v phase=%v", ok, row.Phase)
	}
}

func TestRun_TerminalTurnErrorStillEmitsEventAndReachesDone(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	p := mock.New()
	msg := "run the risky migration"
	p.Script = map[string]mock.ScriptedTurn{
		msg: {Output: "migration failed: constraint violation", IsError: true, Delay: 5 * time.Millisecond},
	}
	provider := &recordingProvider{Provider: p}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, msg)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed event, got %d", len(tcs))
	}
	if !tcs[0].IsError || tcs[0].Output != "migration failed: constraint violation" {
		t.Errorf("a CheckTurn terminal error must surface as is_error=true with the"+
			" agent's own output (05 §5), got %+v", tcs[0])
	}

	row, ok := store.get(1)
	if !ok || row.Phase != agent.PhaseDone {
		t.Errorf("want phase=done after the error event, got ok=%v phase=%v", ok, row.Phase)
	}
}

func TestRun_TransientStartTurnFailuresRetryThenSucceed(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: &mock.Provider{FailStartTurns: 3}}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "retry me")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed event, got %d", len(tcs))
	}
	if tcs[0].IsError {
		t.Errorf("a transient StartTurn failure (FailStartTurns=3, well under the 8-attempt"+
			" budget, 04 §3) must retry inside the machine and eventually succeed,"+
			" got is_error=true output=%q", tcs[0].Output)
	}
}

func TestRun_StartTurnExhaustingRetryBudgetBecomesFailed(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: &mock.Provider{FailStartTurns: 1000}}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "never works")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 {
		t.Fatalf("want exactly 1 agent.turn_completed event once the retry budget is exhausted, got %d", len(tcs))
	}
	if !tcs[0].IsError {
		t.Errorf("exhausting the retry budget must terminate as is_error=true (05 §5), got %+v", tcs[0])
	}

	row, ok := store.get(1)
	if !ok {
		t.Fatal("turn row missing after completion")
	}
	if row.Attempts < 2 {
		t.Errorf("exhausting a retry budget implies more than one attempt was recorded, got Attempts=%d", row.Attempts)
	}
}

func TestRun_ContinuationSendReusesTheRecordedConversation(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	firstMsg := sendPayload(t, testTicketID, testWorkerID, "first message")
	if err := svc.Send(context.Background(), 1, firstMsg); err != nil {
		t.Fatalf("Send #1: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	secondMsg := sendPayload(t, testTicketID, testWorkerID, "follow-up message")
	if err := svc.Send(context.Background(), 2, secondMsg); err != nil {
		t.Fatalf("Send #2: %v", err)
	}

	testutil.Eventually(t, func() bool { return events.count() >= 2 })

	calls := provider.startTurnCalls()
	if len(calls) < 2 {
		t.Fatalf("want at least 2 StartTurn calls (one per Send), got %d: %+v", len(calls), calls)
	}
	first, second := calls[0], calls[1]
	if !first.fresh {
		t.Errorf("first Send on a new worker must be fresh, got %+v", first)
	}
	if second.fresh {
		t.Errorf("a later Send to the same worker must continue the conversation, not"+
			" start fresh (05 §2.1, §3), got %+v", second)
	}
	if first.conversationOut == "" || second.conversationIn != first.conversationOut {
		t.Errorf("a continuation must pass forward the previous turn's recorded conversation"+
			" handle (05 §6 'the recorded session_id', generalized at the port level):"+
			" first returned %q, second sent %q", first.conversationOut, second.conversationIn)
	}
}

func TestRun_ReleaseThenSendStartsFreshConversationAgain(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "first message")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	if err := svc.Release(context.Background(), 2, releasePayload(t, testWorkerID)); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Release has no CheckTurn to observe; wait for its own machine row to
	// settle instead of counting turn_completed events (05 §5: release uses
	// only recorded -> done/failed, destroy then recreate-to-ready, no turn).
	testutil.Eventually(t, func() bool {
		row, ok := store.get(2)
		return ok && row.Phase == agent.PhaseDone
	})

	if err := svc.Send(context.Background(), 3, sendPayload(t, testTicketID, testWorkerID, "second message")); err != nil {
		t.Fatalf("second Send: %v", err)
	}

	testutil.Eventually(t, func() bool { return len(provider.startTurnCalls()) >= 2 })

	calls := provider.startTurnCalls()
	last := calls[len(calls)-1]
	if !last.fresh {
		t.Errorf("a Send after a Release must start a fresh conversation — no row, or a" +
			" release row, means fresh (05 §2.1, §3) — got fresh=false")
	}
}

func TestRun_ConversationLossFallsBackToFreshConversationNeverFailingTheTicket(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	p := mock.New()
	provider := &recordingProvider{Provider: p}
	svc := newService(store, provider, events, slots, clock, nil)

	msg := "please fix the flaky test"
	firstSend := sendPayload(t, testTicketID, testWorkerID, msg)
	if err := svc.Send(context.Background(), 1, firstSend); err != nil {
		t.Fatalf("Send #1: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	p.DropConversation(agent.WorkerName(testWorkerID))

	secondSend := sendPayload(t, testTicketID, testWorkerID, msg)
	if err := svc.Send(context.Background(), 2, secondSend); err != nil {
		t.Fatalf("Send #2: %v", err)
	}

	testutil.Eventually(t, func() bool { return events.count() >= 2 })

	tcs := events.turnCompletedEvents(t)
	last := tcs[len(tcs)-1]
	if last.IsError {
		t.Errorf("a lost provider conversation must fall back to a fresh conversation and"+
			" never fail the ticket (05 §3), got is_error=true output=%q", last.Output)
	}

	foundFreshRetryWithSameMessage := false
	for _, c := range provider.startTurnCalls()[1:] {
		if c.fresh && c.message == msg {
			foundFreshRetryWithSameMessage = true
		}
	}
	if !foundFreshRetryWithSameMessage {
		t.Errorf("expected a fresh-conversation retry carrying the original message after the"+
			" provider lost the conversation (05 §3: context lost, workspace kept, same"+
			" message resent), calls=%+v", provider.startTurnCalls())
	}
}

func TestRun_RecoversNonTerminalRowsOnStartWithoutSend(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	// Simulate a crash: a row was recorded (e.g. by a Send before the
	// crash) but never advanced. Recovery must be nothing but reading this
	// back and continuing it (05 §7) — Send/Release are never called here.
	store.seed(agent.Turn{
		IdempotencyKey: 99,
		Kind:           agent.KindSend,
		TicketID:       testTicketID,
		WorkerID:       testWorkerID,
		Message:        "never got past recorded before the crash",
		Phase:          agent.PhaseRecorded,
	})

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return events.count() >= 1 })

	tcs := events.turnCompletedEvents(t)
	if len(tcs) != 1 || tcs[0].TicketID != testTicketID || tcs[0].IsError {
		t.Fatalf("Run must recover and complete a pre-seeded non-terminal row with no"+
			" Send call (05 §7 recovery), got %+v", tcs)
	}
}

// TestRun_CrashBetweenEmitAndPhaseDoneEmitsExactlyOnce is the direct regression
// for the completion-transactionality gap (architecture audit 3.1). The
// completion emit and the phase→done write are separate steps; a crash after
// the emit but before the write re-runs stepCheckTurn on the still-terminal
// turn and re-emits agent.turn_completed. With the emitting turn's outbox id as
// the events idempotency key, the redelivery is a no-op — so the machine still
// settles at done AND the brain sees exactly one completion.
func TestRun_CrashBetweenEmitAndPhaseDoneEmitsExactlyOnce(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	store.failDoneUpdateOnce = true // drop the first phase→done write, after the emit
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "ship it")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	// The turn must still reach done despite the dropped write — the poller
	// re-runs the terminal turn and the retried phase→done commits.
	testutil.Eventually(t, func() bool {
		row, ok := store.get(1)
		return ok && row.Phase == agent.PhaseDone
	})

	if tcs := events.turnCompletedEvents(t); len(tcs) != 1 {
		t.Fatalf("a crash between the completion emit and the phase→done write must still"+
			" yield exactly one agent.turn_completed event (architecture audit 3.1), got %d: %+v",
			len(tcs), tcs)
	}
}

// TestRun_TransientEmitFailureRetriesThenCompletesOnce covers the other half of
// transactional completion: a failed emit must NOT settle the turn at done (or
// the completion would be lost), and the eventual retry must not double-admit.
// The turn stays at turn_started across the failing emits, then completes with
// exactly one event once the events queue accepts it.
func TestRun_TransientEmitFailureRetriesThenCompletesOnce(t *testing.T) {
	store, events, slots, clock := newHarness(testWorkerID)
	events.failN = 2 // two transient events-DB outages before the emit lands
	provider := &recordingProvider{Provider: mock.New()}
	svc := newService(store, provider, events, slots, clock, nil)

	if err := svc.Send(context.Background(), 1, sendPayload(t, testTicketID, testWorkerID, "ship it")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool {
		row, ok := store.get(1)
		return ok && row.Phase == agent.PhaseDone
	})

	if tcs := events.turnCompletedEvents(t); len(tcs) != 1 {
		t.Fatalf("a transient emit failure must neither drop nor double the completion;"+
			" want exactly 1 agent.turn_completed event, got %d: %+v", len(tcs), tcs)
	}
}
