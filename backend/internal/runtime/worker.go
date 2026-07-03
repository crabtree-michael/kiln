package runtime

import (
	"context"
	"errors"
)

// errNotImplemented marks scaffold stubs. Implementations follow
// docs/specs/04-runtime-and-api.md; remove this once the last stub is gone.
var errNotImplemented = errors.New("runtime: not implemented (scaffold)")

// Handler executes one claimed entry — a brain pass on the events queue, a
// per-topic executor on the outbox (04 §2). It runs outside any queue
// transaction and must be safe to repeat: delivery is at-least-once (04 §3).
type Handler func(ctx context.Context, e Entry) error

// DeadLetter runs the per-kind action after MaxAttempts failures (04 §3):
// exhausted amika.* → MarkBlocked on the ticket; notify.send → log and drop;
// pull.evaluate / board.updated → log, they self-heal; a dead event →
// notify the user.
type DeadLetter func(ctx context.Context, e Entry, lastErr error) error

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
// 1-second poll fallback (04 §5).
func (w *Worker) Run(ctx context.Context) error {
	return errNotImplemented
}

// Nudge wakes the worker immediately (04 §5). Best-effort and non-blocking —
// a dropped nudge costs at most one poll interval.
func (w *Worker) Nudge() {
	select {
	case w.nudge <- struct{}{}:
	default:
	}
}
