package agent

import "context"

// Store is the module's persistence port over its one table, agent_turns
// (05 §7). This is adapter-layer state, not board state — which provider
// conversation serves a ticket is invisible to board invariants (03 I8).
// Implemented by ./postgres, injected at the composition root.
type Store interface {
	// Record inserts the machine's initial row (phase recorded) keyed by the
	// outbox id. created=false means the key was already seen — the caller
	// returns silent success without side effects (05 §2.1, §7).
	Record(ctx context.Context, t Turn) (created bool, err error)

	// ListNonTerminal returns every machine the poller must advance. This is
	// also the whole recovery story: on start, continue these — re-check
	// readiness, re-poll the turn, start it if never started (05 §5, §7).
	ListNonTerminal(ctx context.Context) ([]Turn, error)

	// Update persists one machine step: phase, provider handles as they
	// become known, attempts, last_error (05 §5, §7). Note: moving a machine
	// to done must commit in the same transaction as enqueueing its
	// agent.turn_completed event (05 §5) — how that transaction spans this
	// store and the EventEnqueuer seam is settled at implementation (the
	// board's shared-table outbox pattern, 03 §2.4, is the precedent).
	Update(ctx context.Context, t Turn) error

	// LatestForWorker returns workerID's newest operation row; it decides
	// first-message-vs-continuation (05 §2.1, §3): no row, or a release row,
	// means the next send starts a fresh conversation.
	LatestForWorker(ctx context.Context, workerID string) (Turn, bool, error)
}
