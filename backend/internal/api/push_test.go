package api_test

// Push registration handler unit tests (02 §10): GET /api/push/key and
// POST /api/push/subscribe, against a fake registrar over real net/http.

import (
	"bytes"
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

// endpointA is the sample push endpoint reused across these handler tests.
const endpointA = "https://push.example/a"

// fakePushRegistrar records the subscriptions it is asked to store and holds the
// global notification mode (defaulting to "default", as a fresh store would).
type fakePushRegistrar struct {
	mu           sync.Mutex
	subs         []api.PushSubscription
	unsubscribed []string
	presence     []presenceCall
	mode         string
	err          error
	lastUserID   string
}

// presenceCall records one TouchForeground invocation for assertions.
type presenceCall struct {
	endpoint string
	visible  bool
}

func (f *fakePushRegistrar) Subscribe(_ context.Context, userID string, sub api.PushSubscription) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUserID = userID
	f.subs = append(f.subs, sub)
	return nil
}

func (f *fakePushRegistrar) Unsubscribe(_ context.Context, userID, endpoint string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUserID = userID
	f.unsubscribed = append(f.unsubscribed, endpoint)
	return nil
}

func (f *fakePushRegistrar) TouchForeground(_ context.Context, userID, endpoint string, visible bool) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUserID = userID
	f.presence = append(f.presence, presenceCall{endpoint: endpoint, visible: visible})
	return nil
}

func (f *fakePushRegistrar) Mode(_ context.Context, userID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUserID = userID
	if f.mode == "" {
		return "default", nil
	}
	return f.mode, nil
}

func (f *fakePushRegistrar) SetMode(_ context.Context, userID, mode string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastUserID = userID
	f.mode = mode
	return nil
}

func newPushServer(t *testing.T, reg api.PushRegistrar, vapidKey string) *httptest.Server {
	t.Helper()
	srv := newBareServer()
	srv.EnablePush(reg, vapidKey)
	return httptest.NewServer(enableSession(srv).Handler())
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
		resp := doPost(t, ts.URL+"/api/push/subscribe", body(endpointA, "pub", "auth"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.subs) != 1 {
			t.Fatalf("stored %d subscriptions, want 1", len(reg.subs))
		}
		if got := reg.subs[0]; got.Endpoint != endpointA || got.P256dh != "pub" || got.Auth != "auth" {
			t.Errorf("stored %+v, want endpoint/pub/auth", got)
		}
	})

	t.Run("missing keys returns 400", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/push/subscribe", body(endpointA, "", ""))
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
		resp := doPost(t, ts.URL+"/api/push/subscribe", body(endpointA, "pub", "auth"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandlePushUnsubscribe(t *testing.T) {
	unsubBody := func(endpoint string) []byte {
		return mustJSON(t, wire.PushUnsubscribe{Endpoint: endpoint})
	}

	t.Run("removes the endpoint and returns 204", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doDelete(t, ts.URL+"/api/push/subscribe", unsubBody(endpointA))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.unsubscribed) != 1 || reg.unsubscribed[0] != endpointA {
			t.Errorf("unsubscribed = %v, want [https://push.example/a]", reg.unsubscribed)
		}
	})

	t.Run("missing endpoint returns 400", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doDelete(t, ts.URL+"/api/push/subscribe", unsubBody(""))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.unsubscribed) != 0 {
			t.Errorf("unsubscribed %v on bad request, want none", reg.unsubscribed)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUB")
		defer ts.Close()
		resp := doDelete(t, ts.URL+"/api/push/subscribe", []byte("{not json"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("registrar error returns 500", func(t *testing.T) {
		reg := &fakePushRegistrar{err: errFakeRegistrarFailed}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doDelete(t, ts.URL+"/api/push/subscribe", unsubBody(endpointA))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandlePresence(t *testing.T) {
	body := func(visible bool, endpoint string) []byte {
		return mustJSON(t, wire.PresenceUpdate{Visible: visible, Endpoint: endpoint})
	}

	t.Run("records a visible heartbeat scoped to the session user and returns 204", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/presence", body(true, endpointA))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.presence) != 1 || reg.presence[0] != (presenceCall{endpoint: endpointA, visible: true}) {
			t.Fatalf("recorded presence = %+v, want one visible=true for /a", reg.presence)
		}
		if reg.lastUserID == "" {
			t.Errorf("presence was not scoped to a session user")
		}
	})

	t.Run("records a hidden leave beacon", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/presence", body(false, endpointA))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.presence) != 1 || reg.presence[0].visible {
			t.Fatalf("recorded presence = %+v, want one visible=false", reg.presence)
		}
	})

	t.Run("missing endpoint returns 400", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/presence", body(true, ""))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		reg.mu.Lock()
		defer reg.mu.Unlock()
		if len(reg.presence) != 0 {
			t.Errorf("recorded presence %+v on bad request, want none", reg.presence)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/presence", []byte("{not json"))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		ts := newPushServer(t, &fakePushRegistrar{}, "BPUB")
		defer ts.Close()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
			ts.URL+"/api/presence", bytes.NewReader(body(true, endpointA)))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		closeBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("registrar error returns 500", func(t *testing.T) {
		reg := &fakePushRegistrar{err: errFakeRegistrarFailed}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPost(t, ts.URL+"/api/presence", body(true, endpointA))
		closeBody(t, resp)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.StatusCode)
		}
	})
}

func TestHandlePushMode(t *testing.T) {
	t.Run("GET returns the current mode, defaulting to default", func(t *testing.T) {
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
		if got.Mode != wire.Default {
			t.Errorf("mode = %q, want default", got.Mode)
		}
	})

	t.Run("PUT persists a valid mode and echoes it", func(t *testing.T) {
		reg := &fakePushRegistrar{}
		ts := newPushServer(t, reg, "BPUB")
		defer ts.Close()
		resp := doPut(t, ts.URL+"/api/push/mode", mustJSON(t, wire.NotificationMode{Mode: wire.All}))
		defer closeBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var got wire.NotificationMode
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Mode != wire.All {
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
