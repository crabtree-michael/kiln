package push

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ttlSeconds is how long the push service holds a message for a device that is
// offline. A blocked-ticket nudge is stale within minutes; a few hours covers a
// phone that reconnects without keeping messages around indefinitely.
const ttlSeconds = 3 * 60 * 60

// presenceTTL is the staleness window on a device's foreground-presence lease
// (02 §10 push dedup): a device whose last visible heartbeat is younger than
// this is treated as foregrounded and skipped, since the in-app toast already
// surfaces the event. It is deliberately short — ≈ 2× the client's 15s heartbeat
// plus network slack — because it is the safety window for the crash path: when
// the leave beacon does not fire, a backgrounded device's lease must age out
// fast so we resume sending. "Too short" only costs a duplicate; "too long"
// risks a missed notification, and the design biases to send.
const presenceTTL = 30 * time.Second

// Sender delivers a Notification to every subscription stored for one user,
// encrypting per RFC 8291 and authenticating with the operator's VAPID key pair.
// It is the real notify.send executor (02 §10) built at the composition root
// only when the VAPID env vars are set; otherwise the runtime keeps the
// log-only notifier.
type Sender struct {
	store   Store
	pub     string // VAPID public key (base64url)
	priv    string // VAPID private key (base64url)
	subject string // VAPID "sub": mailto: or https contact for the push service
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time // injectable clock for the presence-lease check (tests)
}

// NewSender builds a Sender over a subscription store and a VAPID key pair. The
// caller is responsible for only constructing one when pub/priv are non-empty
// (an empty key pair would make every send fail).
func NewSender(store Store, pub, priv, subject string, client *http.Client, log *slog.Logger) *Sender {
	if client == nil {
		client = http.DefaultClient
	}
	return &Sender{store: store, pub: pub, priv: priv, subject: subject, client: client, log: log, now: time.Now}
}

// SetClock overrides the Sender's clock, used by tests to drive the
// presence-lease boundary deterministically. Production always uses time.Now.
func (s *Sender) SetClock(now func() time.Time) { s.now = now }

// Send fans a notification out to the given user's stored subscriptions only —
// per-user routing (11 phase 2), never another tenant's browsers. Delivery is
// best-effort per subscription: one failing endpoint never blocks the others,
// and a 404/410 (the push service reporting the subscription gone) prunes it so
// it is not retried next time. "A rare duplicate notification is accepted as
// benign" (04 §3), so partial failure returns nil — a hard error here would
// dead-letter the outbox entry for a problem that is per-device, not systemic.
func (s *Sender) Send(ctx context.Context, userID string, n Notification) error {
	payload, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("push: marshal notification: %w", err)
	}
	subs, err := s.store.List(ctx, userID)
	if err != nil {
		return fmt.Errorf("push: list subscriptions: %w", err)
	}
	now := s.now()
	for _, sub := range subs {
		// Server-side dedup (02 §10): if this device reported itself foregrounded
		// within presenceTTL, the in-app toast already surfaces the event, so skip
		// the push rather than deliver a duplicate OS banner. This is the ONLY
		// behavior change versus always-send, and it is fail-open by construction —
		// a nil/absent lease (see foregrounded) resolves to send. The skip is logged
		// so suppression is observable, never silent.
		if s.foregrounded(sub, now) {
			s.log.Info("push: suppressed (device foregrounded)",
				"endpoint", sub.Endpoint,
				"lease_age", now.Sub(*sub.LastSeenForegroundAt).Round(time.Second))
			continue
		}
		s.sendOne(ctx, sub, payload)
	}
	return nil
}

// foregrounded reports whether a device's presence lease is fresh enough to
// suppress its push. Only a positive, recent heartbeat qualifies: a nil lease
// (never reported foreground, or cleared on backgrounding) and a lease older
// than presenceTTL both return false → send. Every "we couldn't tell" path lands
// here as false, so tracking failure can never eat a notification (02 §10
// reliability constraint).
func (s *Sender) foregrounded(sub Subscription, now time.Time) bool {
	return sub.LastSeenForegroundAt != nil &&
		now.Sub(*sub.LastSeenForegroundAt) < presenceTTL
}

// sendOne delivers to a single subscription and prunes it if the push service
// says it is gone. Errors are logged, never propagated (see Send).
func (s *Sender) sendOne(ctx context.Context, sub Subscription, payload []byte) {
	resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient:      s.client,
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.pub,
		VAPIDPrivateKey: s.priv,
		TTL:             ttlSeconds,
	})
	if err != nil {
		s.log.Warn("push: send failed", "endpoint", sub.Endpoint, "err", err)
		return
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.Warn("push: close response body", "endpoint", sub.Endpoint, "err", cerr)
		}
	}()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The subscription is permanently invalid — drop it so we stop trying.
		if err := s.store.DeleteByEndpoint(ctx, sub.Endpoint); err != nil {
			s.log.Warn("push: prune expired subscription", "endpoint", sub.Endpoint, "err", err)
		}
	case resp.StatusCode >= http.StatusMultipleChoices:
		s.log.Warn("push: send rejected", "endpoint", sub.Endpoint, "status", resp.StatusCode)
	}
}
