package runtime

import (
	"context"
	"time"
)

// Store is the runtime's persistence port over the two queue tables (04 §2).
// Implemented by ./postgres, injected at the composition root. One generic
// seam for both queues keeps the drain machinery single-sourced (04 D1).
type Store interface {
	// InsertEvent appends to the events queue (04 §6 EnqueueEvent), stamped
	// with the tenant project (11 §3). The outbox is never written here — the
	// board appends it transactionally (03 §7).
	//
	// idempotencyKey dedupes at-least-once emitters against a partial unique
	// index (architecture audit 3.1): a non-zero key inserts ON CONFLICT DO
	// NOTHING, so a redelivered event (e.g. an agent completion re-emitted after
	// a crash between its emit and its phase→done write) is a silent no-op
	// reported as id 0. A zero key carries no dedup — the runtime's own
	// human.message emits, which are already exactly-once at their source.
	InsertEvent(ctx context.Context, projectID string, t EventType, idempotencyKey int64, payload []byte) (int64, error)

	// ClaimNextDue picks the next entry — status pending, next_attempt_at
	// due, id order — with FOR UPDATE SKIP LOCKED in a short claim
	// transaction that only increments attempts (04 §3 step 1). busy is the
	// dispatcher's in-flight project set (11 §3): entries belonging to a busy
	// project are skipped, which is what realizes per-project serialization —
	// an empty busy list claims anything due. ok is false when nothing is due.
	ClaimNextDue(ctx context.Context, q QueueName, busy []string) (e Entry, ok bool, err error)

	// MarkDone records success: status done, processed_at now (04 §3).
	MarkDone(ctx context.Context, q QueueName, id int64) error

	// MarkRetry records a failed attempt: last_error and the backoff'd
	// next_attempt_at (04 §3).
	MarkRetry(ctx context.Context, q QueueName, id int64, lastError string, nextAttemptAt time.Time) error

	// MarkDead retires the entry after MaxAttempts (04 §3); the caller runs
	// the per-topic dead-letter action.
	MarkDead(ctx context.Context, q QueueName, id int64, lastError string) error
}

// Clock abstracts time for the workers so backoff is testable without
// sleeping (04 §9).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}
