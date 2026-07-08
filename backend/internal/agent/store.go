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
	// become known, attempts, last_error (05 §5, §7).
	//
	// Moving a machine to done and enqueueing its agent.turn_completed event
	// must be all-or-nothing (05 §5). Rather than span a single transaction
	// across this store and the runtime-owned events table (the agent does not
	// own events, so it cannot mirror the board→outbox shared-table pattern),
	// the completion is made exactly-once at the event seam: the emit carries
	// the turn's outbox id as an idempotency key and the poller only advances
	// to done once it commits, so a crash between emit and phase→done re-emits
	// idempotently rather than duplicating the brain pass (architecture audit
	// 3.1). This Update therefore stays a plain single-row write.
	Update(ctx context.Context, t Turn) error

	// LatestForWorker returns workerID's newest operation row; it decides
	// first-message-vs-continuation (05 §2.1, §3): no row, or a release row,
	// means the next send starts a fresh conversation.
	LatestForWorker(ctx context.Context, workerID string) (Turn, bool, error)
}
