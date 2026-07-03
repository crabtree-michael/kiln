// Package mock is the in-memory Provider selected by AGENT_MODE=mock — the
// default in dev and e2e (05 §8). It fakes only the provider: the generic
// machinery, ports, agent_turns table, and event path all run for real.
// In-memory; a restart resets it, which is fine everywhere the mock is legal.
package mock

import (
	"context"
	"errors"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// errNotImplemented marks scaffold stubs; see docs/specs/05-agent-runtime.md.
var errNotImplemented = errors.New("agent/mock: not implemented (scaffold)")

// DefaultTurnDelay is the canned turn latency for unscripted messages (05 §8).
const DefaultTurnDelay = 100 * time.Millisecond

// ScriptedTurn is one test-configured turn result (05 §8).
type ScriptedTurn struct {
	Output  string
	IsError bool
	Delay   time.Duration
}

// Provider implements agent.Provider entirely in memory (05 §8): instant
// lifecycle (create/list/destroy/ready immediately true, so adoption and
// recycle paths are exercised for real) and scripted turns — a
// message → result map, defaulting to canned success after DefaultTurnDelay.
//
// The exported knobs drive every 01 §8 failure path in tests: provisioning
// that fails terminally, StartTurn failing N times before succeeding, turns
// that return is_error, and dropped conversations to exercise the §3
// fresh-fallback.
type Provider struct {
	// Script maps message → scripted result; unscripted messages succeed
	// with a canned output after DefaultTurnDelay.
	Script map[string]ScriptedTurn

	// FailProvisioning makes CreateWorker fail terminally (05 §8).
	FailProvisioning bool

	// FailStartTurns makes StartTurn fail this many times, then succeed.
	FailStartTurns int
}

var _ agent.Provider = (*Provider)(nil)

// New returns a mock with no script: every turn is the canned success.
func New() *Provider { return &Provider{} }

func (p *Provider) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	return nil, errNotImplemented
}

func (p *Provider) CreateWorker(ctx context.Context, name string) (agent.ProviderWorker, error) {
	return agent.ProviderWorker{}, errNotImplemented
}

func (p *Provider) WorkerReady(ctx context.Context, w agent.ProviderWorker) (bool, error) {
	return false, errNotImplemented
}

func (p *Provider) DestroyWorker(ctx context.Context, w agent.ProviderWorker) error {
	return errNotImplemented
}

func (p *Provider) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	return agent.TurnRef{}, errNotImplemented
}

func (p *Provider) CheckTurn(ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	return agent.TurnStatus{}, errNotImplemented
}

// DropConversation forgets a live conversation on demand (05 §8), so tests
// exercise the fresh-conversation fallback: context lost, workspace kept,
// never a failed ticket (05 §3).
func (p *Provider) DropConversation(workerName string) {
	// Scaffold: lands with the in-memory state (05 §8).
}
