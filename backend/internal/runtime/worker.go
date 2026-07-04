package runtime

import (
	"context"
	"log/slog"
	"time"
)

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

// Worker drains one queue serially, in id order (04 §3–§4). The events
// worker *is* the single-writer-per-project constraint realized in-process:
// at most one brain pass exists at any moment. The outbox worker is
// independent; interleaving is safe by 03's locking and strict preconditions.
//
// Loop: claim (attempts++) → execute outside any transaction → mark done, or
// mark retry with backoff, or mark dead + dead-letter. A crash between claim
// and mark leaves the entry pending; it re-runs after restart (04 §5).
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

// Run drains until ctx is done, blocking on the nudge channel with a
// 1-second poll fallback (04 §5). It returns nil on cancellation — a clean
// shutdown, not an error.
func (w *Worker) Run(ctx context.Context) error {
	for {
		w.drainDue(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-w.nudge:
		case <-w.clock.After(pollInterval):
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

// drainDue processes every currently-due entry, in id order, one at a time,
// until the queue reports nothing due (04 §4). Serial by construction: the
// next claim only happens after the current entry has been marked.
func (w *Worker) drainDue(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		entry, ok, err := w.store.ClaimNextDue(ctx, w.queue)
		if err != nil {
			slog.Error("runtime: claim next due", "queue", w.queue, "err", err)
			return
		}
		if !ok {
			return
		}
		w.process(ctx, entry)
	}
}

// process runs one entry's handler, then marks the outcome (04 §3 steps 2–3):
// success → done; failure with attempts left → retry with backoff; failure at
// MaxAttempts → dead + the per-kind dead-letter action.
func (w *Worker) process(ctx context.Context, e Entry) {
	handleErr := w.handle(ctx, e)
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
