package amika

// Wire DTOs for the v0beta1 endpoints this adapter uses (05 §6). Only the
// fields the Provider port needs are decoded; the rest of each Amika object is
// intentionally ignored so a schema addition never breaks us. Provider ids,
// session ids, and job ids stay inside these structs and the returned
// agent.ProviderWorker/agent.TurnRef — never visible outside the module.

// createSandboxRequest is the POST /sandboxes body (05 §6). auto_stop_interval
// is on (cost control, restart on demand); auto_delete_interval is forced OFF
// (autoDeleteOff) so Amika never yanks a worker out from under a Blocked
// ticket (05 D6).
type createSandboxRequest struct {
	Name               string `json:"name"`
	RepoURL            string `json:"repo_url,omitempty"`
	Agent              string `json:"agent,omitempty"`
	AutoStopInterval   int    `json:"auto_stop_interval"`
	AutoDeleteInterval int    `json:"auto_delete_interval"`
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

// agentSendJobRequest is the POST …/agent-send-jobs body (05 §6): jobs, never
// the synchronous agent-send. new_session opens a fresh conversation; session_id
// continues the recorded one (omitted when unknown ⇒ Amika continues the
// sandbox's current session).
type agentSendJobRequest struct {
	Message    string `json:"message"`
	NewSession bool   `json:"new_session"`
	SessionID  string `json:"session_id,omitempty"`
}

// agentSendJob is the async job object (POST 202 and GET 200 share the shape).
// state drives terminal detection (classifyJob); like sandbox state it is not
// enumerated in v0beta1. result_text/agent_session_id decode null to "".
type agentSendJob struct {
	JobID          string  `json:"job_id"`
	State          string  `json:"state"`
	AgentSessionID string  `json:"agent_session_id"`
	IsError        bool    `json:"is_error"`
	ResultText     string  `json:"result_text"`
	CostUSD        float64 `json:"cost_usd"`
}
