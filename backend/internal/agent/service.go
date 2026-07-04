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

	mu      sync.Mutex
	workers map[string]ProviderWorker // provider-worker cache keyed by name
}

// NewService assembles the agent runtime over its ports.
func NewService(store Store, provider Provider, events EventEnqueuer, slots Slots, clock Clock) *Service {
	return &Service{
		store:    store,
		provider: provider,
		events:   events,
		slots:    slots,
		clock:    clock,
		workers:  map[string]ProviderWorker{},
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
	var p SendPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("agent: decode send payload: %w", err)
	}
	ctx = obs.WithTurn(ctx, deliveryTurn(idempotencyKey))
	turn := Turn{
		IdempotencyKey: idempotencyKey,
		Kind:           KindSend,
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
		WorkerID:       p.WorkerID,
		Phase:          PhaseRecorded,
	}
	slog.InfoContext(ctx, "agent.release.recorded", "idem_key", idempotencyKey, "worker_id", p.WorkerID)
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
	wg.Wait()
	return nil
}

// markContinuation stamps turn with the prior conversation handle when this
// worker's newest operation was a send that already opened one — that is how
// first-message-vs-continuation is derived (05 §2.1, §3): no row, or a release
// row, leaves ProviderTurn nil and the next StartTurn goes fresh.
func (s *Service) markContinuation(ctx context.Context, turn *Turn) {
	prev, found, err := s.store.LatestForWorker(ctx, turn.WorkerID)
	if err != nil || !found {
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

// advance dispatches one machine step by operation kind (05 §5). It stamps the
// context with this delivery's turn id so every step it drives (start, check,
// completed) shares one correlation id across the async poller.
func (s *Service) advance(ctx context.Context, t Turn) {
	ctx = obs.WithTurn(ctx, deliveryTurn(t.IdempotencyKey))
	switch t.Kind {
	case KindSend:
		s.advanceSend(ctx, t)
	case KindRelease:
		s.advanceRelease(ctx, t)
	default:
		slog.WarnContext(ctx, "agent: unknown turn kind", "kind", t.Kind)
	}
}

// advanceSend steps one send machine (05 §5):
// recorded → worker_ready → turn_started → done, with failed owing the event.
func (s *Service) advanceSend(ctx context.Context, t Turn) {
	switch t.Phase {
	case PhaseRecorded:
		s.stepEnsureReady(ctx, t)
	case PhaseWorkerReady:
		s.stepStartTurn(ctx, t)
	case PhaseTurnStarted:
		s.stepCheckTurn(ctx, t)
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
func (s *Service) advanceRelease(ctx context.Context, t Turn) {
	name := WorkerName(t.WorkerID)
	if err := s.provider.DestroyWorker(ctx, s.lookupWorker(name)); err != nil {
		slog.WarnContext(ctx, "agent: release destroy", "worker", name, "err", err)
	}
	if nw, err := s.provider.CreateWorker(ctx, name); err != nil {
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
func (s *Service) stepEnsureReady(ctx context.Context, t Turn) {
	w, err := s.ensureWorker(ctx, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	ready, err := s.provider.WorkerReady(ctx, w)
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
func (s *Service) stepStartTurn(ctx context.Context, t Turn) {
	w, err := s.ensureWorker(ctx, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	fresh := t.ProviderTurn == nil || t.ProviderTurn.Conversation == ""
	conversation := ""
	if !fresh {
		conversation = t.ProviderTurn.Conversation
	}
	ref, err := s.provider.StartTurn(ctx, w, conversation, t.Message, fresh)
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
func (s *Service) stepCheckTurn(ctx context.Context, t Turn) {
	w, err := s.ensureWorker(ctx, t.WorkerID)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	if t.ProviderTurn == nil {
		s.recordFailure(ctx, t, errMissingTurnRef)
		return
	}
	st, err := s.provider.CheckTurn(ctx, w, *t.ProviderTurn)
	if err != nil {
		s.recordFailure(ctx, t, err)
		return
	}
	if st.Running {
		return
	}
	s.emitCompleted(ctx, t, st.IsError, st.Output, st.CostUSD)
	t.Phase = PhaseDone
	s.update(ctx, t)
}

// stepEmitFailure fires the error-shaped event a failed machine owes, then
// rests it at done (05 §5: failed → done).
func (s *Service) stepEmitFailure(ctx context.Context, t Turn) {
	s.emitCompleted(ctx, t, true, failureOutput(t), 0)
	t.Phase = PhaseDone
	s.update(ctx, t)
}

// recordFailure books one retry; exhausting the budget moves the machine to
// failed (05 §5, 04 §3).
func (s *Service) recordFailure(ctx context.Context, t Turn, cause error) {
	t.Attempts++
	t.LastError = cause.Error()
	if t.Attempts >= maxAttempts {
		t.Phase = PhaseFailed
	}
	s.update(ctx, t)
}

// emitCompleted enqueues one agent.turn_completed event (05 §2.2). No provider
// handles leak into the payload.
func (s *Service) emitCompleted(ctx context.Context, t Turn, isErr bool, output string, cost float64) {
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
		slog.ErrorContext(ctx, "agent: marshal turn_completed", "err", err)
		return
	}
	if _, err := s.events.EnqueueEvent(ctx, EventTurnCompleted, payload); err != nil {
		slog.ErrorContext(ctx, "agent: enqueue turn_completed", "err", err)
	}
}

// update persists one machine step, logging (not returning) store errors — the
// poller retries the row on its next sweep.
func (s *Service) update(ctx context.Context, t Turn) {
	if err := s.store.Update(ctx, t); err != nil {
		slog.ErrorContext(ctx, "agent: persist turn", "key", t.IdempotencyKey, "err", err)
	}
}

// reconcile is the adopt-first pool sweep (05 §4): adopt every worker matching
// a slot, create only the missing ones, destroy orphaned kiln-worker-* entries.
func (s *Service) reconcile(ctx context.Context) {
	live, err := s.provider.ListWorkers(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: list workers", "err", err)
		return
	}
	ids, err := s.slots.WorkerIDs(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "agent: read worker slots", "err", err)
		return
	}
	wanted := wantedNames(ids)
	s.adoptAndCreate(ctx, wanted, live)
	s.destroyOrphans(ctx, wanted, live)
}

// adoptAndCreate adopts every wanted worker already live and creates the rest.
func (s *Service) adoptAndCreate(ctx context.Context, wanted map[string]struct{}, live []ProviderWorker) {
	byName := indexByName(live)
	for name := range wanted {
		if w, ok := byName[name]; ok {
			s.putWorker(w)
			continue
		}
		w, err := s.provider.CreateWorker(ctx, name)
		if err != nil {
			slog.ErrorContext(ctx, "agent: create worker", "worker", name, "err", err)
			continue
		}
		s.putWorker(w)
	}
}

// destroyOrphans removes live kiln-worker-* entries that match no slot (05 §4).
func (s *Service) destroyOrphans(ctx context.Context, wanted map[string]struct{}, live []ProviderWorker) {
	for _, w := range live {
		if !strings.HasPrefix(w.Name, WorkerNamePrefix) {
			continue
		}
		if _, ok := wanted[w.Name]; ok {
			continue
		}
		if err := s.provider.DestroyWorker(ctx, w); err != nil {
			slog.ErrorContext(ctx, "agent: destroy orphan worker", "worker", w.Name, "err", err)
			continue
		}
		s.deleteWorker(w.Name)
	}
}

// ensureWorker returns the cached provider worker for a slot, creating it if
// the cache has none yet.
func (s *Service) ensureWorker(ctx context.Context, workerID string) (ProviderWorker, error) {
	name := WorkerName(workerID)
	if w, ok := s.getWorker(name); ok {
		return w, nil
	}
	w, err := s.provider.CreateWorker(ctx, name)
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

// errMissingTurnRef guards the impossible turn_started-without-a-ref case.
var errMissingTurnRef = errors.New("agent: turn started without a provider turn ref")

// failureOutput is the human description carried by an error-turn event.
func failureOutput(t Turn) string {
	if t.LastError != "" {
		return t.LastError
	}
	return "agent turn failed"
}

// wantedNames maps board slot ids to their deterministic provider-worker names.
func wantedNames(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[WorkerName(id)] = struct{}{}
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
