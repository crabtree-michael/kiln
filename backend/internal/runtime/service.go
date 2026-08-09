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

// SnapshotPusher executes board.updated entries: fan out a fresh full board
// snapshot to the project's connected clients (04 §7, 11 §3; implemented by
// the api SSE hub). Snapshots are absolute, so duplicates are harmless
// (04 D7).
type SnapshotPusher interface {
	PushBoard(ctx context.Context, projectID string) error
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
// (04 §6), the transcript operations of 07 §3 (PostMessage, Say, Recent),
// and the wiring that routes claimed entries to the ports above. Constructed
// at the composition root (04 §8).
type Service struct {
	store    Store
	messages MessageStore
	brains   BrainResolver
	puller   Puller
	blocker  Blocker
	agents   AgentRuntime
	pusher   SnapshotPusher
	sayer    SayPusher
	// notifications is narrowed to the write half: the read paths moved to Feed
	// (step 2) and the CRUD to Notifications (step 3), leaving handleFeedCompletion's
	// PostCompletionCard as the one notification write Service still performs
	// itself. It goes to FanOut in step 5, and the port leaves with it.
	notifications  NotificationWriter
	feedPusher     FeedPusher
	activityPusher ActivityPusher

	// notify is the extracted push choke point (god-unit split step 1): it owns
	// the Notifier and Owner ports outright, and Service now delegates to it
	// rather than holding them. Both push callers left on Service — the
	// notify.send route and the feed-update push — go through this one value.
	notify *Notify

	// feed is the extracted feed assembler (god-unit split step 2): it owns the
	// BoardReader outright and reads the notification store through its read half,
	// and Service now delegates its two feed reads to it. Both assembly callers
	// left on Service — the api-facing Feed/FeedHistory and the feed.updated
	// re-render — go through this one value.
	feed *Feed

	// notifs is the extracted notification CRUD facade (god-unit split step 3):
	// it owns the transactional feed mutations, and Service's eight
	// notification methods are now one-line forwards onto it.
	notifs *Notifications

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
// notifier and owner are no longer held directly: they are handed to the
// extracted Notify, which Service delegates every push to; boardReader likewise
// goes to the extracted Feed, which Service delegates every feed read to, and
// notifications to the extracted Notifications, which Service delegates every
// feed mutation to. The signature is unchanged so the composition root and the
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
	return &Service{
		store:          store,
		messages:       messages,
		brains:         brains,
		puller:         puller,
		blocker:        blocker,
		agents:         agents,
		pusher:         pusher,
		sayer:          sayer,
		notifications:  notifications,
		feedPusher:     feedPusher,
		activityPusher: activityPusher,
		notify:         NewNotify(notifier, owner),
		feed:           NewFeed(boardReader, notifications),
		notifs:         NewNotifications(notifications),
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
func (s *Service) EnqueueEvent(
	ctx context.Context, projectID string, t EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	id, err := s.store.InsertEvent(ctx, projectID, t, idempotencyKey, payload)
	if err != nil {
		return 0, fmt.Errorf("runtime: enqueue event: %w", err)
	}
	// A deduped redelivery (id 0) still nudges — harmless, and it keeps the
	// wakeup path uniform; the events worker just finds nothing new to claim.
	s.nudgeEvents()
	return id, nil
}

// PostMessage is the runtime's port for POST /api/message (07 §3–§4, api's
// MessagePoster): append the project's user transcript row and enqueue the
// human.message event {text} in one transaction (MessageStore's job), then
// nudge the events worker. Returns both ids for the 202 response
// ({event_id, message_id}); a failed append surfaces as an error with no
// invented, partial ids (07 §3 — the transcript and the queue cannot disagree).
func (s *Service) PostMessage(ctx context.Context, projectID, text string) (int64, int64, error) {
	messageID, eventID, err := s.messages.AppendUserMessageAndEnqueueEvent(ctx, projectID, text)
	if err != nil {
		return 0, 0, fmt.Errorf("runtime: post message: %w", err)
	}
	s.nudgeEvents()
	return messageID, eventID, nil
}

// Say is the runtime's Say port (07 §3, §6; also brain.Say, matched
// structurally with no adapter): append the project's kiln transcript row,
// then push a say SSE event ({message_id, text, at}) to that project via
// SayPusher. Append-then-push — a crash between them costs a live push, not
// history (07 §3), so the push only ever fires once the row is durable. Every
// user-visible reply goes through this, including the dead-letter
// system-error message.
func (s *Service) Say(ctx context.Context, projectID, text string) error {
	m, err := s.messages.AppendKilnMessage(ctx, projectID, text)
	if err != nil {
		return fmt.Errorf("runtime: say append: %w", err)
	}
	if err := s.sayer.PushSay(ctx, projectID, m); err != nil {
		return fmt.Errorf("runtime: say push: %w", err)
	}
	return nil
}

// Recent is the runtime's ConversationReader-shaped read (07 §3): the
// project's last n transcript rows, oldest first. Backs GET /api/messages
// (api's MessagesReader) directly, and the brain's ConversationReader port
// through a composition-root adapter (brain.Message is a distinct type —
// 06 §3.2).
func (s *Service) Recent(ctx context.Context, projectID string, n int) ([]Message, error) {
	msgs, err := s.messages.Recent(ctx, projectID, n)
	if err != nil {
		return nil, fmt.Errorf("runtime: recent: %w", err)
	}
	return msgs, nil
}

// Workers builds the two serial workers (04 §3–§4): the events worker over
// the Brain port, and the outbox worker routing per-topic to the executor
// ports, each with its dead-letter action. The returned pair is
// (eventsWorker, outboxWorker); both are also retained on the Service so
// EnqueueEvent/PostMessage can nudge the events worker (04 §5).
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

// nudgeEvents wakes the events worker if it has been built (04 §5). No-op
// before Workers runs, so ingestion still works (the poll fallback catches
// the row) during startup.
func (s *Service) nudgeEvents() {
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
	s.pushThinking(ctx, e.ProjectID, true)
	defer s.pushThinking(ctx, e.ProjectID, false)
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
		if sayErr := s.Say(ctx, e.ProjectID, brainUnavailableMessage); sayErr != nil {
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

// pushThinking fans out a thinking activity event to the event's project,
// self-healing on error (08 §4): the spinner is ephemeral, so a lost push is
// cosmetic and must never fail the brain pass it brackets.
func (s *Service) pushThinking(ctx context.Context, projectID string, on bool) {
	if err := s.activityPusher.PushActivity(ctx, projectID, ActivityEvent{Kind: "thinking", On: &on}); err != nil {
		slog.Error("runtime: push thinking activity", "on", on, "err", err)
	}
}

// deadLetterEvent handles an exhausted event (04 §3's last row): log at error
// level and surface a system-error reply to the event's project, so the
// ticket keeps its state and nobody is left waiting silently.
func (s *Service) deadLetterEvent(ctx context.Context, e Entry, cause error) error {
	slog.Error("runtime: event dead-lettered", "id", e.ID, "project_id", e.ProjectID, "type", e.Kind, "err", cause)
	if err := s.Say(ctx, e.ProjectID, systemErrorMessage); err != nil {
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
		return wrapOutbox("push board", s.pusher.PushBoard(ctx, e.ProjectID))
	case topicFeedUpdated:
		s.handleFeedUpdated(ctx, e)
		return nil
	case topicActivityToast:
		s.handleActivityToast(ctx, e)
		return nil
	case topicFeedCompletion:
		return wrapOutbox("post completion card", s.handleFeedCompletion(ctx, e))
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

// notifyPayload is the notify.send payload the Notifier decodes (a
// board.NotifyPayload — Title/Reason → Title/Body), mirrored by value so this
// module keeps not importing internal/board. Kind names the transition (mirrors
// board.NotifyKind*) so the mode gate can decide delivery; a feed-update push
// built here carries notifyKindUpdate.
type notifyPayload struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
	Kind   string `json:"kind"`
}

// toastPayload is the activity.toast outbox payload (08 §4, §7), mirroring the
// board's ToastPayload by value — this module never imports internal/board.
type toastPayload struct {
	Verb        string `json:"verb"`
	TicketID    string `json:"ticket_id"`
	TicketTitle string `json:"ticket_title"`
}

// feedUpdatedPayload mirrors the board's FeedUpdatedPayload (03 §7.1) by value —
// this module never imports internal/board. Title names the changed ticket;
// Verb labels the nature of the change and drives the transition push copy (02
// §10). Empty when a feed.updated carries no descriptor (the update then stays
// silent — no verb is, by definition, not a state transition).
type feedUpdatedPayload struct {
	Title string `json:"title"`
	Verb  string `json:"verb"`
}

// completionPayload is the feed.completion outbox payload (08 §7), mirroring the
// board's CompletionPayload by value — this module never imports internal/board.
// GitHubURL/GitHubLabel are the link to the landed work rendered as the done
// card's second line; both empty when no link is available. Summary is the landed
// work's one-line description rendered as the card body; empty when unavailable.
type completionPayload struct {
	TicketID    string `json:"ticket_id"`
	TicketTitle string `json:"ticket_title"`
	GitHubURL   string `json:"github_url,omitempty"`
	GitHubLabel string `json:"github_label,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// handleFeedUpdated realizes the feed.updated topic (08 §3, §7): re-assemble
// the absolute feed and fan it out. Emitted by both the board (state
// transitions) and the runtime itself (notification mutations). Self-heals —
// a failed assembly or push logs-and-drops (like board.updated) rather than
// wedging the outbox, since the next emission re-renders from scratch.
func (s *Service) handleFeedUpdated(ctx context.Context, e Entry) {
	snap, err := s.feed.Feed(ctx, e.ProjectID)
	if err != nil {
		slog.Error("runtime: feed.updated assemble", "project_id", e.ProjectID, "err", err)
		return
	}
	if err := s.feedPusher.PushFeed(ctx, e.ProjectID, snap); err != nil {
		slog.Error("runtime: feed.updated push", "project_id", e.ProjectID, "err", err)
	}
	// Decode the change descriptor that drives the transition push. A decode
	// failure or an empty payload leaves p zero-valued, so the update stays silent
	// (no verb ⇒ not a state transition).
	var p feedUpdatedPayload
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			slog.Error("runtime: feed.updated decode", "id", e.ID, "err", err)
		}
	}
	s.pushFeedUpdateNotification(ctx, e.ProjectID, p)
}

// pushFeedUpdateNotification fires a Web Push describing a feed update — the
// broad "activity" stream (a new proposal, a queue, a reshape, a nudge, an
// archive) that only the "all" notification mode delivers (02 §10). The mode
// gate lives in Notify.Send: the push carries notifyKindUpdate, which
// notifyModeAllows admits only under "all" and drops under "default"/"blocked".
// The genuine milestones (blocked/started/done) do NOT ride this path — they
// each emit their own dedicated board notify.send with a milestone Kind, so
// their feed-update twins are suppressed here (verb "blocked"/"finished" absent
// from feedUpdateVerbBody) to avoid a duplicate push. Progress narration, edits,
// and mark-seen carry no verb and stay silent in every mode. The push names the
// ticket and what happened, so it reads at a glance rather than as a generic
// "board was updated". It routes to the right tenant (Notify resolves the owning
// user, 11 §3) and self-heals: a send failure logs-and-drops rather than
// wedging the outbox (best-effort, 04 §3).
func (s *Service) pushFeedUpdateNotification(ctx context.Context, projectID string, p feedUpdatedPayload) {
	note, ok := feedUpdateNotification(p)
	if !ok {
		return
	}
	note.Kind = notifyKindUpdate
	// The notifier decodes a board.NotifyPayload (Title/Reason → Title/Body);
	// this marshals the same shape by value so this module keeps not importing
	// internal/board.
	payload, err := json.Marshal(note)
	if err != nil {
		slog.Error("runtime: feed.updated notify marshal", "err", err)
		return
	}
	if err := s.notify.Send(ctx, projectID, notifyKindUpdate, payload); err != nil {
		slog.Error("runtime: feed.updated notify send", "project_id", projectID, "err", err)
	}
}

// feedUpdateVerbBody maps a feed.updated change verb (board.FeedUpdatedPayload)
// to the push body describing what happened, keeping the "all"-mode push copy in
// sync with the board's feed-update verbs (03 §7.1) and the feed's own verb
// vocabulary (08 §5). These are the broad "activity" pushes only "all" mode
// delivers (02 §10) — a new proposal, a queue, a reshape, a nudge, an archive.
// Deliberately absent, so they resolve to ok=false and never push from this
// path — each has a dedicated board notify.send carrying a milestone Kind, and a
// second, vaguer push here would duplicate it:
//   - "blocked":  MarkBlocked emits notify.send with the actual blocker question.
//   - "finished": AcceptToDone emits notify.send for the completion.
//
// An empty/unknown verb (progress narration, edits, mark-seen — the runtime's own
// signal-only feed.updated rows) is likewise absent and stays silent everywhere.
var feedUpdateVerbBody = map[string]string{
	"proposal": "New proposal",
	"reshaped": "Proposal updated",
	"queued":   "Queued for work",
	"nudged":   "Nudged",
	"archived": "Archived",
}

// feedUpdateNotification builds the "all"-mode push payload for a feed change,
// naming the ticket (Title) and what happened (Body). ok is false whenever the
// change carries no push copy — an unrecognized/empty verb (narration, edits,
// mark-seen), or a milestone verb (blocked/finished) whose dedicated notify.send
// already covers it. There is no generic "board was updated" fallback — a change
// with no descriptive verb is not something to notify about. Whether an ok push
// actually reaches the user is the mode gate's call (only "all" admits it).
func feedUpdateNotification(p feedUpdatedPayload) (notifyPayload, bool) {
	body, known := feedUpdateVerbBody[p.Verb]
	if !known || p.Title == "" {
		return notifyPayload{}, false
	}
	return notifyPayload{Title: p.Title, Reason: body}, true
}

// handleActivityToast realizes the activity.toast topic (08 §4, §7): decode
// the board-emitted verb + ticket title and fan out a toast activity event.
// Self-heals — a decode or push failure logs-and-drops (the toast is
// ephemeral, so a lost one is cosmetic).
func (s *Service) handleActivityToast(ctx context.Context, e Entry) {
	var p toastPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		slog.Error("runtime: activity.toast decode", "id", e.ID, "err", err)
		return
	}
	ev := ActivityEvent{Kind: "toast", Verb: p.Verb, TicketID: p.TicketID, TicketTitle: p.TicketTitle}
	if err := s.activityPusher.PushActivity(ctx, e.ProjectID, ev); err != nil {
		slog.Error("runtime: activity.toast push", "id", e.ID, "project_id", e.ProjectID, "err", err)
	}
}

// handleFeedCompletion realizes the feed.completion topic (08 §7): post the
// persistent "done" feed card for a completed ticket. Unlike the ephemeral
// toast, this card is durable, so a decode failure returns an error (the outbox
// retries) rather than logging-and-dropping. The post is idempotent on the
// outbox id (e.ID), so a redelivery is a safe no-op. The card is a "done" kind
// styled like a poke: notificationToCard renders the ticket title as the label,
// the client prefixes a ✅, and the body is empty — no prose.
func (s *Service) handleFeedCompletion(ctx context.Context, e Entry) error {
	var p completionPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("decode feed.completion: %w", err)
	}
	if _, err := s.notifications.PostCompletionCard(
		ctx, e.ProjectID, e.ID, p.TicketID, completionCardBody, p.GitHubURL, p.GitHubLabel, p.Summary,
	); err != nil {
		return fmt.Errorf("post completion card: %w", err)
	}
	return nil
}

// completionCardBody is the body of the auto-posted "done" feed card: empty.
// The card is a "done" kind, so the client renders it single-line like a poke —
// the ticket title as the label with a ✅ prefix and no description body.
const completionCardBody = ""

// wrapOutbox annotates an executor error with the operation name, satisfying
// the wrap-external-errors rule while keeping each route in handleOutbox a
// single line.
func wrapOutbox(op string, err error) error {
	if err != nil {
		return fmt.Errorf("runtime: outbox %s: %w", op, err)
	}
	return nil
}
