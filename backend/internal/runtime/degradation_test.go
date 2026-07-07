package runtime_test

// Cross-tenant degradation (11 §3, spec §8): one project's brain outage must
// not stall another project's events. The events queue is shared, but the
// per-project brain resolution means a project whose brain won't resolve
// dead-letters its own event feed-visibly (a system-error Say + marked done,
// no retry storm), while a healthy project's event on the same queue keeps
// processing normally. This is the isolation sibling of
// service_test.go's single-project BrainResolutionFailureSaysAndMarksDone.

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// TestService_EventsWorker_BrainOutageDegradesOnlyTheFailingTenant seeds one
// event for a project whose brain won't resolve (proj-A) and one for a healthy
// project (proj-B) onto the same events queue, then proves the outage is
// contained to A: A's event is marked done after a single attempt with a
// feed-visible system Say to A only and no brain invocation, while B's event
// is handled by its brain exactly once with no spurious Say — a genuine
// tenant-scoped degradation, not a queue-wide stall.
func TestService_EventsWorker_BrainOutageDegradesOnlyTheFailingTenant(t *testing.T) {
	const projA = "proj-A-degraded"
	const projB = "proj-B-healthy"

	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	brainB := &fakeBrain{} // resolves only for the healthy project.
	resolver := &fakeBrainResolver{
		forFn: func(_ context.Context, projectID string) (runtime.Brain, error) {
			if projectID == projA {
				return nil, errStoreFailed // A's brain is down (bad/decrypt-fail config).
			}
			return brainB, nil
		},
	}
	sayer := &fakeSayPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolver, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, nil, &fakeSnapshotPusher{}, sayer,
		&fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{},
		&fakeOwner{},
	)

	eventsWorker, _ := svc.Workers(clock)
	idA := store.seedProject(runtime.QueueEvents, projA, string(runtime.EventHumanMessage), []byte(`{"text":"a"}`), 0)
	idB := store.seedProject(runtime.QueueEvents, projB, string(runtime.EventHumanMessage), []byte(`{"text":"b"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	// Both events must reach the terminal 'done' status — A via the degradation
	// path, B via a normal brain pass. Neither may wedge the shared queue.
	testutil.Eventually(t, func() bool {
		return store.status(runtime.QueueEvents, idA) == statusDone &&
			store.status(runtime.QueueEvents, idB) == statusDone
	})
	time.Sleep(20 * time.Millisecond) // let any (erroneous) extra work settle.

	// A: contained failure — one attempt, no retry, no brain call.
	if got := store.attempts(idA); got != 1 {
		t.Errorf("proj-A attempts = %d, want 1 — a resolution outage must not retry (no retry storm, 11 §3)", got)
	}
	if got := len(store.retryCallsFor(idA)); got != 0 {
		t.Errorf("proj-A MarkRetry called %d times, want 0", got)
	}

	// B: healthy — its brain handled exactly its one event, and nothing else.
	handled := brainB.callsFor("HandleEvent")
	if len(handled) != 1 {
		t.Fatalf("healthy brain HandleEvent called %d times, want exactly 1 (only proj-B's event)", len(handled))
	}
	ev, ok := handled[0].Args[0].(runtime.Event)
	if !ok {
		t.Fatalf("HandleEvent arg = %T, want runtime.Event", handled[0].Args[0])
	}
	if ev.ProjectID != projB {
		t.Errorf("healthy brain saw Event.ProjectID = %q, want %q — A's event must never reach B's brain", ev.ProjectID, projB)
	}

	// The system-error Say landed on the failing project only, kiln-authored,
	// non-empty — the user sees why their event went nowhere, and the healthy
	// tenant's stream stays clean.
	pushed := sayer.pushedMessages()
	if len(pushed) != 1 {
		t.Fatalf("PushSay called %d times, want exactly 1 (the degraded tenant's system-error Say only)", len(pushed))
	}
	if pushed[0].projectID != projA {
		t.Errorf("system-error Say pushed to project %q, want %q (the failing tenant only)", pushed[0].projectID, projA)
	}
	if pushed[0].m.Text == "" || pushed[0].m.Role != runtime.RoleKiln {
		t.Errorf("system-error Say = %+v, want a non-empty kiln-authored message", pushed[0].m)
	}
}
