package testutil

import (
	"testing"
	"time"
)

// EventuallyTimeout bounds every Eventually() wait: real scheduling slack
// only, never a module's own poll/backoff intervals (those are owned and
// sped up by FakeClock).
const EventuallyTimeout = 2 * time.Second

// pollSleep is the tick between condition checks inside Eventually.
const pollSleep = 2 * time.Millisecond

// Eventually polls cond until it returns true or EventuallyTimeout elapses.
func Eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(EventuallyTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(pollSleep)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", EventuallyTimeout)
	}
}
