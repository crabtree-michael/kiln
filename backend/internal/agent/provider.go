package agent

import (
	"context"
	"errors"
)

// WorkerNamePrefix is the DEFAULT worker-name scope; the reconciler destroys
// prefix-matched workers that match no board slot (05 §4). Under multi-tenancy
// (11 §3) the live prefix is per-project — the ProviderResolver hands one back
// alongside the project's Provider — so no project ever sweeps another's live
// workers, even when several share one provider account.
const WorkerNamePrefix = "kiln-worker-"

// ProviderResolver maps a project to the coding-agent Provider that serves it
// and the worker-name prefix that scopes that project's sandboxes (11 §3).
// Under multi-tenancy the single construction-time provider + prefix become
// per-project: the reconciler, poller, and inspectors all resolve through this,
// so one project's turns and sweeps never touch another's provider or workers.
// A project whose provider cannot be resolved (e.g. a missing credential)
// returns an error the caller logs and isolates — other projects keep running
// (spec §6 failure isolation). Satisfied at the composition root.
type ProviderResolver interface {
	// For returns the provider and sandbox-name prefix for one project.
	For(ctx context.Context, projectID string) (Provider, string, error)
}

// ErrConversationLost is the sentinel an adapter returns from StartTurn when a
// continuation (fresh=false) references a conversation the provider no longer
// has (05 §3). The machine recognises it and falls back to a fresh
// conversation with the same message — context lost, workspace kept, logged;
// it never fails the ticket for it. Adapters wrap it (fmt.Errorf("…: %w", …))
// so errors.Is still matches.
var ErrConversationLost = errors.New("agent: provider lost the conversation")

// ErrOutOfCredits is the sentinel an adapter returns from any port call the
// provider rejects because the account's API credits are exhausted — a billing
// stop, not a transient fault. The machine recognises it and fails the turn
// immediately instead of burning its retry budget on calls that cannot succeed
// until the user tops up, and the error-turn output tells the user to replenish
// (05 §5). Adapters wrap it (fmt.Errorf("…: %w", …)) so errors.Is still matches.
var ErrOutOfCredits = errors.New("agent: provider credits exhausted")

// ErrProviderUnavailable is the sentinel the ProviderResolver returns when a
// project's configured provider cannot be built — most often because its
// registry key names no registered constructor (a rolled-back or dropped
// integration). Per D7 the resolver fails LOUD and CONTAINED rather than
// silently falling back to the deployment default, which would run the
// project's tickets on a different agent with different credentials and a
// different billing model, invisibly. The reconciler/poller already isolate a
// resolve failure per project (spec §6), so a project whose provider is
// unavailable pauses while the others keep running. Callers wrap it
// (fmt.Errorf("…: %w", …)) so errors.Is still matches.
var ErrProviderUnavailable = errors.New("agent: project provider unavailable")

// Capabilities is an adapter's self-description of provider shape (design §5,
// D2). The core reads these booleans to vary core-visible affordances WITHOUT
// naming a provider (05 abstraction rule) — the same leak-free pattern as
// reading TurnStatus.CostUSD == 0. It never asks "is this Amika?". An adapter
// that does not implement CapabilityReporter gets the zero value — the
// conservative, no-managed-sandbox Devin-shaped default — so a provider only
// declares what it genuinely supports.
type Capabilities struct {
	// ManagedSandbox reports that the caller creates and destroys a real,
	// long-lived remote workspace (Amika: true; a session-only provider like
	// Devin: false). Operator affordances that only mean something for a real
	// workspace — force-reset, inspect sandbox state — read this, never the
	// provider name (design §5, Phase 4).
	ManagedSandbox bool
	// ReportsCost reports that TurnStatus.CostUSD carries a meaningful figure
	// (Amika's job cost; Devin's ACU→USD estimate). A provider that reports
	// nothing leaves the field 0 and this false.
	ReportsCost bool
	// Snapshots reports that the provider accepts a base-image/snapshot handle
	// to seed a workspace/session (Amika's snapshot; Devin's snapshot_id).
	Snapshots bool
	// SecretsInject reports that the provider accepts caller-supplied secret
	// refs injected into the workspace (Amika's secret_env_vars). A provider
	// with no such mechanism leaves this false, and the dashboard hides the
	// secret-injection affordance for it (design §8).
	SecretsInject bool
}

// CapabilityReporter is optionally implemented by a Provider to declare its
// Capabilities (design §5). Absent ⇒ the zero-value (conservative) defaults —
// see CapabilitiesOf, the single core-side reader.
type CapabilityReporter interface {
	Capabilities() Capabilities
}

// CapabilitiesOf is the core's leak-free read of a provider's shape (design §5,
// D2): a provider that implements CapabilityReporter has its declaration
// returned; one that does not gets the conservative zero-value default. This is
// the ONLY place the core inspects provider shape, and it does so through a
// declared capability — never a type switch on the concrete adapter, never a
// provider name.
func CapabilitiesOf(p Provider) Capabilities {
	if r, ok := p.(CapabilityReporter); ok {
		return r.Capabilities()
	}
	return Capabilities{}
}

// ProviderErrorFields is optionally implemented by a provider adapter's returned
// error to expose scrub-safe, structured diagnostics — the transport status, the
// provider's error code, and a trace id — carrying no secret values. The
// provider-neutral core logs these as separate attributes so a create/turn
// failure stays diagnosable even when the log backend scrubs the free-text error
// (a provider's message can echo a rejected secret value — the very failure that
// motivated this: a bad injected secret made every CreateWorker fail and the
// scrubbed err read only "[Filtered]"). The amika adapter's *APIError implements
// it; a plain error simply doesn't, and callers fall back to the wrapped err.
type ProviderErrorFields interface {
	ProviderErrorFields() (status int, code, trace string)
}

// WorkerName derives the deterministic provider-side name for a board worker
// slot under the DEFAULT prefix (05 §4). The name is the whole board↔provider
// join — no shared registry, adoption is pure list-and-match (05 D5). A project
// whose ProviderResolver hands back a different prefix derives names from that
// prefix instead.
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
// errors, nothing more. An adapter may also implement CapabilityReporter to
// declare its shape (see Capabilities); the core reads it via CapabilitiesOf.
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
