package push_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/crabtree-michael/kiln/backend/internal/push"
)

// fakeStore is an in-memory push.Store recording deletes.
type fakeStore struct {
	mu      sync.Mutex
	subs    []push.Subscription
	deleted []string
}

func (f *fakeStore) Save(_ context.Context, s push.Subscription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, s)
	return nil
}

func (f *fakeStore) List(_ context.Context) ([]push.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]push.Subscription(nil), f.subs...), nil
}

func (f *fakeStore) DeleteByEndpoint(_ context.Context, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, endpoint)
	return nil
}

// testKeys is a throwaway VAPID pair + a client subscription key pair, generated
// once so the encrypted send path runs end-to-end against a local server. The
// returns are (vapidPublic, vapidPrivate, clientP256dh, clientAuth).
func testKeys(t *testing.T) (string, string, string, string) {
	t.Helper()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("GenerateVAPIDKeys: %v", err)
	}
	// A browser subscription's keys. webpush-go has no public helper to mint a
	// client key pair, but any well-formed P-256 public key + 16-byte auth secret
	// let the library encrypt; these are fixed valid base64url values.
	return pub, priv,
		"BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM",
		"tBHItJI5svbpez7KI4CCXg"
}

func TestSendDeliversToAllSubscriptions(t *testing.T) {
	var mu sync.Mutex
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	pub, priv, p256dh, auth := testKeys(t)
	store := &fakeStore{subs: []push.Subscription{
		{Endpoint: srv.URL + "/a", P256dh: p256dh, Auth: auth},
		{Endpoint: srv.URL + "/b", P256dh: p256dh, Auth: auth},
	}}
	sender := push.NewSender(store, pub, priv, "mailto:ops@example.com", srv.Client(), slog.New(slog.DiscardHandler))

	n := push.Notification{Title: "Blocked", Body: "needs you", URL: "/"}
	if err := sender.Send(context.Background(), n); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 2 {
		t.Fatalf("push service received %d requests, want 2", hits)
	}
}

func TestSendPrunesGoneSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone) // 410: the subscription is permanently invalid.
	}))
	defer srv.Close()

	pub, priv, p256dh, auth := testKeys(t)
	store := &fakeStore{subs: []push.Subscription{
		{Endpoint: srv.URL + "/dead", P256dh: p256dh, Auth: auth},
	}}
	sender := push.NewSender(store, pub, priv, "mailto:ops@example.com", srv.Client(), slog.New(slog.DiscardHandler))

	// Best-effort: a 410 is not a Send error, it prunes the subscription.
	if err := sender.Send(context.Background(), push.Notification{Title: "t", Body: "b", URL: "/"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.deleted) != 1 || store.deleted[0] != srv.URL+"/dead" {
		t.Fatalf("expired subscription not pruned: deleted=%v", store.deleted)
	}
}

func TestSendNoSubscriptionsIsNoop(t *testing.T) {
	pub, priv, _, _ := testKeys(t)
	sender := push.NewSender(&fakeStore{}, pub, priv, "mailto:ops@example.com", nil, slog.New(slog.DiscardHandler))
	if err := sender.Send(context.Background(), push.Notification{Title: "t", Body: "b", URL: "/"}); err != nil {
		t.Fatalf("Send with no subscriptions: %v", err)
	}
}
