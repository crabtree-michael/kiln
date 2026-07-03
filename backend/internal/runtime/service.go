package runtime

import "context"

// Brain is the runtime's port onto the decision step (02 §6): one call per
// event, invoked serially by the events worker (04 §4). A replayed pass
// re-reads fresh board state; the Board API's strict preconditions (03 D8)
// stop a half-applied first run from double-applying.
type Brain interface {
	HandleEvent(ctx context.Context, ev Event) error
}

// Puller is the runtime's port onto the board's deterministic pull, the
// pull.evaluate executor (03 §5, 04 §2). Idempotent by construction.
type Puller interface {
	RunPull(ctx context.Context) error
}

// Blocker is the runtime's port onto the board's mechanical failure path
// (03 §7.3): dead-lettered agent.send entries surface on the ticket as
// Blocked with the delivery failure as reason.
type Blocker interface {
	MarkBlocked(ctx context.Context, ticketID string, reason string) error
}

// AgentRuntime executes agent.* outbox entries (05 §2.1) — the
// provider-neutral contract onto agent platforms. The outbox id travels as
// the idempotency key; the module (and its mock provider) must deduplicate
// on it (04 §3, 05 §7). Calls record-and-return; they never block on
// provisioning or a turn (05 D2).
type AgentRuntime interface {
	Send(ctx context.Context, idempotencyKey int64, payload []byte) error
	Release(ctx context.Context, idempotencyKey int64, payload []byte) error
}

// Notifier executes notify.send entries (02 §10). A rare duplicate
// notification is accepted as benign (04 §3).
type Notifier interface {
	Send(ctx context.Context, payload []byte) error
}

// SnapshotPusher executes board.updated entries: fan out a fresh full board
// snapshot to every connected client (04 §7; implemented by the api SSE hub).
// Snapshots are absolute, so duplicates are harmless (04 D7).
type SnapshotPusher interface {
	PushBoard(ctx context.Context) error
}

// Service is the runtime's core: EnqueueEvent for the two ingestion callers
// (04 §6) and the wiring that routes claimed entries to the ports above.
// Constructed at the composition root (04 §8).
type Service struct {
	store    Store
	brain    Brain
	puller   Puller
	blocker  Blocker
	agents   AgentRuntime
	notifier Notifier
	pusher   SnapshotPusher
}

// NewService assembles the runtime over its ports.
func NewService(store Store, brain Brain, puller Puller, blocker Blocker, agents AgentRuntime, notifier Notifier, pusher SnapshotPusher) *Service {
	return &Service{
		store:    store,
		brain:    brain,
		puller:   puller,
		blocker:  blocker,
		agents:   agents,
		notifier: notifier,
		pusher:   pusher,
	}
}

// EnqueueEvent ingests one of the two 01 event types (04 §6): INSERT into
// events + nudge the events worker. Callers: the Amika inbound handler
// (agent.turn_completed) and the voice route (human.voice_input). Payloads
// are opaque snapshots; shape contracts are the emitting surface's spec.
func (s *Service) EnqueueEvent(ctx context.Context, t EventType, payload []byte) (int64, error) {
	return 0, errNotImplemented
}

// Workers builds the two serial workers (04 §3–§4): the events worker over
// the Brain port, and the outbox worker routing per-topic to the executor
// ports, each with its dead-letter action.
func (s *Service) Workers(clock Clock) (events *Worker, outbox *Worker) {
	// Wiring is implementation; see 04 §2 (executors) and §3 (dead-letter table).
	return nil, nil
}
