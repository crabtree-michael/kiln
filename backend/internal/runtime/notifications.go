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
	// KindDone is the mechanical completion card the runtime posts when a ticket
	// is accepted to Done (08 §7): feed-only, body empty, the ticket title carries
	// a ✅. Like a poke it is not brain-authored — excluded from the brain's
	// editable update list and the unseen-updates badge, but retained in the feed.
	KindDone NotificationKind = "done"
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
	// GitHubURL/GitHubLabel are set on "done" completion cards (08 §7): the link
	// to the landed work (a commit or pull request page) and its clickable label
	// (abbreviated SHA or "#<number>"), rendered as the card's second line. Nil on
	// every other kind, and on completion cards with no link available.
	GitHubURL   *string
	GitHubLabel *string
	// WorkSummary is set on "done" completion cards (08 §7): the full description
	// of the landed work (the commit message, or the PR title + description),
	// rendered as the card's expandable body — a preview that opens to the full
	// text. Nil on every other kind, and on completion cards whose summary could
	// not be read.
	WorkSummary *string
}

// NotificationStore is the runtime's persistence port over the notifications
// table (08 §3, §7). Implemented by ./postgres alongside Store and
// MessageStore. Every mutation (post, retract, mark-seen) appends a
// feed.updated outbox row in the SAME transaction — this makes the runtime a
// second outbox writer (08 §7), so a live feed re-render fans out exactly when
// the feed's contents change. Split into a write half (mutations, each
// outbox-appending) and a read half (queries) — both satisfied by the one
// ./postgres Store — so neither role interface bloats past the surface a single
// interface should carry.
type NotificationStore interface {
	NotificationWriter
	NotificationReader
}

// NotificationWriter is the mutation half of NotificationStore (08 §3, §7):
// every method here appends a feed.updated outbox row in the SAME transaction as
// its write, keeping the runtime's second-outbox-writer guarantee. Every method
// is tenant-scoped (11 §3): inserts stamp project_id (including the appended
// feed.updated row, so the re-render fans out to the right project) and
// mutations predicate on it, so one project can never touch another's rows.
type NotificationWriter interface {
	// PostNotification inserts one brain-authored notification and appends a
	// feed.updated outbox row in one transaction (08 §7). kind is "update" or
	// "preview"; ticketID/imageURL are optional.
	PostNotification(ctx context.Context, projectID, kind, body string, ticketID, imageURL *string) (Notification, error)

	// PostCompletionCard inserts a mechanical "done" feed card (kind "done")
	// for a completed ticket and appends a feed.updated outbox row — the
	// persistent counterpart to the ephemeral finished toast (08 §7). Not
	// brain-authored: the board's feed.completion outbox entry drives it. key is
	// the outbox entry id, used as an idempotency key (ON CONFLICT DO NOTHING) so
	// an at-least-once redelivery posts no duplicate card; posted is false (and no
	// feed.updated is enqueued) when the row already existed. githubURL/githubLabel
	// link the card to the landed commit or pull request (empty when unavailable);
	// workSummary is the landed work's full description shown as the card's
	// expandable body (empty when unavailable → a body-less card).
	PostCompletionCard(
		ctx context.Context, projectID string, key int64, ticketID, body, githubURL, githubLabel, workSummary string,
	) (posted bool, err error)

	// RetractNotification stamps retracted_at=now() on the project's row and
	// appends a feed.updated outbox row in one transaction (08 §3 — the brain
	// withdrew a note that stopped mattering).
	RetractNotification(ctx context.Context, projectID string, id int64) error

	// RetractAllNotifications stamps retracted_at=now() on EVERY still-active
	// (un-retracted) notification of the project and appends a single
	// feed.updated outbox row in one transaction — the user's "clear all" (08 §3,
	// header trash affordance). Idempotent: with nothing active it retracts no
	// rows but still fans out one harmless re-render.
	RetractAllNotifications(ctx context.Context, projectID string) error

	// EditNotification amends a still-active (non-retracted) notification's kind,
	// body and image in place and appends a feed.updated outbox row in one
	// transaction — the brain's edit_update (06 §4 amended): fix a card's wording
	// or swap its preview without retract-and-repost. kind is recomputed by the
	// caller from the image's presence ("preview" with an image, else "update").
	EditNotification(ctx context.Context, projectID string, id int64, kind, body string, imageURL *string) error

	// MarkSeen stamps seen_at=now() on every still-unseen notification of the
	// project with id <= lastID (the high-water mark the client reports), and
	// appends a feed.updated outbox row in one transaction (08 §3).
	MarkSeen(ctx context.Context, projectID string, lastID int64) error
}

// NotificationReader is the query half of NotificationStore (08 §3, D2′): the
// read paths the feed assembly and the brain's active-card view draw on. No
// method here mutates state or touches the outbox. Every read is scoped to one
// project (11 §3) — the WHERE project_id predicate, not the caller, is what
// keeps one tenant's feed out of another's.
type NotificationReader interface {
	// UnseenNotifications returns the project's notifications that are neither
	// seen nor retracted (seen_at IS NULL AND retracted_at IS NULL), newest-first
	// — the brain's active-card view (list_updates: the ids it may edit or
	// retract, 06 §4). NOT the feed's update section anymore — retained history
	// keeps seen rows visible, so the feed reads RecentNotifications instead
	// (08 D2′).
	UnseenNotifications(ctx context.Context, projectID string) ([]Notification, error)

	// RecentNotifications returns the project's newest `limit` unretracted
	// notifications (seen AND unseen), newest-first — the first page of the
	// feed's retained update/preview history (08 D2′). The bool is true when at
	// least one older unretracted row exists beyond the page (drives
	// FeedSnapshot.HasMoreHistory).
	RecentNotifications(ctx context.Context, projectID string, limit int) ([]Notification, bool, error)

	// HistoryBefore returns up to `limit` of the project's unretracted
	// notifications with id < before, newest-first — one older page for keyset
	// pagination (GET /api/feed/history, 08 D2′). The bool is true when another
	// older page remains beyond this one.
	HistoryBefore(ctx context.Context, projectID string, before int64, limit int) ([]Notification, bool, error)

	// LastSeenID returns the greatest id among the project's seen, unretracted
	// notifications — the persistent last-seen divider boundary (08 D2′). Nil
	// when nothing has been seen yet.
	LastSeenID(ctx context.Context, projectID string) (*int64, error)

	// UnseenCount returns the number of the project's unseen, unretracted
	// notifications — the header's "N updates" count, still meaning "new since
	// last seen" (08 §2).
	UnseenCount(ctx context.Context, projectID string) (int, error)
}
