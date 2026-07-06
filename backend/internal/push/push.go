// Package push is the Web Push transport for notify.send (02 §10). The runtime
// routes a notify.send outbox entry to a Notifier (04 §2); this package's Sender
// is the real executor that replaces the v1 log-only stub — it encrypts a
// payload per stored browser subscription (RFC 8291) and POSTs it to the push
// service, authenticated with a VAPID key pair the operator supplies via env
// (02 §10). The subscription store's write side is exposed to the client through
// the api module (POST /api/push/subscribe); the read side is consumed here.
//
// Single user in v1 (spec 10 scope): subscriptions are stored globally and every
// notification fans out to all of them — there is no per-user routing yet.
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

// Store persists browser push subscriptions and the global notification mode
// (02 §2: the module owns its port; the postgres adapter lives in push/postgres).
// Save is an upsert on Endpoint so a browser re-subscribing is idempotent;
// DeleteByEndpoint prunes an endpoint the push service has reported gone
// (404/410) so a dead subscription is dropped on its next send attempt. Mode is
// a single global value in v1 (single user); it defaults to ModeBlocked when
// never set.
type Store interface {
	Save(ctx context.Context, sub Subscription) error
	List(ctx context.Context) ([]Subscription, error)
	DeleteByEndpoint(ctx context.Context, endpoint string) error
	Mode(ctx context.Context) (string, error)
	SetMode(ctx context.Context, mode string) error
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
