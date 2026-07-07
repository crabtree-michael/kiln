package runtime_test

// Worker unit tests (04 §3-§5, §9; 11 §3): the per-project dispatcher —
// per-project serialization, cross-project concurrency, the in-flight bound —
// plus execute-then-mark, the exact backoff schedule and dead-letter boundary,
// wakeup, and crash replay — all against fakeStore/fakeClock so nothing here
// needs a real Postgres or a real sleep.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
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
	if got := store.attempts(ids[1]); got != 0 {
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
	if got := store.attempts(id); got != 8 {
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

	var calls atomic.Int64
	handle := func(_ context.Context, _ runtime.Entry) error {
		calls.Add(1)
		return nil
	}
	w := runtime.NewWorker(store, runtime.QueueOutbox, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler called %d times before the entry was due", got)
	}

	stopPump := make(chan struct{})
	go clock.Pump(stopPump, pumpStep)
	defer close(stopPump)
	testutil.Eventually(t, func() bool { return calls.Load() == 1 })
}

// ---- the per-project dispatcher (11 §3): per-project serialization with
// cross-project concurrency ---------------------------------------------------

// TestWorker_Dispatch_SerializesPerProjectAndOverlapsAcrossProjects is the
// tenancy flip's core concurrency contract: with events A1, A2 (project A) and
// B1 (project B) all due, B1 overlaps a still-running A1, while A2 never
// starts before A1 has finished — and the busy set handed to ClaimNextDue
// names the in-flight project while A1 runs.
func TestWorker_Dispatch_SerializesPerProjectAndOverlapsAcrossProjects(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)

	a1 := store.seedProject(runtime.QueueEvents, "proj-A", string(runtime.EventHumanMessage), []byte(`{}`), 0)
	a2 := store.seedProject(runtime.QueueEvents, "proj-A", string(runtime.EventHumanMessage), []byte(`{}`), 0)
	b1 := store.seedProject(runtime.QueueEvents, "proj-B", string(runtime.EventHumanMessage), []byte(`{}`), 0)

	gateA1 := make(chan struct{}) // holds A1's handler open until the test releases it
	var mu sync.Mutex
	var timeline []string // "start:<id>" / "end:<id>" in observed order

	mark := func(kind string, id int64) {
		mu.Lock()
		defer mu.Unlock()
		timeline = append(timeline, fmt.Sprintf("%s:%d", kind, id))
	}
	started := func(id int64) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.Contains(timeline, fmt.Sprintf("start:%d", id))
	}

	handle := func(_ context.Context, e runtime.Entry) error {
		mark("start", e.ID)
		if e.ID == a1 {
			<-gateA1
		}
		mark("end", e.ID)
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueEvents, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	// B1 must overlap A1: it starts while A1's handler is still blocked.
	testutil.Eventually(t, func() bool { return started(a1) && started(b1) })

	// A2 must NOT have started (nor even been claimed) while A1 is in flight —
	// per-project serialization, realized by the busy exclusion.
	time.Sleep(50 * time.Millisecond)
	if started(a2) {
		t.Fatal("A2 started while A1's handler was still in flight; project A must serialize (11 §3)")
	}
	if got := store.attempts(a2); got != 0 {
		t.Fatalf("A2 was claimed (attempts=%d) while A1 was in flight; ClaimNextDue must exclude busy projects", got)
	}

	// While A1 is in flight, the dispatcher's claims must name project A busy.
	sawABusy := false
	for _, busy := range store.claimBusyLists(runtime.QueueEvents) {
		if slices.Contains(busy, "proj-A") {
			sawABusy = true
			break
		}
	}
	if !sawABusy {
		t.Error("no ClaimNextDue call carried proj-A in its busy list while A1 was in flight")
	}

	close(gateA1)
	testutil.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(timeline) == 6 // start+end for each of the three entries
	})

	mu.Lock()
	tl := append([]string(nil), timeline...)
	mu.Unlock()
	endA1 := slices.Index(tl, fmt.Sprintf("end:%d", a1))
	startA2 := slices.Index(tl, fmt.Sprintf("start:%d", a2))
	if endA1 == -1 || startA2 == -1 || startA2 < endA1 {
		t.Fatalf("timeline = %v: A2 must start only after A1 finished (per-project id order, 04 §4 + 11 §3)", tl)
	}

	for _, id := range []int64{a1, a2, b1} {
		testutil.Eventually(t, func() bool { return store.status(runtime.QueueEvents, id) == statusDone })
	}
}

// TestWorker_Dispatch_BoundsInFlightProjects pins maxInFlightProjects = 4:
// with five distinct projects due, at most four handlers run concurrently;
// the fifth starts only after a slot frees.
func TestWorker_Dispatch_BoundsInFlightProjects(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)

	const projects = 5
	for i := range projects {
		store.seedProject(runtime.QueueEvents, fmt.Sprintf("proj-%d", i),
			string(runtime.EventHumanMessage), []byte(`{}`), 0)
	}

	gate := make(chan struct{})
	var mu sync.Mutex
	inFlight, maxInFlight, startedCount := 0, 0, 0

	handle := func(_ context.Context, _ runtime.Entry) error {
		mu.Lock()
		inFlight++
		startedCount++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		<-gate
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueEvents, handle, noopDeadLetter, clock)
	stop := runWorker(t, w)
	defer stop()

	testutil.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return startedCount == 4
	})
	time.Sleep(50 * time.Millisecond) // give a fifth (over-bound) dispatch a chance to appear
	mu.Lock()
	if startedCount != 4 || maxInFlight != 4 {
		mu.Unlock()
		t.Fatalf("started=%d maxInFlight=%d with all handlers blocked, want exactly 4 (maxInFlightProjects)",
			startedCount, maxInFlight)
	}
	mu.Unlock()

	close(gate)
	testutil.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return startedCount == projects && inFlight == 0
	})
	mu.Lock()
	if maxInFlight > 4 {
		t.Errorf("maxInFlight = %d, want <= 4 at every instant", maxInFlight)
	}
	mu.Unlock()
}

// TestWorker_Dispatch_CtxCancelDrainsInFlight pins the shutdown contract: Run
// returns only after every in-flight handler has finished and marked its
// entry — cancellation must not abandon a claimed entry mid-pass.
func TestWorker_Dispatch_CtxCancelDrainsInFlight(t *testing.T) {
	clock := testutil.NewFakeClock()
	store := newFakeStore(clock)
	id := store.seedProject(runtime.QueueEvents, "proj-A", string(runtime.EventHumanMessage), []byte(`{}`), 0)

	gate := make(chan struct{})
	entered := make(chan struct{})
	var enterOnce sync.Once
	handle := func(_ context.Context, _ runtime.Entry) error {
		enterOnce.Do(func() { close(entered) })
		<-gate
		return nil
	}

	w := runtime.NewWorker(store, runtime.QueueEvents, handle, noopDeadLetter, clock)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	<-entered // the handler is in flight
	cancel()

	select {
	case <-runDone:
		t.Fatal("Run returned while an in-flight handler was still running; ctx.Done must drain in-flight passes")
	case <-time.After(100 * time.Millisecond):
	}

	close(gate)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned %v after drain, want nil (clean shutdown)", err)
		}
	case <-time.After(testutil.EventuallyTimeout):
		t.Fatal("Run did not return after the in-flight handler finished")
	}
	if got := store.status(runtime.QueueEvents, id); got != statusDone {
		t.Errorf("entry status after drained shutdown = %q, want done (the in-flight pass finished marking)", got)
	}
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
