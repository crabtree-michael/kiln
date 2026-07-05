// Package mock is the in-memory Provider selected by AGENT_MODE=mock — the
// default in dev and e2e (05 §8). It fakes only the provider: the generic
// machinery, ports, agent_turns table, and event path all run for real.
// In-memory; a restart resets it, which is fine everywhere the mock is legal.
package mock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// Injected failure sentinels (05 §8). Provisioning and StartTurn failures are
// transient/terminal mechanical errors the machine retries or exhausts (05
// §5); a dropped conversation surfaces as agent.ErrConversationLost.
var (
	errProvisioning      = errors.New("mock: provisioning failed")
	errStartTurnInjected = errors.New("mock: injected StartTurn failure")
	errUnknownJob        = errors.New("mock: unknown job")
)

// DefaultTurnDelay is the canned turn latency for unscripted messages (05 §8).
const DefaultTurnDelay = 100 * time.Millisecond

// cannedOutput is the default success output for an unscripted message (05 §8).
const cannedOutput = "mock: turn complete"

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

	// FailStartTurns makes StartTurn fail this many times, then succeed; each
	// failure decrements it.
	FailStartTurns int

	// StatusByName overrides a live worker's reported liveness in ListWorkers
	// (05 §8 test knob); a name with no entry reports RunReady, matching the
	// mock's instant, always-up lifecycle. Lets inspector/poll tests drive the
	// stopped/errored/starting states without a real sandbox.
	StatusByName map[string]agent.RunStatus

	mu         sync.Mutex
	workers    map[string]bool             // live worker names
	convs      map[string]map[string]bool  // worker name → live conversation ids
	jobs       map[string]scriptedJob      // job id → pending result
	lastOutput map[string]agent.TurnOutput // worker name → latest completed output
	seq        int                         // monotonic id source (deterministic, no rand)
}

// scriptedJob is one in-flight turn's eventual result and when it lands.
type scriptedJob struct {
	output  string
	isError bool
	doneAt  time.Time
}

var _ agent.Provider = (*Provider)(nil)

// New returns a mock with no script: every turn is the canned success.
func New() *Provider { return &Provider{} }

func (p *Provider) ListWorkers(_ context.Context) ([]agent.ProviderWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	out := make([]agent.ProviderWorker, 0, len(p.workers))
	for name := range p.workers {
		status := agent.RunReady
		if s, ok := p.StatusByName[name]; ok {
			status = s
		}
		out = append(out, agent.ProviderWorker{Name: name, Ref: name, Status: status})
	}
	return out, nil
}

func (p *Provider) CreateWorker(_ context.Context, name string) (agent.ProviderWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	if p.FailProvisioning {
		return agent.ProviderWorker{}, errProvisioning
	}
	p.workers[name] = true
	return agent.ProviderWorker{Name: name, Ref: name}, nil
}

func (p *Provider) WorkerReady(_ context.Context, w agent.ProviderWorker) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	return p.workers[w.Name], nil
}

func (p *Provider) DestroyWorker(_ context.Context, w agent.ProviderWorker) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	delete(p.workers, w.Name) // absent worker = success (05 §2.3)
	delete(p.convs, w.Name)   // release recreates a fresh workspace (05 §3, §4)
	return nil
}

func (p *Provider) StartTurn(
	_ context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()

	if p.FailStartTurns > 0 {
		p.FailStartTurns--
		return agent.TurnRef{}, errStartTurnInjected
	}

	conv, err := p.resolveConversation(w.Name, conversation, fresh)
	if err != nil {
		return agent.TurnRef{}, err
	}

	p.seq++
	jobID := fmt.Sprintf("job-%d", p.seq)
	st := p.scriptFor(message)
	p.jobs[jobID] = scriptedJob{output: st.Output, isError: st.IsError, doneAt: time.Now().Add(st.Delay)}
	return agent.TurnRef{Conversation: conv, Turn: jobID}, nil
}

func (p *Provider) CheckTurn(_ context.Context, w agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	j, ok := p.jobs[ref.Turn]
	if !ok {
		return agent.TurnStatus{}, fmt.Errorf("mock: job %q: %w", ref.Turn, errUnknownJob)
	}
	if time.Now().Before(j.doneAt) {
		return agent.TurnStatus{Running: true}, nil
	}
	p.lastOutput[w.Name] = agent.TurnOutput{Output: j.output, At: time.Now()}
	return agent.TurnStatus{Running: false, Output: j.output, IsError: j.isError, CostUSD: 0}, nil
}

// ReadLatestOutput returns the worker's last completed turn output (05 §2),
// recorded by CheckTurn or seeded via SeedLatestOutput. Empty when none.
func (p *Provider) ReadLatestOutput(_ context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	return p.lastOutput[w.Name], nil
}

// SetWorkerStatus overrides a live worker's reported liveness under the lock,
// so a test can flip a running sandbox to stopped/errored mid-loop without
// racing ListWorkers (05 §8 test knob; amended 2026-07-05).
func (p *Provider) SetWorkerStatus(name string, status agent.RunStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	if p.StatusByName == nil {
		p.StatusByName = map[string]agent.RunStatus{}
	}
	p.StatusByName[name] = status
}

// SeedLatestOutput presets a worker's latest output so inspector tests can read
// it without driving a full turn (05 §8 test knob).
func (p *Provider) SeedLatestOutput(name string, out agent.TurnOutput) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	p.lastOutput[name] = out
}

// DropConversation forgets a worker's live conversations on demand (05 §8), so
// tests exercise the fresh-conversation fallback: the next continuation
// StartTurn returns agent.ErrConversationLost — context lost, workspace kept,
// never a failed ticket (05 §3).
func (p *Provider) DropConversation(workerName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.init()
	delete(p.convs, workerName)
}

// init lazily allocates the maps so a bare &Provider{…} literal (the form
// tests use to set knobs) works without calling New.
func (p *Provider) init() {
	if p.workers == nil {
		p.workers = map[string]bool{}
	}
	if p.convs == nil {
		p.convs = map[string]map[string]bool{}
	}
	if p.jobs == nil {
		p.jobs = map[string]scriptedJob{}
	}
	if p.lastOutput == nil {
		p.lastOutput = map[string]agent.TurnOutput{}
	}
}

// resolveConversation opens a fresh conversation or validates a continuation,
// surfacing agent.ErrConversationLost when the referenced conversation is gone
// (05 §3). Caller holds p.mu.
func (p *Provider) resolveConversation(worker, conversation string, fresh bool) (string, error) {
	if fresh {
		p.seq++
		conv := fmt.Sprintf("conv-%d", p.seq)
		if p.convs[worker] == nil {
			p.convs[worker] = map[string]bool{}
		}
		p.convs[worker][conv] = true
		return conv, nil
	}
	if live := p.convs[worker]; live == nil || !live[conversation] {
		return "", fmt.Errorf("mock: worker %q conversation %q: %w", worker, conversation, agent.ErrConversationLost)
	}
	return conversation, nil
}

// scriptFor returns the scripted result for message, or the canned success.
// Caller holds p.mu.
func (p *Provider) scriptFor(message string) ScriptedTurn {
	if st, ok := p.Script[message]; ok {
		return st
	}
	return ScriptedTurn{Output: cannedOutput, IsError: false, Delay: DefaultTurnDelay}
}
