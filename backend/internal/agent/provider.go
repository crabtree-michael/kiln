package agent

import (
	"context"
	"errors"
)

// WorkerNamePrefix is the DEFAULT worker-name scope; the reconciler destroys
// prefix-matched workers that match no board slot (05 §4). Environments
// sharing one provider account must each override it (KILN_WORKER_PREFIX →
// WithWorkerPrefix + amika Config.WorkerPrefix, amended 2026-07-05) so no
// instance ever sweeps another environment's live workers.
const WorkerNamePrefix = "kiln-worker-"

// ErrConversationLost is the sentinel an adapter returns from StartTurn when a
// continuation (fresh=false) references a conversation the provider no longer
// has (05 §3). The machine recognises it and falls back to a fresh
// conversation with the same message — context lost, workspace kept, logged;
// it never fails the ticket for it. Adapters wrap it (fmt.Errorf("…: %w", …))
// so errors.Is still matches.
var ErrConversationLost = errors.New("agent: provider lost the conversation")

// WorkerName derives the deterministic provider-side name for a board worker
// slot under the DEFAULT prefix (05 §4). The name is the whole board↔provider
// join — no shared registry, adoption is pure list-and-match (05 D5). A
// Service configured with WithWorkerPrefix derives names from its own prefix
// instead.
func WorkerName(workerID string) string { return WorkerNamePrefix + workerID }

// RunStatus is the provider-neutral liveness of one worker's underlying
// session/sandbox (05 §2, amended 2026-07-05). It is liveness *only* — whether
// a turn is in flight (building vs idle) is the Service's business, derived from
// agent_turns and composed on top in ListAgents. An adapter classifies its
// platform's own state into these four; a provider that cannot report liveness
// leaves it "" and callers treat that as ready (preserving pre-liveness
// behaviour).
type RunStatus string

const (
	RunStarting RunStatus = "starting" // provisioning / not reachable yet
	RunReady    RunStatus = "ready"    // reachable — can accept a turn
	RunStopped  RunStatus = "stopped"  // auto-stopped / not running
	RunErrored  RunStatus = "errored"  // terminal provisioning/session failure
)

// ProviderWorker is an adapter's handle for one provisioned worker (05 §2.3).
// Name follows WorkerName; Ref is the provider's own id (for Amika: the
// sandbox id) — opaque to the generic machinery and never visible outside
// this module (05 §6). Status is the worker's liveness at the moment it was
// listed; ListWorkers sets it, the lifecycle calls (create/adopt/reconcile)
// ignore it — they match on Name.
type ProviderWorker struct {
	Name   string
	Ref    string
	Status RunStatus
}

// TurnRef identifies one in-flight turn (05 §2.3): the conversation being
// continued plus the turn itself, both opaque provider handles (for Amika:
// session id + job id). Persisted in agent_turns as provider_turn (05 §7).
type TurnRef struct {
	Conversation string `json:"conversation"`
	Turn         string `json:"turn"`
}

// TurnStatus is CheckTurn's answer (05 §2.3): still running, or terminal
// with the turn's outcome.
type TurnStatus struct {
	Running bool
	Output  string // the agent's turn output, or the failure description
	IsError bool
	CostUSD float64 // 0 when the provider doesn't report cost (05 §2.2)
}

// Provider is the internal seam an adapter implements (05 §2.3) — the only
// thing a platform integration writes. The turn state machine, reconciler,
// poller, dedupe table, and mock are written once against this port; Amika
// (./amika) is one implementation, a future provider is another. Calls may
// be transient-retried by the machine (05 §5); adapters map their platform's
// errors, nothing more.
type Provider interface {
	// ListWorkers returns every live worker this module owns (WorkerNamePrefix
	// scoped) — adoption at startup and each reconciler sweep (05 §4). Each
	// worker's Status carries its liveness at list time (05 §2, amended); the
	// reconciler ignores it (matches on Name), the inspector reads it.
	ListWorkers(ctx context.Context) ([]ProviderWorker, error)

	// CreateWorker provisions a worker under the given name. Provisioning may
	// be asynchronous — readiness is WorkerReady's business (05 §2.3).
	CreateWorker(ctx context.Context, name string) (ProviderWorker, error)

	// WorkerReady reports whether w can accept a turn, waking it if the
	// provider idled it (05 §6).
	WorkerReady(ctx context.Context, w ProviderWorker) (bool, error)

	// DestroyWorker removes w; an already-absent worker is success (05 §2.3).
	DestroyWorker(ctx context.Context, w ProviderWorker) error

	// StartTurn begins one turn on w. fresh starts a new conversation (the
	// first send after create/release — 05 §3); otherwise conversation is the
	// handle recorded from the previous turn's TurnRef.
	StartTurn(ctx context.Context, w ProviderWorker, conversation, message string, fresh bool) (TurnRef, error)

	// CheckTurn polls one turn to its terminal outcome (05 §2.3); the §5
	// poller calls it every PollInterval.
	CheckTurn(ctx context.Context, w ProviderWorker, ref TurnRef) (TurnStatus, error)

	// ReadLatestOutput returns the most recent completed assistant output for
	// the worker's current conversation, provider-neutral (05 §2). Empty
	// TurnOutput when there is no completed turn yet; not an error. Used by the
	// brain's get_agent_updates read tool via the Service inspector methods.
	ReadLatestOutput(ctx context.Context, w ProviderWorker) (TurnOutput, error)
}
