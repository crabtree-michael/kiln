package runtime_test

// Feed assembler unit tests (08 §3, D2′): the read-only join of board state and
// retained notification history, built over its two read ports alone — no store,
// no workers, no pushers, no queue. These assertions used to run through Service
// (feed_test.go), which meant standing up an event dispatcher and a push
// coordinator to read a feed; the extraction is what lets them name only what
// they exercise.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

// errBoardViewFailed is a synthetic BoardReader failure — the feed's one
// hard dependency on live board state.
var errBoardViewFailed = errors.New("fakeBoardReader: synthetic failure")

// ---- Feed() assembly: strict order + retained history (08 §3, D2′) ---------

func TestFeed_OrdersBlockersProposalsThenUpdatesNewestFirst(t *testing.T) {
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

	snap, err := runtime.NewFeed(board, notes).Feed(ctx, defaultTestProject)
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
func TestFeed_RetainsSeenUpdatesFiltersOnlyRetracted(t *testing.T) {
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

	snap, err := runtime.NewFeed(&fakeBoardReader{}, notes).Feed(ctx, defaultTestProject)
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
	// SeenAt rides the card (08 D2″) — it is what starts the client's linger
	// window, so an unseen card must carry nil and never be auto-hidden.
	if snap.Cards[0].SeenAt != nil {
		t.Errorf("unseen card SeenAt = %v, want nil (an unseen card never lingers out)", snap.Cards[0].SeenAt)
	}
	if snap.Cards[1].SeenAt == nil {
		t.Error("seen card SeenAt = nil, want the seen stamp (08 D2″ linger window start)")
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
func TestFeed_PagesNewestAndFlagsMoreHistory(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	// Seed one more than a page so the newest page leaves history behind.
	total := 35 // feedPageSize (30) + 5
	for range total {
		notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "n"})
	}

	snap, err := runtime.NewFeed(&fakeBoardReader{}, notes).Feed(ctx, defaultTestProject)
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
func TestFeed_FeedHistory_KeysetPagesOlderUpdates(t *testing.T) {
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

	// Page of 2 older than the 4th id (ids[3]) -> ids[2], ids[1] newest-first, more remains.
	cards, hasMore, err := runtime.NewFeed(board, notes).FeedHistory(ctx, defaultTestProject, ids[3], 2)
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

func TestFeed_TicketTaggedUpdateGetsTicketTitleLabel(t *testing.T) {
	ctx := context.Background()
	board := &fakeBoardReader{view: runtime.BoardView{
		TicketTitles: map[string]string{"t-9": "Rate limiting"},
	}}
	notes := &fakeNotificationStore{}
	tid := "t-9"
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "shipped the limiter", TicketID: &tid})
	notes.seed(runtime.Notification{Kind: runtime.KindUpdate, Body: "standalone note"})

	snap, err := runtime.NewFeed(board, notes).Feed(ctx, defaultTestProject)
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
func TestFeed_DropsCardsForArchivedTickets(t *testing.T) {
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

	snap, err := runtime.NewFeed(board, notes).Feed(ctx, defaultTestProject)
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

// A live done card carries its GitHub link (08 §7) onto the assembled feed card
// so the client renders the clickable commit/PR second line. Only done cards do:
// an update note leaves the link fields nil.
func TestFeed_DoneCardCarriesGitHubLink(t *testing.T) {
	ctx := context.Background()
	tid := "t-done"
	board := &fakeBoardReader{view: runtime.BoardView{
		TicketTitles: map[string]string{tid: "Ship the widget"},
	}}
	notes := &fakeNotificationStore{}
	url, label := "https://github.com/o/r/commit/a1b2c3d", "a1b2c3d"
	summary := "feat(web): ship the widget"
	notes.seed(runtime.Notification{
		Kind: runtime.KindDone, TicketID: &tid,
		GitHubURL: &url, GitHubLabel: &label, WorkSummary: &summary,
	})

	snap, err := runtime.NewFeed(board, notes).Feed(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(snap.Cards) != 1 {
		t.Fatalf("Feed returned %d cards, want 1: %+v", len(snap.Cards), snap.Cards)
	}
	c := snap.Cards[0]
	if c.Kind != "done" {
		t.Fatalf("card kind = %q, want done", c.Kind)
	}
	if c.GitHubURL == nil || *c.GitHubURL != url || c.GitHubLabel == nil || *c.GitHubLabel != label {
		t.Errorf("done card github link = %v/%v, want %q/%q", c.GitHubURL, c.GitHubLabel, url, label)
	}
	if c.WorkSummary == nil || *c.WorkSummary != summary {
		t.Errorf("done card work summary = %v, want %q", c.WorkSummary, summary)
	}
}

func TestFeed_EmptyHasNilLastWord(t *testing.T) {
	snap, err := runtime.NewFeed(&fakeBoardReader{}, &fakeNotificationStore{}).
		Feed(context.Background(), defaultTestProject)
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

// A board read failure fails the whole assembly rather than serving a
// half-feed: blocker and proposal cards come from the board view, and the
// ticket titles that label every ticket-tagged note come from it too, so a
// snapshot built without it would silently drop live cards. Both read paths
// surface it — the caller decides what to do (the api 500s; the feed.updated
// handler logs-and-drops and re-renders on the next emission).
func TestFeed_BoardViewFailureSurfacesOnBothReads(t *testing.T) {
	ctx := context.Background()
	f := runtime.NewFeed(&fakeBoardReader{viewErr: errBoardViewFailed}, &fakeNotificationStore{})

	if _, err := f.Feed(ctx, defaultTestProject); !errors.Is(err, errBoardViewFailed) {
		t.Errorf("Feed error = %v, want the board read failure wrapped", err)
	}
	if _, _, err := f.FeedHistory(ctx, defaultTestProject, 10, 5); !errors.Is(err, errBoardViewFailed) {
		t.Errorf("FeedHistory error = %v, want the board read failure wrapped", err)
	}
}
