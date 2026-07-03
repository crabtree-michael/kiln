package agent_test

// Shared unit-test fakes for the agent module (05 §10: "unit: the §5 machine
// + reconciler against the mock provider and a fake clock"). Every fake here
// is in-memory and offline; nothing in this file talks to a network or a
// real clock tick.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// errUnknownTurn marks an Update() call against a key fakeStore never Recorded.
var errUnknownTurn = errors.New("fakeStore: update of unknown turn")

// ---- fakeClock -------------------------------------------------------

// clockWaiter is one pending After() call: fired once the fake clock's Now()
// reaches or passes deadline.
type clockWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

// fakeClock is a manually-advanced agent.Clock. Tests step simulated time
// forward explicitly (via Advance/pump) so the real 2s poll / 60s reconcile
// cadence (turn.go PollInterval/ReconcileInterval) never costs real wall
// time — the whole point of the Clock port existing (05 §10).
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, clockWaiter{deadline: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward by d and fires every waiter whose deadline
// has elapsed.
func (c *fakeClock) Advance(d time.Duration) {
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

// pump advances the clock by step on a tight real-time heartbeat until stop
// is closed. This is the "without real sleeps" trick: the *simulated*
// interval a Run loop waits on (2s poll, 60s reconcile) is what advances —
// the real wall-clock cost is only the heartbeat tick (1ms), so a test
// crosses many simulated poll/reconcile cycles in well under a second of
// real time.
func (c *fakeClock) pump(stop <-chan struct{}, step time.Duration) {
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

// eventuallyTimeout bounds every eventually() wait below: real scheduling
// slack only, never the module's own PollInterval/ReconcileInterval (those
// are owned and sped up by fakeClock).
const eventuallyTimeout = 2 * time.Second

// eventually polls cond until it's true or eventuallyTimeout elapses.
func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventuallyTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", eventuallyTimeout)
	}
}

// runService starts svc.Run in the background against a fake clock that's
// continuously pumped, and returns a stop func that cancels the context and
// waits for Run to return (failing the test if it doesn't, promptly).
func runService(t *testing.T, svc *agent.Service, clock *fakeClock) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopPump := make(chan struct{})
	go clock.pump(stopPump, time.Second)

	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()

	return func() {
		close(stopPump)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Service.Run returned an error after context cancellation: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Service.Run did not return after context cancellation")
		}
	}
}

// ---- fakeStore --------------------------------------------------------

// fakeStore is an in-memory agent.Store keyed by idempotency_key, mirroring
// the real table's PK dedupe (05 §7) without touching Postgres.
type fakeStore struct {
	mu   sync.Mutex
	rows map[int64]agent.Turn
	// recordCalls counts every Record call per key, including ones that hit
	// an existing row — this is what proves a duplicate Send/Release never
	// creates a second turn (it proves Record was asked, not that it acted).
	recordCalls map[int64]int
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[int64]agent.Turn{}, recordCalls: map[int64]int{}}
}

func (s *fakeStore) Record(ctx context.Context, t agent.Turn) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordCalls[t.IdempotencyKey]++
	if _, ok := s.rows[t.IdempotencyKey]; ok {
		return false, nil
	}
	now := t.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	t.CreatedAt, t.UpdatedAt = now, now
	s.rows[t.IdempotencyKey] = t
	return true, nil
}

func (s *fakeStore) ListNonTerminal(ctx context.Context) ([]agent.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []agent.Turn
	for _, t := range s.rows {
		if !t.Phase.Terminal() {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *fakeStore) Update(ctx context.Context, t agent.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[t.IdempotencyKey]; !ok {
		return fmt.Errorf("%w: %d", errUnknownTurn, t.IdempotencyKey)
	}
	t.UpdatedAt = time.Now()
	s.rows[t.IdempotencyKey] = t
	return nil
}

func (s *fakeStore) LatestForWorker(ctx context.Context, workerID string) (agent.Turn, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest agent.Turn
	found := false
	for _, t := range s.rows {
		if t.WorkerID != workerID {
			continue
		}
		if !found || t.IdempotencyKey > latest.IdempotencyKey {
			latest = t
			found = true
		}
	}
	return latest, found, nil
}

// seed inserts a row directly, bypassing Record — used to simulate a
// pre-crash row already sitting in agent_turns so Run's recovery path (05
// §7: "on start, continue every non-terminal row") is exercised without ever
// calling Send.
func (s *fakeStore) seed(t agent.Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.UpdatedAt = t.CreatedAt
	s.rows[t.IdempotencyKey] = t
	s.recordCalls[t.IdempotencyKey]++
}

func (s *fakeStore) get(key int64) (agent.Turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.rows[key]
	return t, ok
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

func (s *fakeStore) recordCallCount(key int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordCalls[key]
}

// ---- fakeEvents ---------------------------------------------------------

type capturedEvent struct {
	eventType string
	payload   []byte
}

// fakeEvents is an in-memory agent.EventEnqueuer capturing every emission,
// so tests assert on the exact agent.turn_completed payload shape (05 §2.2)
// rather than a mocked side effect.
type fakeEvents struct {
	mu     sync.Mutex
	events []capturedEvent
	nextID int64
}

func (e *fakeEvents) EnqueueEvent(ctx context.Context, eventType string, payload []byte) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	e.events = append(e.events, capturedEvent{eventType: eventType, payload: append([]byte(nil), payload...)})
	return e.nextID, nil
}

func (e *fakeEvents) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.events)
}

func (e *fakeEvents) rawPayloads(eventType string) [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out [][]byte
	for _, ev := range e.events {
		if ev.eventType == eventType {
			out = append(out, ev.payload)
		}
	}
	return out
}

func (e *fakeEvents) turnCompletedEvents(t *testing.T) []agent.TurnCompleted {
	t.Helper()
	raws := e.rawPayloads(agent.EventTurnCompleted)
	out := make([]agent.TurnCompleted, 0, len(raws))
	for _, raw := range raws {
		var tc agent.TurnCompleted
		if err := json.Unmarshal(raw, &tc); err != nil {
			t.Fatalf("unmarshal agent.turn_completed payload %s: %v", raw, err)
		}
		out = append(out, tc)
	}
	return out
}

// ---- fakeSlots ----------------------------------------------------------

// fakeSlots is a read-only agent.Slots backed by a fixed id list — the
// board's capacity slots this module never counts, only matches (05 §3, §4).
type fakeSlots struct {
	mu  sync.Mutex
	ids []string
}

func (s *fakeSlots) WorkerIDs(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ids...), nil
}
