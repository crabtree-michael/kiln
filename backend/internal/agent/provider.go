package agent

import "context"

// WorkerNamePrefix scopes every provider-side worker this module owns; the
// reconciler destroys prefix-matched workers that match no board slot (05 §4).
const WorkerNamePrefix = "kiln-worker-"

// WorkerName derives the deterministic provider-side name for a board
// worker slot (05 §4). The name is the whole board↔provider join — no shared
// registry, adoption is pure list-and-match (05 D5).
func WorkerName(workerID string) string { return WorkerNamePrefix + workerID }

// ProviderWorker is an adapter's handle for one provisioned worker (05 §2.3).
// Name follows WorkerName; Ref is the provider's own id (for Amika: the
// sandbox id) — opaque to the generic machinery and never visible outside
// this module (05 §6).
type ProviderWorker struct {
	Name string
	Ref  string
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
	// scoped) — adoption at startup and each reconciler sweep (05 §4).
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
}
