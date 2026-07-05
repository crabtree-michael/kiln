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
	// retracted (seen_at IS NULL AND retracted_at IS NULL), newest-first —
	// the "update"/"preview" section of the feed (08 §3).
	UnseenNotifications(ctx context.Context) ([]Notification, error)
}
