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

// Owner resolves a project to its owning user id (11 §3) — the notifier
// path's tenant→recipient hop: push subscriptions hang off a user, not a
// project. The runtime resolves the owner before every Notifier.Send and
// refuses to send for an unowned project (a misdirected push is worse than a
// dropped one); the concrete notifier in cmd/kiln wiring may re-resolve to
// pick the actual subscriptions. nil when identity is unconfigured
// (single-user boot) — then the check is skipped and the notifier targets the
// one local user.
type Owner interface {
	Owner(ctx context.Context, projectID string) (string, error)
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

// Notifier executes notify.send entries (02 §10) for one project's owner
// (11 §3 — see Owner). A rare duplicate notification is accepted as benign
// (04 §3).
type Notifier interface {
	Send(ctx context.Context, projectID string, payload []byte) error
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
	store          Store
	messages       MessageStore
	brains         BrainResolver
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
	owner          Owner

	// The two workers Workers() builds, retained so anything that commits a
	// queue row can nudge the matching worker (04 §5). nil until Workers runs.
	eventsWorker *Worker
	outboxWorker *Worker
}

// NewService assembles the runtime over its ports. The 08 §7 ports
// (notifications, boardReader, feedPusher, activityPusher) are appended after
// the original 04/07 ports, and the 11 §3 owner port after those, so the
// composition root updates a single call site.
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
		notifier:       notifier,
		pusher:         pusher,
		sayer:          sayer,
		notifications:  notifications,
		boardReader:    boardReader,
		feedPusher:     feedPusher,
		activityPusher: activityPusher,
		owner:          owner,
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

// feedPageSize is how many update/preview cards the feed snapshot carries in its
// newest page (08 D2′). History older than this pages in via FeedHistory, so a
// long-retained backlog doesn't ship in one snapshot. Also the default
// history-page size; the api clamps an explicit ?limit within [1, 100].
const feedPageSize = 30

// Feed assembles one project's absolute feed snapshot (08 §3, D2′, 11 §3):
// board-derived blocker and proposal cards, then the newest page of
// brain-authored update/preview cards — seen AND unseen (retained history),
// newest-first — plus the header summary counts and the last-seen divider
// boundary. Backs GET /api/feed and every feed SSE push. The card order is
// strict — blockers, then proposals, then updates — because the client renders
// one ordered list and pins blockers on top.
func (s *Service) Feed(ctx context.Context, projectID string) (FeedSnapshot, error) {
	view, err := s.boardReader.BoardView(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed board view: %w", err)
	}
	recent, hasMoreHistory, err := s.notifications.RecentNotifications(ctx, projectID, feedPageSize)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed recent notifications: %w", err)
	}
	unseenCount, err := s.notifications.UnseenCount(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed unseen count: %w", err)
	}
	lastSeen, err := s.notifications.LastSeenID(ctx, projectID)
	if err != nil {
		return FeedSnapshot{}, fmt.Errorf("runtime: feed last seen id: %w", err)
	}

	cards := make([]FeedCard, 0, len(view.Blocked)+len(view.Proposals)+len(recent))
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
	for _, n := range recent {
		if card, ok := notificationToCard(n, view.TicketTitles); ok {
			cards = append(cards, card)
		}
	}

	summary := FeedSummary{
		BlockerCount:           len(view.Blocked),
		UpdateCount:            unseenCount,
		StreamCount:            view.WorkingCount + view.BlockedCount,
		Building:               view.WorkingCount,
		Idle:                   view.BlockedCount,
		LastSeenNotificationID: lastSeen,
	}
	// RecentNotifications is newest-first, so the first row is the last word.
	if len(recent) > 0 {
		at := recent[0].CreatedAt
		summary.LastWordAt = &at
	}

	return FeedSnapshot{Summary: summary, Cards: cards, HasMoreHistory: hasMoreHistory}, nil
}

// FeedHistory assembles one older page of the project's retained
// update/preview history (08 D2′, 11 §3): notification-backed cards with
// id < before, newest-first, plus whether a further page remains.
// Board-derived blocker/proposal cards are never paged. Ticket-tagged notes
// take their label from current board titles, exactly like Feed. Backs
// GET /api/feed/history.
func (s *Service) FeedHistory(
	ctx context.Context, projectID string, before int64, limit int,
) ([]FeedCard, bool, error) {
	view, err := s.boardReader.BoardView(ctx, projectID)
	if err != nil {
		return nil, false, fmt.Errorf("runtime: feed history board view: %w", err)
	}
	notes, hasMore, err := s.notifications.HistoryBefore(ctx, projectID, before, limit)
	if err != nil {
		return nil, false, fmt.Errorf("runtime: feed history notifications: %w", err)
	}
	cards := make([]FeedCard, 0, len(notes))
	for _, n := range notes {
		if card, ok := notificationToCard(n, view.TicketTitles); ok {
			cards = append(cards, card)
		}
	}
	return cards, hasMore, nil
}

// notificationToCard maps a brain-authored notification row to its feed card
// (08 §3), shared by Feed's newest page and FeedHistory's older pages. A
// ticket-tagged note renders the linked ticket's current title as its label
// (titles from the board view); a note with no ticket keeps an empty label,
// which the client renders headless-but-legible.
//
// Returns ok=false when the note is tagged to a ticket that is no longer on the
// board — i.e. the ticket has been archived (deleted). Its title is absent from
// the board view, so the card would otherwise render title-less (a persistent
// "done" card as a bare ✅, or an update/preview as a headless row). Instead the
// card vanishes from the feed entirely, mirroring how board-derived
// blocker/proposal cards disappear when GetBoard stops returning their ticket
// (03 §4 — an archived ticket disappears from every read). The comma-ok lookup
// distinguishes an archived ticket (absent) from a live one whose title is
// present, so untagged notes (TicketID nil) still render headless as before.
func notificationToCard(n Notification, titles map[string]string) (FeedCard, bool) {
	nid := n.ID
	card := FeedCard{
		Kind: string(n.Kind), ID: fmt.Sprintf("update:%d", n.ID),
		Body: n.Body, TicketID: n.TicketID, NotificationID: &nid,
		CreatedAt: n.CreatedAt,
	}
	if n.TicketID != nil {
		title, live := titles[*n.TicketID]
		if !live {
			return FeedCard{}, false
		}
		card.Label = title
	}
	if n.Kind == KindPreview {
		card.ImageURL = n.ImageURL
	}
	if n.Kind == KindDone {
		card.GitHubURL = n.GitHubURL
		card.GitHubLabel = n.GitHubLabel
		card.WorkSummary = n.WorkSummary
	}
	return card, true
}

// PostNotification is the brain-facing port for post_update / preview (08 §3,
// 06 tool set): persist a brain-authored notification and (in the same tx)
// append a feed.updated row so the live feed re-renders. Delegates to the
// store; the returned Notification is dropped here because the brain tool
// handler needs only success/failure.
func (s *Service) PostNotification(
	ctx context.Context, projectID, kind, body string, ticketID, imageURL *string,
) error {
	if _, err := s.notifications.PostNotification(ctx, projectID, kind, body, ticketID, imageURL); err != nil {
		return fmt.Errorf("runtime: post notification: %w", err)
	}
	return nil
}

// PostPoke posts the steward's feed-only poke card for a ticket: a KindPoke
// notification with an empty body, tagged to the ticket so the feed renders its
// current title with a 👉 (notificationToCard takes the label from the board
// view). Excluded from the unseen badge and the brain's update list at the store
// layer — a mechanical signal, not a brain-authored note.
func (s *Service) PostPoke(ctx context.Context, projectID, ticketID string) error {
	if _, err := s.notifications.PostNotification(ctx, projectID, string(KindPoke), "", &ticketID, nil); err != nil {
		return fmt.Errorf("runtime: post poke: %w", err)
	}
	return nil
}

// RetractNotification is the brain-facing port for retract_update (08 §3):
// stamp the row retracted and append feed.updated in one tx. Delegates to the
// store.
func (s *Service) RetractNotification(ctx context.Context, projectID string, id int64) error {
	if err := s.notifications.RetractNotification(ctx, projectID, id); err != nil {
		return fmt.Errorf("runtime: retract notification: %w", err)
	}
	return nil
}

// DismissNotification is the api-facing port for POST /api/feed/{id}/dismiss (08
// §3): the user swiped a single update/preview card away, so clear it for good.
// The effect is identical to the brain's retract — stamp the row retracted and
// append feed.updated in one tx — but this is user-initiated, so it lives beside
// MarkSeen (the other client-driven feed mutation) rather than the brain-facing
// RetractNotification it delegates to.
func (s *Service) DismissNotification(ctx context.Context, projectID string, id int64) error {
	if err := s.notifications.RetractNotification(ctx, projectID, id); err != nil {
		return fmt.Errorf("runtime: dismiss notification: %w", err)
	}
	return nil
}

// DismissAllNotifications is the api-facing port for POST /api/feed/dismiss-all
// (08 §3, clear-all): the user tapped the header trash affordance to clear every
// feed notification at once. Retracts all still-active rows and fans out one
// feed.updated in a single tx — the bulk sibling of DismissNotification.
func (s *Service) DismissAllNotifications(ctx context.Context, projectID string) error {
	if err := s.notifications.RetractAllNotifications(ctx, projectID); err != nil {
		return fmt.Errorf("runtime: dismiss all notifications: %w", err)
	}
	return nil
}

// EditNotification is the brain-facing port for edit_update (08 §3 amended, 06
// tool set): amend a still-active card's kind/body/image in place and append
// feed.updated in one tx. Delegates to the store; the brain tool handler needs
// only success/failure.
func (s *Service) EditNotification(
	ctx context.Context, projectID string, id int64, kind, body string, imageURL *string,
) error {
	if err := s.notifications.EditNotification(ctx, projectID, id, kind, body, imageURL); err != nil {
		return fmt.Errorf("runtime: edit notification: %w", err)
	}
	return nil
}

// ListNotifications is the brain-facing port for list_updates (06 tool set): the
// active (neither seen nor retracted) feed cards, newest-first, so the brain can
// see the ids it may edit or retract. Delegates to the store's UnseenNotifications.
func (s *Service) ListNotifications(ctx context.Context, projectID string) ([]Notification, error) {
	notes, err := s.notifications.UnseenNotifications(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("runtime: list notifications: %w", err)
	}
	return notes, nil
}

// MarkSeen is the api-facing port for POST /api/feed/seen (08 §3): stamp every
// still-unseen notification up to the client's high-water id, and append
// feed.updated in one tx. Delegates to the store.
func (s *Service) MarkSeen(ctx context.Context, projectID string, lastID int64) error {
	if err := s.notifications.MarkSeen(ctx, projectID, lastID); err != nil {
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
		return wrapOutbox("notify send", s.notifyOwner(ctx, e.ProjectID, e.Payload))
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

// notifyOwner is the single choke point for Notifier.Send (11 §3): resolve
// the project's owning user first, then send. The owner resolution lives here
// — not in the concrete notifier — because this module is where the tenant
// boundary is enforced: an unowned/unresolvable project must not emit a push
// at all, and returning the error lets the notify.send retry/dead-letter
// machinery treat a transient resolution failure like any delivery failure.
// The resolved user id is a guard + audit anchor; the concrete notifier
// re-resolves the actual subscription targets in wiring. With no Owner wired
// (single-user boot, identity off) the check is skipped.
func (s *Service) notifyOwner(ctx context.Context, projectID string, payload []byte) error {
	if s.owner != nil {
		userID, err := s.owner.Owner(ctx, projectID)
		if err != nil {
			return fmt.Errorf("resolve project owner: %w", err)
		}
		slog.InfoContext(ctx, "runtime.notify.send", "project_id", projectID, "user_id", userID)
	}
	if err := s.notifier.Send(ctx, projectID, payload); err != nil {
		return fmt.Errorf("notifier send: %w", err)
	}
	return nil
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
// module keeps not importing internal/board. Built here only for the
// transition feed-update notification; the block path's payload is minted by the
// board.
type notifyPayload struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
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
	snap, err := s.Feed(ctx, e.ProjectID)
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

// pushFeedUpdateNotification fires a Web Push on a feed update, but ONLY when the
// update is a genuine ticket state transition — the sole event allowed to reach
// the user (design 2026-07-07, per the user's standing rule: "the only
// notifications I should get are actual ticket status changes"). Progress
// narration, edits, mark-seen, a proposal being reshaped, and an instruction
// resuming a blocked ticket are all silent: feedUpdateNotification returns
// ok=false for each. There is no notification-frequency gate anymore — every
// real status change pushes, and nothing else ever does, so the old
// "blocked"-vs-"all" mode is no longer the right shape and does not participate
// here. The push names the ticket and what happened, so it is informative at a
// glance rather than a generic "board was updated". The push still routes to the
// right tenant: notifyOwner resolves the project's owning user before delivery
// (11 §3), so projectID is threaded through from the emitting feed.updated entry.
// Self-heals like the feed push itself: a send failure logs-and-drops rather than
// wedging the outbox (the notification is best-effort, 04 §3). A block emits its
// own, more-specific notify.send, so its feed-update push is skipped here too
// (ok=false) to avoid a duplicate.
func (s *Service) pushFeedUpdateNotification(ctx context.Context, projectID string, p feedUpdatedPayload) {
	note, ok := feedUpdateNotification(p)
	if !ok {
		return
	}
	// The notifier decodes a board.NotifyPayload (Title/Reason → Title/Body);
	// this marshals the same shape by value so this module keeps not importing
	// internal/board.
	payload, err := json.Marshal(note)
	if err != nil {
		slog.Error("runtime: feed.updated notify marshal", "err", err)
		return
	}
	if err := s.notifyOwner(ctx, projectID, payload); err != nil {
		slog.Error("runtime: feed.updated notify send", "project_id", projectID, "err", err)
	}
}

// feedUpdateVerbBody maps a feed.updated change verb (board.FeedUpdatedPayload)
// to the push body describing what happened, keeping the push copy in sync with
// the board's feed-update verbs (03 §7.1) and the feed's own verb vocabulary
// (08 §5). ONLY genuine ticket state transitions have an entry — the
// sole events allowed to push (design 2026-07-07). Deliberately absent, so they
// resolve to ok=false in feedUpdateNotification and stay silent:
//   - "reshaped": editing a proposal's fields is not a state change.
//   - "nudged":   blocked→working is driven by sending the agent an instruction,
//     which never notifies the user.
//   - "blocked":  a state change, but it emits its own dedicated notify.send
//     carrying the actual blocker question, so a second, vaguer push is skipped.
//
// An empty/unknown verb (progress narration, edits, mark-seen — the runtime's own
// signal-only feed.updated rows) is likewise absent and stays silent.
var feedUpdateVerbBody = map[string]string{
	"proposal": "New proposal",
	"queued":   "Queued for work",
	"finished": "Finished",
	"archived": "Archived",
}

// feedUpdateNotification builds the push payload for a feed change, naming the
// ticket (Title) and what happened (Body). ok is false whenever the
// change is not a genuine ticket state transition and so must not reach the user:
// an unrecognized/empty verb (narration, edits, mark-seen), a reshaped proposal,
// a nudge, or a block (all absent from feedUpdateVerbBody). There is no generic
// "board was updated" fallback — a change with no descriptive verb is, by
// definition, not a status change.
func feedUpdateNotification(p feedUpdatedPayload) (notifyPayload, bool) {
	body, isTransition := feedUpdateVerbBody[p.Verb]
	if !isTransition || p.Title == "" {
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
