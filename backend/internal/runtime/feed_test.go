package runtime_test

// Outbox and events ROUTING tests (08 §4, §7) that run THROUGH the Service: that
// each UI topic reaches its handler at all, and that the thinking bracket wraps a
// real brain pass. What those handlers then do is asserted on the units
// themselves — snapshot assembly in feed_assembler_test.go, notification ops in
// notification_service_test.go, and every push, decode and log-and-drop in
// fanout_test.go, each over a port or two instead of a wired dispatcher. What is
// left here is the wiring between them, which step 6 of the split moves to
// Dispatcher along with the drain that owns it.

import (
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// ---- thinking bracket around a brain pass (08 §4) -------------------------

func TestService_EventsWorker_BracketsBrainPassWithThinking(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	activity := &fakeActivityPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		&fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{}, activity,
		&fakeOwner{},
	)

	eventsWorker, _ := svc.Workers(clock)
	store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{"text":"hi"}`), 0)

	stop := runWorker(t, eventsWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(activity.events()) >= 2 })
	time.Sleep(20 * time.Millisecond)

	evs := activity.events()
	if len(evs) != 2 {
		t.Fatalf("thinking events = %d, want exactly 2 (on then off) for one brain pass", len(evs))
	}
	for i, p := range evs {
		if p.ev.Kind != "thinking" || p.ev.On == nil {
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

// ---- feed.updated / activity.toast outbox routing (08 §7) -----------------

// A claimed feed.updated reaches FanOut and fans a snapshot out to the entry's
// project. WHAT that snapshot contains, and how a failed assembly or push
// degrades, is fanout_test.go's — here the claim is only that the topic is wired
// to its handler with the tenant threaded through (11 §3).
func TestService_Outbox_FeedUpdatedRoutesToTheFeedFanOut(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	feed := &fakeFeedPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		&fakeNotificationStore{}, &fakeBoardReader{}, feed, &fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated", []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
	pushes := feed.pushes()
	if pushes[0].projectID != defaultTestProject {
		t.Errorf("PushFeed projectID = %q, want the outbox entry's %q (11 §3)",
			pushes[0].projectID, defaultTestProject)
	}
}

// The board's dedicated notify.send milestones (blocked/started/done) are gated
// by the owner's notification mode (02 §10): "blocked" mode delivers only the
// blocked milestone, "default" all three, "all" all three. This pins each cell.
func TestService_Outbox_NotifySendMilestonesGatedByMode(t *testing.T) {
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
			store := newFakeStore(clock)
			notifier := &fakeNotifier{}
			sayer := &fakeSayPusher{}
			svc := runtime.NewService(
				store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
				&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
				sayer, &fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{},
				&fakeActivityPusher{},
				&fakeOwner{mode: tc.mode},
			)

			_, outboxWorker := svc.Workers(clock)
			id := store.seed(runtime.QueueOutbox, "notify.send",
				[]byte(`{"ticket_id":"tk-1","title":"t","reason":"r","kind":"`+tc.kind+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			// The entry always reaches "done" (a suppressed push is a successful
			// no-op, not a failure); then the Send count reflects the gate.
			testutil.Eventually(t, func() bool { return store.status(runtime.QueueOutbox, id) == "done" })
			if got := notifier.count("Send"); got != tc.wantSend {
				t.Errorf("Send calls = %d, want %d for mode=%q kind=%q", got, tc.wantSend, tc.mode, tc.kind)
			}
		})
	}
}

func TestService_Outbox_ActivityToastDecodesAndPushes(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	activity := &fakeActivityPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		&fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{}, activity,
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "activity.toast",
		[]byte(`{"verb":"started","ticket_id":"t-42","ticket_title":"Build the widget"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	// The decoded toast's fields are fanout_test.go's business; what this pins is
	// that the topic reaches the handler at all, on the entry's project (11 §3).
	testutil.Eventually(t, func() bool {
		for _, p := range activity.events() {
			if p.ev.Kind == "toast" && p.projectID == defaultTestProject {
				return true
			}
		}
		return false
	})
}

// feed.completion is the one UI topic whose handler returns its errors, so the
// route is wrapped like the durable ones (wrapOutbox) rather than swallowed.
// The card's shape — empty body, GitHub link, work summary, idempotency key — is
// asserted on the unit in fanout_test.go; here it is that the topic lands on the
// notification writer at all, for the entry's project.
func TestService_Outbox_FeedCompletionRoutesToTheCompletionCard(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notes := &fakeNotificationStore{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	id := store.seed(runtime.QueueOutbox, "feed.completion",
		[]byte(`{"ticket_id":"t1","ticket_title":"Build the widget"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(notes.completionPosts()) >= 1 })
	posts := notes.completionPosts()
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
