package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// FanOut is the runtime's push coordinator: every SSE fan-out the module emits
// (04 §7, 08 §3–§4) and every self-healing UI topic the outbox routes to one
// (08 §7). Four ports point at the client — board snapshots, feed snapshots,
// activity events, and the completion card's row — and two collaborators do the
// work behind them: Feed assembles the snapshot a feed push carries, Notify
// reaches the user when the app is closed.
//
// The discipline that runs through it is best-effort visibility — the mirror
// image of Transcript's and Notifications' durability-before-visibility. What
// this type emits is a *view* of state that is already durable, so a failed push
// is logged and dropped rather than returned: feed and board snapshots are
// absolute (04 D7) and the next emission re-renders from scratch, while toasts
// and thinking spinners are ephemeral and a lost one is cosmetic. Wedging the
// outbox on a dropped frame would cost more than the frame. Isolating these
// methods is what makes that contract legible — every log-and-drop in the
// runtime now lives in one file, instead of interleaved with the transactional
// writes next door.
//
// HandleFeedCompletion is the one exception, and the reason this type holds a
// NotificationWriter at all: the "done" card it posts is a persistent feed row,
// not a frame, so its failures are returned and the outbox retries. It belongs
// here rather than with Notifications because it is a UI-topic handler — driven
// by a board-emitted outbox entry, idempotent on that entry's id — not a
// caller-facing mutation.
type FanOut struct {
	snapshots     SnapshotPusher
	feeds         FeedPusher
	activity      ActivityPusher
	notifications NotificationWriter

	// feed assembles the absolute snapshot a feed.updated push carries; notify is
	// the tenant-scoped push choke point the transition push goes through (never a
	// raw Notifier — Send is where the 02 §10 mode gate lives).
	feed   *Feed
	notify *Notify
}

// NewFanOut wires the push coordinator over its four client-facing ports and
// the two units it collaborates with.
func NewFanOut(
	snapshots SnapshotPusher, feeds FeedPusher, activity ActivityPusher,
	notifications NotificationWriter, feed *Feed, notify *Notify,
) *FanOut {
	return &FanOut{
		snapshots: snapshots, feeds: feeds, activity: activity,
		notifications: notifications, feed: feed, notify: notify,
	}
}

// PushThinking fans out a thinking activity event to one project (08 §4): the
// spinner that brackets a brain pass, On=true before and On=false after. The
// events dispatcher calls it around the pass it brackets — this is the events
// queue only, so the spinner tracks a decision step, not an outbox delivery.
// Self-healing: a failed push is logged and dropped, because a lost spinner
// frame must never derail the brain pass it decorates.
func (f *FanOut) PushThinking(ctx context.Context, projectID string, on bool) {
	if err := f.activity.PushActivity(ctx, projectID, ActivityEvent{Kind: "thinking", On: &on}); err != nil {
		slog.Error("runtime: push thinking activity", "on", on, "err", err)
	}
}

// PushBoard executes a board.updated entry (04 §7, 11 §3): fan out a fresh full
// board snapshot to the project's connected clients. Snapshots are absolute, so
// a duplicate delivery is harmless (04 D7). Unlike the handlers below it returns
// its error — the dispatcher wraps it and the outbox retries, and 04 §3's
// log-and-drop dead-letter row catches an exhausted one.
func (f *FanOut) PushBoard(ctx context.Context, projectID string) error {
	if err := f.snapshots.PushBoard(ctx, projectID); err != nil {
		return fmt.Errorf("push board snapshot: %w", err)
	}
	return nil
}

// HandleFeedUpdated realizes the feed.updated topic (08 §3, §7): re-assemble
// the absolute feed and fan it out. Emitted by both the board (state
// transitions) and the runtime itself (notification mutations). Self-heals —
// a failed assembly or push logs-and-drops (like board.updated) rather than
// wedging the outbox, since the next emission re-renders from scratch.
func (f *FanOut) HandleFeedUpdated(ctx context.Context, e Entry) {
	snap, err := f.feed.Feed(ctx, e.ProjectID)
	if err != nil {
		slog.Error("runtime: feed.updated assemble", "project_id", e.ProjectID, "err", err)
		return
	}
	// Deliberately not an early return: the live SSE frame and the Web Push below
	// are independent best-effort effects, so a client that could not be reached
	// live must not also cost the user the notification.
	if err := f.feeds.PushFeed(ctx, e.ProjectID, snap); err != nil {
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
	f.pushFeedUpdateNotification(ctx, e.ProjectID, p)
}

// HandleActivityToast realizes the activity.toast topic (08 §4, §7): decode
// the board-emitted verb + ticket title and fan out a toast activity event.
// Self-heals — a decode or push failure logs-and-drops (the toast is
// ephemeral, so a lost one is cosmetic).
func (f *FanOut) HandleActivityToast(ctx context.Context, e Entry) {
	var p toastPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		slog.Error("runtime: activity.toast decode", "id", e.ID, "err", err)
		return
	}
	ev := ActivityEvent{Kind: "toast", Verb: p.Verb, TicketID: p.TicketID, TicketTitle: p.TicketTitle}
	if err := f.activity.PushActivity(ctx, e.ProjectID, ev); err != nil {
		slog.Error("runtime: activity.toast push", "id", e.ID, "project_id", e.ProjectID, "err", err)
	}
}

// HandleFeedCompletion realizes the feed.completion topic (08 §7): post the
// persistent "done" feed card for a completed ticket. Unlike the ephemeral
// toast, this card is durable, so a decode failure returns an error (the outbox
// retries) rather than logging-and-dropping. The post is idempotent on the
// outbox id (e.ID), so a redelivery is a safe no-op. The card is a "done" kind
// styled like a poke: notificationToCard renders the ticket title as the label,
// the client prefixes a ✅, and the body is empty — no prose.
func (f *FanOut) HandleFeedCompletion(ctx context.Context, e Entry) error {
	var p completionPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("decode feed.completion: %w", err)
	}
	if _, err := f.notifications.PostCompletionCard(
		ctx, e.ProjectID, e.ID, p.TicketID, completionCardBody, p.GitHubURL, p.GitHubLabel, p.Summary,
	); err != nil {
		return fmt.Errorf("post completion card: %w", err)
	}
	return nil
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
func (f *FanOut) pushFeedUpdateNotification(ctx context.Context, projectID string, p feedUpdatedPayload) {
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
	if err := f.notify.Send(ctx, projectID, notifyKindUpdate, payload); err != nil {
		slog.Error("runtime: feed.updated notify send", "project_id", projectID, "err", err)
	}
}

// completionCardBody is the body of the auto-posted "done" feed card: empty.
// The card is a "done" kind, so the client renders it single-line like a poke —
// the ticket title as the label with a ✅ prefix and no description body.
const completionCardBody = ""

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

// The four payload mirrors below are the board-emitted outbox shapes this module
// decodes, defined here by value so it never imports internal/board (the same
// layering rule the board and brain modules state in the other direction). They
// collapse into this file because it is where three of the four are decoded; the
// fourth, notifyPayload, is also read by the notify.send route on the way to
// Notify.Send, and is built here for the feed-update push that shares its shape.

// notifyPayload is the notify.send payload the Notifier decodes (a
// board.NotifyPayload — Title/Reason → Title/Body). Kind names the transition
// (mirrors board.NotifyKind*) so the mode gate can decide delivery; a feed-update
// push built here carries notifyKindUpdate.
type notifyPayload struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
	Kind   string `json:"kind"`
}

// toastPayload is the activity.toast outbox payload (08 §4, §7), mirroring the
// board's ToastPayload by value.
type toastPayload struct {
	Verb        string `json:"verb"`
	TicketID    string `json:"ticket_id"`
	TicketTitle string `json:"ticket_title"`
}

// feedUpdatedPayload mirrors the board's FeedUpdatedPayload (03 §7.1) by value.
// Title names the changed ticket; Verb labels the nature of the change and drives
// the transition push copy (02 §10). Empty when a feed.updated carries no
// descriptor (the update then stays silent — no verb is, by definition, not a
// state transition).
type feedUpdatedPayload struct {
	Title string `json:"title"`
	Verb  string `json:"verb"`
}

// completionPayload is the feed.completion outbox payload (08 §7), mirroring the
// board's CompletionPayload by value. GitHubURL/GitHubLabel are the link to the
// landed work rendered as the done card's second line; both empty when no link is
// available. Summary is the landed work's one-line description rendered as the
// card body; empty when unavailable.
type completionPayload struct {
	TicketID    string `json:"ticket_id"`
	TicketTitle string `json:"ticket_title"`
	GitHubURL   string `json:"github_url,omitempty"`
	GitHubLabel string `json:"github_label,omitempty"`
	Summary     string `json:"summary,omitempty"`
}
