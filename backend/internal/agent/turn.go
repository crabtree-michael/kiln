package agent

import "time"

// Kind separates the two operations the machine runs (05 §5): a send walks
// the full machine; a release uses only recorded → done/failed — destroy,
// then recreate-to-ready, no turn.
type Kind string

const (
	KindSend    Kind = "send"
	KindRelease Kind = "release"
)

// Phase is the per-operation machine state persisted in agent_turns (05 §5,
// §7), advanced by the reconciler/poller loop — never inside a port call
// (05 D2).
type Phase string

const (
	PhaseRecorded    Phase = "recorded"     // intent recorded; worker not yet confirmed ready
	PhaseWorkerReady Phase = "worker_ready" // worker exists and is ready; turn not started
	PhaseTurnStarted Phase = "turn_started" // provider turn in flight; poll CheckTurn
	PhaseDone        Phase = "done"         // the only resting state; turn_completed enqueued
	PhaseFailed      Phase = "failed"       // terminal failure — still owes the error event
)

// Terminal reports whether the machine is at rest. failed is NOT terminal:
// the poller still owes the error-shaped agent.turn_completed event, after
// which the machine moves failed → done (05 §5).
func (p Phase) Terminal() bool { return p == PhaseDone }

// Turn is one row of agent_turns (05 §7) — one in-flight Send or Release
// keyed by its outbox id, the idempotency dedupe the provider doesn't give
// us. Recovery is reading these back: on start, continue every non-terminal
// row (05 §7).
type Turn struct {
	IdempotencyKey int64 // the outbox id (04 §3); primary key and the dedupe
	Kind           Kind
	TicketID       string // empty for release operations
	WorkerID       string // the board worker-slot uuid (03 §2.3)
	Message        string // what StartTurn sends; persisted so recovery can start a never-started turn (05 §7)
	Phase          Phase

	// Opaque provider handles as they become known (05 §6–§7).
	ProviderWorker string
	ProviderTurn   *TurnRef

	// The machine's own retry bookkeeping (05 §5): transient provider errors
	// retry with the runtime's backoff policy (04 §3); terminal exhaustion
	// moves the machine to failed.
	Attempts  int
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SendPayload is the agent.send emission (03 §7.1) as this module decodes it
// (05 §2.1). Message is RunPull's work order or SendToAgent's instruction —
// the module doesn't distinguish them; first-message-vs-continuation is
// derived from its own state, never from the caller.
type SendPayload struct {
	TicketID string `json:"ticket_id"`
	WorkerID string `json:"worker_id"`
	Message  string `json:"message"`
}

// ReleasePayload is the agent.release emission (03 §7.1, 05 §4).
type ReleasePayload struct {
	WorkerID string `json:"worker_id"`
}

// EventTurnCompleted is the one event type this module emits (05 §2.2); the
// runtime enumerates it as an EventType (04 §6).
const EventTurnCompleted = "agent.turn_completed"

// TurnCompleted is the agent.turn_completed payload (05 §2.2): every
// terminal outcome — agent finished, agent errored, provisioning died —
// becomes exactly one of these. No provider handles leak into it; the brain
// owns what a failure means for the ticket (05 D3).
type TurnCompleted struct {
	TicketID string  `json:"ticket_id"`
	WorkerID string  `json:"worker_id"`
	IsError  bool    `json:"is_error"`
	Output   string  `json:"output"` // the agent's turn output, or the failure description
	CostUSD  float64 `json:"cost_usd"`
}

// Loop cadence (05 §4–§5): trivial polling at N ≤ a handful of workers.
const (
	PollInterval      = 2 * time.Second  // advance every non-terminal machine
	ReconcileInterval = 60 * time.Second // adopt/create/destroy pool sweep; heals dead-lettered recreates
	// LivenessInterval is how often the status loop re-reads worker liveness so a
	// silently auto-stopped session becomes visible in Streams without a nudge
	// (amended 2026-07-05). Faster than reconcile (a status the user watches),
	// slower than the turn poller (liveness moves in seconds, not sub-seconds);
	// one ListWorkers call per tick regardless of pool size.
	LivenessInterval = 10 * time.Second
)
