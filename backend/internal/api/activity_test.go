package api_test

// Activity-status endpoint tests (08 §4): GET /api/activity reflects the
// current `thinking` bracket the hub last fanned out, so a client that missed
// the ephemeral SSE frame can resync the spinner on foreground/resume. Driven
// over real net/http via httptest, exercising the hub -> handler seam.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// thinkingEvent builds a kind=thinking activity event with the given on-bracket.
func thinkingEvent(on bool) runtime.ActivityEvent {
	return runtime.ActivityEvent{Kind: "thinking", On: &on}
}

// getThinking issues GET /api/activity and returns the decoded thinking flag.
func getThinking(t *testing.T, baseURL string) bool {
	t.Helper()
	resp := doGet(t, baseURL+"/api/activity")
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("close body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/activity: status = %d, want 200", resp.StatusCode)
	}
	var status wire.ActivityStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode ActivityStatus: %v", err)
	}
	return status.Thinking
}

func TestActivityStatusReflectsThinkingBracket(t *testing.T) {
	boards := &fakeBoardReader{}
	hub := api.NewHub(boards)
	srv := api.NewServer(boards, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, &fakeVoiceTokenMinter{})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A fresh hub has never bracketed a pass, so nothing is in flight.
	if got := getThinking(t, ts.URL); got {
		t.Fatalf("initial thinking = %v, want false", got)
	}

	// The events worker opens a pass: on:true fans out and the status flips.
	if err := hub.PushActivity(context.Background(), thinkingEvent(true)); err != nil {
		t.Fatalf("push thinking on: %v", err)
	}
	if got := getThinking(t, ts.URL); !got {
		t.Fatalf("thinking after on = %v, want true", got)
	}

	// A toast in the middle of the pass must not disturb the thinking bracket.
	if err := hub.PushActivity(context.Background(),
		runtime.ActivityEvent{Kind: "toast", Verb: "started", TicketTitle: "Login"}); err != nil {
		t.Fatalf("push toast: %v", err)
	}
	if got := getThinking(t, ts.URL); !got {
		t.Fatalf("thinking after intervening toast = %v, want true", got)
	}

	// The pass closes: on:false fans out and the status clears — the value a
	// backgrounded client resyncs to once its stream reattaches.
	if err := hub.PushActivity(context.Background(), thinkingEvent(false)); err != nil {
		t.Fatalf("push thinking off: %v", err)
	}
	if got := getThinking(t, ts.URL); got {
		t.Fatalf("thinking after off = %v, want false", got)
	}
}
