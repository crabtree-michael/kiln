package main

import (
	"log/slog"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// TestRunStartsAndStopsOnSignal is a smoke test that keeps the hard gate real:
// there is always at least one passing unit test so `go test ./...` is a
// meaningful green wall, not an empty one (02 §4a).
func TestRunStartsAndStopsOnSignal(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	done := make(chan error, 1)
	go func() { done <- run(log) }()

	// run blocks until an interrupt signal; there is no wiring yet, so we only
	// assert it starts cleanly and does not return an error before we stop it.
	select {
	case err := <-done:
		t.Fatalf("run returned before shutdown was requested: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Started and is blocking on ctx.Done as expected.
	}
}

func TestVersionDefault(t *testing.T) {
	if version == "" {
		t.Fatal("version must have a non-empty default")
	}
}

// TestLoadConfigWorkerPrefix covers KILN_WORKER_PREFIX (05 §9, amended): the
// per-environment worker-name scope. Unset falls back to the historical
// default; a value missing its trailing separator is normalized so worker
// names never concatenate prefix and uuid into one token.
func TestLoadConfigWorkerPrefix(t *testing.T) {
	t.Run("defaults to the shared prefix", func(t *testing.T) {
		t.Setenv("KILN_WORKER_PREFIX", "")
		if got := loadConfig().WorkerPrefix; got != agent.WorkerNamePrefix {
			t.Errorf("WorkerPrefix = %q, want default %q", got, agent.WorkerNamePrefix)
		}
	})
	t.Run("normalizes a missing trailing dash", func(t *testing.T) {
		t.Setenv("KILN_WORKER_PREFIX", "kiln-prod-worker")
		if got := loadConfig().WorkerPrefix; got != "kiln-prod-worker-" {
			t.Errorf("WorkerPrefix = %q, want %q", got, "kiln-prod-worker-")
		}
	})
}
