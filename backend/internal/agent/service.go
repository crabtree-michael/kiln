package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

// maxAttempts is the machine's own retry budget for transient provider errors
// (05 §5, 04 §3): terminal exhaustion moves the machine to failed, which then
// owes the error-turn event.
const maxAttempts = 8

// instructionSummaryBytes / outputSummaryBytes bound how much of a delivered
// instruction and a returned agent output a log line carries. The paired
// *_hash fingerprints give exact identity — a redelivered stale instruction
// (ticket 841fb6cc) shows the same instruction_hash, an unchanged output shows
// the same output_hash — without logging kilobytes of diff.
const (
	instructionSummaryBytes = 512
	outputSummaryBytes      = 1024
)

// deliveryTurn is the correlation/turn id for one delivery's whole agent-side
// lifecycle (record → start → completed), keyed by the outbox id that also
// serves as the idempotency key. ticket_id links it back to the brain pass
// (evt-<id>) that decided the send.
func deliveryTurn(idempotencyKey int64) string {
	return fmt.Sprintf("delivery-%d", idempotencyKey)
}

// EventEnqueuer is this module's port onto the runtime's event queue
// (04 §6): every terminal turn outcome becomes exactly one
// agent.turn_completed event — the single inbound seam; this module never
// mutates board state (05 §2.2, D3). Satisfied at the composition root by a
// thin adapter over the runtime's EnqueueEvent. Under multi-tenancy (11 §3)
// the emitting turn's projectID travels alongside the event so the runtime can
// stamp events.project_id and resolve the right tenant's brain — the agent
// records it on the Turn (and agent_turns.project_id) and threads it here.
//
// idempotencyKey is the emitting turn's outbox id, threaded so the runtime can
// dedupe the completion against its events-queue unique index (architecture
// audit 3.1). A turn's emit and its phase→done write are two statements: a
// crash between them re-runs stepCheckTurn and re-emits agent.turn_completed,
// so without a key the brain would run a second pass on the same completion.
// The key makes the redelivery a no-op — exactly-once completion.
type EventEnqueuer interface {
	EnqueueEvent(ctx context.Context, projectID, eventType string, idempotencyKey int64, payload []byte) (int64, error)
}

// Projects is this module's read-only port onto the set of live projects the
// reconciler must sweep (11 §3). Each tick asks for the current ids and
// reconciles each against its own provider; a project that appears or vanishes
// between ticks is picked up or dropped on the next sweep. Satisfied at the
// composition root.
type Projects interface {
	ProjectIDs(ctx context.Context) ([]string, error)
}

// Slots is this module's read-only port onto one project's board capacity slots
// (03 §2.3): the reconciler matches that project's provider workers against
// these ids (05 §4). Capacity questions stay the board's alone — this module
// never counts (05 §3). Scoped by projectID under multi-tenancy (11 §3).
type Slots interface {
	WorkerIDs(ctx context.Context, projectID string) ([]string, error)
}

// Clock abstracts time for the reconciler/poller so unit tests drive the
// machine with a fake clock (05 §10). Mirrors the runtime's Clock (04 §9);
// module-local to keep the boundary clean.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// BoardRefresher lets the status loop ask the client-facing layer to re-push
// the board snapshot when a *silent* liveness change — e.g. a sandbox
// auto-stop, which fires no event — means the rendered Streams status is now
// stale (amended 2026-07-05). It is a fire-and-forget nudge: a missed push
// self-heals on the next liveness change or the next board.updated. The agent
// module never imports the api layer; the composition root satisfies this with
// the SSE hub. Optional — a nil refresher just skips the nudge.
type BoardRefresher interface {
	RefreshBoard(ctx context.Context) error

	// SetWorkerHealth reports the project's currently-errored worker ids to the
	// board so the pull binds Ready tickets only to healthy sandboxes (03 §5
	// amended). Full reconcile per project: ids not listed are treated healthy.
	// The agent module detects the failure (terminal RunErrored liveness); the
	// board owns the workers row and performs the write. Fire-and-forget like
	// RefreshBoard — a missed write self-heals on the next liveness tick.
	SetWorkerHealth(ctx context.Context, projectID string, erroredWorkerIDs []string) error
}

// Service is the provider-agnostic core (05 §9): it implements the
// AgentRuntime consumer contract the runtime's outbox worker calls (05 §2.1;
// the port shape is runtime.AgentRuntime, matched structurally — this module
// never imports the runtime), and owns the §5 turn state machine, the §4
// pool reconciler, and the §5 poller, all written once against the Provider
// port. Under multi-tenancy (11 §3) it resolves a project's Provider and
// worker-name prefix per project via the ProviderResolver — the reconciler
// iterates every project, the poller resolves per turn — so one project's turns
// and sweeps never touch another's provider or workers. Constructed at the
// composition root (05 §9); AGENT_MODE selects the real or mock Provider there.
type Service struct {
	store     Store
	providers ProviderResolver
	projects  Projects
	events    EventEnqueuer
	slots     Slots
	clock     Clock
	refresher BoardRefresher // may be nil (05 §9): no board push nudge on liveness change

	mu      sync.Mutex
	workers map[string]ProviderWorker // cache keyed by name; names are prefix-scoped so unique per project

	statusMu   sync.Mutex             // guards lastStatus only — never held across a ListAgents call
	lastStatus map[string]AgentStatus // worker id → last-pushed status, for the liveness diff

	// provisionMu guards provisionErrs: project id → the worker ids whose sandbox
	// failed to provision on the last reconcile sweep (CreateWorker errored, so no
	// live sandbox exists to observe). The 60s reconcile loop writes it; the 10s
	// liveness loop unions it into the errored set it reports to the board, so a
	// never-provisioned slot is health-gated out of the pull between sweeps instead
	// of staying silently 'ok'. Held only for the map swap, never across a provider
	// or board call.
	provisionMu   sync.Mutex
	provisionErrs map[string]map[string]struct{}
}

// NewService assembles the agent runtime over its ports. providers resolves a
// project's Provider + worker-name prefix; projects enumerates the projects to
// reconcile (11 §3). refresher is optional (nil disables the liveness push
// nudge — e.g. in tests that do not exercise it).
func NewService(
	store Store, providers ProviderResolver, projects Projects,
	events EventEnqueuer, slots Slots, clock Clock, refresher BoardRefresher,
) *Service {
	return &Service{
		store:     store,
		providers: providers,
		projects:  projects,
		events:    events,
		slots:     slots,
		clock:     clock,
		refresher: refresher,
		workers:   map[string]ProviderWorker{},

		provisionErrs: map[string]map[string]struct{}{},
	}
}

// Send delivers one message to a worker (05 §2.1): decode the agent.send
// payload (SendPayload — 03 §7.1), record the operation in agent_turns keyed
// by the outbox id, and return. Record-and-return — never blocks on
// provisioning or the turn; the machine owns progression (05 D2). A repeated
// key is a silent success (04 §3). The first Send after a worker is
// (re)created starts a fresh conversation; later Sends continue it — derived
// from this module's own state (05 §2.1, §3). The payload's project_id is
// persisted so the poller can resolve this turn's provider (11 §3).
func (s *Service) Send(ctx context.Context, idempotencyKey int64, payload []byte) error {
	var p SendPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("agent: decode send payload: %w", err)
	}
	ctx = obs.WithTurn(ctx, deliveryTurn(idempotencyKey))
	turn := Turn{
		IdempotencyKey: idempotencyKey,
		Kind:           KindSend,
		ProjectID:      p.ProjectID,
		TicketID:       p.TicketID,
		WorkerID:       p.WorkerID,
		Message:        p.Message,
		Phase:          PhaseRecorded,
	}
	s.markContinuation(ctx, &turn)
	// The outbound delivery, logged at the seam it lands: instruction fingerprint
	// + summary, whether it continues an existing conversation, and the outbox
	// idempotency key. A stale/duplicate redelivery is the same instruction_hash
	// on the same ticket (ticket 841fb6cc).
	slog.InfoContext(ctx, "agent.delivery.recorded",
		"idem_key", idempotencyKey,
		"project_id", p.ProjectID,
		"ticket_id", p.TicketID,
		"worker_id", p.WorkerID,
		"instruction_hash", obs.Hash(p.Message),
		"instruction", obs.Summary(p.Message, instructionSummaryBytes),
		"continuation", turn.ProviderTurn != nil)
	if _, err := s.store.Record(ctx, turn); err != nil {
		return fmt.Errorf("agent: record send: %w", err)
	}
	return nil
}

// Release recycles a worker after AcceptToDone (05 §2.1, §4): decode the
// agent.release payload (ReleasePayload), record, return. The machine
// destroys and recreates the slot's provider worker so the next conversation
// starts from a fresh workspace; a dead-lettered recreate is healed by the
// reconciler sweep — the cost is latency on that slot's next Send, never a
// stuck ticket (05 §4). A release carries no ticket and emits no
// agent.turn_completed — it is worker recycling, not a turn (05 §2.2, §4).
func (s *Service) Release(ctx context.Context, idempotencyKey int64, payload []byte) error {
	var p ReleasePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("agent: decode release payload: %w", err)
	}
	ctx = obs.WithTurn(ctx, deliveryTurn(idempotencyKey))
	turn := Turn{
		IdempotencyKey: idempotencyKey,
		Kind:           KindRelease,
		ProjectID:      p.ProjectID,
		WorkerID:       p.WorkerID,
		Phase:          PhaseRecorded,
	}
	slog.InfoContext(ctx, "agent.release.recorded",
		"idem_key", idempotencyKey, "project_id", p.ProjectID, "worker_id", p.WorkerID)
	if _, err := s.store.Record(ctx, turn); err != nil {
		return fmt.Errorf("agent: record release: %w", err)
	}
	return nil
}

// Run drives the module's two loops until ctx ends (05 §4–§5): an initial
// reconcile, then the poller every PollInterval and the reconciler every
// ReconcileInterval. Recovery is the same loop (05 §7): on start, the
// non-terminal rows of agent_turns simply continue.
func (s *Service) Run(ctx context.Context) error {
	s.reconcile(ctx)

	var wg sync.WaitGroup
	wg.Go(func() { s.loop(ctx, PollInterval, s.pollOnce) })
	wg.Go(func() { s.loop(ctx, ReconcileInterval, s.reconcile) })
	wg.Go(func() { s.loop(ctx, LivenessInterval, s.refreshStatuses) })
	wg.Wait()
	return nil
}

// ListAgents reports every live worker one project owns with its neutral
// busy/idle status and current ticket binding (05 §2) — backs the brain's
// list_agents tool. The project's provider + worker-name prefix come from the
// resolver (11 §3), so the result is scoped to that project. Status and ticket
// come from the module's own agent_turns (LatestForWorker); no provider handle
// is exposed.
func (s *Service) ListAgents(ctx context.Context, projectID string) ([]AgentInfo, error) {
	provider, prefix, err := s.providers.For(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve provider for project %q: %w", projectID, err)
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent: list agents: %w", err)
	}
	out := make([]AgentInfo, 0, len(live))
	for _, w := range live {
		workerID := strings.TrimPrefix(w.Name, prefix)
		info := AgentInfo{WorkerID: workerID, Status: statusFor(w.Status, false)}
		if prev, found, lerr := s.store.LatestForWorker(ctx, workerID); lerr == nil && found {
			info.UpdatedAt = prev.UpdatedAt
			if prev.Kind == KindSend {
				info.TicketID = prev.TicketID
				info.Status = statusFor(w.Status, isRunning(prev.Phase))
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// statusFor folds provider liveness (RunStatus) with turn activity into the
// AgentStatus the brain and Streams see (05 §2, amended). Liveness dominates: a
// stopped/errored/starting session is reported as such regardless of a possibly
// stale in-flight turn row; only a ready worker distinguishes building (turn in
// flight) from idle. An empty RunStatus — a provider that does not report
// liveness — is treated as ready, preserving the pre-liveness working|idle
// behaviour.
func statusFor(run RunStatus, turnRunning bool) AgentStatus {
	switch run {
	case RunStopped:
		return AgentStopped
	case RunErrored:
		return AgentErrored
	case RunStarting:
		return AgentStarting
	case RunReady:
		// A live worker: distinguished by turn activity below.
	}
	// RunReady, or "" from a provider that reports no liveness: fall back to the
	// turn-derived building|idle.
	if turnRunning {
		return AgentBuilding
	}
	return AgentIdle
}

// GetAgentUpdates returns one worker's status plus its latest completed output
// (05 §2) — backs the brain's get_agent_updates tool. The worker's provider +
// prefix come from the project's resolver (11 §3). An unknown/never-created
// worker is an empty idle update, not an error (best-effort read, 05 D2).
func (s *Service) GetAgentUpdates(ctx context.Context, projectID, workerID string) (AgentUpdate, error) {
	provider, prefix, err := s.providers.For(ctx, projectID)
	if err != nil {
		return AgentUpdate{}, fmt.Errorf("agent: resolve provider for project %q: %w", projectID, err)
	}
	u := AgentUpdate{WorkerID: workerID, Status: AgentIdle}
	turnRunning := false
	if prev, found, lerr := s.store.LatestForWorker(ctx, workerID); lerr == nil && found && prev.Kind == KindSend {
		turnRunning = isRunning(prev.Phase)
		u.IsError = prev.Phase == PhaseFailed
	}
	w, err := s.resolveWorker(ctx, provider, workerName(prefix, workerID))
	if err != nil {
		return AgentUpdate{}, fmt.Errorf("agent: get agent updates: %w", err)
	}
	// Fold liveness with turn activity (statusFor); a not-live worker has an
	// empty RunStatus, degrading to the turn-derived building|idle.
	u.Status = statusFor(w.Status, turnRunning)
	if w == (ProviderWorker{}) {
		return u, nil // worker not live yet — status only
	}
	out, err := provider.ReadLatestOutput(ctx, w)
	if err != nil {
		return AgentUpdate{}, fmt.Errorf("agent: read latest output: %w", err)
	}
	u.LatestOutput = out.Output
	u.At = out.At
	return u, nil
}

// ResetProject tears down one project's live workers and clears that project's
// entries from the in-memory worker cache — the developer "fresh session" reset,
// scoped to the caller's project (docs/superpowers/specs/
// 2026-07-04-debug-reset-session-design.md, 11 §3). It resolves ONLY that
// project's provider + prefix and destroys only its prefix-matched sandboxes, so
// it never touches another tenant's workers. Best-effort: a destroy failure on
// one worker is logged and does not abort the others, so a single stuck sandbox
// never blocks the reset. Clearing the cache under the same mutex that guards the
// reconcile loop and turn execution is what a bare DB delete misses — stale
// cached handles would otherwise survive the wipe. The caller deletes the
// project's board rows first, so the reconcile loop has no wanted slots to
// re-provision for this project while this runs.
func (s *Service) ResetProject(ctx context.Context, projectID string) error {
	provider, prefix, err := s.providers.For(ctx, projectID)
	if err != nil {
		return fmt.Errorf("agent: reset resolve provider for project %s: %w", projectID, err)
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		return fmt.Errorf("agent: reset list workers for project %s: %w", projectID, err)
	}
	for _, w := range live {
		if !strings.HasPrefix(w.Name, prefix) {
			continue
		}
		if derr := provider.DestroyWorker(ctx, w); derr != nil {
			slog.ErrorContext(ctx, "agent: reset destroy worker", "worker", w.Name, "err", derr)
		}
	}
	// Drop only this project's cached handles; names are prefix-scoped, so the
	// prefix uniquely selects one tenant's slots (11 §3).
	s.mu.Lock()
	for name := range s.workers {
		if strings.HasPrefix(name, prefix) {
			delete(s.workers, name)
		}
	}
	s.mu.Unlock()
	return nil
}

// refreshStatuses re-reads every worker's composed status across all projects
// and, when any has changed since the last tick, nudges the board to re-push so
// Streams reflects the new liveness (amended 2026-07-05). This is what surfaces
// a *silent* auto-stop: nothing else emits an event when a sandbox stops. One
// ListWorkers call per project per tick; the push only fires on a real change.
// A project whose provider cannot be resolved is logged and skipped — its
// absence just doesn't contribute to the diff (spec §6 failure isolation).
func (s *Service) refreshStatuses(ctx context.Context) {
	pids, err := s.projects.ProjectIDs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: liveness list projects", "err", err)
		return
	}
	var infos []AgentInfo
	for _, pid := range pids {
		got, gerr := s.ListAgents(ctx, pid)
		if gerr != nil {
			slog.ErrorContext(ctx, "agent: liveness refresh; skipping project", "project", pid, "err", gerr)
			continue
		}
		infos = append(infos, got...)
		s.reconcileWorkerHealth(ctx, pid, got)
	}
	if !s.statusChanged(infos) || s.refresher == nil {
		return
	}
	if err := s.refresher.RefreshBoard(ctx); err != nil {
		slog.WarnContext(ctx, "agent: refresh board after liveness change", "err", err)
	}
}

// reconcileWorkerHealth reports the project's currently-errored worker ids to
// the board so the pull binds Ready tickets only to healthy sandboxes (03 §5
// amended). Called every tick per project — the board write is an idempotent
// full reconcile keyed on the project's own worker ids, so it must NOT ride the
// aggregated statusChanged gate (which spans all projects and decides only
// whether to re-push Streams). A nil refresher (test wiring that does not
// exercise the board seam) skips it, exactly like the board nudge.
func (s *Service) reconcileWorkerHealth(ctx context.Context, projectID string, infos []AgentInfo) {
	if s.refresher == nil {
		return
	}
	// Two disjoint errored sources: slots whose sandbox failed to provision (no
	// live sandbox in infos, carried by the reconcile loop) and live sandboxes
	// reporting a terminal RunErrored. Union both so a never-provisioned slot is
	// gated out of the pull just like a sandbox that died after coming up.
	errored := s.provisionFailedIDs(projectID)
	seen := make(map[string]struct{}, len(errored))
	for _, id := range errored {
		seen[id] = struct{}{}
	}
	for _, in := range infos {
		if in.Status != AgentErrored {
			continue
		}
		if _, dup := seen[in.WorkerID]; dup {
			continue
		}
		seen[in.WorkerID] = struct{}{}
		errored = append(errored, in.WorkerID)
	}
	if err := s.refresher.SetWorkerHealth(ctx, projectID, errored); err != nil {
		slog.WarnContext(ctx, "agent: set worker health after liveness refresh",
			"project", projectID, "err", err)
	}
}

// statusChanged swaps in the current per-worker status set and reports whether
// it differs from the previous tick (added, removed, or changed status). Holds
// statusMu only — never the worker mutex — and never across a provider/store
// call, so it cannot deadlock with ListAgents.
func (s *Service) statusChanged(infos []AgentInfo) bool {
	next := make(map[string]AgentStatus, len(infos))
	for _, in := range infos {
		next[in.WorkerID] = in.Status
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	changed := len(next) != len(s.lastStatus)
	if !changed {
		for id, st := range next {
			if s.lastStatus[id] != st {
				changed = true
				break
			}
		}
	}
	s.lastStatus = next
	return changed
}

// markContinuation stamps turn with the prior conversation handle when this
// worker's newest operation was a send that already opened one — that is how
// first-message-vs-continuation is derived (05 §2.1, §3): no row, or a release
// row, leaves ProviderTurn nil and the next StartTurn goes fresh.
func (s *Service) markContinuation(ctx context.Context, turn *Turn) {
	prev, found, err := s.store.LatestForWorker(ctx, turn.WorkerID)
	if err != nil {
		slog.WarnContext(ctx, "agent: lookup previous turn for continuation; proceeding as fresh",
			"worker", turn.WorkerID, "err", err)
		return
	}
	if !found {
		return
	}
	if prev.Kind == KindSend && prev.ProviderTurn != nil && prev.ProviderTurn.Conversation != "" {
		turn.ProviderTurn = &TurnRef{Conversation: prev.ProviderTurn.Conversation}
	}
}

// loop runs step every interval on the injected clock until ctx is done.
func (s *Service) loop(ctx context.Context, interval time.Duration, step func(context.Context)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.clock.After(interval):
			step(ctx)
		}
	}
}

// pollOnce advances every non-terminal machine one step (05 §5).
func (s *Service) pollOnce(ctx context.Context) {
	rows, err := s.store.ListNonTerminal(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: list non-terminal turns", "err", err)
		return
	}
	for _, t := range rows {
		s.advance(ctx, t)
	}
}

// advance dispatches one machine step by operation kind (05 §5). It resolves
// the turn's project provider (11 §3) — a project whose provider cannot be
// resolved (e.g. a missing credential) is logged and left for the next poll,
// isolating it from other turns (spec §6) — then stamps the context with this
// delivery's turn id so every step it drives (start, check, completed) shares
// one correlation id across the async poller.
func (s *Service) advance(ctx context.Context, t Turn) {
	ctx = obs.WithTurn(ctx, deliveryTurn(t.IdempotencyKey))
	provider, prefix, err := s.providers.For(ctx, t.ProjectID)
	if err != nil {
		slog.WarnContext(ctx, "agent: resolve provider for turn; will retry next poll",
			"project", t.ProjectID, "worker", t.WorkerID, "err", err)
		return
	}
	switch t.Kind {
	case KindSend:
		s.advanceSend(ctx, provider, prefix, t)
	case KindRelease:
		s.advanceRelease(ctx, provider, prefix, t)
	default:
		slog.WarnContext(ctx, "agent: unknown turn kind", "kind", t.Kind)
	}
}

// advanceSend steps one send machine (05 §5):
// recorded → worker_ready → turn_started → done, with failed owing the event.
func (s *Service) advanceSend(ctx context.Context, provider Provider, prefix string, t Turn) {
	switch t.Phase {
	case PhaseRecorded:
		s.stepEnsureReady(ctx, provider, prefix, t)
	case PhaseWorkerReady:
		s.stepStartTurn(ctx, provider, prefix, t)
	case PhaseTurnStarted:
		s.stepCheckTurn(ctx, provider, prefix, t)
	case PhaseFailed:
		s.stepEmitFailure(ctx, t)
	case PhaseDone:
		// Resting; nothing to do (05 §5).
	default:
		slog.WarnContext(ctx, "agent: unknown phase", "phase", t.Phase)
	}
}

// advanceRelease destroys then recreates the slot's worker for a fresh
// workspace and rests at done — no turn, no event (05 §4). A failed
// recreate is left for the reconciler's next sweep to heal; the row still
// settles so it never lingers non-terminal.
func (s *Service) advanceRelease(ctx context.Context, provider Provider, prefix string, t Turn) {
	name := workerName(prefix, t.WorkerID)
	if err := provider.DestroyWorker(ctx, s.lookupWorker(name)); err != nil {
		slog.WarnContext(ctx, "agent: release destroy", "worker", name, "err", err)
	}
	if nw, err := provider.CreateWorker(ctx, name); err != nil {
		slog.WarnContext(ctx, "agent: release recreate; reconciler will heal", "worker", name, "err", err)
	} else {
		s.putWorker(nw)
	}
	t.Phase = PhaseDone
	s.update(ctx, t)
}

// stepEnsureReady moves recorded → worker_ready once the worker exists and is
// ready (05 §5). Provider errors count against the retry budget; a not-yet
// ready worker just waits for the next poll.
func (s *Service) stepEnsureReady(ctx context.Context, provider Provider, prefix string, t Turn) {
	w, err := s.ensureWorker(ctx, provider, prefix, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	ready, err := provider.WorkerReady(ctx, w)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	if !ready {
		return
	}
	t.Phase = PhaseWorkerReady
	s.update(ctx, t)
}

// stepStartTurn moves worker_ready → turn_started (05 §5). fresh ⇔ the first
// send of a conversation (no recorded conversation handle). A lost
// conversation falls back to fresh with the same message (05 §3).
func (s *Service) stepStartTurn(ctx context.Context, provider Provider, prefix string, t Turn) {
	w, err := s.ensureWorker(ctx, provider, prefix, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	fresh := t.ProviderTurn == nil || t.ProviderTurn.Conversation == ""
	conversation := ""
	if !fresh {
		conversation = t.ProviderTurn.Conversation
	}
	ref, err := provider.StartTurn(ctx, w, conversation, t.Message, fresh)
	if err != nil {
		s.handleStartTurnErr(ctx, t, fresh, err)
		return
	}
	// The instruction is now actually in flight at the provider. fresh vs a
	// continuation, plus the instruction fingerprint, is exactly what
	// distinguishes a correct new turn from a stale redelivery (ticket 841fb6cc).
	slog.InfoContext(ctx, "agent.turn.started",
		"idem_key", t.IdempotencyKey, "ticket_id", t.TicketID, "worker_id", t.WorkerID,
		"fresh", fresh, "instruction_hash", obs.Hash(t.Message))
	t.ProviderTurn = &ref
	t.Phase = PhaseTurnStarted
	s.update(ctx, t)
}

// handleStartTurnErr routes a StartTurn failure: a lost continuation falls
// back to a fresh conversation (05 §3); anything else counts against the
// retry budget (05 §5).
func (s *Service) handleStartTurnErr(ctx context.Context, t Turn, fresh bool, cause error) {
	if !fresh && errors.Is(cause, ErrConversationLost) {
		slog.WarnContext(ctx, "agent: conversation lost; retrying fresh with the same message",
			"worker", t.WorkerID, "err", cause)
		t.ProviderTurn = nil
		s.update(ctx, t)
		return
	}
	s.recordFailure(ctx, t, cause)
}

// stepCheckTurn polls the in-flight turn; on a terminal outcome it enqueues
// the agent.turn_completed event and rests the machine at done (05 §5).
func (s *Service) stepCheckTurn(ctx context.Context, provider Provider, prefix string, t Turn) {
	w, err := s.ensureWorker(ctx, provider, prefix, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	if t.ProviderTurn == nil {
		s.recordFailure(ctx, t, errMissingTurnRef)
		return
	}
	st, err := provider.CheckTurn(ctx, w, *t.ProviderTurn)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	if st.Running {
		return
	}
	// Emit first, mark done only if it committed. A failed emit leaves the turn
	// at turn_started so the next poll re-checks and re-emits — deduped by the
	// event idempotency key, so the retry is exactly-once, never a double brain
	// pass (architecture audit 3.1).
	if err := s.emitCompleted(ctx, t, st.IsError, st.Output, st.CostUSD); err != nil {
		slog.ErrorContext(ctx, "agent: emit turn_completed; will retry next poll", "err", err)
		return
	}
	t.Phase = PhaseDone
	s.update(ctx, t)
}

// stepEmitFailure fires the error-shaped event a failed machine owes, then
// rests it at done (05 §5: failed → done). Same emit-then-settle ordering as
// stepCheckTurn: a failed emit leaves the machine at failed for the next poll to
// re-emit, deduped by the idempotency key (architecture audit 3.1).
func (s *Service) stepEmitFailure(ctx context.Context, t Turn) {
	if err := s.emitCompleted(ctx, t, true, failureOutput(t), 0); err != nil {
		slog.ErrorContext(ctx, "agent: emit failure turn_completed; will retry next poll", "err", err)
		return
	}
	t.Phase = PhaseDone
	s.update(ctx, t)
}

// outOfCreditsMessage is the user-facing failure output a turn carries when the
// provider rejected it for exhausted API credits (05 §5). It replaces the raw
// provider error so the brain surfaces plain, actionable feedback rather than a
// billing envelope. Provider-neutral by design — nothing outside the module names
// the platform (05 §1).
const outOfCreditsMessage = "I'm out of API credits, so I can't run the agent right now. " +
	"Please replenish your credits and try again."

// recordFailure books one retry; exhausting the budget moves the machine to
// failed (05 §5, 04 §3). An out-of-credits rejection is terminal, not transient:
// no retry succeeds until the user tops up, so it fails the turn now — sparing the
// retry budget the doomed provider calls — and carries a plain out-of-credits
// message instead of the raw error (05 §5).
func (s *Service) recordFailure(ctx context.Context, t Turn, cause error) {
	t.Attempts++
	if errors.Is(cause, ErrOutOfCredits) {
		slog.WarnContext(ctx, "agent: provider out of credits; failing turn without retry",
			"ticket_id", t.TicketID, "worker_id", t.WorkerID, "err", cause)
		t.LastError = outOfCreditsMessage
		t.Phase = PhaseFailed
		s.update(ctx, t)
		return
	}
	t.LastError = cause.Error()
	if t.Attempts >= maxAttempts {
		t.Phase = PhaseFailed
	}
	s.update(ctx, t)
}

// emitCompleted enqueues one agent.turn_completed event (05 §2.2). No provider
// handles leak into the payload. The turn's outbox id is threaded as the event
// idempotency key so a crash-replayed emit (stepCheckTurn re-running before the
// phase→done write commits) is deduped by the runtime rather than waking the
// brain twice on the same completion (architecture audit 3.1).
func (s *Service) emitCompleted(ctx context.Context, t Turn, isErr bool, output string, cost float64) error {
	// The inbound result, logged before it becomes an event: output fingerprint
	// + summary keyed to the same delivery turn id and ticket, closing the loop
	// opened by agent.delivery.recorded / agent.turn.started.
	slog.InfoContext(ctx, "agent.turn.completed",
		"idem_key", t.IdempotencyKey, "ticket_id", t.TicketID, "worker_id", t.WorkerID,
		"is_error", isErr, "cost_usd", cost,
		"output_hash", obs.Hash(output), "output", obs.Summary(output, outputSummaryBytes))
	payload, err := json.Marshal(TurnCompleted{
		TicketID: t.TicketID,
		WorkerID: t.WorkerID,
		IsError:  isErr,
		Output:   output,
		CostUSD:  cost,
	})
	if err != nil {
		return fmt.Errorf("agent: marshal turn_completed: %w", err)
	}
	if _, err := s.events.EnqueueEvent(ctx, t.ProjectID, EventTurnCompleted, t.IdempotencyKey, payload); err != nil {
		return fmt.Errorf("agent: enqueue turn_completed: %w", err)
	}
	return nil
}

// update persists one machine step, logging (not returning) store errors — the
// poller retries the row on its next sweep.
func (s *Service) update(ctx context.Context, t Turn) {
	if err := s.store.Update(ctx, t); err != nil {
		slog.ErrorContext(ctx, "agent: persist turn", "key", t.IdempotencyKey, "err", err)
	}
}

// reconcile sweeps every project's pool (05 §4, 11 §3): for each project resolve
// its provider + prefix and adopt-first reconcile that project's slots against
// that provider alone. A project whose provider cannot be resolved (e.g. a
// missing credential) is logged and skipped — the others keep reconciling
// (spec §6 failure isolation).
func (s *Service) reconcile(ctx context.Context) {
	pids, err := s.projects.ProjectIDs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: list projects", "err", err)
		return
	}
	for _, pid := range pids {
		s.reconcileProject(ctx, pid)
	}
}

// reconcileProject is the adopt-first pool sweep for one project (05 §4): adopt
// every worker matching a slot, create only the missing ones, destroy orphaned
// prefix-matched entries. Scoped entirely to this project's own provider and
// worker-name prefix, so it never touches another project's workers (11 §3).
func (s *Service) reconcileProject(ctx context.Context, projectID string) {
	provider, prefix, err := s.providers.For(ctx, projectID)
	if err != nil {
		slog.WarnContext(ctx, "agent: resolve provider for project; skipping reconcile",
			"project", projectID, "err", err)
		return
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: list workers", "project", projectID, "err", err)
		return
	}
	ids, err := s.slots.WorkerIDs(ctx, projectID)
	if err != nil {
		slog.ErrorContext(ctx, "agent: read worker slots", "project", projectID, "err", err)
		return
	}
	wanted := wantedNames(prefix, ids)
	failed := s.adoptAndCreate(ctx, provider, wanted, live)
	s.destroyOrphans(ctx, provider, prefix, wanted, live)
	s.recordProvisionFailures(projectID, prefix, failed)
}

// adoptAndCreate adopts every wanted worker already live and creates the rest on
// the given provider, returning the names whose CreateWorker failed this sweep —
// slots with no live sandbox, which the health reconcile must gate out of the
// pull until they provision.
func (s *Service) adoptAndCreate(
	ctx context.Context, provider Provider, wanted map[string]struct{}, live []ProviderWorker,
) []string {
	byName := indexByName(live)
	var failed []string
	for name := range wanted {
		if w, ok := byName[name]; ok {
			s.putWorker(w)
			continue
		}
		w, err := provider.CreateWorker(ctx, name)
		if err != nil {
			// Log the wrapped err (a backend may scrub it — a provider message can
			// echo a rejected secret) plus the provider's scrub-safe status/code/trace
			// so the failure stays diagnosable even when err reads "[Filtered]".
			slog.ErrorContext(ctx, "agent: create worker",
				append([]any{"worker", name, "err", err}, providerErrAttrs(err)...)...)
			failed = append(failed, name)
			continue
		}
		s.putWorker(w)
	}
	return failed
}

// providerErrAttrs returns scrub-safe slog attributes for a provider error that
// carries structured diagnostics (ProviderErrorFields); nil for a plain error.
// Kept separate from the wrapped err attr: the status/code/trace never carry
// secret values, so they survive a log backend that filters the free-text err.
func providerErrAttrs(err error) []any {
	var pe ProviderErrorFields
	if !errors.As(err, &pe) {
		return nil
	}
	status, code, trace := pe.ProviderErrorFields()
	return []any{"provider_status", status, "provider_error_code", code, "provider_trace", trace}
}

// recordProvisionFailures replaces the project's provisioning-failure set with
// the worker ids behind failedNames (empty clears it). A full replace per sweep,
// so a slot that provisions on a later sweep — or is no longer wanted — drops out
// automatically and the liveness loop stops reporting it errored.
func (s *Service) recordProvisionFailures(projectID, prefix string, failedNames []string) {
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()
	if len(failedNames) == 0 {
		delete(s.provisionErrs, projectID)
		return
	}
	ids := make(map[string]struct{}, len(failedNames))
	for _, name := range failedNames {
		ids[strings.TrimPrefix(name, prefix)] = struct{}{}
	}
	s.provisionErrs[projectID] = ids
}

// provisionFailedIDs returns the worker ids whose sandbox failed to provision on
// the last sweep for the project — the slots the health reconcile must add to the
// errored set even though no live sandbox exists to observe.
func (s *Service) provisionFailedIDs(projectID string) []string {
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()
	ids := s.provisionErrs[projectID]
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out
}

// destroyOrphans removes live prefix-matched entries that match no slot (05 §4).
// The prefix is this project's own, so the sweep never touches another project's
// (or another environment's) workers (11 §3).
func (s *Service) destroyOrphans(
	ctx context.Context, provider Provider, prefix string,
	wanted map[string]struct{}, live []ProviderWorker,
) {
	for _, w := range live {
		if !strings.HasPrefix(w.Name, prefix) {
			continue
		}
		if _, ok := wanted[w.Name]; ok {
			continue
		}
		if err := provider.DestroyWorker(ctx, w); err != nil {
			slog.ErrorContext(ctx, "agent: destroy orphan worker", "worker", w.Name, "err", err)
			continue
		}
		s.deleteWorker(w.Name)
	}
}

// ensureWorker returns the cached provider worker for a slot, creating it on the
// given provider if the cache has none yet.
func (s *Service) ensureWorker(
	ctx context.Context, provider Provider, prefix, workerID string,
) (ProviderWorker, error) {
	name := workerName(prefix, workerID)
	if w, ok := s.getWorker(name); ok {
		return w, nil
	}
	w, err := provider.CreateWorker(ctx, name)
	if err != nil {
		return ProviderWorker{}, fmt.Errorf("agent: create worker %q: %w", name, err)
	}
	s.putWorker(w)
	return w, nil
}

func (s *Service) getWorker(name string) (ProviderWorker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[name]
	return w, ok
}

func (s *Service) putWorker(w ProviderWorker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[w.Name] = w
}

func (s *Service) deleteWorker(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workers, name)
}

// lookupWorker returns the cached worker, or a name-only handle when the cache
// has none (enough for a destroy, which treats an absent worker as success).
func (s *Service) lookupWorker(name string) ProviderWorker {
	if w, ok := s.getWorker(name); ok {
		return w
	}
	return ProviderWorker{Name: name}
}

// resolveWorker returns the cached provider worker for a name, falling back to a
// list-and-match on the given provider (never creating one — this is a read
// path). A zero ProviderWorker means "not live", handled by the caller as an
// empty update.
func (s *Service) resolveWorker(ctx context.Context, provider Provider, name string) (ProviderWorker, error) {
	if w, ok := s.getWorker(name); ok {
		return w, nil
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		return ProviderWorker{}, fmt.Errorf("agent: list workers: %w", err)
	}
	for _, w := range live {
		if w.Name == name {
			s.putWorker(w)
			return w, nil
		}
	}
	return ProviderWorker{}, nil
}

// isRunning reports whether a send machine's phase means a turn is in flight
// (05 §5) — everything before the two resting/terminal phases.
func isRunning(p Phase) bool { return p != PhaseDone && p != PhaseFailed }

// errMissingTurnRef guards the impossible turn_started-without-a-ref case.
var errMissingTurnRef = errors.New("agent: turn started without a provider turn ref")

// failureOutput is the human description carried by an error-turn event.
func failureOutput(t Turn) string {
	if t.LastError != "" {
		return t.LastError
	}
	return "agent turn failed"
}

// workerName derives the deterministic provider-side name for a board worker
// slot under a given prefix (05 §4, D5, 11 §3).
func workerName(prefix, workerID string) string { return prefix + workerID }

// wantedNames maps board slot ids to their deterministic provider-worker names
// under a given prefix.
func wantedNames(prefix string, ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[workerName(prefix, id)] = struct{}{}
	}
	return out
}

// indexByName keys live workers by their provider-side name.
func indexByName(ws []ProviderWorker) map[string]ProviderWorker {
	out := make(map[string]ProviderWorker, len(ws))
	for _, w := range ws {
		out[w.Name] = w
	}
	return out
}
