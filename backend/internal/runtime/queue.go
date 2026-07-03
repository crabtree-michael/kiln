package runtime

import (
	"encoding/json"
	"time"
)

// QueueName names one of the two durable queues (02 §2, 04 §2). They share
// delivery-state columns and drain machinery; they differ in who writes them
// and what a handled entry means.
type QueueName string

const (
	QueueEvents QueueName = "events" // drives the brain: one LLM pass per entry
	QueueOutbox QueueName = "outbox" // drives the machinery: mechanical execution, no LLM
)

// EventType enumerates the two 01 event types (04 §2, §6).
type EventType string

const (
	EventAgentTurnCompleted EventType = "agent.turn_completed" // from the agent-runtime module (05 §2.2)
	EventHumanVoiceInput    EventType = "human.voice_input"    // from the voice route (04 §7, 02 §9)
)

// Entry is one row of either queue, as the drain machinery sees it (04 §3).
// Kind is an EventType on the events queue and a board outbox Topic on the
// outbox queue. Payload is the emitter's snapshot, opaque to the runtime.
type Entry struct {
	ID        int64 // monotonic; the outbox id doubles as the idempotency key (03 §7)
	Kind      string
	Payload   json.RawMessage
	Attempts  int
	CreatedAt time.Time
}

// Event is an events-queue entry typed for the brain port (04 §6).
type Event struct {
	ID        int64
	Type      EventType
	Payload   json.RawMessage // shape owned by the emitter's spec (02 §8 / §9)
	CreatedAt time.Time
}

// Retry policy (04 §3, D8): backoff min(1s × 2^(attempts−1), 60s); after
// MaxAttempts the entry goes dead and the per-topic dead-letter action runs.
const (
	BackoffBase = time.Second
	BackoffCap  = time.Minute
	MaxAttempts = 8
)
