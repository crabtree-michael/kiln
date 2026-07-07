// Package push is the Web Push transport for notify.send (02 §10). The runtime
// routes a notify.send outbox entry to a Notifier (04 §2); this package's Sender
// is the real executor that replaces the v1 log-only stub — it encrypts a
// payload per stored browser subscription (RFC 8291) and POSTs it to the push
// service, authenticated with a VAPID key pair the operator supplies via env
// (02 §10). The subscription store's write side is exposed to the client through
// the api module (POST /api/push/subscribe); the read side is consumed here.
//
// Per-user routing (11 phase 2): subscriptions and the notification mode are
// keyed by user, and Send fans a notification out to exactly one user's
// subscriptions — never to another tenant's browsers.
package push

import (
	"context"
	"time"
)

// Subscription is one browser Web Push registration: the push-service endpoint
// plus the two client keys used to encrypt payloads (RFC 8291). It mirrors the
// browser's `PushSubscription.toJSON()` shape (wire.PushSubscription); ID and
// CreatedAt are store-assigned.
type Subscription struct {
	ID        int64
	Endpoint  string
	P256dh    string
	Auth      string
	CreatedAt time.Time
}

// Notification frequency modes (02 §10). ModeBlocked is the default and
// preserves the pre-existing behavior — a push only when a ticket needs a human
// decision. ModeAll fans a push out on every feed update, a testing aid. The set
// is deliberately small and extensible (more modes may be added later).
const (
	ModeBlocked = "blocked"
	ModeAll     = "all"
)

// Store persists browser push subscriptions and each user's notification mode
// (02 §2: the module owns its port; the postgres adapter lives in push/postgres).
// Save is an upsert on Endpoint so a browser re-subscribing is idempotent — the
// endpoint is globally unique, so a re-subscribe under a different user moves it
// to that user. List returns only the given user's subscriptions.
// DeleteByEndpoint prunes an endpoint the push service has reported gone
// (404/410) so a dead subscription is dropped on its next send attempt; no
// userID because the endpoint alone identifies the row. Mode is per user
// (push_user_settings); a user who never set one gets ModeBlocked, not an error.
type Store interface {
	Save(ctx context.Context, userID string, sub Subscription) error
	List(ctx context.Context, userID string) ([]Subscription, error)
	DeleteByEndpoint(ctx context.Context, endpoint string) error
	Mode(ctx context.Context, userID string) (string, error)
	SetMode(ctx context.Context, userID, mode string) error
}

// Notification is the delivered content, mapped from a board.NotifyPayload at
// the composition root (Title/Reason → Title/Body) and rendered by the service
// worker's `push` handler. URL is the path the client opens on tap
// (notificationclick) — the deep link back into the board (02 §10).
type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}
