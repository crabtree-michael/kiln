package api_test

// Push registration handler unit tests (02 §10): GET /api/push/key and
// POST /api/push/subscribe, against a fake registrar over real net/http.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

var errFakeRegistrarFailed = errors.New("fakePushRegistrar: synthetic failure")

// fakePushRegistrar records the subscriptions it is asked to store and holds the
// global notification mode (defaulting to "blocked", as a fresh store would).
type fakePushRegistrar struct {
	mu   sync.Mutex
	subs []api.PushSubscription
	mode string
	err  error
}

func (f *fakePushRegistrar) Subscribe(_ context.Context, sub api.PushSubscription) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, sub)
	return nil
}

func (f *fakePushRegistrar) Mode(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mode == "" {
		return "blocked", nil
	}
	return f.mode, nil
}

func (f *fakePushRegistrar) SetMode(_ context.Context, mode string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
	return nil
}

func newPushServer(t *testing.T, reg api.PushRegistrar, vapidKey string) *httptest.Server {
	t.Helper()
	srv := newBareServer()
	srv.EnablePush(reg, vapidKey)
	return httptest.NewServer(srv.Handler())
}

func TestHandlePushKey(t *testing.T) {
	t.Run("returns configured key", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUBLICKEY")
		defer ts.Close()
		resp := doGet(t, ts.URL+"/api/push/key")
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got wire.PushKey
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Key != "BPUBLICKEY" {
			t.Errorf("key = %q, want BPUBLICKEY", got.Key)
		}
	})

	t.Run("404 when push not configured", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "")
		defer ts.Close()
		resp := doGet(t, ts.URL+"/api/push/key")
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestHandlePushSubscribe(t *testing.T) {
	body := func(endpoint, p256dh, auth string) []byte {
		return mustJSON(t, wire.PushSubscription{
			Endpoint: endpoint,
			Keys: struct {
				Auth   string `json:"auth"`
				P256dh string `json:"p256dh"`
			}{Auth: auth, P256dh: p256dh},
		})
	}

	t.Run("stores subscription and returns 204", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/push/subscribe", body("https://push.example/a", "pub", "auth"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.subs) != 1 {
			t.Fatalf("stored %d subscriptions, want 1", len(reg.subs))
		}
		if got := reg.subs[0]; got.Endpoint != "https://push.example/a" || got.P256dh != "pub" || got.Auth != "auth" {
			t.Errorf("stored %+v, want endpoint/pub/auth", got)
		}
	})

	t.Run("missing keys returns 400", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/push/subscribe", body("https://push.example/a", "", ""))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.subs) != 0 {
			t.Errorf("stored %d subscriptions on bad request, want 0", len(reg.subs))
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/push/subscribe", []byte("{not json"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("registrar error returns 500", func(t *testing.T) {
		reg := &fakePushRegistrar{err: errFakeRegistrarFailed}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/push/subscribe", body("https://push.example/a", "pub", "auth"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandlePushMode(t *testing.T) {
	t.Run("GET returns the current mode, defaulting to blocked", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUB")
		defer ts.Close()
		resp := doGet(t, ts.URL+"/api/push/mode")
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got wire.NotificationMode
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Mode != wire.NotificationModeModeBlocked {
			t.Errorf("mode = %q, want blocked", got.Mode)
		}
	})

	t.Run("PUT persists a valid mode and echoes it", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPut(t, ts.URL+"/api/push/mode", mustJSON(t, wire.NotificationMode{Mode: wire.NotificationModeModeAll}))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got wire.NotificationMode
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Mode != wire.NotificationModeModeAll {
			t.Errorf("echoed mode = %q, want all", got.Mode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if reg.mode != "all" {
			t.Errorf("stored mode = %q, want all", reg.mode)
		}
	})

	t.Run("PUT rejects an unknown mode with 400", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPut(t, ts.URL+"/api/push/mode", []byte(`{"mode":"loud"}`))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if reg.mode != "" {
			t.Errorf("stored mode = %q on bad request, want unset", reg.mode)
		}
	})
}
