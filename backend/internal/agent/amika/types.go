package amika

// Wire DTOs for the v0beta1 endpoints this adapter uses (05 §6). Only the
// fields the Provider port needs are decoded; the rest of each Amika object is
// intentionally ignored so a schema addition never breaks us. Provider ids,
// session ids, and job ids stay inside these structs and the returned
// agent.ProviderWorker/agent.TurnRef — never visible outside the module.

// createSandboxRequest is the POST /sandboxes body (05 §6). auto_stop_interval
// is on (cost control, restart on demand); auto_delete_interval is forced OFF
// (autoDeleteOff) so Amika never yanks a worker out from under a Blocked
// ticket (05 D6). repo_url and snapshot seed the workspace and are both
// omitted when unset so the environment stays optional. agent_credentials
// attaches the org's coding-agent credential — REQUIRED for API-key-created
// sandboxes to run the agent (without it the agent command fails); omitted when
// unset.
type createSandboxRequest struct {
	Name               string            `json:"name"`
	RepoURL            string            `json:"repo_url,omitempty"`
	Snapshot           string            `json:"snapshot,omitempty"`
	Agent              string            `json:"agent,omitempty"`
	AutoStopInterval   int               `json:"auto_stop_interval"`
	AutoDeleteInterval int               `json:"auto_delete_interval"`
	AgentCredentials   []agentCredential `json:"agent_credentials,omitempty"`
}

// agentCredential references an org agent credential to attach to a sandbox so
// the coding agent can authenticate (05 §9). kind is the agent family ("claude");
// id is the credential's id (AMIKA_CLAUDE_CRED_ID).
type agentCredential struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// sandbox is the subset of the Amika sandbox object we read. state drives
// WorkerReady (classifyState); its value set is NOT enumerated in v0beta1, so
// classification is defensive (see states.go, 05 §11). error_message and
// current_session_id decode a JSON null to "".
type sandbox struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	ErrorMessage     string `json:"error_message"`
	CurrentSessionID string `json:"current_session_id"`
}

// --- Synchronous-send bridge (temporary) ---
//
// Amika's async …/agent-send-jobs endpoint 500s org-wide (2026-07, "Agent launch
// failed") while the synchronous …/agent-send works. StartTurn mints a session up
// front (createSessionRequest), fires agent-send (bounding the wait; the turn keeps
// running server-side), and CheckTurn recovers the output from the session
// transcript. Restore the async agent-send-jobs types from git once Amika fixes it.
// See client.go StartTurn/CheckTurn.

const roleAssistant = "assistant"

// agentSendRequest is the POST …/agent-send body (same shape as the job request).
type agentSendRequest struct {
	Message    string `json:"message"`
	NewSession bool   `json:"new_session"`
	SessionID  string `json:"session_id,omitempty"`
}

// createSessionRequest is the POST …/sessions body: agent_name is required, so a
// fresh StartTurn can mint a conversation id before sending anything into it.
type createSessionRequest struct {
	AgentName string `json:"agent_name"`
}

// sessionObject is the subset of GET …/sessions/{id} we read: metadata.messages
// carries the transcript (user + assistant turns) CheckTurn reads back.
type sessionObject struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Metadata sessionMetadata `json:"metadata"`
}

type sessionMetadata struct {
	Messages []sessionMessage `json:"messages"`
}

type sessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
