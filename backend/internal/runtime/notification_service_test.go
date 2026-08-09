package runtime_test

// Notification CRUD unit tests (08 §3, §7): the transactional feed mutations and
// the brain's active-card read, built over the one notification store alone — no
// board reader, no pushers, no workers, no queue. These assertions used to run
// through Service (feed_test.go), which meant standing up an event dispatcher
// and a push coordinator to retract a card; the extraction is what lets them
// name only what they exercise.

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
)

func TestNotifications_PostNotification_DelegatesToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	notifs := runtime.NewNotifications(notes)
	if err := notifs.PostNotification(context.Background(), defaultTestProject, "update", "hello", nil, nil); err != nil {
		t.Fatalf("PostNotification: %v", err)
	}
	if len(notes.posts) != 1 || notes.posts[0].Body != "hello" || notes.posts[0].Kind != runtime.KindUpdate {
		t.Errorf("store posts = %+v, want a single update 'hello'", notes.posts)
	}
}

// A poke is a mechanical, feed-only card: the steward names only the ticket, and
// the facade fills in the KindPoke kind, the empty body and the ticket tag that
// makes the feed render the ticket's current title with a 👉.
func TestNotifications_PostPoke_PostsTicketTaggedPokeCard(t *testing.T) {
	notes := &fakeNotificationStore{}
	notifs := runtime.NewNotifications(notes)
	if err := notifs.PostPoke(context.Background(), defaultTestProject, "t-7"); err != nil {
		t.Fatalf("PostPoke: %v", err)
	}
	if len(notes.posts) != 1 {
		t.Fatalf("store posts = %d, want 1", len(notes.posts))
	}
	p := notes.posts[0]
	if p.Kind != runtime.KindPoke || p.Body != "" || p.TicketID == nil || *p.TicketID != "t-7" {
		t.Errorf("poke = %+v, want kind=poke, empty body, ticket t-7", p)
	}
}

func TestNotifications_EditNotification_DelegatesToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	notifs := runtime.NewNotifications(notes)
	img := "https://example.com/p.png"
	err := notifs.EditNotification(context.Background(), defaultTestProject, 7, "preview", "fixed wording", &img)
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

// The brain's retract_update and the user's per-card swipe are the same write —
// stamp the row retracted — reached from two surfaces, so both must clear the
// card from the active set.
func TestNotifications_RetractAndDismiss_ClearOneCardEach(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	retracted := notes.seed(runtime.Notification{Body: "brain withdrew this"})
	dismissed := notes.seed(runtime.Notification{Body: "user swiped this"})
	notes.seed(runtime.Notification{Body: "still active"})
	notifs := runtime.NewNotifications(notes)

	if err := notifs.RetractNotification(ctx, defaultTestProject, retracted.ID); err != nil {
		t.Fatalf("RetractNotification: %v", err)
	}
	if err := notifs.DismissNotification(ctx, defaultTestProject, dismissed.ID); err != nil {
		t.Fatalf("DismissNotification: %v", err)
	}

	got, err := notifs.ListNotifications(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(got) != 1 || got[0].Body != "still active" {
		t.Fatalf("active cards = %+v, want only 'still active' (retract and dismiss each cleared one)", got)
	}
}

// Clear-all is the bulk sibling: every still-active row goes at once.
func TestNotifications_DismissAll_ClearsEveryActiveCard(t *testing.T) {
	ctx := context.Background()
	notes := &fakeNotificationStore{}
	notes.seed(runtime.Notification{Body: "first"})
	notes.seed(runtime.Notification{Body: "second"})
	notifs := runtime.NewNotifications(notes)

	if err := notifs.DismissAllNotifications(ctx, defaultTestProject); err != nil {
		t.Fatalf("DismissAllNotifications: %v", err)
	}

	got, err := notifs.ListNotifications(ctx, defaultTestProject)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("active cards = %+v, want none after clear-all", got)
	}
}

func TestNotifications_ListNotifications_ReturnsActiveNewestFirst(t *testing.T) {
	notes := &fakeNotificationStore{}
	seen := notes.seed(runtime.Notification{Body: "first card"})
	notes.seed(runtime.Notification{Body: "second card"})
	// Mark the first as seen so it drops out of the active set.
	if err := notes.MarkSeen(context.Background(), defaultTestProject, seen.ID); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	notifs := runtime.NewNotifications(notes)

	got, err := notifs.ListNotifications(context.Background(), defaultTestProject)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(got) != 1 || got[0].Body != "second card" {
		t.Fatalf("ListNotifications = %+v, want a single active card 'second card'", got)
	}
}

func TestNotifications_MarkSeen_DelegatesHighWaterToStore(t *testing.T) {
	notes := &fakeNotificationStore{}
	notifs := runtime.NewNotifications(notes)
	if err := notifs.MarkSeen(context.Background(), defaultTestProject, 42); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	if len(notes.markSeenN) != 1 || notes.markSeenN[0] != 42 {
		t.Errorf("store MarkSeen calls = %v, want a single call with lastID=42", notes.markSeenN)
	}
}
