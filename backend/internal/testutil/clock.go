// Package testutil provides shared test helpers used across module test suites.
// The FakeClock here satisfies both agent.Clock and runtime.Clock (structural
// typing) so each module's tests share a single, well-tested implementation
// instead of maintaining independent copies.
package testutil

import (
	"sync"
	"time"
)

// ClockWaiter is one pending After() call: fired once the fake clock's Now()
// reaches or passes deadline.
type ClockWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

// FakeClock is a manually-advanced clock satisfying both agent.Clock and
// runtime.Clock. Tests step simulated time forward (via Advance/Pump) so
// poll/reconcile/backoff cadences never cost real wall time.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []ClockWaiter
}

// NewFakeClock returns a FakeClock seeded at a fixed deterministic instant.
func NewFakeClock() *FakeClock {
	return &FakeClock{now: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)}
}

// Now returns the current simulated time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After returns a channel that fires once the simulated time reaches now+d.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, ClockWaiter{deadline: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires every waiter whose deadline
// has elapsed.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.deadline.After(c.now) {
			w.ch <- c.now
		} else {
			remaining = append(remaining, w)
		}
	}
	c.waiters = remaining
}

// Pump advances the clock by step on a tight real-time heartbeat until stop
// is closed. The real wall-clock cost is only the heartbeat tick (1ms), so a
// test crosses many simulated poll/reconcile cycles in well under a second.
func (c *FakeClock) Pump(stop <-chan struct{}, step time.Duration) {
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			c.Advance(step)
		}
	}
}
