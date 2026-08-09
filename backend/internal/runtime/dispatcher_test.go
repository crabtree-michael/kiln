package runtime_test

// Dispatcher unit tests: the queue core (04 §2–§6) built over its five executor
// ports and the three units it routes to. Two things are pinned here and nowhere
// else — the ingest contract (EnqueueEvent stamps the tenant and lands a
// claimable row) and the ROUTING the drain performs: events reach the Brain
// exactly once each, resolved per project; every outbox topic reaches its
// executor with the claimed entry's project and id threaded through; and the
// 04 §3 dead-letter table's one blocking row (agent.send → MarkBlocked) fires
// while the others log and drop.
//
// What the routed-to units then DO is asserted on the units themselves — the
// system-error and brain-unresolved Says in transcript_service_test.go, snapshot
// assembly in feed_assembler_test.go, notification ops in
// notification_service_test.go, and every push, decode and log-and-drop in
// fanout_test.go. Those suites each need a port or two; what needs a store, a
// clock and a running worker is exactly what is left here.

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// Dispatcher is the Transcript's Nudger (split plan §4) — it owns the events
// worker, so an ingest through the conversation surface wakes the same worker
// EnqueueEvent does. The hook's own nil/no-op contract is asserted in
// transcript_service_test.go; what matters here is that this type still fits it.
var _ runtime.Nudger = (*runtime.Dispatcher)(nil)

// dispatcherRig is one Dispatcher wired over fresh fakes, each kept to hand so a
// test can read what it received or — because every fake reads its behaviour
// field at call time, and nothing is running yet — stage a failure on it before
// starting a worker.
//
// It is deliberately the whole graph: unlike the four leaf units, the drain's
// subject IS the wiring between them, so the collaborators are real Transcript /
// Notify / FanOut values over fakes rather than stubs. Notify is built once and
// shared, exactly as wiring does, so the mode gate a test sets on owner applies
// to both the notify.send route and FanOut's feed-update push.
type dispatcherRig struct {
	dispatcher *runtime.Dispatcher

	store    *fakeStore
	brain    *fakeBrain
	brains   *fakeBrainResolver
	puller   *fakePuller
	blocker  *fakeBlocker
	agents   *fakeAgentRuntime
	messages *fakeMessageStore
	sayer    *fakeSayPusher
	notifier *fakeNotifier
	owner    *fakeOwner
	notes    *fakeNotificationStore
	board    *fakeBoardReader

	snapshots *fakeSnapshotPusher
	feeds     *fakeFeedPusher
	activity  *fakeActivityPusher
}

func newDispatcherRig(clock runtime.Clock) *dispatcherRig {
	brain := &fakeBrain{}
	r := &dispatcherRig{
		store:     newFakeStore(clock),
		brain:     brain,
		brains:    resolverFor(brain),
		puller:    &fakePuller{},
		blocker:   &fakeBlocker{},
		agents:    &fakeAgentRuntime{},
		messages:  &fakeMessageStore{},
		sayer:     &fakeSayPusher{},
		notifier:  &fakeNotifier{},
		owner:     &fakeOwner{},
		notes:     &fakeNotificationStore{},
		board:     &fakeBoardReader{},
		snapshots: &fakeSnapshotPusher{},
		feeds:     &fakeFeedPusher{},
		activity:  &fakeActivityPusher{},
	}
	notify := runtime.NewNotify(r.notifier, r.owner)
	transcript := runtime.NewTranscript(r.messages, r.sayer)
	fanout := runtime.NewFanOut(
		r.snapshots, r.feeds, r.activity, r.notes,
		runtime.NewFeed(r.board, r.notes), notify,
	)
	r.dispatcher = runtime.NewDispatcher(
		r.store, r.brains, r.puller, r.blocker, r.agents, transcript, notify, fanout,
	)
	transcript.SetNudger(r.dispatcher)
	return r
}

// ---- EnqueueEvent (04 §6) --------------------------------------------------

func TestDispatcher_EnqueueEvent_InsertsIntoStore(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	payload := []byte(`{"text":"hello"}`)
	id, err := rig.dispatcher.EnqueueEvent(
		context.Background(), defaultTestProject, runtime.EventHumanMessage, 0, payload,
	)
	if err != nil {
		t.Fatalf("EnqueueEvent: unexpected error: %v", err)
	}
	if id == 0 {
		t.Fatal("EnqueueEvent returned id 0; want the inserted row's id")
	}

	entry, ok, err := rig.store.ClaimNextDue(context.Background(), runtime.QueueEvents, nil)
	if err != nil || !ok {
		t.Fatalf("expected EnqueueEvent to have inserted a claimable events row; ClaimNextDue ok=%v err=%v", ok, err)
	}
	if entry.ID != id {
		t.Errorf("inserted row id = %d, EnqueueEvent returned %d", entry.ID, id)
	}
	if entry.ProjectID != defaultTestProject {
		t.Errorf("inserted row project = %q, want %q (EnqueueEvent must stamp the tenant, 11 §3)",
			entry.ProjectID, defaultTestProject)
	}
	if entry.Kind != string(runtime.EventHumanMessage) {
		t.Errorf("inserted row kind = %q, want %q", entry.Kind, runtime.EventHumanMessage)
	}
	if string(entry.Payload) != string(payload) {
		t.Errorf("inserted payload = %s, want %s", entry.Payload, payload)
	}
}

// TestDispatcher_EnqueueEvent_BeforeWorkersStillLandsTheRow pins the nudge's
// nil-safety from the owning side (04 §5): ingest during startup, before Workers
// has built anything to wake, must still commit the row and return its id — the
// worker's poll fallback catches it. A missing nudge costs latency, not the event.
func TestDispatcher_EnqueueEvent_BeforeWorkersStillLandsTheRow(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock) // Workers deliberately not called.

	id, err := rig.dispatcher.EnqueueEvent(
		context.Background(), defaultTestProject, runtime.EventHumanMessage, 0, []byte(`{"text":"hi"}`),
	)
	if err != nil {
		t.Fatalf("EnqueueEvent before Workers: unexpected error: %v", err)
	}
	if _, ok, err := rig.store.ClaimNextDue(context.Background(), runtime.QueueEvents, nil); err != nil || !ok {
		t.Fatalf("row %d not claimable after an un-nudged ingest; ClaimNextDue ok=%v err=%v", id, ok, err)
	}
}

// ---- Workers(clock): events worker drives the Brain exactly once per event
// (04 §4, §6) ----------------------------------------------------------------

func TestDispatcher_Workers_EventsWorkerDrivesBrainOncePerEvent(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	eventsWorker, outboxWorker := rig.dispatcher.Workers(clock)
	if eventsWorker == nil || outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil worker; expected both the events and outbox workers wired (04 §3-§4)")
	}

	turnID := rig.store.seed(
		runtime.QueueEvents, string(runtime.EventAgentTurnCompleted), []byte(`{"worker_id":"w-1"}`), 0,
	)
	msgID := rig.store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{"text":"hi"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return rig.brain.count("HandleEvent") == 2 })
	time.Sleep(20 * time.Millisecond) // give any stray extra dispatch a chance to show up
	if got := rig.brain.count("HandleEvent"); got != 2 {
		t.Fatalf("Brain.HandleEvent called %d times, want exactly 2 (one per event, 04 §4)", got)
	}

	gotIDs := map[int64]bool{}
	for _, c := range rig.brain.callsFor("HandleEvent") {
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

// ---- per-event brain resolution (11 §3) ------------------------------------

// TestDispatcher_EventsWorker_ResolvesBrainPerProjectAndThreadsProjectID pins
// the BrainResolver seam: every event resolves the brain for ITS project, and
// the Event handed to the brain carries that ProjectID.
func TestDispatcher_EventsWorker_ResolvesBrainPerProjectAndThreadsProjectID(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	eventsWorker, _ := rig.dispatcher.Workers(clock)
	rig.store.seedProject(runtime.QueueEvents, "proj-A", string(runtime.EventHumanMessage), []byte(`{"text":"a"}`), 0)
	rig.store.seedProject(runtime.QueueEvents, "proj-B", string(runtime.EventHumanMessage), []byte(`{"text":"b"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return rig.brain.count("HandleEvent") == 2 })

	resolvedFor := map[string]bool{}
	for _, c := range rig.brains.callsFor("For") {
		pid, ok := c.Args[0].(string)
		if !ok {
			t.Fatalf("For arg = %T, want string", c.Args[0])
		}
		resolvedFor[pid] = true
	}
	if !resolvedFor["proj-A"] || !resolvedFor["proj-B"] {
		t.Errorf("BrainResolver.For called for %v, want both proj-A and proj-B (per-event resolution, 11 §3)", resolvedFor)
	}

	gotProjects := map[string]bool{}
	for _, c := range rig.brain.callsFor("HandleEvent") {
		ev, ok := c.Args[0].(runtime.Event)
		if !ok {
			t.Fatalf("HandleEvent arg = %T, want runtime.Event", c.Args[0])
		}
		gotProjects[ev.ProjectID] = true
	}
	if !gotProjects["proj-A"] || !gotProjects["proj-B"] {
		t.Errorf("brain saw Event.ProjectID set %v, want both proj-A and proj-B", gotProjects)
	}
}

// TestDispatcher_EventsWorker_BrainResolutionFailureSaysAndMarksDone pins the
// no-retry-storm contract (11 §3): a project whose brain won't resolve gets a
// feed-visible system-error Say on that project, the event is marked done
// after ONE attempt, and the brain is never invoked.
func TestDispatcher_EventsWorker_BrainResolutionFailureSaysAndMarksDone(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)
	rig.brains.forFn = func(context.Context, string) (runtime.Brain, error) { return nil, errStoreFailed }

	eventsWorker, _ := rig.dispatcher.Workers(clock)
	id := rig.store.seedProject(runtime.QueueEvents, "proj-broken", string(runtime.EventHumanMessage), []byte(`{}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return rig.store.status(runtime.QueueEvents, id) == statusDone })
	time.Sleep(20 * time.Millisecond)

	if got := rig.store.attempts(id); got != 1 {
		t.Errorf("attempts = %d, want 1 — resolution failure must not retry (no retry storm, 11 §3)", got)
	}
	if got := rig.store.retryCallCount(); got != 0 {
		t.Errorf("MarkRetry called %d times, want 0", got)
	}
	if got := rig.brain.count("HandleEvent"); got != 0 {
		t.Errorf("Brain.HandleEvent called %d times, want 0 (nothing resolved)", got)
	}
	pushed := rig.sayer.pushedMessages()
	if len(pushed) != 1 {
		t.Fatalf("PushSay called %d times, want exactly 1 system-error Say", len(pushed))
	}
	if pushed[0].projectID != "proj-broken" {
		t.Errorf("system-error Say pushed to project %q, want proj-broken (that project only)", pushed[0].projectID)
	}
	if pushed[0].m.Text == "" || pushed[0].m.Role != runtime.RoleKiln {
		t.Errorf("system-error Say = %+v, want a non-empty kiln-authored message", pushed[0].m)
	}
}

// ---- the thinking bracket around a brain pass (08 §4) ----------------------

func TestDispatcher_EventsWorker_BracketsBrainPassWithThinking(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	eventsWorker, _ := rig.dispatcher.Workers(clock)
	rig.store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{"text":"hi"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(rig.activity.events()) >= 2 })
	time.Sleep(20 * time.Millisecond)

	evs := rig.activity.events()
	if len(evs) != 2 {
		t.Fatalf("thinking events = %d, want exactly 2 (on then off) for one brain pass", len(evs))
	}
	for i, p := range evs {
		if p.ev.Kind != activityThinking || p.ev.On == nil {
			t.Fatalf("event[%d] = %+v, want a thinking event with On set", i, p.ev)
		}
		if p.projectID != defaultTestProject {
			t.Errorf("event[%d] pushed to project %q, want %q (per-project activity fan-out, 11 §3)",
				i, p.projectID, defaultTestProject)
		}
	}
	if *evs[0].ev.On != true || *evs[1].ev.On != false {
		t.Errorf("thinking sequence = [%v, %v], want [true, false]", *evs[0].ev.On, *evs[1].ev.On)
	}
}

// ---- Workers(clock): outbox topic -> executor routing (04 §2) -------------

const (
	topicAgentSend      = "agent.send"
	topicAgentRelease   = "agent.release"
	topicNotifySend     = "notify.send"
	topicPullEvaluate   = "pull.evaluate"
	topicBoardUpdated   = "board.updated"
	topicFeedUpdated    = "feed.updated"
	topicActivityToast  = "activity.toast"
	topicFeedCompletion = "feed.completion"
)

func TestDispatcher_Workers_OutboxRoutesEachTopicToItsExecutor(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	_, outboxWorker := rig.dispatcher.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	sendID := rig.store.seed(
		runtime.QueueOutbox, topicAgentSend, []byte(`{"ticket_id":"tk-1","worker_id":"w-1","message":"go"}`), 0,
	)
	releaseID := rig.store.seed(runtime.QueueOutbox, topicAgentRelease, []byte(`{"worker_id":"w-1"}`), 0)
	rig.store.seed(runtime.QueueOutbox, topicPullEvaluate, []byte(`{}`), 0)
	rig.store.seed(runtime.QueueOutbox, topicNotifySend,
		[]byte(`{"ticket_id":"tk-2","title":"t","reason":"r","kind":"blocked"}`), 0)
	rig.store.seed(runtime.QueueOutbox, topicBoardUpdated, []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool {
		return rig.agents.count("Send") >= 1 && rig.agents.count("Release") >= 1 &&
			rig.puller.count("RunPull") >= 1 && rig.notifier.count("Send") >= 1 &&
			rig.snapshots.count("PushBoard") >= 1
	})

	sendCalls := rig.agents.callsFor("Send")
	if pid, ok := sendCalls[0].Args[0].(string); !ok || pid != defaultTestProject {
		t.Errorf("agent.send routed with projectID = %v, want the claimed entry's %q (11 §3)",
			sendCalls[0].Args[0], defaultTestProject)
	}
	if key, ok := sendCalls[0].Args[1].(int64); !ok || key != sendID {
		t.Errorf("agent.send routed with idempotencyKey = %v, want the outbox id %d (04 §3: id doubles as idempotency key)",
			sendCalls[0].Args[1], sendID)
	}

	releaseCalls := rig.agents.callsFor("Release")
	if key, ok := releaseCalls[0].Args[1].(int64); !ok || key != releaseID {
		t.Errorf("agent.release routed with idempotencyKey = %v, want the outbox id %d", releaseCalls[0].Args[1], releaseID)
	}

	// Every executor gets the claimed entry's project (11 §3).
	if pid, ok := rig.puller.callsFor("RunPull")[0].Args[0].(string); !ok || pid != defaultTestProject {
		t.Errorf("pull.evaluate routed with projectID = %v, want %q",
			rig.puller.callsFor("RunPull")[0].Args[0], defaultTestProject)
	}
	if pid, ok := rig.notifier.callsFor("Send")[0].Args[0].(string); !ok || pid != defaultTestProject {
		t.Errorf("notify.send routed with projectID = %v, want %q",
			rig.notifier.callsFor("Send")[0].Args[0], defaultTestProject)
	}
	if pid, ok := rig.snapshots.callsFor("PushBoard")[0].Args[0].(string); !ok || pid != defaultTestProject {
		t.Errorf("board.updated routed with projectID = %v, want %q",
			rig.snapshots.callsFor("PushBoard")[0].Args[0], defaultTestProject)
	}
}

// A claimed feed.updated reaches FanOut and fans a snapshot out to the entry's
// project. WHAT that snapshot contains, and how a failed assembly or push
// degrades, is fanout_test.go's — here the claim is only that the topic is wired
// to its handler with the tenant threaded through (11 §3).
func TestDispatcher_Outbox_FeedUpdatedRoutesToTheFeedFanOut(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	_, outboxWorker := rig.dispatcher.Workers(clock)
	rig.store.seed(runtime.QueueOutbox, topicFeedUpdated, []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(rig.feeds.pushes()) >= 1 })
	pushes := rig.feeds.pushes()
	if pushes[0].projectID != defaultTestProject {
		t.Errorf("PushFeed projectID = %q, want the outbox entry's %q (11 §3)",
			pushes[0].projectID, defaultTestProject)
	}
}

// The board's dedicated notify.send milestones (blocked/started/done) are gated
// by the owner's notification mode (02 §10): "blocked" mode delivers only the
// blocked milestone, "default" all three, "all" all three. The gate itself lives
// in Notify and is unit-tested there; what this pins is that the notify.send
// route decodes the entry's Kind and hands it over, so each cell of the matrix
// survives the trip through the drain.
func TestDispatcher_Outbox_NotifySendMilestonesGatedByMode(t *testing.T) {
	cases := []struct {
		mode, kind string
		wantSend   int
	}{
		{modeDefault, kindBlocked, 1},
		{modeDefault, kindStarted, 1},
		{modeDefault, kindDone, 1},
		{modeBlocked, kindBlocked, 1},
		{modeBlocked, kindStarted, 0},
		{modeBlocked, kindDone, 0},
		{modeAll, kindStarted, 1},
		{modeAll, kindDone, 1},
	}
	for _, tc := range cases {
		t.Run(tc.mode+"/"+tc.kind, func(t *testing.T) {
			clock := testutil.NewFakeClock()
			rig := newDispatcherRig(clock)
			rig.owner.mode = tc.mode

			_, outboxWorker := rig.dispatcher.Workers(clock)
			id := rig.store.seed(runtime.QueueOutbox, topicNotifySend,
				[]byte(`{"ticket_id":"tk-1","title":"t","reason":"r","kind":"`+tc.kind+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			// The entry always reaches "done" (a suppressed push is a successful
			// no-op, not a failure); then the Send count reflects the gate.
			testutil.Eventually(t, func() bool { return rig.store.status(runtime.QueueOutbox, id) == statusDone })
			if got := rig.notifier.count("Send"); got != tc.wantSend {
				t.Errorf("Send calls = %d, want %d for mode=%q kind=%q", got, tc.wantSend, tc.mode, tc.kind)
			}
		})
	}
}

func TestDispatcher_Outbox_ActivityToastDecodesAndPushes(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	_, outboxWorker := rig.dispatcher.Workers(clock)
	rig.store.seed(runtime.QueueOutbox, topicActivityToast,
		[]byte(`{"verb":"started","ticket_id":"t-42","ticket_title":"Build the widget"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	// The decoded toast's fields are fanout_test.go's business; what this pins is
	// that the topic reaches the handler at all, on the entry's project (11 §3).
	testutil.Eventually(t, func() bool {
		for _, p := range rig.activity.events() {
			if p.ev.Kind == activityToast && p.projectID == defaultTestProject {
				return true
			}
		}
		return false
	})
}

// feed.completion is the one UI topic whose handler returns its errors, so the
// route is wrapped like the durable ones rather than swallowed. The card's shape
// — empty body, GitHub link, work summary, idempotency key — is asserted on the
// unit in fanout_test.go; here it is that the topic lands on the notification
// writer at all, for the entry's project.
func TestDispatcher_Outbox_FeedCompletionRoutesToTheCompletionCard(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	_, outboxWorker := rig.dispatcher.Workers(clock)
	id := rig.store.seed(runtime.QueueOutbox, topicFeedCompletion,
		[]byte(`{"ticket_id":"t1","ticket_title":"Build the widget"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(rig.notes.completionPosts()) >= 1 })
	posts := rig.notes.completionPosts()
	if posts[0].ProjectID != defaultTestProject {
		t.Errorf("completion card posted for project %q, want the outbox entry's %q (11 §3)",
			posts[0].ProjectID, defaultTestProject)
	}
	// The claimed entry's id is what travels as the idempotency key (04 §3) — the
	// routing detail the unit test cannot see, because it is the worker that
	// supplies it.
	if posts[0].Key != id {
		t.Errorf("completion key = %d, want the claimed outbox id %d", posts[0].Key, id)
	}
}

// TestDispatcher_Outbox_UnknownTopicRetriesThenDeadLetters pins the switch's
// default arm: a topic outside the eight the drain routes is a contract
// violation by whoever appended it, and is surfaced as a retryable handler error
// rather than silently marked done. It exhausts its attempts and lands dead —
// visible in the queue — without touching any executor port.
func TestDispatcher_Outbox_UnknownTopicRetriesThenDeadLetters(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)

	_, outboxWorker := rig.dispatcher.Workers(clock)
	id := rig.store.seed(runtime.QueueOutbox, "wat.unrecognized", []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return rig.store.status(runtime.QueueOutbox, id) == "dead" })
	if got := len(rig.store.retryCallsFor(id)); got != int(runtime.MaxAttempts)-1 {
		t.Errorf("MarkRetry called %d times, want MaxAttempts-1=%d — an unknown topic must retry like any handler "+
			"error, not drop", got, int(runtime.MaxAttempts)-1)
	}
	if got := rig.agents.count("Send") + rig.puller.count("RunPull") + rig.notifier.count("Send"); got != 0 {
		t.Errorf("%d executor calls for an unknown topic, want 0", got)
	}
	if got := rig.blocker.count("MarkBlocked"); got != 0 {
		t.Errorf("MarkBlocked called %d times for an exhausted unknown topic, want 0 (only agent.send blocks)", got)
	}
}

// ---- Workers(clock): exhausted agent.send -> MarkBlocked (04 §3 dead-letter
// table, 03 §7.3) ------------------------------------------------------------

func TestDispatcher_Workers_ExhaustedAgentSendMarksTicketBlocked(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)
	rig.agents.sendFn = func(context.Context, string, int64, []byte) error { return errHandlerFailed }

	_, outboxWorker := rig.dispatcher.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	rig.store.seed(runtime.QueueOutbox, topicAgentSend,
		[]byte(`{"ticket_id":"tk-blocked","worker_id":"w-1","message":"go"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return rig.blocker.count("MarkBlocked") >= 1 })
	time.Sleep(20 * time.Millisecond)
	if got := rig.blocker.count("MarkBlocked"); got != 1 {
		t.Fatalf("MarkBlocked called %d times, want exactly 1", got)
	}

	call := rig.blocker.callsFor("MarkBlocked")[0]
	projectID, ok := call.Args[0].(string)
	if !ok {
		t.Fatalf("MarkBlocked arg 0 = %T, want string", call.Args[0])
	}
	ticketID, ok := call.Args[1].(string)
	if !ok {
		t.Fatalf("MarkBlocked arg 1 = %T, want string", call.Args[1])
	}
	reason, ok := call.Args[2].(string)
	if !ok {
		t.Fatalf("MarkBlocked arg 2 = %T, want string", call.Args[2])
	}
	if projectID != defaultTestProject {
		t.Errorf("MarkBlocked projectID = %q, want the claimed entry's %q (11 §3)", projectID, defaultTestProject)
	}
	if ticketID != "tk-blocked" {
		t.Errorf("MarkBlocked ticketID = %q, want %q (extracted from the agent.send payload)", ticketID, "tk-blocked")
	}
	if reason == "" {
		t.Error("MarkBlocked reason was empty; want the delivery-failure reason (04 §3 dead-letter table)")
	}

	// Attempts must have been retried up to MaxAttempts, not short-circuited.
	if got := rig.agents.count("Send"); got != int(runtime.MaxAttempts) {
		t.Errorf("AgentRuntime.Send called %d times, want exactly MaxAttempts=%d before dead-lettering",
			got, runtime.MaxAttempts)
	}
}

// TestDispatcher_Workers_ExhaustedNonAgentSendTopics_DoNotMarkBlocked pins the
// dead-letter table's other rows (04 §3): notify.send/agent.release/
// pull.evaluate/board.updated log-and-drop (or self-heal) rather than
// touching the Blocker port at all — only agent.send does.
func TestDispatcher_Workers_ExhaustedNonAgentSendTopics_DoNotMarkBlocked(t *testing.T) {
	clock := testutil.NewFakeClock()
	rig := newDispatcherRig(clock)
	rig.notifier.sendFn = func(context.Context, string, []byte) error { return errHandlerFailed }

	_, outboxWorker := rig.dispatcher.Workers(clock)
	if outboxWorker == nil {
		t.Fatal("Workers(clock) returned a nil outbox worker")
	}

	id := rig.store.seed(
		runtime.QueueOutbox, topicNotifySend, []byte(`{"ticket_id":"tk-3","title":"t","reason":"r","kind":"blocked"}`), 0,
	)

	stop := runWorker(t, outboxWorker)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return rig.store.status(runtime.QueueOutbox, id) == "dead" })
	time.Sleep(20 * time.Millisecond)
	if got := rig.blocker.count("MarkBlocked"); got != 0 {
		t.Errorf("MarkBlocked called %d times for an exhausted notify.send, want 0 (04 §3: log-and-drop, not blocked)", got)
	}
}
