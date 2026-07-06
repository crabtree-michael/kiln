package board

// Topic enumerates the outbox emissions the board may append (03 §7.1). The
// board owns this emission contract; delivery — drain loop, retries,
// dead-lettering — is the runtime's (04 §2–§3). The outbox is distinct from
// the event queue that wakes the brain (02 §2).
type Topic string

const (
	TopicAgentSend      Topic = "agent.send"      // RunPull (work order) and SendToAgent (instruction)
	TopicAgentRelease   Topic = "agent.release"   // AcceptToDone: recycle the worker to a fresh workspace
	TopicNotifySend     Topic = "notify.send"     // MarkBlocked: push notification to the user
	TopicPullEvaluate   Topic = "pull.evaluate"   // MarkReady, AcceptToDone: trigger RunPull
	TopicBoardUpdated   Topic = "board.updated"   // every mutation: full-snapshot push (03 D7)
	TopicFeedUpdated    Topic = "feed.updated"    // feed-relevant change: reassemble the feed (08 §7)
	TopicActivityToast  Topic = "activity.toast"  // ephemeral activity pill (08 §5): started/nudged/finished/queued
	TopicFeedCompletion Topic = "feed.completion" // AcceptToDone: post the persistent "done" feed card
)

// Emission is one outbox row, appended in the same transaction as the state
// change it belongs to (03 §7, I7). Payload is a snapshot taken at emit time —
// executing an entry never re-reads mutable state. Signal-only topics
// (pull.evaluate, board.updated) carry a nil Payload, persisted as an empty
// JSON object.
type Emission struct {
	Topic   Topic
	Payload any // one of the *Payload structs below; marshaled to jsonb by the store
}

// SendPayload — agent.send (03 §7.1, 05 §2.1). Message is the work order
// (title + body) from RunPull, or the instruction from SendToAgent; the
// agent-runtime module derives first-message-vs-continuation itself. The
// outbox id is attached by the runtime as the idempotency key (04 §3).
type SendPayload struct {
	TicketID TicketID `json:"ticket_id"`
	WorkerID WorkerID `json:"worker_id"`
	Message  string   `json:"message"`
}

// ReleasePayload — agent.release (03 §7.1, 05 §4).
type ReleasePayload struct {
	WorkerID WorkerID `json:"worker_id"`
}

// NotifyPayload — notify.send (03 §7.1).
type NotifyPayload struct {
	TicketID TicketID `json:"ticket_id"`
	Title    string   `json:"title"`
	Reason   string   `json:"reason"`
}

// ToastPayload — activity.toast (08 §5): a short-lived pill announcing a board
// verb. Verb ∈ {started, nudged, finished, queued}; TicketTitle names the
// ticket the toast is about. The runtime turns this into a wire ActivityEvent.
type ToastPayload struct {
	Verb        string `json:"verb"`
	TicketTitle string `json:"ticket_title"`
}

// CompletionPayload — feed.completion (08 §7): the persistent counterpart to the
// ephemeral "finished" activity.toast. Emitted by AcceptToDone so the runtime
// posts a lasting feed card for the completion deterministically, independent of
// whether the brain/agent remembers to. TicketID tags the card to its ticket
// (the feed renders the current title as the card label); TicketTitle is carried
// for the card body. The runtime uses the outbox id as the idempotency key so an
// at-least-once redelivery posts no duplicate card.
type CompletionPayload struct {
	TicketID    TicketID `json:"ticket_id"`
	TicketTitle string   `json:"ticket_title"`
}
