// Package amika is the Amika API v0beta1 HTTP adapter behind the agent
// module's Provider port (05 §6) — the one place Amika vocabulary is legal.
// Bearer auth, the port↔API mapping, and error-envelope translation; nothing
// else. Every rule (machine, retries, dedupe, recovery) lives in the generic
// machinery (05 §5, §7).
//
// Port ↔ v0beta1 mapping (05 §6):
//
//	ListWorkers   — GET /sandboxes, filtered to kiln-worker-* names
//	CreateWorker  — POST /sandboxes (202, async): repo_url, agent,
//	                auto_stop_interval on, auto_delete_interval OFF (05 D6)
//	WorkerReady   — GET /sandboxes/{name} (id or name — adoption relies on
//	                this); start via POST …/start if auto-stopped. Sandbox
//	                state values are NOT enumerated in v0beta1: written
//	                defensively, hardened against the real set during
//	                implementation (05 §6, §11)
//	DestroyWorker — DELETE /sandboxes/{id}; 404 = already gone = success
//	StartTurn     — POST /sandboxes/{id}/agent-send-jobs: new_session when
//	                fresh, else the recorded session id. Jobs, never the
//	                synchronous agent-send — a coding turn outlives any sane
//	                HTTP timeout (05 D6)
//	CheckTurn     — GET …/agent-send-jobs/{job_id}: terminal state +
//	                result_text / is_error / cost_usd
//
// No webhooks and no idempotency keys exist in v0beta1 — hence the module's
// poller and its own agent_turns dedupe (05 §6).
package amika

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// errNotImplemented marks scaffold stubs; see docs/specs/05-agent-runtime.md.
var errNotImplemented = errors.New("agent/amika: not implemented (scaffold)")

// Defaults for the composition-root config (05 §9).
const (
	DefaultBaseURL  = "https://app.amika.dev/api/v0beta1"
	DefaultAgent    = "claude"
	DefaultAutoStop = 30 * time.Minute
)

// Config is read at the composition root (05 §9, 04 §8); the API key never
// leaves /backend (02 §2).
type Config struct {
	BaseURL  string        // AMIKA_BASE_URL, default DefaultBaseURL
	APIKey   string        // AMIKA_API_KEY (secret) — Bearer auth
	RepoURL  string        // KILN_REPO_URL — the project repo every worker clones
	Agent    string        // KILN_AGENT, default DefaultAgent
	AutoStop time.Duration // KILN_WORKER_AUTO_STOP, default DefaultAutoStop; auto_delete stays OFF (05 D6)
}

// APIError is v0beta1's uniform error envelope over
// 400/401/403/404/409/502 — the one error-mapping layer (05 §6).
type APIError struct {
	Status  int    // HTTP status
	Code    string `json:"error_code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("amika: %d %s: %s (trace %s)", e.Status, e.Code, e.Message, e.TraceID)
}

// Client implements agent.Provider over Amika v0beta1. Sandbox, session, and
// job ids stay inside agent.ProviderWorker/agent.TurnRef as opaque handles —
// never visible outside the agent module (05 §6).
type Client struct {
	cfg Config
	hc  *http.Client
}

var _ agent.Provider = (*Client)(nil)

// New builds the adapter; hc nil means http.DefaultClient.
func New(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{cfg: cfg, hc: hc}
}

func (c *Client) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	return nil, errNotImplemented
}

func (c *Client) CreateWorker(ctx context.Context, name string) (agent.ProviderWorker, error) {
	return agent.ProviderWorker{}, errNotImplemented
}

func (c *Client) WorkerReady(ctx context.Context, w agent.ProviderWorker) (bool, error) {
	return false, errNotImplemented
}

func (c *Client) DestroyWorker(ctx context.Context, w agent.ProviderWorker) error {
	return errNotImplemented
}

func (c *Client) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	return agent.TurnRef{}, errNotImplemented
}

func (c *Client) CheckTurn(ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	return agent.TurnStatus{}, errNotImplemented
}
