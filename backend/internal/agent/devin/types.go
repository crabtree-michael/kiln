package devin

import "encoding/json"

// Wire DTOs for the Devin v1 endpoints this adapter uses (design §4). Only the
// fields the Provider port needs are decoded; every other field Devin returns is
// intentionally ignored so a schema addition never breaks us. Session ids stay
// inside these structs and the returned agent.TurnRef — never visible outside
// the agent module (05 abstraction rule).

// createSessionRequest is the POST /v1/sessions body (design §4). prompt seeds
// the session (Kiln's first message); idempotent asks Devin to dedupe a retried
// create (design §... Devin has native idempotency, unlike Amika); snapshot_id
// seeds the workspace image and max_acu_limit caps the session's spend — both
// omitted when unset so a deployment can leave them at Devin's defaults.
type createSessionRequest struct {
	Prompt      string `json:"prompt"`
	Idempotent  bool   `json:"idempotent,omitempty"`
	Snapshot    string `json:"snapshot_id,omitempty"`
	MaxACULimit int    `json:"max_acu_limit,omitempty"`
	Title       string `json:"title,omitempty"`
}

// createSessionResponse is the POST /v1/sessions reply. session_id is the
// conversation handle the machinery persists per binding (agent_turns) and
// reuses on continuations; is_new_session reports whether Devin's idempotency
// reused an existing session (false) or opened a new one (true) — logged for
// diagnosis, it does not change the recorded handle.
type createSessionResponse struct {
	SessionID    string `json:"session_id"`
	IsNewSession bool   `json:"is_new_session"`
	URL          string `json:"url"`
}

// messageRequest is the POST /v1/sessions/{id}/messages body — how a
// continuation delivers the next instruction into an existing session (design
// §4).
type messageRequest struct {
	Message string `json:"message"`
}

// sessionDetail is the subset of GET /v1/sessions/{id} the port reads (design
// §4). status_enum drives CheckTurn's running/terminal decision (classifyStatus);
// structured_output and pull_request are the turn's surfaced output; the ACU
// fields feed the best-effort ACU→USD cost estimate (acuUsed tolerates the
// several field names the young API has used).
type sessionDetail struct {
	SessionID        string          `json:"session_id"`
	Status           string          `json:"status"`
	StatusEnum       string          `json:"status_enum"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	PullRequest      *pullRequest    `json:"pull_request"`
	Messages         []sessionMsg    `json:"messages"`

	// ACU accounting — the field name is not firmly documented and has moved;
	// acuUsed (states.go) takes whichever is set. A hardening point once a live
	// session's shape is confirmed.
	ACUUsed     float64 `json:"acu_used"`
	ACUConsumed float64 `json:"acu_consumed"`
	ACUUsage    float64 `json:"acu_usage"`
}

// statusValue returns the status string to classify — status_enum when Devin
// sends it, falling back to the older `status` field so a response carrying only
// one is still classified.
func (d sessionDetail) statusValue() string {
	if d.StatusEnum != "" {
		return d.StatusEnum
	}
	return d.Status
}

// pullRequest is the PR Devin opened for the session, if any — its URL is a
// good human-facing turn output when there is no structured_output.
type pullRequest struct {
	URL string `json:"url"`
}

// sessionMsg is one entry of a session's message list; the last assistant/Devin
// message is the fallback turn output when neither structured_output nor a PR is
// present.
type sessionMsg struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Message string `json:"message"`
	Content string `json:"content"`
}
