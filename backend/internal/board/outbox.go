package board

// Topic enumerates the outbox emissions the board may append (03 §7.1). The
// board owns this emission contract; delivery — drain loop, retries,
// dead-lettering — is the runtime's (04 §2–§3). The outbox is distinct from
// the event queue that wakes the brain (02 §2).
type Topic string

const (
	TopicAmikaDispatch Topic = "amika.dispatch" // RunPull: dispatch the agent into the bound sandbox
	TopicAmikaInstruct Topic = "amika.instruct" // SendToAgent: resume, or a new turn
	TopicNotifySend    Topic = "notify.send"    // MarkBlocked: push notification to the user
	TopicPullEvaluate  Topic = "pull.evaluate"  // MarkReady, AcceptToDone: trigger RunPull
	TopicBoardUpdated  Topic = "board.updated"  // every mutation: full-snapshot push (03 D7)
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

// DispatchPayload — amika.dispatch (03 §7.1). Title + body are the work
// instruction; the outbox id is attached by the runtime as the Amika
// idempotency key (04 §3).
type DispatchPayload struct {
	TicketID  TicketID  `json:"ticket_id"`
	SandboxID SandboxID `json:"sandbox_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
}

// InstructPayload — amika.instruct (03 §7.1).
type InstructPayload struct {
	TicketID    TicketID  `json:"ticket_id"`
	SandboxID   SandboxID `json:"sandbox_id"`
	Instruction string    `json:"instruction"`
}

// NotifyPayload — notify.send (03 §7.1).
type NotifyPayload struct {
	TicketID TicketID `json:"ticket_id"`
	Title    string   `json:"title"`
	Reason   string   `json:"reason"`
}
