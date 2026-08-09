package runtime_test

// Feed/activity unit tests (08 §3, §4, §7) that run THROUGH the Service: the
// notification-op delegation, the thinking bracket around a brain pass, and the
// feed.updated / activity.toast / feed.completion outbox routing. Snapshot
// assembly itself moved to feed_assembler_test.go with the Feed unit — it needs
// two read ports, not a wired dispatcher. What is left here is routing, which
// steps 5 and 6 of the split will move to FanOut and Dispatcher.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// newFeedService builds a Service with the caller's 08 §7 fakes and inert
// 04/07 ports, for the feed/activity paths.
func newFeedService(
	notes runtime.NotificationStore, board runtime.BoardReader,
	feed runtime.FeedPusher, activity runtime.ActivityPusher,
) *runtime.Service {
	clock := testutil.NewFakeClock()
	return runtime.NewService(
		newFakeStore(clock), &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		notes, board, feed, activity,
		&fakeOwner{},
	)
}

// ---- notification-op delegation (08 §3) -----------------------------------

func TestService_PostNotification_DelegatesToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})
	if err := svc.PostNotification(context.Background(), defaultTestProject, "update", "hello", nil, nil); err != nil {
		t.Fatalf("PostNotification: %v", err)
	}
	if len(notes.posts) != 1 || notes.posts[0].Body != "hello" || notes.posts[0].Kind != runtime.KindUpdate {
		t.Errorf("store posts = %+v, want a single update 'hello'", notes.posts)
	}
}

func TestService_EditNotification_DelegatesToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})
	img := "https://example.com/p.png"
	err := svc.EditNotification(context.Background(), defaultTestProject, 7, "preview", "fixed wording", &img)
	if err != nil {
		t.Fatalf("EditNotification: %v", err)
	}
	if len(notes.edits) != 1 {
		t.Fatalf("store edits = %d, want 1", len(notes.edits))
	}
	e := notes.edits[0]
	if e.ID != 7 || e.Kind != "preview" || e.Body != "fixed wording" || e.ImageURL == nil || *e.ImageURL != img {
		t.Errorf("edit = %+v, want id=7 kind=preview body='fixed wording' image=%q", e, img)
	}
}

func TestService_ListNotifications_ReturnsActiveNewestFirst(t *testing.T) {
	notes := &fakeNotificationStore{}
	seen := notes.seed(runtime.Notification{Body: "first card"})
	notes.seed(runtime.Notification{Body: "second card"})
	// Mark the first as seen so it drops out of the active set.
	if err := notes.MarkSeen(context.Background(), defaultTestProject, seen.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})

	got, err := svc.ListNotifications(context.Background(), defaultTestProject)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(got) != 1 || got[0].Body != "second card" {
		t.Fatalf("ListNotifications = %+v, want a single active card 'second card'", got)
	}
}

func TestService_MarkSeen_DelegatesHighWaterToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})
	if err := svc.MarkSeen(context.Background(), defaultTestProject, 42); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if len(notes.markSeenN) != 1 || notes.markSeenN[0] != 42 {
		t.Errorf("store MarkSeen calls = %v, want a single call with lastID=42", notes.markSeenN)
	}
}

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

func TestService_Outbox_FeedUpdatedAssemblesAndPushesFeed(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notes := &fakeNotificationStore{}
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "note"})
	feed := &fakeFeedPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, &fakeNotifier{}, &fakeSnapshotPusher{}, &fakeSayPusher{},
		notes, &fakeBoardReader{}, feed, &fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated", []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
	pushes := feed.pushes()
	if len(pushes) < 1 {
		t.Fatalf("PushFeed calls = %d, want >= 1", len(pushes))
	}
	if len(pushes[0].snap.Cards) != 1 || pushes[0].snap.Cards[0].Body != "note" {
		t.Errorf("pushed feed = %+v, want the assembled note card", pushes[0].snap)
	}
	if pushes[0].projectID != defaultTestProject {
		t.Errorf("PushFeed projectID = %q, want the outbox entry's %q (11 §3)",
			pushes[0].projectID, defaultTestProject)
	}
}

// A signal-only feed.updated (empty payload) carries no verb — it is the
// runtime's own re-render trigger for progress narration, edits, and mark-seen.
// It must never push, in any mode: with the notification mode set to "all" (the
// firehose), the empty payload still resolves to no push (02 §10).
func TestService_Outbox_FeedUpdatedNarrationNeverNotifies(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notifier := &fakeNotifier{}
	feed := &fakeFeedPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
		&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, feed,
		&fakeActivityPusher{},
		&fakeOwner{mode: modeAll},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated", []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	// The feed push confirms the entry was handled; the notifier must stay
	// untouched — narration has no verb, so it is silent even in "all" mode.
	testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
	if got := notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — a payload with no verb must not push", got)
	}
}

// Feed-update activity (a proposal, a queue, a reshape, a nudge, an archive) is
// the broad stream only "all" mode delivers (02 §10). Under the recommended
// "default" mode it is suppressed — those pushes are chatter next to the genuine
// milestones, which arrive on their own dedicated notify.send.
func TestService_Outbox_FeedUpdatedActivitySilentInDefaultMode(t *testing.T) {
	for _, verb := range []string{verbProposal, "reshaped", "queued", "nudged", "archived"} {
		t.Run(verb, func(t *testing.T) {
			clock := testutil.NewFakeClock()
			store := newFakeStore(clock)
			notifier := &fakeNotifier{}
			feed := &fakeFeedPusher{}
			svc := runtime.NewService(
				store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
				&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
				&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, feed,
				&fakeActivityPusher{},
				&fakeOwner{mode: modeDefault},
			)

			_, outboxWorker := svc.Workers(clock)
			store.seed(runtime.QueueOutbox, "feed.updated",
				[]byte(`{"title":"Login Redesign","verb":"`+verb+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
			if got := notifier.count("Send"); got != 0 {
				t.Errorf("Send calls = %d, want 0 — %q is feed activity, silent under the default mode", got, verb)
			}
		})
	}
}

// Under "all" mode every feed-update verb drives a specific push (Title = ticket,
// Body = what changed) rather than a generic "board was updated" placeholder.
func TestService_Outbox_FeedUpdatedActivityNotifiesInAllMode(t *testing.T) {
	cases := []struct{ verb, body string }{
		{verbProposal, "New proposal"},
		{"reshaped", "Proposal updated"},
		{"queued", "Queued for work"},
		{"nudged", "Nudged"},
		{"archived", "Archived"},
	}
	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			clock := testutil.NewFakeClock()
			store := newFakeStore(clock)
			notifier := &fakeNotifier{}
			svc := runtime.NewService(
				store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
				&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
				&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{},
				&fakeActivityPusher{},
				&fakeOwner{mode: modeAll},
			)

			_, outboxWorker := svc.Workers(clock)
			store.seed(runtime.QueueOutbox, "feed.updated",
				[]byte(`{"title":"Login Redesign","verb":"`+tc.verb+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			testutil.Eventually(t, func() bool { return notifier.count("Send") >= 1 })
			calls := notifier.callsFor("Send")
			if len(calls) != 1 {
				t.Fatalf("Send calls = %d, want exactly 1", len(calls))
			}
			var got struct {
				Title  string `json:"title"`
				Reason string `json:"reason"`
			}
			payload, ok := calls[0].Args[1].([]byte)
			if !ok {
				t.Fatalf("Send payload arg type = %T, want []byte", calls[0].Args[1])
			}
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("decode push payload: %v", err)
			}
			if got.Title != "Login Redesign" || got.Reason != tc.body {
				t.Errorf("push payload = %+v, want title=Login Redesign reason=%q", got, tc.body)
			}
		})
	}
}

// The milestones (blocked, finished) each emit their own dedicated board
// notify.send; the feed.updated they also emit must not fire a second, vaguer
// push — even in "all" mode. Both verbs are absent from feedUpdateVerbBody so
// the feed path stays silent and the completion/block push is never doubled.
func TestService_Outbox_FeedUpdatedMilestoneVerbsNeverNotify(t *testing.T) {
	for _, verb := range []string{"blocked", "finished"} {
		t.Run(verb, func(t *testing.T) {
			clock := testutil.NewFakeClock()
			store := newFakeStore(clock)
			notifier := &fakeNotifier{}
			feed := &fakeFeedPusher{}
			svc := runtime.NewService(
				store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
				&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
				&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, feed,
				&fakeActivityPusher{},
				&fakeOwner{mode: modeAll},
			)

			_, outboxWorker := svc.Workers(clock)
			store.seed(runtime.QueueOutbox, "feed.updated",
				[]byte(`{"title":"Login Redesign","verb":"`+verb+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			// The feed push confirms the entry was handled; the notifier must stay
			// untouched — the milestone's dedicated notify.send is its only push.
			testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
			if got := notifier.count("Send"); got != 0 {
				t.Errorf("Send calls = %d, want 0 — %q's push comes from notify.send, not feed.updated", got, verb)
			}
		})
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

	testutil.Eventually(t, func() bool {
		for _, p := range activity.events() {
			if p.ev.Kind == "toast" {
				return true
			}
		}
		return false
	})

	var toast *runtime.ActivityEvent
	for _, p := range activity.events() {
		if p.ev.Kind == "toast" {
			e := p.ev
			toast = &e
			break
		}
	}
	if toast == nil {
		t.Fatal("no toast activity event pushed")
	}
	if toast.Verb != "started" || toast.TicketID != "t-42" || toast.TicketTitle != "Build the widget" {
		t.Errorf("toast = %+v, want Verb=started TicketID=t-42 TicketTitle='Build the widget'", *toast)
	}
}

func TestService_Outbox_FeedCompletionPostsCard(t *testing.T) {
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
	store.seed(runtime.QueueOutbox, "feed.completion",
		[]byte(`{"ticket_id":"t1","ticket_title":"Build the widget",`+
			`"github_url":"https://github.com/o/r/pull/7","github_label":"#7",`+
			`"summary":"feat(web): build the widget"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return len(notes.completionPosts()) >= 1 })
	posts := notes.completionPosts()
	if len(posts) != 1 {
		t.Fatalf("completion posts = %d, want 1", len(posts))
	}
	if posts[0].TicketID != "t1" {
		t.Errorf("completion TicketID = %q, want t1", posts[0].TicketID)
	}
	// Styled like a poke: an empty body, with the ticket title rendered
	// separately as the card label and the client fronting a ✅ (no prose).
	if posts[0].Body != "" {
		t.Errorf("completion body = %q, want empty", posts[0].Body)
	}
	if posts[0].Key == 0 {
		t.Error("completion must be keyed on the outbox id for idempotency, got key 0")
	}
	// The GitHub link on the payload threads through to the completion card so the
	// feed can render the done card's clickable second line (08 §7).
	if posts[0].GitHubURL != "https://github.com/o/r/pull/7" || posts[0].GitHubLabel != "#7" {
		t.Errorf("completion github link = %q/%q, want the payload's pull-request URL + #7",
			posts[0].GitHubURL, posts[0].GitHubLabel)
	}
	// The work summary on the payload threads through to the completion card so the
	// feed can render the done card's body (08 §7).
	if posts[0].WorkSummary != "feat(web): build the widget" {
		t.Errorf("completion work summary = %q, want the payload's summary", posts[0].WorkSummary)
	}
	if posts[0].ProjectID != defaultTestProject {
		t.Errorf("completion card posted for project %q, want the outbox entry's %q (11 §3)",
			posts[0].ProjectID, defaultTestProject)
	}
}
