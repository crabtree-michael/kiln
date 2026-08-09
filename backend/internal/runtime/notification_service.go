package runtime

import (
	"context"
	"fmt"
)

// Notifications is the notification CRUD facade: every mutation of the feed's
// notification rows (08 §3, §7) plus the brain's active-card read. It is the
// write authority over the feed — the counterpart to Feed, which assembles it
// and cannot touch a row.
//
// One discipline runs through the whole type: each method is a single
// transactional store call that writes its row AND appends a feed.updated
// outbox entry in the same transaction (the runtime's second-outbox-writer
// guarantee, 08 §7). There is no best-effort work here and nothing to
// log-and-drop — a failure is returned to the caller, so a mutation either
// lands with its re-render or does not land at all. That is why these methods
// belong together and apart from FanOut's push handlers, whose failures are
// cosmetic.
//
// Backs api's FeedMutator (MarkSeen, Dismiss*), the brain's NotificationStore
// and FeedReader (post/edit/retract/list), and the steward's Feed (PostPoke).
type Notifications struct {
	store NotificationStore
}

// NewNotifications wires the CRUD facade over the notification store. The full
// store (not just its write half) because ListNotifications reads the brain's
// active-card view.
func NewNotifications(store NotificationStore) *Notifications {
	return &Notifications{store: store}
}

// PostNotification is the brain-facing port for post_update / preview (08 §3,
// 06 tool set): persist a brain-authored notification and (in the same tx)
// append a feed.updated row so the live feed re-renders. Delegates to the
// store; the returned Notification is dropped here because the brain tool
// handler needs only success/failure.
func (n *Notifications) PostNotification(
	ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
) error {
	if _, err := n.store.PostNotification(ctx, projectID, kind, body, ticketID, imageURL); err != nil {
		return fmt.Errorf("runtime: post notification: %w", err)
	}
	return nil
}

// PostPoke posts the steward's feed-only poke card for a ticket: a KindPoke
// notification with an empty body, tagged to the ticket so the feed renders its
// current title with a 👉 (notificationToCard takes the label from the board
// view). Excluded from the unseen badge and the brain's update list at the store
// layer — a mechanical signal, not a brain-authored note.
func (n *Notifications) PostPoke(ctx context.Context, projectID, ticketID string) error {
	if _, err := n.store.PostNotification(ctx, projectID, string(KindPoke), "", &ticketID, nil); err != nil {
		return fmt.Errorf("runtime: post poke: %w", err)
	}
	return nil
}

// RetractNotification is the brain-facing port for retract_update (08 §3):
// stamp the row retracted and append feed.updated in one tx. Delegates to the
// store.
func (n *Notifications) RetractNotification(ctx context.Context, projectID string, id int64) error {
	if err := n.store.RetractNotification(ctx, projectID, id); err != nil {
		return fmt.Errorf("runtime: retract notification: %w", err)
	}
	return nil
}

// DismissNotification is the api-facing port for POST /api/feed/{id}/dismiss (08
// §3): the user swiped a single update/preview card away, so clear it for good.
// The effect is identical to the brain's retract — stamp the row retracted and
// append feed.updated in one tx — but this is user-initiated, so it lives beside
// MarkSeen (the other client-driven feed mutation) rather than the brain-facing
// RetractNotification it delegates to.
func (n *Notifications) DismissNotification(ctx context.Context, projectID string, id int64) error {
	if err := n.store.RetractNotification(ctx, projectID, id); err != nil {
		return fmt.Errorf("runtime: dismiss notification: %w", err)
	}
	return nil
}

// DismissAllNotifications is the api-facing port for POST /api/feed/dismiss-all
// (08 §3, clear-all): the user tapped the header trash affordance to clear every
// feed notification at once. Retracts all still-active rows and fans out one
// feed.updated in a single tx — the bulk sibling of DismissNotification.
func (n *Notifications) DismissAllNotifications(ctx context.Context, projectID string) error {
	if err := n.store.RetractAllNotifications(ctx, projectID); err != nil {
		return fmt.Errorf("runtime: dismiss all notifications: %w", err)
	}
	return nil
}

// EditNotification is the brain-facing port for edit_update (08 §3 amended, 06
// tool set): amend a still-active card's kind/body/image in place and append
// feed.updated in one tx. Delegates to the store; the brain tool handler needs
// only success/failure.
func (n *Notifications) EditNotification(
	ctx context.Context, projectID string, id int64, kind, body string, imageURL *string,
) error {
	if err := n.store.EditNotification(ctx, projectID, id, kind, body, imageURL); err != nil {
		return fmt.Errorf("runtime: edit notification: %w", err)
	}
	return nil
}

// ListNotifications is the brain-facing port for list_updates (06 tool set): the
// active (neither seen nor retracted) feed cards, newest-first, so the brain can
// see the ids it may edit or retract. Delegates to the store's UnseenNotifications.
func (n *Notifications) ListNotifications(ctx context.Context, projectID string) ([]Notification, error) {
	notes, err := n.store.UnseenNotifications(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("runtime: list notifications: %w", err)
	}
	return notes, nil
}

// MarkSeen is the api-facing port for POST /api/feed/seen (08 §3): stamp every
// still-unseen notification up to the client's high-water id, and append
// feed.updated in one tx. Delegates to the store.
func (n *Notifications) MarkSeen(ctx context.Context, projectID string, lastID int64) error {
	if err := n.store.MarkSeen(ctx, projectID, lastID); err != nil {
		return fmt.Errorf("runtime: mark seen: %w", err)
	}
	return nil
}
