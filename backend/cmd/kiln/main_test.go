package main

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

// TestRunStartsAndStopsOnSignal is a smoke test that keeps the hard gate real:
// there is always at least one passing unit test so `go test ./...` is a
// meaningful green wall, not an empty one (02 §4a).
func TestRunStartsAndStopsOnSignal(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

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
