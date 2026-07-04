package runtime_test

// Worker unit tests (04 §3-§5, §9): the serial drain loop, execute-then-mark,
// the exact backoff schedule and dead-letter boundary, wakeup, and crash
// replay — all against fakeStore/fakeClock so nothing here needs a real
// Postgres or a real sleep.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

var errHandlerFailed = errors.New("handler: synthetic failure")

// noopDeadLetter is used by tests that don't care about the dead-letter
// action itself, only that the worker keeps draining.
func noopDeadLetter(_ context.Context, _ runtime.Entry, _ error) error { return nil }

// ---- serial, id-ordered drain (04 §4) -------------------------------------

func TestWorker_ProcessesEntriesSeriallyInIDOrder(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)

	const n = 5
	ids := make([]int64, n)
	for i := range n {
		ids[i] = store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{}`), 0)
	}

	gate := make(chan struct{}) // closed once the test wants entry 1 released
	var orderMu sync.Mutex
	var order []int64

	handle := func(_ context.Context, e runtime.Entry) error {
		if e.ID == ids[0] {
			<-gate // block the first entry until the test says go
		}
		orderMu.Lock()
		order = append(order, e.ID)
		orderMu.Unlock()
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueEvents, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	// While entry 1 is gated in the handler, entry 2 must not have been
	// claimed yet (attempts still 0) — proves single-writer, not fan-out.
	time.Sleep(50 * time.Millisecond)
	if got := store.attempts(runtime.QueueEvents, ids[1]); got != 0 {
		t.Fatalf("entry 2 was claimed (attempts=%d) while entry 1's handler was still in flight; "+
			"the events worker must process one entry at a time (04 §4)", got)
	}

	close(gate)
	testutil.Eventually(t, func() bool {
		orderMu.Lock()
		defer orderMu.Unlock()
		return len(order) == n
	})

	orderMu.Lock()
	got := append([]int64(nil), order...)
	orderMu.Unlock()
	for i, id := range ids {
		if got[i] != id {
			t.Fatalf("processing order = %v, want strict id order %v (04 §4: \"strictly serially, in id order\")", got, ids)
		}
	}
}

// ---- execute-then-mark, at-least-once (04 §3 step 2-3) --------------------

func TestWorker_ExecuteThenMark_SuccessMarksDone(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	id := store.seed(runtime.QueueOutbox, "pull.evaluate", []byte(`{}`), 0)

	handled := make(chan runtime.Entry, 1)
	handle := func(_ context.Context, e runtime.Entry) error {
		handled <- e
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueOutbox, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	select {
	case e := <-handled:
		if e.ID != id {
			t.Fatalf("handler got entry %d, want %d", e.ID, id)
		}
		if e.Attempts != 1 {
			t.Errorf("first claim's Attempts = %d, want 1 (claim increments attempts, 04 §3 step 1)", e.Attempts)
		}
	case <-time.After(testutil.EventuallyTimeout):
		t.Fatal("handler was never invoked")
	}

	testutil.Eventually(t, func() bool { return store.status(runtime.QueueOutbox, id) == statusDone })
	if got := store.doneCallCount(); got != 1 {
		t.Errorf("MarkDone called %d times, want exactly 1", got)
	}
}

// ---- the exact backoff schedule (04 §3, D8) -------------------------------

// TestWorker_RetryBackoffSchedule_ExactSequence drives a single always-
// failing entry through all 8 attempts and asserts the precise retry delay
// after each of the first 7, then the dead-letter action on the 8th — the
// schedule the spec pins verbatim: min(1s*2^(attempts-1), 60s).
func TestWorker_RetryBackoffSchedule_ExactSequence(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	id := store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{}`), 0)

	handle := func(_ context.Context, _ runtime.Entry) error { return errHandlerFailed }

	var deadLetterCalls []runtime.Entry
	deadLetter := func(_ context.Context, e runtime.Entry, err error) error {
		deadLetterCalls = append(deadLetterCalls, e)
		if !errors.Is(err, errHandlerFailed) {
			t.Errorf("dead-letter received err = %v, want errHandlerFailed", err)
		}
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueEvents, handle, deadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)

	testutil.Eventually(t, func() bool { return store.retryCallCount() >= 7 })
	testutil.Eventually(t, func() bool { return store.deadCallCount() >= 1 })

	wantDelays := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second,
	}
	calls := store.retryCallsFor(id)
	if len(calls) != len(wantDelays) {
		t.Fatalf("got %d MarkRetry calls, want exactly %d (MaxAttempts=8: 7 retries then dead-letter)",
			len(calls), len(wantDelays))
	}
	for i, c := range calls {
		gotDelay := c.nextAttemptAt.Sub(c.calledAt)
		if gotDelay != wantDelays[i] {
			t.Errorf("retry %d (attempt %d): backoff delay = %s, want %s (min(1s*2^(attempts-1),60s), 04 D8)",
				i+1, i+1, gotDelay, wantDelays[i])
		}
		if c.lastError == "" {
			t.Errorf("retry %d: last_error was not recorded", i+1)
		}
	}

	deadCalls := store.deadCallsFor(id)
	if len(deadCalls) != 1 {
		t.Fatalf("got %d MarkDead calls, want exactly 1", len(deadCalls))
	}
	if got := store.attempts(runtime.QueueEvents, id); got != 8 {
		t.Errorf("final attempts = %d, want 8 (MaxAttempts, 04 §3)", got)
	}
	if len(deadLetterCalls) != 1 {
		t.Fatalf("dead-letter callback invoked %d times, want exactly 1", len(deadLetterCalls))
	}
	if deadLetterCalls[0].ID != id || deadLetterCalls[0].Attempts != 8 {
		t.Errorf("dead-letter callback got entry %+v, want ID=%d Attempts=8", deadLetterCalls[0], id)
	}
}

// TestWorker_DoesNotClaimBeforeDue proves ClaimNextDue's "due" gate matters
// to the worker's loop: an entry whose next_attempt_at is in the future must
// not be handled until the clock reaches it.
func TestWorker_DoesNotClaimBeforeDue(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	id := store.seed(runtime.QueueOutbox, "notify.send", []byte(`{}`), 0)
	// Push the row's due time 10s into the future, simulating a row already
	// mid-backoff when the worker starts.
	store.mu.Lock()
	store.rows[runtime.QueueOutbox][id].nextAttemptAt = clock.Now().Add(10 * time.Second)
	store.mu.Unlock()

	var calls int
	handle := func(_ context.Context, _ runtime.Entry) error {
		calls++
		return nil
	}
	w := runtime.NewWorker(store, runtime.QueueOutbox, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	time.Sleep(50 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("handler called %d times before the entry was due", calls)
	}

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)
	testutil.Eventually(t, func() bool { return calls == 1 })
}

// ---- wakeup: Nudge beats the poll fallback (04 §5) ------------------------

func TestWorker_Nudge_WakesFasterThanPollFallback(t *testing.T) {
	clock := realTestClock{}
	store := newFakeStore(clock)

	handled := make(chan runtime.Entry, 1)
	handle := func(_ context.Context, e runtime.Entry) error {
		handled <- e
		return nil
	}
	w := runtime.NewWorker(store, runtime.QueueEvents, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	// Let the worker settle into its idle wait with nothing to do.
	time.Sleep(50 * time.Millisecond)

	id := store.seed(runtime.QueueEvents, string(runtime.EventHumanMessage), []byte(`{}`), 0)
	start := time.Now()
	w.Nudge()

	select {
	case e := <-handled:
		if e.ID != id {
			t.Fatalf("handler got entry %d, want %d", e.ID, id)
		}
		if elapsed := time.Since(start); elapsed >= 900*time.Millisecond {
			t.Errorf("Nudge took %s to wake the worker; want well under the 1s poll fallback (04 §5)", elapsed)
		}
	case <-time.After(900 * time.Millisecond):
		t.Fatal("Nudge did not wake the worker within 900ms (poll fallback is 1s, 04 §5) — " +
			"either Nudge is not wired to the drain loop, or it fell back to the poll")
	}
}

// ---- deploy-safe recovery: crash-replay needs no special code path (04 §5) -

func TestWorker_CrashReplay_ReRunsAPendingEntryWithPriorAttempts(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	// Simulate a crash between claim and mark: the row is still "pending"
	// (04 D4 — no in-flight status) but already carries one attempt.
	id := store.seed(runtime.QueueOutbox, "board.updated", []byte(`{}`), 1)

	handled := make(chan runtime.Entry, 1)
	handle := func(_ context.Context, e runtime.Entry) error {
		handled <- e
		return nil
	}
	w := runtime.NewWorker(store, runtime.QueueOutbox, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	select {
	case e := <-handled:
		if e.ID != id {
			t.Fatalf("handler got entry %d, want %d", e.ID, id)
		}
		if e.Attempts != 2 {
			t.Errorf("Attempts = %d, want 2 (1 pre-crash + 1 re-claim) — recovery is just re-finding "+
				"pending rows, no special code path (04 §5)", e.Attempts)
		}
	case <-time.After(testutil.EventuallyTimeout):
		t.Fatal("worker never re-ran the crashed, still-pending entry")
	}
	testutil.Eventually(t, func() bool { return store.status(runtime.QueueOutbox, id) == statusDone })
}
