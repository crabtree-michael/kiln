package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/crabtree-michael/kiln/backend/internal/obs"
)

// eventPayloadSummaryBytes bounds how much of an event's raw payload is logged
// at ingest — enough to eyeball a human message or turn-completed shape without
// carrying a full (possibly truncated-elsewhere) agent output.
const eventPayloadSummaryBytes = 1024

// Brain is the runtime's port onto the decision step (02 §6): one call per
// event, invoked with per-project serialization by the events dispatcher
// (04 §4, 11 §3). A replayed pass re-reads fresh board state; the Board API's
// strict preconditions (03 D8) stop a half-applied first run from
// double-applying.
type Brain interface {
	HandleEvent(ctx context.Context, ev Event) error
}

// BrainResolver resolves the Brain for one project per event (11 §3): each
// tenant runs the brain over its own credentials/config, so the events
// dispatcher asks for the project's brain at dispatch time rather than
// holding a single global one. Resolution failure is a per-project
// configuration problem, not a queue problem — handleEvent surfaces it as a
// system-error Say on that project and marks the event done (no retry storm),
// leaving other projects' events untouched.
type BrainResolver interface {
	For(ctx context.Context, projectID string) (Brain, error)
}

// Puller is the runtime's port onto the board's deterministic pull, the
// pull.evaluate executor (03 §5, 04 §2), scoped to one project's board
// (11 §3). Idempotent by construction.
type Puller interface {
	RunPull(ctx context.Context, projectID string) error
}

// Blocker is the runtime's port onto the board's mechanical failure path
// (03 §7.3): dead-lettered agent.send entries surface on the ticket as
// Blocked with the delivery failure as reason. projectID scopes the lookup so
// a ticket id can never be blocked across tenants (11 §3).
type Blocker interface {
	MarkBlocked(ctx context.Context, projectID, ticketID, reason string) error
}

// AgentRuntime executes agent.* outbox entries (05 §2.1) — the
// provider-neutral contract onto agent platforms. projectID is the claimed
// entry's tenant (11 §3); the agent module threads it into its turn Record so
// agent_turns.project_id is stamped. The outbox id travels as the idempotency
// key; the module (and its mock provider) must deduplicate on it (04 §3,
// 05 §7). Calls record-and-return; they never block on provisioning or a turn
// (05 D2).
//
// Snapshot is the exception to record-and-return: it captures the workspace
// behind a slot as a reusable base image (the saved-sandbox exit from
// Developing, 05 §4, §6) and DOES call the provider inline, because the capture
// request is one call and the outbox's own retry/dead-letter policy is the right
// durability for it — there is no turn to progress afterwards. Its payload is
// board's SnapshotPayload; it must be safe to redeliver (04 §3).
type AgentRuntime interface {
	Send(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
	Release(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
	Snapshot(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
}

// Outbox topic names (04 §2) as the runtime routes them — carried in
// Entry.Kind on the outbox queue. They mirror board's Topic values by value;
// this module never imports internal/board (the same layering rule the board
// and brain modules state in the other direction).
const (
	topicAgentSend    = "agent.send"
	topicAgentRelease = "agent.release"
	// agent.snapshot is agent.release's saved-sandbox counterpart (05 §4, §6):
	// capture the slot's workspace as a reusable base image rather than recycle
	// it. Board-emitted by both exits from Developing on a ticket whose sandbox
	// is saved.
	topicAgentSnapshot = "agent.snapshot"
	topicNotifySend    = "notify.send"
	topicPullEvaluate  = "pull.evaluate"
	topicBoardUpdated  = "board.updated"
	// feed.updated / activity.toast are the 08 §7 additions. feed.updated is
	// emitted by both the board (state transitions) and the runtime itself
	// (notification post/retract/seen — the second outbox writer); either way
	// the runtime re-assembles the feed and fans it out. activity.toast is
	// board-emitted and carries a ToastPayload.
	topicFeedUpdated   = "feed.updated"
	topicActivityToast = "activity.toast"
	// feed.completion is board-emitted by AcceptToDone and carries a
	// CompletionPayload. The runtime posts the persistent "done" feed card,
	// deduped on the outbox id — the deterministic replacement for the brain
	// remembering to post a completion update.
	topicFeedCompletion = "feed.completion"
)

// systemErrorMessage is the user-visible reply when a brain pass exhausts its
// retries (04 §3's last dead-letter row): the ticket keeps its state and the
// user is pulled in rather than left waiting (07 §8 — the chat panel is the
// v1 notification surface).
const systemErrorMessage = "Kiln hit a system error handling that. I've left the board unchanged; please try again."

// brainUnavailableMessage is the user-visible reply when the project's brain
// cannot be resolved at all (11 §3 — e.g. missing or invalid model
// credentials): unlike a failed pass, retrying cannot help, so the event is
// surfaced once and marked done rather than retried.
const brainUnavailableMessage = "Kiln couldn't start its brain for this project — most likely a settings problem " +
	"(model credentials). The board is unchanged; please check the project's settings and try again."

// errUnknownTopic is returned by the outbox handler for a topic outside the
// nine it routes — a contract violation by whoever appended it, surfaced as a
// retryable handler error rather than a silent drop.
var errUnknownTopic = errors.New("runtime: unknown outbox topic")

// Dispatcher is the runtime's queue core — the deploy-resumable drain (04 §5)
// and the module's spine. It owns both halves of the durable path: EnqueueEvent,
// the ingest the two 04 §6 callers reach, and Workers, which builds the two
// serial workers that claim from the events and outbox queues and route each
// claimed entry to its executor.
//
// The discipline that runs through it is at-least-once durability, the opposite
// of FanOut's best-effort visibility. A handler that returns an error is retried
// with backoff and, once exhausted, hands the entry to a dead-letter action
// (04 §3); returning nil marks it done. So every route here is deliberate about
// which of those it wants, and the two places that swallow rather than retry say
// why: a brain that will not resolve is a settings problem eight spaced retries
// cannot fix, and the two log-and-drop UI topics own their own failures inside
// FanOut. Everything else is wrapped and returned.
//
// What it does NOT do any more is implement the work it routes to. The feed,
// notification, transcript and push responsibilities each moved to a focused
// unit; the three it still talks to are collaborators, reached through their own
// types rather than through shared ports:
//
//   - Transcript, for the two user-visible replies the drain itself emits (the
//     dead-letter system error and the brain-unresolved notice);
//   - Notify, for the board-emitted notify.send milestone, which never sees a
//     raw Notifier — Send is where the 02 §10 mode gate lives;
//   - FanOut, for the four UI topics and the thinking bracket around a pass.
//
// It is also the Transcript's Nudger (split plan §4): it holds the events
// worker, so an ingest that lands through the conversation surface wakes the
// same worker EnqueueEvent does.
type Dispatcher struct {
	store   Store
	brains  BrainResolver
	puller  Puller
	blocker Blocker
	agents  AgentRuntime

	transcript *Transcript
	notify     *Notify
	fanout     *FanOut

	// The two workers Workers() builds, retained so anything that commits a
	// queue row can nudge the matching worker (04 §5). nil until Workers runs.
	eventsWorker *Worker
	outboxWorker *Worker
}

// NewDispatcher wires the queue core over its five executor ports and the three
// units it routes to. The ports come first in 04 §2 order (the store the drain
// claims from, then the executors), the collaborators after.
func NewDispatcher(
	store Store, brains BrainResolver, puller Puller, blocker Blocker, agents AgentRuntime,
	transcript *Transcript, notify *Notify, fanout *FanOut,
) *Dispatcher {
	return &Dispatcher{
		store: store, brains: brains, puller: puller, blocker: blocker, agents: agents,
		transcript: transcript, notify: notify, fanout: fanout,
	}
}

// EnqueueEvent ingests one of the two 01 event types (04 §6): INSERT into
// events, stamped with the tenant project (11 §3), + nudge the events worker.
// Callers: the agent-runtime inbound handler (agent.turn_completed) and the
// message route (human.message). Payloads are opaque snapshots; shape
// contracts are the emitting surface's spec.
//
// idempotencyKey dedupes an at-least-once emitter (architecture audit 3.1): a
// non-zero key makes a redelivered event a no-op (returns id 0), which is how a
// crash-replayed agent completion avoids a duplicate brain pass. human.message
// passes 0 — its at-most-once emit needs no dedup.
func (d *Dispatcher) EnqueueEvent(
	ctx context.Context, projectID string, t EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	id, err := d.store.InsertEvent(ctx, projectID, t, idempotencyKey, payload)
	if err != nil {
		return 0, fmt.Errorf("runtime: enqueue event: %w", err)
	}
	// A deduped redelivery (id 0) still nudges — harmless, and it keeps the
	// wakeup path uniform; the events worker just finds nothing new to claim.
	d.NudgeEvents()
	return id, nil
}

// Workers builds the two serial workers (04 §3–§4): the events worker over
// the Brain port, and the outbox worker routing per-topic to the executor
// ports, each with its dead-letter action. The returned pair is
// (eventsWorker, outboxWorker); both are also retained on the Dispatcher so
// EnqueueEvent — and the Transcript's ingest, through NudgeEvents — can nudge
// the events worker (04 §5).
func (d *Dispatcher) Workers(clock Clock) (*Worker, *Worker) {
	events := NewWorker(d.store, QueueEvents, d.handleEvent, d.deadLetterEvent, clock)
	outbox := NewWorker(d.store, QueueOutbox, d.handleOutbox, d.deadLetterOutbox, clock)
	d.eventsWorker = events
	d.outboxWorker = outbox
	return events, outbox
}

// NudgeEvents wakes the events worker if it has been built (04 §5). No-op
// before Workers runs, so ingestion still works (the poll fallback catches
// the row) during startup. Exported because this type is the Transcript's
// Nudger (split plan §4) — it holds the worker, so the conversation surface's
// ingest wakes it through here.
func (d *Dispatcher) NudgeEvents() {
	if d.eventsWorker != nil {
		d.eventsWorker.Nudge()
	}
}

// handleEvent is the events worker's handler: one brain pass per queued event
// (04 §4, §6), typed from the raw Entry. The brain is resolved per event from
// the entry's project (11 §3) — a resolution failure is that project's
// configuration problem, so it is surfaced as a system-error Say on that
// project and the event is marked done (return nil ⇒ MarkDone; no retry
// storm), leaving every other project's events flowing.
func (d *Dispatcher) handleEvent(ctx context.Context, e Entry) error {
	// The event id is this turn's correlation anchor: it rides the context so
	// every log the brain pass emits — board mutations, transitions, says —
	// carries turn_id=evt-<id>, letting a full turn be reconstructed end-to-end
	// (trigger event → actions taken → result). Downstream agent deliveries the
	// pass triggers run asynchronously in their own turn ids; ticket_id links
	// the two sides.
	ctx = obs.WithTurn(ctx, fmt.Sprintf("evt-%d", e.ID))
	ev := Event{ID: e.ID, ProjectID: e.ProjectID, Type: EventType(e.Kind), Payload: e.Payload, CreatedAt: e.CreatedAt}
	slog.InfoContext(ctx, "runtime.event.received",
		"event_id", e.ID, "project_id", e.ProjectID, "event_type", e.Kind, "attempts", e.Attempts,
		"payload", obs.Summary(string(e.Payload), eventPayloadSummaryBytes))
	// Bracket the brain pass with a thinking activity event (08 §4): On=true
	// before, On=false after. This is the events queue only — the spinner
	// tracks a decision step, not an outbox delivery. A failed push must not
	// derail the brain pass, so activity errors are logged and dropped.
	d.fanout.PushThinking(ctx, e.ProjectID, true)
	defer d.fanout.PushThinking(ctx, e.ProjectID, false)
	// Trace the brain pass as one span (design 2026-07-05); no-op when Sentry is
	// disabled. Description carries the event type so traces group by trigger.
	ctx, finish := obs.StartSpan(ctx, "brain.dispatch", e.Kind)
	defer finish()
	brain, err := d.brains.For(ctx, e.ProjectID)
	if err != nil {
		// Deliberately NOT a handler error: retrying cannot fix a project whose
		// brain won't resolve (missing/broken credentials), and 8 spaced retries
		// would just re-burn the failure. Same surface-to-the-user shape as
		// deadLetterEvent, then swallow so the worker marks the entry done.
		slog.ErrorContext(ctx, "runtime.event.brain_unresolved",
			"event_id", e.ID, "project_id", e.ProjectID, "event_type", e.Kind, "err", err)
		if sayErr := d.transcript.Say(ctx, e.ProjectID, brainUnavailableMessage); sayErr != nil {
			slog.ErrorContext(ctx, "runtime: brain-unresolved say", "project_id", e.ProjectID, "err", sayErr)
		}
		return nil
	}
	if err := brain.HandleEvent(ctx, ev); err != nil {
		slog.ErrorContext(ctx, "runtime.event.failed", "event_id", e.ID, "event_type", e.Kind, "err", err)
		return fmt.Errorf("runtime: brain pass for event %d: %w", e.ID, err)
	}
	slog.InfoContext(ctx, "runtime.event.handled", "event_id", e.ID, "event_type", e.Kind)
	return nil
}

// deadLetterEvent handles an exhausted event (04 §3's last row): log at error
// level and surface a system-error reply to the event's project, so the
// ticket keeps its state and nobody is left waiting silently.
func (d *Dispatcher) deadLetterEvent(ctx context.Context, e Entry, cause error) error {
	slog.Error("runtime: event dead-lettered", "id", e.ID, "project_id", e.ProjectID, "type", e.Kind, "err", cause)
	if err := d.transcript.Say(ctx, e.ProjectID, systemErrorMessage); err != nil {
		return fmt.Errorf("runtime: dead-letter say: %w", err)
	}
	return nil
}

// handleOutbox is the outbox worker's handler: route the topic (Entry.Kind)
// to its executor (04 §2). Every executor reads the claimed entry's ProjectID
// and threads it into its port call (11 §3), so the side effect lands on the
// emitting tenant. The outbox id travels as the idempotency key for the agent.*
// topics (04 §3, 05 §7), which route through routeAgent.
func (d *Dispatcher) handleOutbox(ctx context.Context, e Entry) error {
	// Trace each outbox delivery as one span keyed on the topic (design
	// 2026-07-05); no-op when Sentry is disabled.
	ctx, finish := obs.StartSpan(ctx, "outbox.deliver", e.Kind)
	defer finish()
	if routed, err := d.routeAgent(ctx, e); routed {
		return err
	}
	switch e.Kind {
	case topicPullEvaluate:
		return wrapOutbox("run pull", d.puller.RunPull(ctx, e.ProjectID))
	case topicNotifySend:
		return wrapOutbox("notify send", d.handleNotifySend(ctx, e))
	case topicBoardUpdated:
		return wrapOutbox("push board", d.fanout.PushBoard(ctx, e.ProjectID))
	case topicFeedUpdated:
		d.fanout.HandleFeedUpdated(ctx, e)
		return nil
	case topicActivityToast:
		d.fanout.HandleActivityToast(ctx, e)
		return nil
	case topicFeedCompletion:
		return wrapOutbox("post completion card", d.fanout.HandleFeedCompletion(ctx, e))
	default:
		return fmt.Errorf("%w %q", errUnknownTopic, e.Kind)
	}
}

// routeAgent handles the three agent.* topics — the AgentRuntime port's own
// slice of the routing table (05 §2.1), split out of handleOutbox so the two
// groups read separately: these all hand the entry's project, outbox id and raw
// payload straight to the agent module, while everything else in handleOutbox
// decodes or fans out. routed=false means the topic is not one of these and the
// caller keeps looking.
func (d *Dispatcher) routeAgent(ctx context.Context, e Entry) (bool, error) {
	switch e.Kind {
	case topicAgentSend:
		return true, wrapOutbox("agent send", d.agents.Send(ctx, e.ProjectID, e.ID, e.Payload))
	case topicAgentRelease:
		return true, wrapOutbox("agent release", d.agents.Release(ctx, e.ProjectID, e.ID, e.Payload))
	case topicAgentSnapshot:
		return true, wrapOutbox("agent snapshot", d.agents.Snapshot(ctx, e.ProjectID, e.ID, e.Payload))
	default:
		return false, nil
	}
}

// handleNotifySend routes a board-emitted notify.send (blocked/started/done
// milestone) to the push choke point: decode the transition Kind, then hand the
// payload to Notify, which applies the owner's mode gate (02 §10). A malformed
// payload is a contract violation by the emitter, surfaced as a retryable error.
func (d *Dispatcher) handleNotifySend(ctx context.Context, e Entry) error {
	var p notifyPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("decode notify payload: %w", err)
	}
	return d.notify.Send(ctx, e.ProjectID, p.Kind, e.Payload)
}

// deadLetterOutbox handles an exhausted outbox entry per the 04 §3 table:
// agent.send blocks the ticket; every other topic logs and drops (it either
// self-heals or is benign) — only agent.send touches the Blocker port.
func (d *Dispatcher) deadLetterOutbox(ctx context.Context, e Entry, cause error) error {
	if e.Kind == topicAgentSend {
		return d.blockOnDeliveryFailure(ctx, e, cause)
	}
	slog.Error("runtime: outbox entry dead-lettered", "id", e.ID, "topic", e.Kind, "err", cause)
	return nil
}

// blockOnDeliveryFailure realizes the agent.send dead-letter row (04 §3, 03
// §7.3): unmarshal the ticket id out of the otherwise-opaque outbox payload
// and mark it Blocked with the delivery failure as the reason, so the failure
// surfaces on the ticket and pulls the user in.
func (d *Dispatcher) blockOnDeliveryFailure(ctx context.Context, e Entry, cause error) error {
	var p struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("runtime: dead-letter agent.send: decode ticket id: %w", err)
	}
	reason := fmt.Sprintf("delivery failure: %v", cause)
	if err := d.blocker.MarkBlocked(ctx, e.ProjectID, p.TicketID, reason); err != nil {
		return fmt.Errorf("runtime: dead-letter agent.send: mark blocked: %w", err)
	}
	slog.Error("runtime: agent.send dead-lettered, ticket blocked",
		"id", e.ID, "project_id", e.ProjectID, "ticket", p.TicketID, "err", cause)
	return nil
}

// wrapOutbox annotates an executor error with the operation name, satisfying
// the wrap-external-errors rule while keeping each route in handleOutbox a
// single line.
func wrapOutbox(op string, err error) error {
	if err != nil {
		return fmt.Errorf("runtime: outbox %s: %w", op, err)
	}
	return nil
}
