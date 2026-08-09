package runtime

import (
	"context"
)

// Service is what is left of the runtime's original god unit: a thin aggregate
// over the six focused types the split produced (Dispatcher, Transcript,
// Notifications, Feed, Notify, FanOut), holding no port of its own and doing no
// work of its own. Every method on it is a one-line forward.
//
// It exists only so the composition root and the consumer role interfaces keep
// working while the split lands — steps 7–9 of
// docs/god-units-plans/runtime-service.md flip cmd/kiln to the six values
// directly and delete this file. Nothing new should be added here; add it to the
// unit that owns the responsibility.
type Service struct {
	// dispatcher is the extracted queue core (god-unit split step 6): it owns the
	// five drain ports — store, brains, puller, blocker, agents — the two workers,
	// and every route between them, and it is the Transcript's Nudger. With it
	// gone from here, Service holds no port at all.
	dispatcher *Dispatcher

	// notify is the extracted push choke point (step 1): the Notifier and Owner
	// ports, behind the 02 §10 mode gate. Reached by the dispatcher's notify.send
	// route and by FanOut's feed-update push; Service itself no longer calls it.
	notify *Notify

	// feed is the extracted feed assembler (step 2): the BoardReader and the read
	// half of the notification store. Backs the two api-facing shims below.
	feed *Feed

	// notifs is the extracted notification CRUD facade (step 3): the transactional
	// feed mutations behind the eight forwards below.
	notifs *Notifications

	// transcript is the extracted conversation surface (step 4): the MessageStore
	// and SayPusher ports behind the three forwards below. The dispatcher is its
	// Nudger, so an ingest here wakes the events worker there.
	transcript *Transcript

	// fanout is the extracted push coordinator (step 5): the three pushers and the
	// completion card's writer. Reached by the dispatcher's four UI-topic routes
	// and its thinking bracket; Service itself no longer calls it.
	fanout *FanOut
}

// NewService assembles the six units over the ports the composition root still
// hands it as one flat list. The signature is unchanged from the pre-split
// constructor so no caller and no existing test has had to move while the six
// extractions landed; steps 7–9 replace this call with the six constructors
// below, wired directly (docs/god-units-plans/runtime-service.md §6).
//
// The build order is the dependency order: Notify and Feed first (FanOut fans out
// from them), then FanOut and Transcript, then the Dispatcher that routes to all
// three — and finally SetNudger, closing the ingest→nudge edge back the other way
// (§4).
func NewService(
	store Store, messages MessageStore, brains BrainResolver, puller Puller, blocker Blocker,
	agents AgentRuntime, notifier Notifier,
	pusher SnapshotPusher, sayer SayPusher,
	notifications NotificationStore, boardReader BoardReader, feedPusher FeedPusher,
	activityPusher ActivityPusher,
	owner Owner,
) *Service {
	notify := NewNotify(notifier, owner)
	feed := NewFeed(boardReader, notifications)
	fanout := NewFanOut(pusher, feedPusher, activityPusher, notifications, feed, notify)
	transcript := NewTranscript(messages, sayer)
	dispatcher := NewDispatcher(store, brains, puller, blocker, agents, transcript, notify, fanout)
	transcript.SetNudger(dispatcher)
	return &Service{
		dispatcher: dispatcher,
		notify:     notify,
		feed:       feed,
		notifs:     NewNotifications(notifications),
		transcript: transcript,
		fanout:     fanout,
	}
}

// EnqueueEvent delegates to the Dispatcher (dispatcher.go) — the 04 §6 ingest
// behind the agent-runtime inbound handler's agent.turn_completed.
func (s *Service) EnqueueEvent(
	ctx context.Context, projectID string, t EventType, idempotencyKey int64, payload []byte,
) (int64, error) {
	return s.dispatcher.EnqueueEvent(ctx, projectID, t, idempotencyKey, payload)
}

// Workers delegates to the Dispatcher (dispatcher.go) — the two serial workers
// the composition root runs (04 §3–§4), returned as (events, outbox).
func (s *Service) Workers(clock Clock) (*Worker, *Worker) {
	return s.dispatcher.Workers(clock)
}

// The three conversation methods below delegate to the extracted Transcript
// (transcript_service.go) so *Service keeps satisfying api's MessagePoster and
// MessagesReader, and the brain's Say and ConversationReader, while the split
// lands.

// PostMessage delegates to the Transcript — POST /api/message's transactional
// append+enqueue, which nudges the events worker through the Dispatcher
// (07 §3–§4).
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
