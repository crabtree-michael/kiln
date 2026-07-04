package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// Brain is the runtime's port onto the decision step (02 §6): one call per
// event, invoked serially by the events worker (04 §4). A replayed pass
// re-reads fresh board state; the Board API's strict preconditions (03 D8)
// stop a half-applied first run from double-applying.
type Brain interface {
	HandleEvent(ctx context.Context, ev Event) error
}

// Puller is the runtime's port onto the board's deterministic pull, the
// pull.evaluate executor (03 §5, 04 §2). Idempotent by construction.
type Puller interface {
	RunPull(ctx context.Context) error
}

// Blocker is the runtime's port onto the board's mechanical failure path
// (03 §7.3): dead-lettered agent.send entries surface on the ticket as
// Blocked with the delivery failure as reason.
type Blocker interface {
	MarkBlocked(ctx context.Context, ticketID, reason string) error
}

// AgentRuntime executes agent.* outbox entries (05 §2.1) — the
// provider-neutral contract onto agent platforms. The outbox id travels as
// the idempotency key; the module (and its mock provider) must deduplicate
// on it (04 §3, 05 §7). Calls record-and-return; they never block on
// provisioning or a turn (05 D2).
type AgentRuntime interface {
	Send(ctx context.Context, idempotencyKey int64, payload []byte) error
	Release(ctx context.Context, idempotencyKey int64, payload []byte) error
}

// Notifier executes notify.send entries (02 §10). A rare duplicate
// notification is accepted as benign (04 §3).
type Notifier interface {
	Send(ctx context.Context, payload []byte) error
}

// SnapshotPusher executes board.updated entries: fan out a fresh full board
// snapshot to every connected client (04 §7; implemented by the api SSE hub).
// Snapshots are absolute, so duplicates are harmless (04 D7).
type SnapshotPusher interface {
	PushBoard(ctx context.Context) error
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
)

// systemErrorMessage is the user-visible reply when a brain pass exhausts its
// retries (04 §3's last dead-letter row): the ticket keeps its state and the
// user is pulled in rather than left waiting (07 §8 — the chat panel is the
// v1 notification surface).
const systemErrorMessage = "Kiln hit a system error handling that. I've left the board unchanged; please try again."

// errUnknownTopic is returned by the outbox handler for a topic outside the
// five it routes — a contract violation by whoever appended it, surfaced as a
// retryable handler error rather than a silent drop.
var errUnknownTopic = errors.New("runtime: unknown outbox topic")

// Service is the runtime's core: EnqueueEvent for the two ingestion callers
// (04 §6), the transcript operations of 07 §3 (PostMessage, Say, Recent),
// and the wiring that routes claimed entries to the ports above. Constructed
// at the composition root (04 §8).
type Service struct {
	store          Store
	messages       MessageStore
	brain          Brain
	puller         Puller
	blocker        Blocker
	agents         AgentRuntime
	notifier       Notifier
	pusher         SnapshotPusher
	sayer          SayPusher
	notifications  NotificationStore
	boardReader    BoardReader
	feedPusher     FeedPusher
	activityPusher ActivityPusher

	// The two workers Workers() builds, retained so anything that commits a
	// queue row can nudge the matching worker (04 §5). nil until Workers runs.
	eventsWorker *Worker
	outboxWorker *Worker
}

// NewService assembles the runtime over its ports. The 08 §7 ports
// (notifications, boardReader, feedPusher, activityPusher) are appended after
// the original 04/07 ports so the composition root updates a single call site.
func NewService(
	store Store, messages MessageStore, brain Brain, puller Puller, blocker Blocker,
	agents AgentRuntime, notifier Notifier, pusher SnapshotPusher, sayer SayPusher,
	notifications NotificationStore, boardReader BoardReader, feedPusher FeedPusher,
	activityPusher ActivityPusher,
) *Service {
	return &Service{
		store:          store,
		messages:       messages,
		brain:          brain,
		puller:         puller,
		blocker:        blocker,
		agents:         agents,
		notifier:       notifier,
		pusher:         pusher,
		sayer:          sayer,
		notifications:  notifications,
		boardReader:    boardReader,
		feedPusher:     feedPusher,
		activityPusher: activityPusher,
	}
}

// EnqueueEvent ingests one of the two 01 event types (04 §6): INSERT into
// events + nudge the events worker. Callers: the agent-runtime inbound
// handler (agent.turn_completed) and the message route (human.message).
// Payloads are opaque snapshots; shape contracts are the emitting surface's
// spec.
func (s *Service) EnqueueEvent(ctx context.Context, t EventType, payload []byte) (int64, error) {
	id, err := s.store.InsertEvent(ctx, t, payload)
	if err != nil {
		return 0, fmt.Errorf("runtime: enqueue event: %w", err)
	}
	s.nudgeEvents()
	return id, nil
}

// PostMessage is the runtime's port for POST /api/message (07 §3–§4, api's
// MessagePoster): append the user transcript row and enqueue the
// human.message event {text} in one transaction (MessageStore's job), then
// nudge the events worker. Returns both ids for the 202 response
// ({event_id, message_id}); a failed append surfaces as an error with no
// invented, partial ids (07 §3 — the transcript and the queue cannot disagree).
func (s *Service) PostMessage(ctx context.Context, text string) (int64, int64, error) {
	messageID, eventID, err := s.messages.AppendUserMessageAndEnqueueEvent(ctx, text)
	if err != nil {
		return 0, 0, fmt.Errorf("runtime: post message: %w", err)
	}
	s.nudgeEvents()
	return messageID, eventID, nil
}

// Say is the runtime's Say port (07 §3, §6; also brain.Say, matched
// structurally with no adapter): append the kiln transcript row, then push
// a say SSE event ({message_id, text, at}) via SayPusher. Append-then-push —
// a crash between them costs a live push, not history (07 §3), so the push
// only ever fires once the row is durable. Every user-visible reply goes
// through this, including the dead-letter system-error message.
func (s *Service) Say(ctx context.Context, text string) error {
	m, err := s.messages.AppendKilnMessage(ctx, text)
	if err != nil {
		return fmt.Errorf("runtime: say append: %w", err)
	}
	if err := s.sayer.PushSay(ctx, m); err != nil {
		return fmt.Errorf("runtime: say push: %w", err)
	}
	return nil
}

// Recent is the runtime's ConversationReader-shaped read (07 §3): the last
// n transcript rows, oldest first. Backs GET /api/messages (api's
// MessagesReader) directly, and the brain's ConversationReader port through
// a composition-root adapter (brain.Message is a distinct type — 06 §3.2).
func (s *Service) Recent(ctx context.Context, n int) ([]Message, error) {
	msgs, err := s.messages.Recent(ctx, n)
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

// Feed assembles the absolute feed snapshot (08 §3): board-derived blocker and
// proposal cards, then brain-authored update/preview cards newest-first, plus
// the header summary counts. Backs GET /api/feed and every feed SSE push. The
// card order is strict — blockers, then proposals, then updates — because the
// client renders one ordered list and pins blockers on top.
func (s *Service) Feed(ctx context.Context) (FeedSnapshot, error) {
	view, err := s.boardReader.BoardView(ctx)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed board view: %w", err)
	}
	unseen, err := s.notifications.UnseenNotifications(ctx)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed unseen notifications: %w", err)
	}

	cards := make([]FeedCard, 0, len(view.Blocked)+len(view.Proposals)+len(unseen))
	for _, t := range view.Blocked {
		id := t.ID
		cards = append(cards, FeedCard{
			Kind: "blocker", ID: "blocker:" + t.ID, Label: t.Title,
			Body: t.BlockedReason, TicketID: &id, CreatedAt: t.UpdatedAt,
		})
	}
	for _, t := range view.Proposals {
		id := t.ID
		cards = append(cards, FeedCard{
			Kind: "proposal", ID: "proposal:" + t.ID, Label: t.Title,
			Body: t.Body, TicketID: &id, CreatedAt: t.UpdatedAt,
		})
	}
	for _, n := range unseen {
		nid := n.ID
		card := FeedCard{
			Kind: string(n.Kind), ID: fmt.Sprintf("update:%d", n.ID),
			Body: n.Body, TicketID: n.TicketID, NotificationID: &nid,
			CreatedAt: n.CreatedAt,
		}
		if n.Kind == KindPreview {
			card.ImageURL = n.ImageURL
		}
		cards = append(cards, card)
	}

	summary := FeedSummary{
		BlockerCount: len(view.Blocked),
		UpdateCount:  len(unseen),
		StreamCount:  view.WorkingCount + view.BlockedCount,
		Building:     view.WorkingCount,
		Idle:         view.BlockedCount,
	}
	// UnseenNotifications is newest-first, so the first row is the last word.
	if len(unseen) > 0 {
		at := unseen[0].CreatedAt
		summary.LastWordAt = &at
	}

	return FeedSnapshot{Summary: summary, Cards: cards}, nil
}

// PostNotification is the brain-facing port for post_update / preview (08 §3,
// 06 tool set): persist a brain-authored notification and (in the same tx)
// append a feed.updated row so the live feed re-renders. Delegates to the
// store; the returned Notification is dropped here because the brain tool
// handler needs only success/failure.
func (s *Service) PostNotification(ctx context.Context, kind, body string, ticketID, imageURL *string) error {
	if _, err := s.notifications.PostNotification(ctx, kind, body, ticketID, imageURL); err != nil {
		return fmt.Errorf("runtime: post notification: %w", err)
	}
	return nil
}

// RetractNotification is the brain-facing port for retract_update (08 §3):
// stamp the row retracted and append feed.updated in one tx. Delegates to the
// store.
func (s *Service) RetractNotification(ctx context.Context, id int64) error {
	if err := s.notifications.RetractNotification(ctx, id); err != nil {
		return fmt.Errorf("runtime: retract notification: %w", err)
	}
	return nil
}

// MarkSeen is the api-facing port for POST /api/feed/seen (08 §3): stamp every
// still-unseen notification up to the client's high-water id, and append
// feed.updated in one tx. Delegates to the store.
func (s *Service) MarkSeen(ctx context.Context, lastID int64) error {
	if err := s.notifications.MarkSeen(ctx, lastID); err != nil {
		return fmt.Errorf("runtime: mark seen: %w", err)
	}
	return nil
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
// (04 §4, §6), typed from the raw Entry.
func (s *Service) handleEvent(ctx context.Context, e Entry) error {
	ev := Event{ID: e.ID, Type: EventType(e.Kind), Payload: e.Payload, CreatedAt: e.CreatedAt}
	// Bracket the brain pass with a thinking activity event (08 §4): On=true
	// before, On=false after. This is the events queue only — the spinner
	// tracks a decision step, not an outbox delivery. A failed push must not
	// derail the brain pass, so activity errors are logged and dropped.
	s.pushThinking(ctx, true)
	defer s.pushThinking(ctx, false)
	if err := s.brain.HandleEvent(ctx, ev); err != nil {
		return fmt.Errorf("runtime: brain pass for event %d: %w", e.ID, err)
	}
	return nil
}

// pushThinking fans out a thinking activity event, self-healing on error (08
// §4): the spinner is ephemeral, so a lost push is cosmetic and must never
// fail the brain pass it brackets.
func (s *Service) pushThinking(ctx context.Context, on bool) {
	if err := s.activityPusher.PushActivity(ctx, ActivityEvent{Kind: "thinking", On: &on}); err != nil {
		slog.Error("runtime: push thinking activity", "on", on, "err", err)
	}
}

// deadLetterEvent handles an exhausted event (04 §3's last row): log at error
// level and surface a system-error reply to the user, so the ticket keeps its
// state and nobody is left waiting silently.
func (s *Service) deadLetterEvent(ctx context.Context, e Entry, cause error) error {
	slog.Error("runtime: event dead-lettered", "id", e.ID, "type", e.Kind, "err", cause)
	if err := s.Say(ctx, systemErrorMessage); err != nil {
		return fmt.Errorf("runtime: dead-letter say: %w", err)
	}
	return nil
}

// handleOutbox is the outbox worker's handler: route the topic (Entry.Kind)
// to its executor (04 §2). The outbox id travels as the idempotency key for
// agent.send/agent.release (04 §3, 05 §7).
func (s *Service) handleOutbox(ctx context.Context, e Entry) error {
	switch e.Kind {
	case topicAgentSend:
		return wrapOutbox("agent send", s.agents.Send(ctx, e.ID, e.Payload))
	case topicAgentRelease:
		return wrapOutbox("agent release", s.agents.Release(ctx, e.ID, e.Payload))
	case topicPullEvaluate:
		return wrapOutbox("run pull", s.puller.RunPull(ctx))
	case topicNotifySend:
		return wrapOutbox("notify send", s.notifier.Send(ctx, e.Payload))
	case topicBoardUpdated:
		return wrapOutbox("push board", s.pusher.PushBoard(ctx))
	case topicFeedUpdated:
		return s.handleFeedUpdated(ctx)
	case topicActivityToast:
		return s.handleActivityToast(ctx, e)
	default:
		return fmt.Errorf("%w %q", errUnknownTopic, e.Kind)
	}
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
	if err := s.blocker.MarkBlocked(ctx, p.TicketID, reason); err != nil {
		return fmt.Errorf("runtime: dead-letter agent.send: mark blocked: %w", err)
	}
	slog.Error("runtime: agent.send dead-lettered, ticket blocked",
		"id", e.ID, "ticket", p.TicketID, "err", cause)
	return nil
}

// toastPayload is the activity.toast outbox payload (08 §4, §7), mirroring the
// board's ToastPayload by value — this module never imports internal/board.
type toastPayload struct {
	Verb        string `json:"verb"`
	TicketTitle string `json:"ticket_title"`
}

// handleFeedUpdated realizes the feed.updated topic (08 §3, §7): re-assemble
// the absolute feed and fan it out. Emitted by both the board (state
// transitions) and the runtime itself (notification mutations). Self-heals —
// a failed assembly or push logs-and-drops (like board.updated) rather than
// wedging the outbox, since the next emission re-renders from scratch.
func (s *Service) handleFeedUpdated(ctx context.Context) error {
	snap, err := s.Feed(ctx)
	if err != nil {
		slog.Error("runtime: feed.updated assemble", "err", err)
		return nil
	}
	if err := s.feedPusher.PushFeed(ctx, snap); err != nil {
		slog.Error("runtime: feed.updated push", "err", err)
	}
	return nil
}

// handleActivityToast realizes the activity.toast topic (08 §4, §7): decode
// the board-emitted verb + ticket title and fan out a toast activity event.
// Self-heals — a decode or push failure logs-and-drops (the toast is
// ephemeral, so a lost one is cosmetic).
func (s *Service) handleActivityToast(ctx context.Context, e Entry) error {
	var p toastPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		slog.Error("runtime: activity.toast decode", "id", e.ID, "err", err)
		return nil
	}
	ev := ActivityEvent{Kind: "toast", Verb: p.Verb, TicketTitle: p.TicketTitle}
	if err := s.activityPusher.PushActivity(ctx, ev); err != nil {
		slog.Error("runtime: activity.toast push", "id", e.ID, "err", err)
	}
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
