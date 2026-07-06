package runtime_test

// Service unit tests: EnqueueEvent/PostMessage/Say/Recent's delegation
// contracts (04 §6, 07 §3), and Workers(clock)'s wiring — events -> Brain
// exactly once per event, and the outbox's per-topic executor routing plus
// the exhausted-agent.send -> MarkBlocked dead-letter action (04 §2-§3).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

var errStoreFailed = errors.New("fakeMessageStore: synthetic failure")

// newTestService builds a Service over the 04/07 ports, defaulting the 08 §7
// ports (notifications, board reader, feed/activity pushers) to empty fakes so
// the existing call sites stay unchanged. Tests that exercise the feed or
// activity paths call runtime.NewService directly with their own fakes.
func newTestService(
	store *fakeStore, messages *fakeMessageStore, brain *fakeBrain, puller *fakePuller,
	blocker *fakeBlocker, agents *fakeAgentRuntime, notifier *fakeNotifier,
	pusher *fakeSnapshotPusher, sayer *fakeSayPusher,
) *runtime.Service {
	return runtime.NewService(
		store, messages, brain, puller, blocker, agents, notifier, nil, pusher, sayer,
		&fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{},
	)
}

// ---- EnqueueEvent (04 §6) --------------------------------------------------

func TestService_EnqueueEvent_InsertsIntoStore(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	svc := newTestService(store, &fakeMessageStore{}, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	payload := []byte(`{"text":"hello"}`)
	id, err := svc.EnqueueEvent(context.Background(), runtime.EventHumanMessage, payload)
	if err != nil {
		t.Fatalf("EnqueueEvent: unexpected error: %v", err)
	}
	if id == 0 {
		t.Fatal("EnqueueEvent returned id 0; want the inserted row's id")
	}

	entry, ok, err := store.ClaimNextDue(context.Background(), runtime.QueueEvents)
	if err != nil || !ok {
		t.Fatalf("expected EnqueueEvent to have inserted a claimable events row; ClaimNextDue ok=%v err=%v", ok, err)
	}
	if entry.ID != id {
		t.Errorf("inserted row id = %d, EnqueueEvent returned %d", entry.ID, id)
	}
	if entry.Kind != string(runtime.EventHumanMessage) {
		t.Errorf("inserted row kind = %q, want %q", entry.Kind, runtime.EventHumanMessage)
	}
	if string(entry.Payload) != string(payload) {
		t.Errorf("inserted payload = %s, want %s", entry.Payload, payload)
	}
}

// ---- PostMessage (07 §3-§4) ------------------------------------------------

func TestService_PostMessage_DelegatesToMessageStoreAndReturnsBothIDs(t *testing.T) {
	clock := testutil.NewFakeClock()
	messages := &fakeMessageStore{}
	svc := newTestService(newFakeStore(clock), messages, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	msgID, evID, err := svc.PostMessage(context.Background(), "build the widget")
	if err != nil {
		t.Fatalf("PostMessage: unexpected error: %v", err)
	}
	if messages.appendUserCalls != 1 {
		t.Fatalf("AppendUserMessageAndEnqueueEvent called %d times, want exactly 1", messages.appendUserCalls)
	}
	if msgID == 0 || evID == 0 {
		t.Errorf("PostMessage returned (messageID=%d, eventID=%d), want both non-zero", msgID, evID)
	}
}

// TestService_PostMessage_PropagatesStoreErrorWithoutPartialIDs pins that
// PostMessage does not invent ids or otherwise paper over a failed
// transactional append+enqueue (07 §3: "the transcript and the event queue
// cannot disagree" — a failure here must be visible, not silently partial).
func TestService_PostMessage_PropagatesStoreErrorWithoutPartialIDs(t *testing.T) {
	clock := testutil.NewFakeClock()
	messages := &fakeMessageStore{
		appendUserFn: func(context.Context, string) (int64, int64, error) {
			return 0, 0, errStoreFailed
		},
	}
	svc := newTestService(newFakeStore(clock), messages, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	msgID, evID, err := svc.PostMessage(context.Background(), "hello")
	if !errors.Is(err, errStoreFailed) {
		t.Fatalf("PostMessage error = %v, want errStoreFailed", err)
	}
	if msgID != 0 || evID != 0 {
		t.Errorf("PostMessage on failure returned (messageID=%d, eventID=%d), want (0,0)", msgID, evID)
	}
}

// ---- Say: append-then-push (07 §3, §6) ------------------------------------

func TestService_Say_AppendsThenPushes(t *testing.T) {
	clock := testutil.NewFakeClock()
	messages := &fakeMessageStore{}
	sayer := &fakeSayPusher{}
	svc := newTestService(newFakeStore(clock), messages, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, sayer)

	if err := svc.Say(context.Background(), "hi there"); err != nil {
		t.Fatalf("Say: unexpected error: %v", err)
	}
	if messages.appendKilnCalls != 1 {
		t.Fatalf("AppendKilnMessage called %d times, want exactly 1", messages.appendKilnCalls)
	}
	pushed := sayer.pushedMessages()
	if len(pushed) != 1 {
		t.Fatalf("PushSay called %d times, want exactly 1", len(pushed))
	}
	if pushed[0].Text != "hi there" || pushed[0].Role != runtime.RoleKiln {
		t.Errorf("pushed message = %+v, want Text=%q Role=kiln", pushed[0], "hi there")
	}
}

// TestService_Say_DoesNotPushWhenAppendFails proves the append-then-push
// ordering is real, not incidental: a failed append must never reach the
// SSE push (07 §3 — "a crash between them costs a live push, not history",
// implying the push only happens once the row is durable).
func TestService_Say_DoesNotPushWhenAppendFails(t *testing.T) {
	clock := testutil.NewFakeClock()
	messages := &fakeMessageStore{
		appendKilnFn: func(context.Context, string) (runtime.Message, error) {
			return runtime.Message{}, errStoreFailed
		},
	}
	sayer := &fakeSayPusher{}
	svc := newTestService(newFakeStore(clock), messages, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, sayer)

	if err := svc.Say(context.Background(), "hi"); !errors.Is(err, errStoreFailed) {
		t.Fatalf("Say error = %v, want errStoreFailed", err)
	}
	if got := len(sayer.pushedMessages()); got != 0 {
		t.Errorf("PushSay called %d times after a failed append, want 0", got)
	}
}

// ---- Recent (07 §3-§4) ------------------------------------------------------

func TestService_Recent_DelegatesToMessageStore(t *testing.T) {
	clock := testutil.NewFakeClock()
	messages := &fakeMessageStore{}
	ctx := context.Background()
	if _, _, err := messages.AppendUserMessageAndEnqueueEvent(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := messages.AppendKilnMessage(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	svc := newTestService(newFakeStore(clock), messages, &fakeBrain{}, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	got, err := svc.Recent(ctx, 20)
	if err != nil {
		t.Fatalf("Recent: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Recent returned %d messages, want 2", len(got))
	}
	if got[0].Text != "one" || got[1].Text != "two" {
		t.Errorf("Recent order = [%q, %q], want oldest-first [one, two]", got[0].Text, got[1].Text)
	}
	if len(messages.recentCalls) != 1 || messages.recentCalls[0] != 20 {
		t.Errorf("MessageStore.Recent calls = %v, want a single call with n=20", messages.recentCalls)
	}
}

// ---- Workers(clock): events worker drives the Brain exactly once per event
// (04 §4, §6) ----------------------------------------------------------------

func TestService_Workers_EventsWorkerDrivesBrainOncePerEvent(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	brain := &fakeBrain{}
	svc := newTestService(store, &fakeMessageStore{}, brain, &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	eventsWorker, outboxWorker := svc.Workers(clock)
	if eventsWorker == nil || outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil worker; expected both the events and outbox workers wired (04 §3-§4)")
	}

	turnID := store.seed(
		runtime.QueueEvents, string(runtime.EventAgentTurnCompleted), []byte(`{"worker_id":"w-1"}`), 0,
	)
	msgID := store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{"text":"hi"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return brain.count("HandleEvent") == 2 })
	time.Sleep(20 * time.Millisecond) // give any stray extra dispatch a chance to show up
	if got := brain.count("HandleEvent"); got != 2 {
		t.Fatalf("Brain.HandleEvent called %d times, want exactly 2 (one per event, 04 §4)", got)
	}

	gotIDs := map[int64]bool{}
	for _, c := range brain.callsFor("HandleEvent") {
		ev, ok := c.Args[0].(runtime.Event)
		if !ok {
			t.Fatalf("HandleEvent arg = %T, want runtime.Event", c.Args[0])
		}
		gotIDs[ev.ID] = true
	}
	if !gotIDs[turnID] || !gotIDs[msgID] {
		t.Errorf("HandleEvent was not called with both seeded event ids (%d, %d): got %v", turnID, msgID, gotIDs)
	}
}

// ---- Workers(clock): outbox topic -> executor routing (04 §2) -------------

const (
	topicAgentSend    = "agent.send"
	topicAgentRelease = "agent.release"
	topicNotifySend   = "notify.send"
	topicPullEvaluate = "pull.evaluate"
	topicBoardUpdated = "board.updated"
)

func TestService_Workers_OutboxRoutesEachTopicToItsExecutor(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	agents := &fakeAgentRuntime{}
	notifier := &fakeNotifier{}
	puller := &fakePuller{}
	pusher := &fakeSnapshotPusher{}
	svc := newTestService(store, &fakeMessageStore{}, &fakeBrain{}, puller, &fakeBlocker{},
		agents, notifier, pusher, &fakeSayPusher{})

	_, outboxWorker := svc.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	sendID := store.seed(
		runtime.QueueOutbox, topicAgentSend, []byte(`{"ticket_id":"tk-1","worker_id":"w-1","message":"go"}`), 0,
	)
	releaseID := store.seed(runtime.QueueOutbox, topicAgentRelease, []byte(`{"worker_id":"w-1"}`), 0)
	store.seed(runtime.QueueOutbox, topicPullEvaluate, []byte(`{}`), 0)
	store.seed(runtime.QueueOutbox, topicNotifySend, []byte(`{"ticket_id":"tk-2","title":"t","reason":"r"}`), 0)
	store.seed(runtime.QueueOutbox, topicBoardUpdated, []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool {
		return agents.count("Send") >= 1 && agents.count("Release") >= 1 &&
			puller.count("RunPull") >= 1 && notifier.count("Send") >= 1 && pusher.count("PushBoard") >= 1
	})

	sendCalls := agents.callsFor("Send")
	if key, ok := sendCalls[0].Args[0].(int64); !ok || key != sendID {
		t.Errorf("agent.send routed with idempotencyKey = %v, want the outbox id %d (04 §3: id doubles as idempotency key)",
			sendCalls[0].Args[0], sendID)
	}

	releaseCalls := agents.callsFor("Release")
	if key, ok := releaseCalls[0].Args[0].(int64); !ok || key != releaseID {
		t.Errorf("agent.release routed with idempotencyKey = %v, want the outbox id %d", releaseCalls[0].Args[0], releaseID)
	}
}

// ---- Workers(clock): exhausted agent.send -> MarkBlocked (04 §3 dead-letter
// table, 03 §7.3) ------------------------------------------------------------

func TestService_Workers_ExhaustedAgentSendMarksTicketBlocked(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	blocker := &fakeBlocker{}
	agents := &fakeAgentRuntime{
		sendFn: func(context.Context, int64, []byte) error { return errHandlerFailed },
	}
	svc := newTestService(store, &fakeMessageStore{}, &fakeBrain{}, &fakePuller{}, blocker,
		agents, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{})

	_, outboxWorker := svc.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	store.seed(runtime.QueueOutbox, topicAgentSend,
		[]byte(`{"ticket_id":"tk-blocked","worker_id":"w-1","message":"go"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return blocker.count("MarkBlocked") >= 1 })
	time.Sleep(20 * time.Millisecond)
	if got := blocker.count("MarkBlocked"); got != 1 {
		t.Fatalf("MarkBlocked called %d times, want exactly 1", got)
	}

	call := blocker.callsFor("MarkBlocked")[0]
	ticketID, ok := call.Args[0].(string)
	if !ok {
		t.Fatalf("MarkBlocked arg 0 = %T, want string", call.Args[0])
	}
	reason, ok := call.Args[1].(string)
	if !ok {
		t.Fatalf("MarkBlocked arg 1 = %T, want string", call.Args[1])
	}
	if ticketID != "tk-blocked" {
		t.Errorf("MarkBlocked ticketID = %q, want %q (extracted from the agent.send payload)", ticketID, "tk-blocked")
	}
	if reason == "" {
		t.Error("MarkBlocked reason was empty; want the delivery-failure reason (04 §3 dead-letter table)")
	}

	// Attempts must have been retried up to MaxAttempts, not short-circuited.
	if got := agents.count("Send"); got != int(runtime.MaxAttempts) {
		t.Errorf("AgentRuntime.Send called %d times, want exactly MaxAttempts=%d before dead-lettering",
			got, runtime.MaxAttempts)
	}
}

// TestService_Workers_ExhaustedNonAgentSendTopics_DoNotMarkBlocked pins the
// dead-letter table's other rows (04 §3): notify.send/agent.release/
// pull.evaluate/board.updated log-and-drop (or self-heal) rather than
// touching the Blocker port at all — only agent.send does.
func TestService_Workers_ExhaustedNonAgentSendTopics_DoNotMarkBlocked(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	blocker := &fakeBlocker{}
	notifier := &fakeNotifier{
		sendFn: func(context.Context, []byte) error { return errHandlerFailed },
	}
	svc := newTestService(store, &fakeMessageStore{}, &fakeBrain{}, &fakePuller{}, blocker,
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{}, &fakeSayPusher{})

	_, outboxWorker := svc.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	id := store.seed(
		runtime.QueueOutbox, topicNotifySend, []byte(`{"ticket_id":"tk-3","title":"t","reason":"r"}`), 0,
	)

	stop := runWorker(t, outboxWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return store.status(runtime.QueueOutbox, id) == "dead" })
	time.Sleep(20 * time.Millisecond)
	if got := blocker.count("MarkBlocked"); got != 0 {
		t.Errorf("MarkBlocked called %d times for an exhausted notify.send, want 0 (04 §3: log-and-drop, not blocked)", got)
	}
}
