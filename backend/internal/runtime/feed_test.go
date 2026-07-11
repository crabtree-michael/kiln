package runtime_test

// Feed/activity unit tests (08 §3, §4, §7): Feed() assembly ordering and
// seen filtering, the notification-op delegation, the thinking bracket around
// a brain pass, and the feed.updated / activity.toast outbox routing.

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

// ---- Feed() assembly: strict order + seen filtering (08 §3) ---------------

func TestService_Feed_OrdersBlockersProposalsThenUpdatesNewestFirst(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

	board := &fakeBoardReader{view: runtime.BoardView{
		Blocked: []runtime.BoardTicket{
			{ID: "b1", Title: "Blocked one", BlockedReason: "needs a key", UpdatedAt: base},
		},
		Proposals: []runtime.BoardTicket{
			{ID: "p1", Title: "Proposal one", Body: "shaped plan", UpdatedAt: base.Add(time.Minute)},
		},
		WorkingCount: 3,
		BlockedCount: 1,
	}}
	notes := &fakeNotificationStore{}
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "older", CreatedAt: base.Add(2 * time.Minute)})
	img := "https://img/x.png"
	newest := notes.seed(runtime.Notification{
		Kind: runtime.KindPreview, Body: "newer", ImageURL: &img, CreatedAt: base.Add(3 * time.Minute),
	})

	svc := newFeedService(notes, board, &fakeFeedPusher{}, &fakeActivityPusher{})
	snap, err := svc.Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}

	if len(snap.Cards) != 4 {
		t.Fatalf("Feed returned %d cards, want 4 (1 blocker, 1 proposal, 2 updates)", len(snap.Cards))
	}
	if snap.Cards[0].Kind != "blocker" || snap.Cards[0].ID != "blocker:b1" {
		t.Errorf("card[0] = %+v, want blocker:b1 first (blockers pinned on top)", snap.Cards[0])
	}
	if snap.Cards[0].Label != "Blocked one" || snap.Cards[0].Body != "needs a key" {
		t.Errorf("blocker card = %+v, want Label=title Body=blocked_reason", snap.Cards[0])
	}
	if !snap.Cards[0].CreatedAt.Equal(base) {
		t.Errorf("blocker CreatedAt = %v, want UpdatedAt %v", snap.Cards[0].CreatedAt, base)
	}
	if snap.Cards[1].Kind != "proposal" || snap.Cards[1].ID != "proposal:p1" || snap.Cards[1].Body != "shaped plan" {
		t.Errorf("card[1] = %+v, want proposal:p1 with Body=shaped plan", snap.Cards[1])
	}
	// Updates newest-first: preview 'newer' before 'older'.
	if snap.Cards[2].Kind != "preview" || snap.Cards[2].Body != "newer" {
		t.Errorf("card[2] = %+v, want the newest (preview 'newer') first", snap.Cards[2])
	}
	if snap.Cards[2].ImageURL == nil || *snap.Cards[2].ImageURL != img {
		t.Errorf("preview card ImageURL = %v, want %q", snap.Cards[2].ImageURL, img)
	}
	if snap.Cards[2].NotificationID == nil || *snap.Cards[2].NotificationID != newest.ID {
		t.Errorf("preview card NotificationID = %v, want %d", snap.Cards[2].NotificationID, newest.ID)
	}
	if snap.Cards[3].Kind != "update" || snap.Cards[3].Body != "older" {
		t.Errorf("card[3] = %+v, want the older update last", snap.Cards[3])
	}
	if snap.Cards[3].ImageURL != nil {
		t.Errorf("update card ImageURL = %v, want nil (only previews carry an image)", snap.Cards[3].ImageURL)
	}

	// Summary.
	s := snap.Summary
	if s.BlockerCount != 1 || s.UpdateCount != 2 || s.StreamCount != 4 || s.Building != 3 || s.Idle != 1 {
		t.Errorf("summary = %+v, want BlockerCount=1 UpdateCount=2 StreamCount=4 Building=3 Idle=1", s)
	}
	if s.LastWordAt == nil || !s.LastWordAt.Equal(base.Add(3*time.Minute)) {
		t.Errorf("LastWordAt = %v, want the newest notification's CreatedAt %v", s.LastWordAt, base.Add(3*time.Minute))
	}
}

// Retained history (08 D2′): seen updates STAY in the feed as history — only
// retracted ones drop out. UpdateCount still counts unseen (the "new" ones), and
// LastSeenNotificationID marks the last-seen divider boundary.
func TestService_Feed_RetainsSeenUpdatesFiltersOnlyRetracted(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	seen := notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "already seen"})
	retracted := notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "withdrawn"})
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "still unseen"})

	// Mark the first as seen via the high-water path, and retract the second.
	if err := notes.MarkSeen(ctx, defaultTestProject, seen.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if err := notes.RetractNotification(ctx, defaultTestProject, retracted.ID); err != nil {
		t.Fatalf("RetractNotification: %v", err)
	}

	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})
	snap, err := svc.Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	// Both the seen and the unseen update remain (newest-first); the retracted one is gone.
	if len(snap.Cards) != 2 {
		t.Fatalf("Feed cards = %+v, want 2 (seen retained + unseen), retracted filtered", snap.Cards)
	}
	if snap.Cards[0].Body != "still unseen" || snap.Cards[1].Body != "already seen" {
		t.Fatalf("Feed cards = %+v, want [still unseen, already seen] newest-first", snap.Cards)
	}
	if snap.Summary.UpdateCount != 1 {
		t.Errorf("UpdateCount = %d, want 1 (only the unseen one is 'new')", snap.Summary.UpdateCount)
	}
	if snap.Summary.LastSeenNotificationID == nil || *snap.Summary.LastSeenNotificationID != seen.ID {
		t.Errorf("LastSeenNotificationID = %v, want the seen high-water %d", snap.Summary.LastSeenNotificationID, seen.ID)
	}
	if snap.HasMoreHistory {
		t.Errorf("HasMoreHistory = true, want false (only 2 retained rows, well under a page)")
	}
}

// The snapshot carries only the newest page; HasMoreHistory flags that older
// retained updates exist to page in via FeedHistory (08 D2′).
func TestService_Feed_PagesNewestAndFlagsMoreHistory(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	// Seed one more than a page so the newest page leaves history behind.
	total := 35 // feedPageSize (30) + 5
	for range total {
		notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "n"})
	}

	svc := newFeedService(notes, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{})
	snap, err := svc.Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(snap.Cards) != 30 {
		t.Fatalf("Feed returned %d update cards, want the newest page of 30", len(snap.Cards))
	}
	if !snap.HasMoreHistory {
		t.Errorf("HasMoreHistory = false, want true (35 retained rows > one 30 page)")
	}
	// UpdateCount is the total unseen, not just the page.
	if snap.Summary.UpdateCount != total {
		t.Errorf("UpdateCount = %d, want %d (all unseen, page-independent)", snap.Summary.UpdateCount, total)
	}
}

// FeedHistory is keyset-paged: cards older than `before`, newest-first, with a
// has-more flag; it never returns board-derived cards (08 D2′).
func TestService_FeedHistory_KeysetPagesOlderUpdates(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	ids := make([]int64, 0, 5)
	for range 5 {
		ids = append(ids, notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "n"}).ID)
	}
	// A blocker in the board view must NOT leak into a history page.
	board := &fakeBoardReader{view: runtime.BoardView{
		Blocked: []runtime.BoardTicket{{ID: "b1", Title: "Blocked"}},
	}}
	svc := newFeedService(notes, board, &fakeFeedPusher{}, &fakeActivityPusher{})

	// Page of 2 older than the 4th id (ids[3]) -> ids[2], ids[1] newest-first, more remains.
	cards, hasMore, err := svc.FeedHistory(ctx, defaultTestProject, ids[3], 2)
	if err != nil {
		t.Fatalf("FeedHistory: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("FeedHistory returned %d cards, want the 2-card page", len(cards))
	}
	for _, c := range cards {
		if c.Kind != "update" {
			t.Errorf("history card kind = %q, want only update/preview (no board cards)", c.Kind)
		}
	}
	if cards[0].NotificationID == nil || *cards[0].NotificationID != ids[2] {
		t.Errorf("history[0] id = %v, want ids[2]=%d (newest below the cursor)", cards[0].NotificationID, ids[2])
	}
	if !hasMore {
		t.Errorf("hasMore = false, want true (ids[0] still remains below the page)")
	}
}

func TestService_Feed_TicketTaggedUpdateGetsTicketTitleLabel(t *testing.T) {
	ctx := context.Background()
	board := &fakeBoardReader{view: runtime.BoardView{
		TicketTitles: map[string]string{"t-9": "Rate limiting"},
	}}
	notes := &fakeNotificationStore{}
	tid := "t-9"
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "shipped the limiter", TicketID: &tid})
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "standalone note"})

	snap, err := newFeedService(notes, board, &fakeFeedPusher{}, &fakeActivityPusher{}).Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(snap.Cards) != 2 {
		t.Fatalf("Feed returned %d cards, want 2 updates", len(snap.Cards))
	}
	// Newest-first: the standalone note (no ticket) keeps an empty label; the
	// ticket-tagged note renders the linked ticket's title.
	if snap.Cards[0].Body != "standalone note" || snap.Cards[0].Label != "" {
		t.Errorf("card[0] = %+v, want the standalone note with an empty label", snap.Cards[0])
	}
	if snap.Cards[1].Body != "shipped the limiter" || snap.Cards[1].Label != "Rate limiting" {
		t.Errorf("card[1] = %+v, want the ticket-tagged note labelled with the ticket title", snap.Cards[1])
	}
}

// A note tagged to a ticket that has been archived (deleted) drops out of the
// feed entirely rather than rendering title-less. The archived ticket is gone
// from the board view (GetBoard excludes it), so its persistent "done" card
// would otherwise show as a bare ✅ with an empty title. Untagged notes and
// notes on still-live tickets are unaffected.
func TestService_Feed_DropsCardsForArchivedTickets(t *testing.T) {
	ctx := context.Background()
	board := &fakeBoardReader{view: runtime.BoardView{
		TicketTitles: map[string]string{"t-live": "Auth tokens"},
	}}
	notes := &fakeNotificationStore{}
	live, archived := "t-live", "t-gone"
	// A completion card whose ticket was archived after it was posted — the bug.
	notes.seed(runtime.Notification{Kind: runtime.KindDone, TicketID: &archived})
	// An update note tagged to the same archived ticket — same ghost class.
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "orphaned note", TicketID: &archived})
	// Survivors: a live-ticket note and an untagged note.
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "live note", TicketID: &live})
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "headless note"})

	snap, err := newFeedService(notes, board, &fakeFeedPusher{}, &fakeActivityPusher{}).Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(snap.Cards) != 2 {
		t.Fatalf("Feed returned %d cards, want 2 (archived-ticket cards dropped): %+v", len(snap.Cards), snap.Cards)
	}
	for _, c := range snap.Cards {
		if c.TicketID != nil && *c.TicketID == archived {
			t.Errorf("archived-ticket card leaked into feed: %+v", c)
		}
	}
	// Newest-first: the headless note, then the live-ticket note.
	if snap.Cards[0].Body != "headless note" || snap.Cards[0].Label != "" {
		t.Errorf("card[0] = %+v, want the headless note with an empty label", snap.Cards[0])
	}
	if snap.Cards[1].Body != "live note" || snap.Cards[1].Label != "Auth tokens" {
		t.Errorf("card[1] = %+v, want the live-ticket note labelled with its title", snap.Cards[1])
	}
}

func TestService_Feed_EmptyHasNilLastWord(t *testing.T) {
	snap, err := newFeedService(
		&fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{}, &fakeActivityPusher{},
	).Feed(context.Background(), defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(snap.Cards) != 0 {
		t.Errorf("empty Feed returned %d cards, want 0", len(snap.Cards))
	}
	if snap.Summary.LastWordAt != nil {
		t.Errorf("LastWordAt = %v on an empty feed, want nil", snap.Summary.LastWordAt)
	}
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

// A signal-only feed.updated (empty payload) carries no state transition — it is
// the runtime's own re-render trigger for progress narration, edits, and
// mark-seen. It must never push: only a real status change may reach the user
// (design 2026-07-07). There is no generic "board was updated" fallback anymore.
func TestService_Outbox_FeedUpdatedGenericPayloadDoesNotNotify(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notifier := &fakeNotifier{}
	feed := &fakeFeedPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
		&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, feed,
		&fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated", []byte(`{}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	// The feed push confirms the entry was handled; the notifier must stay
	// untouched — narration is silent even in all mode.
	testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
	if got := notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — a payload with no state transition must not push", got)
	}
}

// "nudged" (blocked→working) is a real state change, but it is driven by sending
// the agent an instruction — which never notifies the user (design 2026-07-07).
// "reshaped" (editing a proposal's fields) is not a state change at all. Neither
// pushes.
func TestService_Outbox_FeedUpdatedNonTransitionVerbsDoNotNotify(t *testing.T) {
	for _, verb := range []string{"nudged", "reshaped"} {
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
				&fakeOwner{},
			)

			_, outboxWorker := svc.Workers(clock)
			store.seed(runtime.QueueOutbox, "feed.updated",
				[]byte(`{"title":"Login Redesign","verb":"`+verb+`"}`), 0)

			stop := runWorker(t, outboxWorker)
			defer stop()

			testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
			if got := notifier.count("Send"); got != 0 {
				t.Errorf("Send calls = %d, want 0 — %q is not a notifying state transition", got, verb)
			}
		})
	}
}

// A descriptive feed.updated payload drives a specific push (Title = ticket,
// Body = what changed) rather than the generic "board was updated" placeholder.
func TestService_Outbox_FeedUpdatedNotifiesWithChangeDescription(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notifier := &fakeNotifier{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
		&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{},
		&fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated",
		[]byte(`{"title":"Login Redesign","verb":"finished"}`), 0)

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
	if got.Title != "Login Redesign" || got.Reason != "Finished" {
		t.Errorf("push payload = %+v, want title=Login Redesign reason=Finished (not a generic placeholder)", got)
	}
}

// A block emits its own, more-specific notify.send; the feed.updated it also
// emits must not fire a second, vaguer push (verb "blocked" is suppressed).
func TestService_Outbox_FeedUpdatedBlockedVerbSkipsGenericPush(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notifier := &fakeNotifier{}
	feed := &fakeFeedPusher{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
		&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, feed,
		&fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated",
		[]byte(`{"title":"Login Redesign","verb":"blocked"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	// The feed push confirms the entry was handled; the notifier must stay
	// untouched — the dedicated notify.send is the block's push.
	testutil.Eventually(t, func() bool { return len(feed.pushes()) >= 1 })
	if got := notifier.count("Send"); got != 0 {
		t.Errorf("Send calls = %d, want 0 — a block's push comes from notify.send, not feed.updated", got)
	}
}

// A genuine state transition pushes unconditionally — there is no
// notification-frequency gate anymore, so "done" and every other real transition
// reach the user by default (design 2026-07-07). "queued" (shaping→ready) stands
// in for the transition set.
func TestService_Outbox_FeedUpdatedTransitionNotifiesByDefault(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	notifier := &fakeNotifier{}
	svc := runtime.NewService(
		store, &fakeMessageStore{}, resolverFor(&fakeBrain{}), &fakePuller{}, &fakeBlocker{},
		&fakeAgentRuntime{}, notifier, &fakeSnapshotPusher{},
		&fakeSayPusher{}, &fakeNotificationStore{}, &fakeBoardReader{}, &fakeFeedPusher{},
		&fakeActivityPusher{},
		&fakeOwner{},
	)

	_, outboxWorker := svc.Workers(clock)
	store.seed(runtime.QueueOutbox, "feed.updated",
		[]byte(`{"title":"Login Redesign","verb":"queued"}`), 0)

	stop := runWorker(t, outboxWorker)
	defer stop()

	testutil.Eventually(t, func() bool { return notifier.count("Send") >= 1 })
	if got := notifier.count("Send"); got != 1 {
		t.Fatalf("Send calls = %d, want exactly 1 — a state transition must push by default", got)
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
		[]byte(`{"ticket_id":"t1","ticket_title":"Build the widget"}`), 0)

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
	if posts[0].ProjectID != defaultTestProject {
		t.Errorf("completion card posted for project %q, want the outbox entry's %q (11 §3)",
			posts[0].ProjectID, defaultTestProject)
	}
}
