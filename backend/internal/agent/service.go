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

	// creditsMu guards creditsOut: the projects whose provider last rejected a
	// call for exhausted account credits (ErrOutOfCredits). Read by OutOfCredits,
	// which the api joins onto the board snapshot as a persistent alert.
	//
	// It exists because credit exhaustion is the one failure that stops the whole
	// project without anything on screen saying so: every turn fails fast and
	// terminally (recordFailure) and every worker create fails, so the board fills
	// with errored slots and the user is left reading "N of M sandboxes failing"
	// with no hint that the answer is a payment. It stalled production for hours
	// once, and nobody knew why until someone read the logs.
	//
	// Set from the two places a provider rejection actually surfaces — a failed
	// turn and a failed provision — and CLEARED only by a reconcile sweep that
	// got through its provider calls without one. Clearing on that heartbeat
	// rather than on any success is deliberate: the sweep runs every 60s and
	// always talks to the provider, so a topped-up account puts the band away by
	// itself, while a project nobody is running work on does not silently drop an
	// alert that is still true. Held only for the map read/write, never across a
	// provider call.
	creditsMu  sync.Mutex
	creditsOut map[string]struct{}
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
		creditsOut:    map[string]struct{}{},
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
	// Board slots let a gen≥1 short name (which carries only the slot fragment) be
	// mapped back to its full board slot id; best-effort, so a nil/erroring Slots
	// port just falls back to the parsed remainder (gen-0 names are the full id
	// already). Read once per call, outside the per-worker loop.
	slotIDs := s.slotIDsFor(ctx, projectID)
	out := make([]AgentInfo, 0, len(live))
	for _, w := range live {
		workerID := slotIDForName(prefix, w.Name, slotIDs)
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
	w, err := s.resolveSlotWorker(ctx, provider, prefix, workerID)
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

// SaveWorkerSnapshot captures the workspace behind one worker slot as a new
// named snapshot (05 §4, §6) — the executor behind board's agent.snapshot, and
// the saved-sandbox counterpart to Release. Where Release destroys the slot's
// sandbox and recreates it empty, this freezes it into the provider's snapshot
// catalog, so what the ticket's agent installed, cloned, built and authenticated
// outlives the box it lived in and can become the image later workers start from.
//
// It resolves the project's provider and its OPTIONAL snapshot catalog
// (SandboxCatalogOf — the leak-free read); a provider with no catalog is
// ErrNoCatalog, a slot with no live sandbox is ErrNoLiveWorker. Both are
// terminal facts, not transient failures: there is nothing to capture and
// retrying cannot change that, so the caller reports them and stops.
//
// Idempotent on the name, which is what makes it safe on an at-least-once
// outbox (04 §3). The provider API has no idempotency key, so a redelivery
// would otherwise start a second capture of the same workspace; instead an
// existing snapshot under this name is returned as the capture that already
// ran. The name must therefore be DERIVED, not freshly generated per attempt —
// board stamps the emit-time instant into the payload for exactly this reason.
// A catalog read that fails is logged and the capture proceeds: losing the
// workspace is the worse outcome, and a duplicate snapshot is only clutter.
//
// The capture CONSUMES its source. The provider scrubs the box's injected
// secrets and deletes it (the only safe mode — a Kiln worker holds the owner's
// git credential and the project's secrets, and this image is about to become
// the base every future worker starts from), so the slot's cached worker is
// dropped on the way out and the reconciler re-provisions it on its next sweep —
// the same heal advanceRelease leans on. The capture runs in the background, so
// the returned Snapshot is typically still SnapshotCapturing.
func (s *Service) SaveWorkerSnapshot(
	ctx context.Context, projectID, workerID, name, description string,
) (Snapshot, error) {
	provider, prefix, err := s.providers.For(ctx, projectID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("agent: resolve provider for project %q: %w", projectID, err)
	}
	catalog, ok := SandboxCatalogOf(provider)
	if !ok {
		return Snapshot{}, ErrNoCatalog
	}
	if prior, found := s.capturedAs(ctx, catalog, name); found {
		slog.InfoContext(ctx, "agent.snapshot.already_captured",
			"project_id", projectID, "worker_id", workerID, "name", name, "state", prior.State)
		return prior, nil
	}
	w, err := s.resolveSlotWorker(ctx, provider, prefix, workerID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("agent: save worker snapshot: %w", err)
	}
	if w.Ref == "" {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrNoLiveWorker, workerID)
	}
	snap, err := catalog.SaveSnapshot(ctx, SaveSnapshotRequest{
		DevBoxRef: w.Ref, Name: name, Description: description,
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("agent: save worker snapshot %q: %w", workerID, err)
	}
	slog.InfoContext(ctx, "agent.snapshot.captured",
		"project_id", projectID, "worker_id", workerID, "worker", w.Name,
		"name", name, "ref", snap.Ref, "state", snap.State)
	s.deleteWorker(w.Name)
	return snap, nil
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

// OutOfCredits reports whether this project's provider is currently rejecting
// calls for exhausted account credits (05 §5). Read by the api on every board
// join to raise the persistent alert band, so it is a cached observation rather
// than a provider call — there is nothing here to fail, and asking the provider
// on each board read would put a billing round-trip in front of every snapshot.
// See the creditsOut field for what sets and clears it.
func (s *Service) OutOfCredits(projectID string) bool {
	s.creditsMu.Lock()
	defer s.creditsMu.Unlock()
	_, out := s.creditsOut[projectID]
	return out
}

// capturedAs looks the catalog up for a snapshot already captured under name —
// SaveWorkerSnapshot's redelivery guard. A read failure is not a "no": it is
// unknown, so it is logged and reported as not-found, which re-captures rather
// than skips (see SaveWorkerSnapshot on why that is the right way to be wrong).
func (s *Service) capturedAs(ctx context.Context, catalog SandboxCatalog, name string) (Snapshot, bool) {
	snaps, err := catalog.ListSnapshots(ctx)
	if err != nil {
		slog.WarnContext(ctx, "agent: list snapshots for capture guard; capturing anyway",
			"name", name, "err", err)
		return Snapshot{}, false
	}
	for _, snap := range snaps {
		if snap.Name == name {
			return snap, true
		}
	}
	return Snapshot{}, false
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
	gen := 0
	if w, ok := s.slotWorker(prefix, t.WorkerID); ok {
		if _, g, pok := parseWorkerName(prefix, w.Name); pok {
			gen = g
		}
		if err := provider.DestroyWorker(ctx, w); err != nil {
			slog.WarnContext(ctx, "agent: release destroy", "worker", w.Name, "err", err)
		}
		s.deleteWorker(w.Name)
	}
	// Recreate for a fresh workspace at the same generation; a name still squatted
	// by the just-destroyed sandbox's VM (auto_delete off — D6) rotates to the next
	// generation via ErrNameConflict rather than dead-lettering the slot.
	if nw, err := s.createWorkerRotating(ctx, provider, prefix, t.WorkerID, gen); err != nil {
		slog.WarnContext(ctx, "agent: release recreate; reconciler will heal",
			"worker_id", t.WorkerID, "err", err)
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
		// The turn's own message tells whoever reads this ticket. The alert tells
		// everyone else — the next ticket fails the same way, and the one after it.
		s.noteOutOfCredits(ctx, t.ProjectID, cause)
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
	failed, creditErr := s.adoptAndCreate(ctx, provider, prefix, ids, live)
	s.recordProvisionFailures(projectID, failed)
	// This sweep is the credit alert's heartbeat: it just made real provider calls
	// for this project, so it is in a position to say either way. A sweep that hit
	// the rejection raises it; one that got through clears it, which is what puts
	// the band away on its own once the account is topped up.
	if creditErr != nil {
		s.noteOutOfCredits(ctx, projectID, creditErr)
		return
	}
	s.clearOutOfCredits(projectID)
}

// adoptAndCreate reconciles one project's live sandboxes against its board slots
// (05 §4, generalised for generational names): each slot adopts the highest
// non-errored generation among its live sandboxes, or — when none is adoptable —
// creates the next generation under a fresh name (routing around a squatting
// failed record, D6). Every prefix-scoped sandbox it did NOT adopt (a lower/stale
// generation, a terminally-failed record, or an orphan matching no slot) is
// destroyed. Returns the slot ids whose create failed this sweep, which the health
// reconcile gates out of the pull until they provision, plus the first
// credit-exhaustion rejection among those failures — a project-wide fact rather
// than a slot's, so the caller raises it as an alert rather than gating a slot on
// it. The sweep still runs to the end on one: the destroy pass is the part that
// keeps a shared provider account tidy, and it is not the part credits stop.
func (s *Service) adoptAndCreate(
	ctx context.Context, provider Provider, prefix string, ids []string, live []ProviderWorker,
) ([]string, error) {
	// Group live, prefix-scoped sandboxes by board slot (board-slot-driven, not
	// exact-equality on the parsed remainder): a gen≥1 name carries only the slot
	// fragment, so slotCandidates matches each candidate against the slot's id AND
	// its fragment via slotMatches. A foreign-prefix name parses to ok=false and is
	// left untouched (11 §3).
	kept := make(map[string]struct{}, len(ids))
	var failed []string
	var creditErr error
	for _, id := range ids {
		w, err := s.adoptOrCreateSlot(ctx, provider, prefix, id, slotCandidates(prefix, id, live))
		if err != nil {
			// Log the wrapped err (a backend may scrub it — a provider message can
			// echo a rejected secret) plus the provider's scrub-safe status/code/trace
			// so the failure stays diagnosable even when err reads "[Filtered]".
			slog.ErrorContext(ctx, "agent: create worker",
				append([]any{"worker_id", id, "err", err}, providerErrAttrs(err)...)...)
			failed = append(failed, id)
			if creditErr == nil && errors.Is(err, ErrOutOfCredits) {
				creditErr = err
			}
			continue
		}
		kept[w.Name] = struct{}{}
	}
	s.destroyUnkept(ctx, provider, prefix, live, kept)
	return failed, creditErr
}

// adoptOrCreateSlot resolves the one provider worker a slot should use from its
// live sandboxes (05 §4): adopt the highest-generation sandbox that is not
// terminally errored, else create the next generation under a fresh name past the
// highest generation seen — so a squatting failed gen-N record never blocks the
// slot, because gen N+1 gets a name nothing holds. Shared by the reconciler and
// the turn machine's ensureWorker so both settle on the same current generation.
func (s *Service) adoptOrCreateSlot(
	ctx context.Context, provider Provider, prefix, workerID string, candidates []ProviderWorker,
) (ProviderWorker, error) {
	adopt, maxGen := pickAdoptable(prefix, candidates)
	if adopt != nil {
		s.putWorker(*adopt)
		return *adopt, nil
	}
	newGen := maxGen + 1
	if newGen >= 1 {
		// No adoptable sandbox but a higher generation already exists — we are healing
		// a wedge by rebuilding the slot past a squatting/failed record (the
		// amika-sandbox-name-conflict fix). Log the rotation with scrub-safe fields
		// only (slot id + generation; the name is derived, no secrets).
		slog.InfoContext(ctx, "agent: rotating slot to next generation past unadoptable record",
			"worker_id", workerID, "new_gen", newGen)
	}
	w, err := s.createWorkerRotating(ctx, provider, prefix, workerID, newGen)
	if err != nil {
		return ProviderWorker{}, err
	}
	s.putWorker(w)
	return w, nil
}

// pickAdoptable selects a slot's adoptable sandbox — the highest-generation one
// whose liveness is not terminally RunErrored — and reports the highest generation
// seen across ALL of the slot's sandboxes (errored included) so the caller can
// create past a squatting failed record. The returned worker is nil when the slot
// has no non-errored sandbox; maxGen is -1 when it has none at all.
func pickAdoptable(prefix string, candidates []ProviderWorker) (*ProviderWorker, int) {
	var adopt *ProviderWorker
	maxGen, bestGen := -1, -1
	for i := range candidates {
		_, gen, ok := parseWorkerName(prefix, candidates[i].Name)
		if !ok {
			continue
		}
		if gen > maxGen {
			maxGen = gen
		}
		if candidates[i].Status == RunErrored {
			continue // a terminally-failed record is never adopted (route around it)
		}
		if gen > bestGen {
			bestGen, adopt = gen, &candidates[i]
		}
	}
	return adopt, maxGen
}

// nameRotateAttempts bounds how many generations the create path tries when the
// provider rejects a name as already in use (ErrNameConflict) — the
// belt-and-suspenders for the destroy→recreate race where the prior record has
// left the live list but its sandbox VM still squats the deterministic name
// (auto_delete off — D6). After the bound the create fails and the slot is
// recorded errored / sandbox_health fires, exactly as a provision failure does
// today.
const nameRotateAttempts = 5

// createWorkerRotating creates the slot's sandbox starting at startGen, advancing
// one generation (a fresh name) each time CreateWorker returns ErrNameConflict, up
// to nameRotateAttempts. A non-conflict error returns immediately (the existing
// provision-failure handling owns it). Each rotation logs the slot id and the new
// generation — scrub-safe fields only, no secrets.
func (s *Service) createWorkerRotating(
	ctx context.Context, provider Provider, prefix, workerID string, startGen int,
) (ProviderWorker, error) {
	if startGen < 0 {
		startGen = 0
	}
	gen := startGen
	var lastErr error
	for range nameRotateAttempts {
		w, err := provider.CreateWorker(ctx, workerName(prefix, workerID, gen))
		if err == nil {
			return w, nil
		}
		lastErr = err
		if !errors.Is(err, ErrNameConflict) {
			return ProviderWorker{}, fmt.Errorf("agent: create worker for slot %q: %w", workerID, err)
		}
		slog.WarnContext(ctx, "agent: worker name conflict; rotating to next generation",
			"worker_id", workerID, "gen", gen, "next_gen", gen+1)
		gen++
	}
	return ProviderWorker{}, fmt.Errorf("agent: create worker for slot %q exhausted name rotation: %w", workerID, lastErr)
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

// recordProvisionFailures replaces the project's provisioning-failure set with the
// slot ids whose create failed this sweep (empty clears it). A full replace per
// sweep, so a slot that provisions on a later sweep — or is no longer wanted —
// drops out automatically and the liveness loop stops reporting it errored.
func (s *Service) recordProvisionFailures(projectID string, failedIDs []string) {
	s.provisionMu.Lock()
	defer s.provisionMu.Unlock()
	if len(failedIDs) == 0 {
		delete(s.provisionErrs, projectID)
		return
	}
	ids := make(map[string]struct{}, len(failedIDs))
	for _, id := range failedIDs {
		ids[id] = struct{}{}
	}
	s.provisionErrs[projectID] = ids
}

// noteOutOfCredits records that this project's provider rejected a call for
// exhausted account credits, so the api can raise a persistent alert naming the
// cause. Provider-neutral: it keys off the ErrOutOfCredits sentinel every adapter
// maps its own billing rejection to (05 §5), so nothing about a platform's
// envelope reaches this side of the port. A nil or unrelated error is NOT a
// clear — see clearOutOfCredits for why the reconcile sweep owns that.
func (s *Service) noteOutOfCredits(ctx context.Context, projectID string, err error) {
	if err == nil || !errors.Is(err, ErrOutOfCredits) {
		return
	}
	s.creditsMu.Lock()
	defer s.creditsMu.Unlock()
	if _, already := s.creditsOut[projectID]; !already {
		slog.WarnContext(ctx, "agent: provider out of credits; raising alert for project",
			"project", projectID)
	}
	s.creditsOut[projectID] = struct{}{}
}

// clearOutOfCredits drops the project's credit alert. Called by a reconcile sweep
// that reached the provider without a credit rejection — the account is paying
// again, so the band goes away without the user having to do anything but top up.
func (s *Service) clearOutOfCredits(projectID string) {
	s.creditsMu.Lock()
	defer s.creditsMu.Unlock()
	delete(s.creditsOut, projectID)
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

// destroyUnkept destroys every prefix-scoped live sandbox the sweep did not adopt
// (05 §4): a slot's lower/stale generations, its terminally-failed records, and
// orphans matching no slot at all. Prefix-scoped, so it never touches another
// project's (or environment's) workers (11 §3). Best-effort — a destroy failure is
// logged and the sweep continues; a 404 is success (DestroyWorker semantics).
func (s *Service) destroyUnkept(
	ctx context.Context, provider Provider, prefix string,
	live []ProviderWorker, kept map[string]struct{},
) {
	for _, w := range live {
		if !strings.HasPrefix(w.Name, prefix) {
			continue
		}
		if _, ok := kept[w.Name]; ok {
			continue
		}
		if err := provider.DestroyWorker(ctx, w); err != nil {
			slog.ErrorContext(ctx, "agent: destroy stale/orphan worker", "worker", w.Name, "err", err)
			continue
		}
		s.deleteWorker(w.Name)
	}
}

// ensureWorker returns the provider worker a slot should use, reading the current
// generation from the cache the reconciler populates; on a cache miss (a turn
// racing the first sweep, or a slot whose earlier provision failed) it re-derives
// it from the live list — adopting the highest non-errored generation or creating a
// fresh one, rotating past a squatting name (05 §4).
func (s *Service) ensureWorker(
	ctx context.Context, provider Provider, prefix, workerID string,
) (ProviderWorker, error) {
	if w, ok := s.slotWorker(prefix, workerID); ok {
		return w, nil
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		return ProviderWorker{}, fmt.Errorf("agent: ensure worker %q: list: %w", workerID, err)
	}
	w, err := s.adoptOrCreateSlot(ctx, provider, prefix, workerID, slotCandidates(prefix, workerID, live))
	if err != nil {
		return ProviderWorker{}, fmt.Errorf("agent: ensure worker %q: %w", workerID, err)
	}
	return w, nil
}

// slotWorker returns the cached provider worker for a slot: the highest-generation
// cached entry whose name parses to workerID under prefix. The reconciler
// populates the cache, so the turn machine reads the generation the last sweep
// settled on rather than recomputing it.
func (s *Service) slotWorker(prefix, workerID string) (ProviderWorker, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best ProviderWorker
	bestGen := -1
	for name, w := range s.workers {
		if rem, gen, ok := parseWorkerName(prefix, name); ok && slotMatches(workerID, rem) && gen > bestGen {
			bestGen, best = gen, w
		}
	}
	return best, bestGen >= 0
}

// slotCandidates filters a live worker list to the sandboxes belonging to one slot
// (parsed under prefix) — the input pickAdoptable ranks by generation.
func slotCandidates(prefix, workerID string, live []ProviderWorker) []ProviderWorker {
	var out []ProviderWorker
	for _, w := range live {
		if rem, _, ok := parseWorkerName(prefix, w.Name); ok && slotMatches(workerID, rem) {
			out = append(out, w)
		}
	}
	return out
}

// slotIDsFor reads the project's board slot ids, best-effort: a nil Slots port
// (test wiring that does not exercise it) or a read error yields nil, and the
// caller falls back to the parsed name remainder. Used only to resolve a gen≥1
// short name (fragment) back to its full board slot id on the read path.
func (s *Service) slotIDsFor(ctx context.Context, projectID string) []string {
	if s.slots == nil {
		return nil
	}
	ids, err := s.slots.WorkerIDs(ctx, projectID)
	if err != nil {
		slog.WarnContext(ctx, "agent: read worker slots for name resolution; falling back to parsed name",
			"project", projectID, "err", err)
		return nil
	}
	return ids
}

// slotIDForName resolves a provider worker name back to its board slot id. A
// gen-0 name's remainder is the full id already; a gen≥1 name's remainder is the
// slot fragment, so it is matched against slotIDs via slotMatches. A name matching
// no known slot (or a foreign prefix) falls back to the parsed remainder — the
// pre-generation trim behaviour.
func slotIDForName(prefix, name string, slotIDs []string) string {
	rem, _, ok := parseWorkerName(prefix, name)
	if !ok {
		return strings.TrimPrefix(name, prefix)
	}
	for _, id := range slotIDs {
		if slotMatches(id, rem) {
			return id
		}
	}
	return rem
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

// resolveSlotWorker returns the current provider worker for a slot on a read path
// (never creating one): the cached generation, else the highest non-errored
// generation from a live list-and-match. A zero ProviderWorker means "not live",
// handled by the caller as an empty update.
func (s *Service) resolveSlotWorker(
	ctx context.Context, provider Provider, prefix, workerID string,
) (ProviderWorker, error) {
	if w, ok := s.slotWorker(prefix, workerID); ok {
		return w, nil
	}
	live, err := provider.ListWorkers(ctx)
	if err != nil {
		return ProviderWorker{}, fmt.Errorf("agent: list workers: %w", err)
	}
	if adopt, _ := pickAdoptable(prefix, slotCandidates(prefix, workerID, live)); adopt != nil {
		s.putWorker(*adopt)
		return *adopt, nil
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
