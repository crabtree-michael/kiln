package brain

import (
	"encoding/json"
	"time"
)

// EventType enumerates the two 01 event types this module decodes (06 §3.3).
// Mirrors runtime.EventType (04 §2, §6) by value, not by import — this
// module does not depend on internal/runtime (see doc.go); the composition
// root maps runtime.EventType <-> EventType when it adapts runtime.Event to
// Event.
type EventType string

const (
	EventHumanMessage       EventType = "human.message"        // 07 A1: {text}
	EventAgentTurnCompleted EventType = "agent.turn_completed" // 05 §2.2 payload
)

// Event is one events-queue entry as the brain decodes it (06 §3.3): tagged
// with its queue id, carrying the emitter's opaque payload. Structurally
// mirrors runtime.Event; kept as this module's own type per doc.go's
// no-runtime-import rule.
type Event struct {
	ID        int64
	Type      EventType
	Payload   json.RawMessage
	CreatedAt time.Time
}

// HumanMessagePayload is the human.message event payload (06 §3.3, 07 A1):
// the user's text, verbatim.
type HumanMessagePayload struct {
	Text string `json:"text"`
}

// AgentTurnCompletedPayload is the agent.turn_completed event payload as the
// brain decodes it (06 §3.3), mirroring agent.TurnCompleted's JSON shape
// (05 §2.2) by value — this module does not import internal/agent, for the
// same reason it does not import internal/runtime (doc.go). Output is the
// agent's turn output or the failure description, subject to the ~8k
// head+tail truncation budget (AgentOutputTruncateBytes) before it enters a
// pass's context — the brain judges outcomes, it does not re-review diffs
// (06 §3.3).
type AgentTurnCompletedPayload struct {
	TicketID string  `json:"ticket_id"`
	WorkerID string  `json:"worker_id"`
	IsError  bool    `json:"is_error"`
	Output   string  `json:"output"`
	CostUSD  float64 `json:"cost_usd"`
}

// AgentOutputTruncateBytes bounds agent.turn_completed's Output field before
// it enters a pass's context (06 §3.3): ~8k chars, head + tail, with an
// elision marker in between. The truncation itself happens during context
// assembly inside the (stubbed) pass — see service.go.
const AgentOutputTruncateBytes = 8000

// MessageRole is a transcript message's speaker (07 §3).
type MessageRole string

const (
	RoleUser MessageRole = "user"
	RoleKiln MessageRole = "kiln"
)

// Message is one transcript row as the brain reads it through
// ConversationReader (07 §3): oldest first when returned by Recent.
type Message struct {
	Role MessageRole
	Text string
	At   time.Time
}

// TranscriptWindow is how much conversation history enters every pass
// (06 §3.2, D2): the last 20 messages, oldest first — enough to cover any
// plausible shaping exchange without inviting "the brain didn't know X" bugs
// from over-trimming.
const TranscriptWindow = 20

// PassInput is one pass's assembled context (06 §3 amended): built fresh every
// HandleEvent call, never held between events (06 §9). The board is no longer
// injected — the model pulls it on demand via list_tickets / get_ticket
// (06 §4 amended) — so only two blocks go into the user message after the fixed
// system prompt (prompt.go):
//
//  1. Transcript — the last TranscriptWindow messages, oldest first (06 §3.2).
//  2. Event — the triggering event, tagged with its queue id (06 §3.3).
type PassInput struct {
	Transcript []Message
	Event      Event
}

// Update is one active feed card as the brain reads it through FeedReader
// (06 §4 amended, 08 §7), mirroring the runtime's Notification by value — brain
// cannot import internal/runtime (doc.go), so the cmd/kiln adapter converts
// runtime.Notification → brain.Update. ID is the notification id the model
// supplies to edit_update / retract_update; TicketID/ImageURL are "" when unset.
type Update struct {
	ID        int64
	Kind      string
	TicketID  string
	Body      string
	ImageURL  string
	CreatedAt time.Time
}

// AgentStatus / AgentInfo / AgentUpdate mirror the agent runtime's neutral
// inspector shapes by value (06 §4, amended) — brain cannot import internal/agent
// (same rule as the runtime payloads above), so the cmd/kiln adapter converts
// agent.AgentInfo → brain.AgentInfo. No provider handle ever appears here.
type AgentStatus string

const (
	AgentBuilding AgentStatus = "building" // alive + a turn in flight
	AgentIdle     AgentStatus = "idle"     // alive + no turn
	AgentStopped  AgentStatus = "stopped"  // session auto-stopped / not running
	AgentErrored  AgentStatus = "errored"  // terminal session failure
	AgentStarting AgentStatus = "starting" // session provisioning
)

type AgentInfo struct {
	WorkerID  string
	TicketID  string
	Status    AgentStatus
	UpdatedAt time.Time
}

type AgentUpdate struct {
	WorkerID     string
	Status       AgentStatus
	LatestOutput string
	IsError      bool
	At           time.Time
}

// RepoResult is the neutral outcome of one bash tool command against the
// project clone (RepoShell.Run), mirroring internal/repo's Result by value —
// brain cannot import internal/repo (same rule as the runtime/agent payloads
// above), so the cmd/kiln adapter converts repo.Result → brain.RepoResult.
// Output is the command's combined stdout+stderr; a non-zero ExitCode is a
// normal result the model reads, not an error. TimedOut / Truncated flag a
// wall-clock timeout or an output cap. Unavailable (with Reason) means the
// clone could not be set up — the tool reports it instead of erroring the pass.
type RepoResult struct {
	Output      string
	ExitCode    int
	TimedOut    bool
	Truncated   bool
	Unavailable bool
	Reason      string
}

// RepoVerify is the neutral outcome of a RepoShell merge-gate check, mirroring
// internal/repo's Verify by value (same import rule as RepoResult). OnMain is
// true only when the commit is an ancestor of origin/main (VerifyOnMain); InPR
// is true only when the commit is associated with a pull request (VerifyInPR).
// The gate mode picks which check runs, so only its field is meaningful per
// call. Unavailable (with Reason) means the clone could not be used; the done
// gate fails closed on it. Reason explains a negative result.
//
// On a positive result URL and Ref name the completed work on GitHub so the done
// feed card can link to it: a commit page + abbreviated SHA for VerifyOnMain, or
// the pull request page + "#<number>" for VerifyInPR. Summary is the landed
// work's one-line description (commit subject or PR title) for the card body (08
// §7). Empty otherwise.
type RepoVerify struct {
	OnMain      bool
	InPR        bool
	URL         string
	Ref         string
	Summary     string
	Unavailable bool
	Reason      string
}
