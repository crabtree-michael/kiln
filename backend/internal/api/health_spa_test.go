package api_test

// Health probe, embedded-SPA fallback, and the SSE-through-Sentry-middleware
// guarantee (design 2026-07-05). The last is the load-bearing check: the Sentry
// HTTP middleware wraps the ResponseWriter, and the board stream (hub.go) needs
// that wrapper to still expose http.Flusher or SSE silently stops streaming.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sentryhttp "github.com/getsentry/sentry-go/http"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// errPingDown is the static failure a fake health pinger returns for the
// degraded-path test (err113: no dynamic inline errors).
var errPingDown = errors.New("db down")

func TestHealthz_OKWhenPingSucceeds(t *testing.T) {
	srv := newBareServer()
	srv.EnableHealthz("v-test", func(context.Context) error { return nil })
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/healthz")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" || got.Version != "v-test" {
		t.Errorf("body = %+v, want status=ok version=v-test", got)
	}
}

func TestHealthz_DegradedWhenPingFails(t *testing.T) {
	srv := newBareServer()
	srv.EnableHealthz("v-test", func(context.Context) error { return errPingDown })
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	resp := doGet(t, ts.URL+"/healthz")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	var got struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
}

func TestSPA_FallsBackToIndexForClientRoute(t *testing.T) {
	srv := newBareServer()
	srv.EnableSPA(fakeSPAHandler(t))
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	// A client route with no matching asset must render the SPA shell, not 404.
	resp := doGet(t, ts.URL+"/debug")
	defer closeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestSPA_DoesNotShadowAPIRoutes(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 3}}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	srv.EnableSPA(fakeSPAHandler(t))
	ts := httptest.NewServer(enableSession(srv).Handler())
	defer ts.Close()

	// The specific /api/board pattern must win over the "/" SPA catch-all.
	resp := doGet(t, ts.URL+"/api/board")
	defer closeBody(t, resp)
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (API route, not SPA)", ct)
	}
}

// TestSSE_SurvivesSentryHTTPMiddleware is the SSE-flush guarantee: wrap the mux
// in the same Sentry HTTP middleware used in production and confirm the board
// stream still connects (text/event-stream) and delivers its snapshot frame —
// which only happens if the wrapped ResponseWriter still implements http.Flusher.
func TestSSE_SurvivesSentryHTTPMiddleware(t *testing.T) {
	boards := &fakeBoardReader{snapshot: board.Snapshot{WorkerTotal: 7, WorkerFree: 1}}
	srv := api.NewServer(
		boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, api.NewHub(boards), &fakeVoiceTokenMinter{},
	)
	wrapped := sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(enableSession(srv).Handler())
	ts := httptest.NewServer(wrapped)
	defer ts.Close()

	client := connectStream(t, ts.URL) // asserts 200 + text/event-stream
	defer client.close()

	ev, ok := client.nextEvent(streamReadTimeout)
	if !ok {
		t.Fatal("no board frame through the Sentry middleware; Flusher was likely hidden")
	}
	if ev.name != sseNameBoard {
		t.Fatalf("first event = %q, want board", ev.name)
	}
}

// fakeSPAHandler stands in for the embedded web.Handler in api-package tests: it
// serves a minimal HTML shell for any path, exactly as the SPA catch-all does
// for a client route.
func fakeSPAHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write([]byte("<!doctype html><title>Kiln</title>")); err != nil {
			t.Errorf("fake SPA write: %v", err)
		}
	})
}
