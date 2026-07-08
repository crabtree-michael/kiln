package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

// errHandlerPanic is the static sentinel a recovered handler panic is wrapped
// with, so the retry/dead-letter path carries a typed error rather than a bare
// dynamic one.
var errHandlerPanic = errors.New("runtime: panic in queue handler")

// Handler executes one claimed entry — a brain pass on the events queue, a
// per-topic executor on the outbox (04 §2). It runs outside any queue
// transaction and must be safe to repeat: delivery is at-least-once (04 §3).
type Handler func(ctx context.Context, e Entry) error

// DeadLetter runs the per-kind action after MaxAttempts failures (04 §3):
// exhausted agent.send → MarkBlocked on the ticket; notify.send → log and drop;
// pull.evaluate / board.updated → log, they self-heal; a dead event →
// notify the user.
type DeadLetter func(ctx context.Context, e Entry, lastErr error) error

// pollInterval is the fallback wakeup cadence (04 §5): the worker blocks on
// its nudge channel but also wakes at least this often, so a dropped nudge
// costs at most one interval and a restart still finds pending work.
const pollInterval = time.Second

// maxInFlightProjects bounds cross-project concurrency (11 §3): at most this
// many projects have an entry in flight on one queue at any moment. Within a
// project, entries stay strictly serial in id order — the single-writer-per-
// project constraint of 04 §4, now per tenant.
//
// This is a v1 SCALING CEILING (a global concurrency budget), NOT a tenant
// isolation boundary. Tenant isolation is the per-project serial claim above
// (ClaimNextDue skips busy projects), which holds at any value of this const.
// The ceiling is global and unweighted, so it offers no per-project fairness: a
// project with a continuous backlog can hold slots ahead of a quieter one
// (soft-starve it), since ClaimNextDue picks the globally-oldest non-busy entry.
// That is a throughput/fairness limit to revisit with real multi-tenant load
// (per-project fair queueing), not a data-isolation gap — no cross-tenant state
// is ever exposed by raising, lowering, or contending on this budget.
const maxInFlightProjects = 4

// Worker drains one queue with a per-project dispatcher (04 §3–§4, 11 §3):
// per project, entries run strictly serially in id order — the events worker
// *is* the single-writer-per-project constraint realized in-process (at most
// one brain pass per project at any moment) — while up to maxInFlightProjects
// different projects proceed concurrently. Serialization is realized by the
// claim itself: ClaimNextDue is handed the busy project set and never returns
// an entry belonging to a project already in flight. The outbox worker is
// independent; interleaving is safe by 03's locking and strict preconditions.
//
// Per entry: claim (attempts++) → execute outside any transaction → mark
// done, or mark retry with backoff, or mark dead + dead-letter. A crash
// between claim and mark leaves the entry pending; it re-runs after restart
// (04 §5).
type Worker struct {
	store      Store
	queue      QueueName
	handle     Handler
	deadLetter DeadLetter
	clock      Clock
	nudge      chan struct{}
}

// NewWorker assembles a worker; nothing runs until Run.
func NewWorker(store Store, queue QueueName, handle Handler, deadLetter DeadLetter, clock Clock) *Worker {
	return &Worker{
		store:      store,
		queue:      queue,
		handle:     handle,
		deadLetter: deadLetter,
		clock:      clock,
		nudge:      make(chan struct{}, 1),
	}
}

// Run is the per-project dispatcher loop (11 §3): fill up to
// maxInFlightProjects slots by claiming due entries whose projects are not
// already busy — each claimed entry runs process in its own goroutine — then
// block until a slot frees (done), a nudge arrives, or the poll fallback
// fires (04 §5). On cancellation it stops claiming, waits for every in-flight
// pass to finish marking its entry (execute-then-mark must not be torn), and
// returns nil — a clean shutdown, not an error.
func (w *Worker) Run(ctx context.Context) error {
	// busy is the in-flight project set: the serialization invariant is that a
	// project appears here iff exactly one of its entries is between claim and
	// mark. done carries the finished pass's project back to free the slot;
	// unbuffered, so a finishing goroutine parks until the loop takes it —
	// which the ctx.Done drain below relies on to count stragglers exactly.
	busy := make(map[string]struct{})
	done := make(chan string)
	for {
		w.fillSlots(ctx, busy, done)
		select {
		case pid := <-done:
			delete(busy, pid)
		case <-w.nudge:
		case <-w.clock.After(pollInterval):
		case <-ctx.Done():
			// Drain: let every in-flight pass finish executing and marking. A
			// pass that fails mid-mark because its ctx is canceled leaves its
			// row pending with the claim's pushed-out due time — exactly the
			// crash-between-claim-and-mark shape restart already heals (04 §5).
			for len(busy) > 0 {
				delete(busy, <-done)
			}
			return nil
		}
	}
}

// Nudge wakes the worker immediately (04 §5). Best-effort and non-blocking —
// a dropped nudge costs at most one poll interval.
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}

// fillSlots claims due entries for not-busy projects until the in-flight
// bound is hit or the queue reports nothing claimable, dispatching each
// claimed entry's process pass onto its own goroutine. The claim's busy
// exclusion is what keeps a project's entries strictly serial: a project in
// busy can never be claimed again until its pass sends on done.
func (w *Worker) fillSlots(ctx context.Context, busy map[string]struct{}, done chan<- string) {
	for len(busy) < maxInFlightProjects {
		e, ok, err := w.store.ClaimNextDue(ctx, w.queue, busyProjects(busy))
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("runtime: claim next due", "queue", w.queue, "err", err)
			}
			return
		}
		if !ok {
			return
		}
		busy[e.ProjectID] = struct{}{}
		go func(e Entry) {
			w.process(ctx, e)
			done <- e.ProjectID
		}(e)
	}
}

// busyProjects flattens the in-flight set for ClaimNextDue's busy argument.
func busyProjects(busy map[string]struct{}) []string {
	out := make([]string, 0, len(busy))
	for p := range busy {
		out = append(out, p)
	}
	return out
}

// process runs one entry's handler, then marks the outcome (04 §3 steps 2–3):
// success → done; failure with attempts left → retry with backoff; failure at
// MaxAttempts → dead + the per-kind dead-letter action.
func (w *Worker) process(ctx context.Context, e Entry) {
	handleErr := w.safeHandle(ctx, e)
	if handleErr == nil {
		if err := w.store.MarkDone(ctx, w.queue, e.ID); err != nil {
			slog.Error("runtime: mark done", "queue", w.queue, "id", e.ID, "err", err)
		}
		return
	}
	if e.Attempts >= MaxAttempts {
		w.retire(ctx, e, handleErr)
		return
	}
	next := w.clock.Now().Add(backoff(e.Attempts))
	if err := w.store.MarkRetry(ctx, w.queue, e.ID, handleErr.Error(), next); err != nil {
		slog.Error("runtime: mark retry", "queue", w.queue, "id", e.ID, "err", err)
	}
}

// safeHandle runs the entry's handler with a panic guard: a panic (e.g. a nil
// deref deep in a brain pass or outbox executor) is captured to Sentry, re-logged
// with a stack, and converted into a normal handler error so the entry retries
// or dead-letters through the usual path — the worker keeps draining instead of
// the panic unwinding the goroutine and taking the process down.
func (w *Worker) safeHandle(ctx context.Context, e Entry) (err error) {
	defer func() {
		if r := recover(); r != nil {
			obs.Capture(ctx, r)
			err = fmt.Errorf("%w (%s entry %d): %v", errHandlerPanic, w.queue, e.ID, r)
		}
	}()
	return w.handle(ctx, e)
}

// retire dead-letters an exhausted entry (04 §3): mark it dead, then run the
// per-kind dead-letter action. Both failures are logged, never fatal — the
// worker keeps draining.
func (w *Worker) retire(ctx context.Context, e Entry, cause error) {
	if err := w.store.MarkDead(ctx, w.queue, e.ID, cause.Error()); err != nil {
		slog.Error("runtime: mark dead", "queue", w.queue, "id", e.ID, "err", err)
	}
	if err := w.deadLetter(ctx, e, cause); err != nil {
		slog.Error("runtime: dead-letter action", "queue", w.queue, "id", e.ID, "err", err)
	}
}

// backoff is the retry delay after a failed attempt (04 §3, D8):
// min(1s × 2^(attempts−1), 60s). attempts is the just-incremented claim count,
// so the first failure (attempts=1) waits 1s and the seventh (attempts=7)
// waits 60s; the eighth exhausts and never reaches here.
func backoff(attempts int) time.Duration {
	shift := max(attempts-1, 0)
	// Guard against shifting past the width of time.Duration on absurd inputs.
	const maxShift = 62
	if shift > maxShift {
		return BackoffCap
	}
	d := BackoffBase << shift
	if d <= 0 || d > BackoffCap {
		return BackoffCap
	}
	return d
}
