package agent

import (
	"context"
	"errors"
	"time"
)

// errNotImplemented marks scaffold stubs. Implementations follow
// docs/specs/05-agent-runtime.md; remove this once the last stub is gone.
var errNotImplemented = errors.New("agent: not implemented (scaffold)")

// EventEnqueuer is this module's port onto the runtime's event queue
// (04 §6): every terminal turn outcome becomes exactly one
// agent.turn_completed event — the single inbound seam; this module never
// mutates board state (05 §2.2, D3). Satisfied at the composition root by a
// thin adapter over the runtime's EnqueueEvent.
type EventEnqueuer interface {
	EnqueueEvent(ctx context.Context, eventType string, payload []byte) (int64, error)
}

// Slots is this module's read-only port onto the board's capacity slots
// (03 §2.3): the reconciler matches provider workers against these ids
// (05 §4). Capacity questions stay the board's alone — this module never
// counts (05 §3).
type Slots interface {
	WorkerIDs(ctx context.Context) ([]string, error)
}

// Clock abstracts time for the reconciler/poller so unit tests drive the
// machine with a fake clock (05 §10). Mirrors the runtime's Clock (04 §9);
// module-local to keep the boundary clean.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Service is the provider-agnostic core (05 §9): it implements the
// AgentRuntime consumer contract the runtime's outbox worker calls (05 §2.1;
// the port shape is runtime.AgentRuntime, matched structurally — this module
// never imports the runtime), and owns the §5 turn state machine, the §4
// pool reconciler, and the §5 poller, all written once against the Provider
// port. Constructed at the composition root (05 §9); AGENT_MODE selects the
// real or mock Provider there.
type Service struct {
	store    Store
	provider Provider
	events   EventEnqueuer
	slots    Slots
	clock    Clock
}

// NewService assembles the agent runtime over its ports.
func NewService(store Store, provider Provider, events EventEnqueuer, slots Slots, clock Clock) *Service {
	return &Service{
		store:    store,
		provider: provider,
		events:   events,
		slots:    slots,
		clock:    clock,
	}
}

// Send delivers one message to a worker (05 §2.1): decode the agent.send
// payload (SendPayload — 03 §7.1), record the operation in agent_turns keyed
// by the outbox id, and return. Record-and-return — never blocks on
// provisioning or the turn; the machine owns progression (05 D2). A repeated
// key is a silent success (04 §3). The first Send after a worker is
// (re)created starts a fresh conversation; later Sends continue it — derived
// from this module's own state (05 §2.1, §3).
func (s *Service) Send(ctx context.Context, idempotencyKey int64, payload []byte) error {
	return errNotImplemented
}

// Release recycles a worker after AcceptToDone (05 §2.1, §4): decode the
// agent.release payload (ReleasePayload), record, return. The machine
// destroys and recreates the slot's provider worker so the next conversation
// starts from a fresh workspace; a dead-lettered recreate is healed by the
// reconciler sweep — the cost is latency on that slot's next Send, never a
// stuck ticket (05 §4).
func (s *Service) Release(ctx context.Context, idempotencyKey int64, payload []byte) error {
	return errNotImplemented
}

// Run drives the module's two loops until ctx ends (05 §4–§5):
//
//   - The reconciler, on start and every ReconcileInterval: ListWorkers,
//     adopt every name matching a board slot, create only for slots with no
//     live worker, destroy orphaned kiln-worker-* entries (05 §4).
//   - The poller, every PollInterval: advance every non-terminal machine —
//     ensure the worker is ready, start the turn (fresh ⇔ first send of a
//     conversation), poll CheckTurn, and on a terminal outcome enqueue the
//     agent.turn_completed event and mark the machine done in one
//     transaction (05 §5). The crash window between poll and commit
//     re-enqueues a duplicate; the brain absorbs it (04 §3, 03 D8).
//
// Recovery is the same loop (05 §7): on start, the non-terminal rows of
// agent_turns simply continue. If a provider lost a conversation between
// turns, fall back to a fresh conversation with the same message — context
// lost, workspace kept, logged; never fail the ticket for it (05 §3).
func (s *Service) Run(ctx context.Context) error {
	return errNotImplemented
}
