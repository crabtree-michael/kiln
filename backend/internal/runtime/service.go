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
type AgentRuntime interface {
	Send(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
	Release(ctx context.Context, projectID string, idempotencyKey int64, payload []byte) error
}

// Outbox topic names (04 §2) as the runtime routes them — carried in
// Entry.Kind on the outbox queue. They mirror board's Topic values by value;
// this module never imports internal/board (the same layering rule the board
// and brain modules state in the other direction).
const (
	topicAgentSend    = "agent.send"
	topicAgentRelease = "agent.release"
	topicNotifySend   = "notify.send"
	topicPullEvaluate = "pull.evaluate"
	topicBoardUpdated = "board.updated"
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
// five it routes — a contract violation by whoever appended it, surfaced as a
// retryable handler error rather than a silent drop.
var errUnknownTopic = errors.New("runtime: unknown outbox topic")

// Service is the runtime's core: EnqueueEvent for the two ingestion callers
// (04 §6), the drain that routes claimed entries to the executor ports above,
// and — until the split finishes — a one-line forward for every method that has
// already moved onto a focused unit. Constructed at the composition root
// (04 §8). After step 5 the only ports it still holds itself are the five the
// drain uses; the rest belong to the five values below.
type Service struct {
	store   Store
	brains  BrainResolver
	puller  Puller
	blocker Blocker
	agents  AgentRuntime

	// notify is the extracted push choke point (god-unit split step 1): it owns
	// the Notifier and Owner ports outright, and Service now delegates to it
	// rather than holding them. Its one caller left here is the notify.send route
	// (the feed-update push moved to FanOut in step 5, which reaches the same
	// value).
	notify *Notify

	// feed is the extracted feed assembler (god-unit split step 2): it owns the
	// BoardReader outright and reads the notification store through its read half,
	// and Service now delegates its two feed reads to it. Its callers left here
	// are the api-facing Feed/FeedHistory shims (the feed.updated re-render moved
	// to FanOut in step 5, which reaches the same value).
	feed *Feed

	// notifs is the extracted notification CRUD facade (god-unit split step 3):
	// it owns the transactional feed mutations, and Service's eight
	// notification methods are now one-line forwards onto it.
	notifs *Notifications

	// transcript is the extracted conversation surface (god-unit split step 4):
	// it owns the MessageStore and SayPusher ports, and Service delegates
	// PostMessage/Say/Recent to it. Service is also its Nudger for now — it still
	// holds the events worker — so the ingest→nudge edge routes back here through
	// NudgeEvents until Dispatcher takes the worker in step 6.
	transcript *Transcript

	// fanout is the extracted push coordinator (god-unit split step 5): it owns
	// the three pushers and the completion card's NotificationWriter outright, and
	// Service now routes the four UI topics and the thinking bracket to it. With
	// this, every port Service still holds belongs to the durable drain — what is
	// left of it is Dispatcher (step 6).
	fanout *FanOut

	// The two workers Workers() builds, retained so anything that commits a
	// queue row can nudge the matching worker (04 §5). nil until Workers runs.
	eventsWorker *Worker
	outboxWorker *Worker
}

// NewService assembles the runtime over its ports. The 08 §7 ports
// (notifications, boardReader, feedPusher, activityPusher) are appended after
// the original 04/07 ports, and the 11 §3 owner port after those, so the
// composition root updates a single call site.
//
// None of the ports below are held directly any more — every one of them now
// belongs to an extracted unit that Service delegates to: notifier/owner to
// Notify (every push), boardReader to Feed (every feed read), notifications to
// Notifications (every feed mutation), messages/sayer to Transcript (every
// conversation operation), and pusher/feedPusher/activityPusher to FanOut (every
// SSE fan-out and UI topic). What Service still holds itself is the durable
// drain's own five. The signature is unchanged so the composition root and the
// existing tests keep working while the rest of the split lands
// (docs/god-units-plans/runtime-service.md §8).
func NewService(
	store Store, messages MessageStore, brains BrainResolver, puller Puller, blocker Blocker,
	agents AgentRuntime, notifier Notifier,
	pusher SnapshotPusher, sayer SayPusher,
	notifications NotificationStore, boardReader BoardReader, feedPusher FeedPusher,
	activityPusher ActivityPusher,
	owner Owner,
) *Service {
	// Notify and Feed are built first: FanOut fans out *from* them, so they are
	// its collaborators as well as Service's own delegates.
	notify := NewNotify(notifier, owner)
	feed := NewFeed(boardReader, notifications)
	s := &Service{
		store:      store,
		brains:     brains,
		puller:     puller,
		blocker:    blocker,
		agents:     agents,
		notify:     notify,
		feed:       feed,
		notifs:     NewNotifications(notifications),
		transcript: NewTranscript(messages, sayer),
		fanout:     NewFanOut(pusher, feedPusher, activityPusher, notifications, feed, notify),
	}
	// Service still owns the events worker, so it is the Transcript's Nudger
	// (§4 of the split plan). Step 6 hands both the worker and this role to
	// Dispatcher; the wiring line moves with them.
	s.transcript.SetNudger(s)
	return s
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
func (s *Service) EnqueueEvent(
	ctx context.Context, projectID string, t EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	id, err := s.store.InsertEvent(ctx, projectID, t, idempotencyKey, payload)
	if err != nil {
		return 0, fmt.Errorf("runtime: enqueue event: %w", err)
	}
	// A deduped redelivery (id 0) still nudges — harmless, and it keeps the
	// wakeup path uniform; the events worker just finds nothing new to claim.
	s.NudgeEvents()
	return id, nil
}

// The three conversation methods below delegate to the extracted Transcript
// (transcript_service.go) so *Service keeps satisfying api's MessagePoster and
// MessagesReader, and the brain's Say and ConversationReader, while the split
// lands.

// PostMessage delegates to the Transcript — POST /api/message's transactional
// append+enqueue, which nudges the events worker back through Service (07 §3–§4).
func (s *Service) PostMessage(ctx context.Context, projectID, text string) (int64, int64, error) {
	return s.transcript.PostMessage(ctx, projectID, text)
}

// Say delegates to the Transcript — the append-then-push kiln reply (07 §3, §6).
func (s *Service) Say(ctx context.Context, projectID, text string) error {
	return s.transcript.Say(ctx, projectID, text)
}

// Recent delegates to the Transcript — the oldest-first tail behind
// GET /api/messages and the brain's ConversationReader (07 §3).
func (s *Service) Recent(ctx context.Context, projectID string, n int) ([]Message, error) {
	return s.transcript.Recent(ctx, projectID, n)
}

// Workers builds the two serial workers (04 §3–§4): the events worker over
// the Brain port, and the outbox worker routing per-topic to the executor
// ports, each with its dead-letter action. The returned pair is
// (eventsWorker, outboxWorker); both are also retained on the Service so
// EnqueueEvent — and the Transcript's ingest, through NudgeEvents — can nudge
// the events worker (04 §5).
func (s *Service) Workers(clock Clock) (*Worker, *Worker) {
	events := NewWorker(s.store, QueueEvents, s.handleEvent, s.deadLetterEvent, clock)
	outbox := NewWorker(s.store, QueueOutbox, s.handleOutbox, s.deadLetterOutbox, clock)
	s.eventsWorker = events
	s.outboxWorker = outbox
	return events, outbox
}

// Feed delegates to the extracted assembler (feed_assembler.go) so *Service
// keeps satisfying api's FeedReader while the split lands.
func (s *Service) Feed(ctx context.Context, projectID string) (FeedSnapshot, error) {
	return s.feed.Feed(ctx, projectID)
}

// FeedHistory delegates to the extracted assembler (feed_assembler.go), the
// keyset-paged sibling of Feed.
func (s *Service) FeedHistory(
	ctx context.Context, projectID string, before int64, limit int,
) ([]FeedCard, bool, error) {
	return s.feed.FeedHistory(ctx, projectID, before, limit)
}

// The eight notification methods below delegate to the extracted CRUD facade
// (notification_service.go) so *Service keeps satisfying api's FeedMutator, the
// brain's NotificationStore/FeedReader and the steward's Feed while the split
// lands.

// PostNotification delegates to the CRUD facade — the brain's post_update /
// preview (08 §3, 06 tool set).
func (s *Service) PostNotification(
	ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
) error {
	return s.notifs.PostNotification(ctx, projectID, kind, body, ticketID, imageURL)
}

// PostPoke delegates to the CRUD facade — the steward's feed-only poke card.
func (s *Service) PostPoke(ctx context.Context, projectID, ticketID string) error {
	return s.notifs.PostPoke(ctx, projectID, ticketID)
}

// RetractNotification delegates to the CRUD facade — the brain's retract_update
// (08 §3).
func (s *Service) RetractNotification(ctx context.Context, projectID string, id int64) error {
	return s.notifs.RetractNotification(ctx, projectID, id)
}

// DismissNotification delegates to the CRUD facade — the user's per-card swipe
// (POST /api/feed/{id}/dismiss, 08 §3).
func (s *Service) DismissNotification(ctx context.Context, projectID string, id int64) error {
	return s.notifs.DismissNotification(ctx, projectID, id)
}

// DismissAllNotifications delegates to the CRUD facade — the user's clear-all
// (POST /api/feed/dismiss-all, 08 §3).
func (s *Service) DismissAllNotifications(ctx context.Context, projectID string) error {
	return s.notifs.DismissAllNotifications(ctx, projectID)
}

// EditNotification delegates to the CRUD facade — the brain's edit_update
// (08 §3 amended, 06 tool set).
func (s *Service) EditNotification(
	ctx context.Context, projectID string, id int64, kind, body string, imageURL *string,
) error {
	return s.notifs.EditNotification(ctx, projectID, id, kind, body, imageURL)
}

// ListNotifications delegates to the CRUD facade — the brain's list_updates
// (06 tool set).
func (s *Service) ListNotifications(ctx context.Context, projectID string) ([]Notification, error) {
	return s.notifs.ListNotifications(ctx, projectID)
}

// MarkSeen delegates to the CRUD facade — the client's seen high-water mark
// (POST /api/feed/seen, 08 §3).
func (s *Service) MarkSeen(ctx context.Context, projectID string, lastID int64) error {
	return s.notifs.MarkSeen(ctx, projectID, lastID)
}

// NudgeEvents wakes the events worker if it has been built (04 §5). No-op
// before Workers runs, so ingestion still works (the poll fallback catches
// the row) during startup. Exported because Service is the Transcript's Nudger
// while it still owns the worker (split plan §4); the role moves to Dispatcher
// with the worker in step 6.
func (s *Service) NudgeEvents() {
	if s.eventsWorker != nil {
		s.eventsWorker.Nudge()
	}
}

// handleEvent is the events worker's handler: one brain pass per queued event
// (04 §4, §6), typed from the raw Entry. The brain is resolved per event from
// the entry's project (11 §3) — a resolution failure is that project's
// configuration problem, so it is surfaced as a system-error Say on that
// project and the event is marked done (return nil ⇒ MarkDone; no retry
// storm), leaving every other project's events flowing.
func (s *Service) handleEvent(ctx context.Context, e Entry) error {
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
	s.fanout.PushThinking(ctx, e.ProjectID, true)
	defer s.fanout.PushThinking(ctx, e.ProjectID, false)
	// Trace the brain pass as one span (design 2026-07-05); no-op when Sentry is
	// disabled. Description carries the event type so traces group by trigger.
	ctx, finish := obs.StartSpan(ctx, "brain.dispatch", e.Kind)
	defer finish()
	brain, err := s.brains.For(ctx, e.ProjectID)
	if err != nil {
		// Deliberately NOT a handler error: retrying cannot fix a project whose
		// brain won't resolve (missing/broken credentials), and 8 spaced retries
		// would just re-burn the failure. Same surface-to-the-user shape as
		// deadLetterEvent, then swallow so the worker marks the entry done.
		slog.ErrorContext(ctx, "runtime.event.brain_unresolved",
			"event_id", e.ID, "project_id", e.ProjectID, "event_type", e.Kind, "err", err)
		if sayErr := s.transcript.Say(ctx, e.ProjectID, brainUnavailableMessage); sayErr != nil {
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
func (s *Service) deadLetterEvent(ctx context.Context, e Entry, cause error) error {
	slog.Error("runtime: event dead-lettered", "id", e.ID, "project_id", e.ProjectID, "type", e.Kind, "err", cause)
	if err := s.transcript.Say(ctx, e.ProjectID, systemErrorMessage); err != nil {
		return fmt.Errorf("runtime: dead-letter say: %w", err)
	}
	return nil
}

// handleOutbox is the outbox worker's handler: route the topic (Entry.Kind)
// to its executor (04 §2). Every executor reads the claimed entry's ProjectID
// and threads it into its port call (11 §3), so the side effect lands on the
// emitting tenant. The outbox id travels as the idempotency key for
// agent.send/agent.release (04 §3, 05 §7).
func (s *Service) handleOutbox(ctx context.Context, e Entry) error {
	// Trace each outbox delivery as one span keyed on the topic (design
	// 2026-07-05); no-op when Sentry is disabled.
	ctx, finish := obs.StartSpan(ctx, "outbox.deliver", e.Kind)
	defer finish()
	switch e.Kind {
	case topicAgentSend:
		return wrapOutbox("agent send", s.agents.Send(ctx, e.ProjectID, e.ID, e.Payload))
	case topicAgentRelease:
		return wrapOutbox("agent release", s.agents.Release(ctx, e.ProjectID, e.ID, e.Payload))
	case topicPullEvaluate:
		return wrapOutbox("run pull", s.puller.RunPull(ctx, e.ProjectID))
	case topicNotifySend:
		return wrapOutbox("notify send", s.handleNotifySend(ctx, e))
	case topicBoardUpdated:
		return wrapOutbox("push board", s.fanout.PushBoard(ctx, e.ProjectID))
	case topicFeedUpdated:
		s.fanout.HandleFeedUpdated(ctx, e)
		return nil
	case topicActivityToast:
		s.fanout.HandleActivityToast(ctx, e)
		return nil
	case topicFeedCompletion:
		return wrapOutbox("post completion card", s.fanout.HandleFeedCompletion(ctx, e))
	default:
		return fmt.Errorf("%w %q", errUnknownTopic, e.Kind)
	}
}

// handleNotifySend routes a board-emitted notify.send (blocked/started/done
// milestone) to the push choke point: decode the transition Kind, then hand the
// payload to Notify, which applies the owner's mode gate (02 §10). A malformed
// payload is a contract violation by the emitter, surfaced as a retryable error.
func (s *Service) handleNotifySend(ctx context.Context, e Entry) error {
	var p notifyPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("decode notify payload: %w", err)
	}
	return s.notify.Send(ctx, e.ProjectID, p.Kind, e.Payload)
}

// deadLetterOutbox handles an exhausted outbox entry per the 04 §3 table:
// agent.send blocks the ticket; every other topic logs and drops (it either
// self-heals or is benign) — only agent.send touches the Blocker port.
func (s *Service) deadLetterOutbox(ctx context.Context, e Entry, cause error) error {
	if e.Kind == topicAgentSend {
		return s.blockOnDeliveryFailure(ctx, e, cause)
	}
	slog.Error("runtime: outbox entry dead-lettered", "id", e.ID, "topic", e.Kind, "err", cause)
	return nil
}

// blockOnDeliveryFailure realizes the agent.send dead-letter row (04 §3, 03
// §7.3): unmarshal the ticket id out of the otherwise-opaque outbox payload
// and mark it Blocked with the delivery failure as the reason, so the failure
// surfaces on the ticket and pulls the user in.
func (s *Service) blockOnDeliveryFailure(ctx context.Context, e Entry, cause error) error {
	var p struct {
		TicketID string `json:"ticket_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("runtime: dead-letter agent.send: decode ticket id: %w", err)
	}
	reason := fmt.Sprintf("delivery failure: %v", cause)
	if err := s.blocker.MarkBlocked(ctx, e.ProjectID, p.TicketID, reason); err != nil {
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
