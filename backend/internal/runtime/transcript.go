package runtime

import (
	"context"
	"time"
)

// MessageRole is a transcript message's speaker (07 §3). Mirrors brain's
// MessageRole by value — this module does not import internal/brain, the
// same layering rule brain's doc.go states in the other direction; the
// composition root adapts between the two when it wires ConversationReader.
type MessageRole string

const (
	RoleUser MessageRole = "user"
	RoleKiln MessageRole = "kiln"
)

// Message is one row of the persisted transcript (07 §3): the messages
// table, owned by this module because it already owns the client-facing
// conversation surfaces (04 §7). Append-only; no edits, no deletes in v1.
type Message struct {
	ID        int64
	Role      MessageRole
	Text      string
	CreatedAt time.Time
}

// MessageStore is the runtime's persistence port over the messages table
// (07 §3). Implemented by ./postgres alongside Store; kept as a separate
// port so Store stays scoped to the two queue tables (04 §2). Every method
// is tenant-scoped (11 §3): writes stamp project_id, reads predicate on it.
type MessageStore interface {
	// AppendUserMessageAndEnqueueEvent appends the user row and enqueues the
	// human.message event {text} in one transaction (07 §3 — user decision:
	// the transcript and the event queue cannot disagree), both stamped with
	// the project. Backs the runtime's PostMessage and, through it,
	// POST /api/message (07 §4).
	AppendUserMessageAndEnqueueEvent(ctx context.Context, projectID, text string) (messageID, eventID int64, err error)

	// AppendKilnMessage appends one kiln-authored row (07 §3): the first
	// half of the Say port, called by Service.Say before the SSE push.
	AppendKilnMessage(ctx context.Context, projectID, text string) (Message, error)

	// Recent returns the project's last n transcript rows, oldest first
	// (07 §3). Backs both the brain's ConversationReader (06 §3.2, adapted at
	// the composition root) and GET /api/messages (07 §4).
	Recent(ctx context.Context, projectID string, n int) ([]Message, error)
}

// SayPusher is the runtime's port onto the api SSE hub's say fan-out
// (07 §3–§4): the second half of the Say port — push one say event
// ({message_id, text, at}) to the project's connected clients after the kiln
// row is durably appended. Implemented by the api package's Hub, which fans
// out per project (11 §3).
type SayPusher interface {
	PushSay(ctx context.Context, projectID string, m Message) error
}
