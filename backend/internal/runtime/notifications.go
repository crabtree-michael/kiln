package runtime

import (
	"context"
	"time"
)

// NotificationKind is a brain-authored notification's flavor (08 §3): a plain
// "update" note, or a "preview" that carries an image_url. Mirrors the wire
// FeedCard kinds for update/preview by value; the api package maps to wire.
type NotificationKind string

const (
	// KindUpdate is a brain-authored note worth a glance (08 §3).
	KindUpdate NotificationKind = "update"
	// KindPreview is a brain-authored note with an image (08 §3).
	KindPreview NotificationKind = "preview"
	// KindPoke is a mechanical stall-nudge card the steward posts when it pokes a
	// stalled Working ticket's agent (feed-only: the ticket title carries a 👉,
	// body empty). Not brain-authored — excluded from the brain's editable update
	// list and the unseen-updates badge, but retained in the feed like any note.
	KindPoke NotificationKind = "poke"
)

// Notification is one row of the notifications table (08 §3, §7): a
// brain-authored feed card the runtime owns because it faces the client and is
// the second outbox writer. Append-only in spirit — a row is never deleted,
// only stamped SeenAt (the user caught up) or RetractedAt (the brain withdrew
// it).
type Notification struct {
	ID          int64
	Kind        NotificationKind
	TicketID    *string
	Body        string
	ImageURL    *string
	CreatedAt   time.Time
	SeenAt      *time.Time
	RetractedAt *time.Time
}

// NotificationStore is the runtime's persistence port over the notifications
// table (08 §3, §7). Implemented by ./postgres alongside Store and
// MessageStore. Every mutation (post, retract, mark-seen) appends a
// feed.updated outbox row in the SAME transaction — this makes the runtime a
// second outbox writer (08 §7), so a live feed re-render fans out exactly when
// the feed's contents change.
type NotificationStore interface {
	// PostNotification inserts one brain-authored notification and appends a
	// feed.updated outbox row in one transaction (08 §7). kind is "update" or
	// "preview"; ticketID/imageURL are optional.
	PostNotification(ctx context.Context, kind, body string, ticketID, imageURL *string) (Notification, error)

	// RetractNotification stamps retracted_at=now() on the row and appends a
	// feed.updated outbox row in one transaction (08 §3 — the brain withdrew a
	// note that stopped mattering).
	RetractNotification(ctx context.Context, id int64) error

	// EditNotification amends a still-active (non-retracted) notification's kind,
	// body and image in place and appends a feed.updated outbox row in one
	// transaction — the brain's edit_update (06 §4 amended): fix a card's wording
	// or swap its preview without retract-and-repost. kind is recomputed by the
	// caller from the image's presence ("preview" with an image, else "update").
	EditNotification(ctx context.Context, id int64, kind, body string, imageURL *string) error

	// MarkSeen stamps seen_at=now() on every still-unseen notification with
	// id <= lastID (the high-water mark the client reports), and appends a
	// feed.updated outbox row in one transaction (08 §3).
	MarkSeen(ctx context.Context, lastID int64) error

	// UnseenNotifications returns notifications that are neither seen nor
	// retracted (seen_at IS NULL AND retracted_at IS NULL), newest-first — the
	// brain's active-card view (list_updates: the ids it may edit or retract,
	// 06 §4). NOT the feed's update section anymore — retained history keeps
	// seen rows visible, so the feed reads RecentNotifications instead (08 D2′).
	UnseenNotifications(ctx context.Context) ([]Notification, error)

	// RecentNotifications returns the newest `limit` unretracted notifications
	// (seen AND unseen), newest-first — the first page of the feed's retained
	// update/preview history (08 D2′). The bool is true when at least one older
	// unretracted row exists beyond the page (drives FeedSnapshot.HasMoreHistory).
	RecentNotifications(ctx context.Context, limit int) ([]Notification, bool, error)

	// HistoryBefore returns up to `limit` unretracted notifications with
	// id < before, newest-first — one older page for keyset pagination
	// (GET /api/feed/history, 08 D2′). The bool is true when another older page
	// remains beyond this one.
	HistoryBefore(ctx context.Context, before int64, limit int) ([]Notification, bool, error)

	// LastSeenID returns the greatest id among seen, unretracted notifications —
	// the persistent last-seen divider boundary (08 D2′). Nil when nothing has
	// been seen yet.
	LastSeenID(ctx context.Context) (*int64, error)

	// UnseenCount returns the number of unseen, unretracted notifications — the
	// header's "N updates" count, still meaning "new since last seen" (08 §2).
	UnseenCount(ctx context.Context) (int, error)
}
