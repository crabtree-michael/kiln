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
//	                defensively (states.go), hardened against the real set
//	                during implementation (05 §6, §11)
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// Defaults for the composition-root config (05 §9).
const (
	DefaultBaseURL  = "https://app.amika.dev/api/v0beta1"
	DefaultAgent    = "claude"
	DefaultAutoStop = 30 * time.Minute
)

// maxErrorBody caps how much of an error response we read before giving up on
// the envelope — a defensive bound, envelopes are tiny.
const maxErrorBody = 1 << 20

// errWorkerErrored is the sentinel WorkerReady wraps when a sandbox reports a
// terminal provisioning failure; the machine exhausts it into a failed turn
// (05 §5).
var errWorkerErrored = errors.New("amika: worker errored")

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
	Status  int    `json:"-"` // HTTP status (set from the response, not the body)
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

// New builds the adapter; hc nil means http.DefaultClient. Empty
// BaseURL/Agent/AutoStop fall back to the defaults (auto_stop stays on per
// 05 §9, D6) and a trailing slash is trimmed so path joins are clean.
func New(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.AutoStop == 0 {
		cfg.AutoStop = DefaultAutoStop
	}
	if cfg.Agent == "" {
		cfg.Agent = DefaultAgent
	}
	return &Client{cfg: cfg, hc: hc}
}

// ListWorkers lists every sandbox and keeps the ones this module owns
// (WorkerNamePrefix). GET /sandboxes accepts no local state — adoption is pure
// list-and-match (05 §4, D5).
func (c *Client) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	var list []sandbox
	if err := c.do(ctx, http.MethodGet, "/sandboxes", nil, &list); err != nil {
		return nil, err
	}
	out := make([]agent.ProviderWorker, 0, len(list))
	for _, s := range list {
		if strings.HasPrefix(s.Name, agent.WorkerNamePrefix) {
			out = append(out, agent.ProviderWorker{Name: s.Name, Ref: s.ID})
		}
	}
	return out, nil
}

// CreateWorker provisions a sandbox under name. Provisioning is async (202);
// readiness is WorkerReady's job (05 §2.3). auto_stop on, auto_delete OFF (D6).
func (c *Client) CreateWorker(ctx context.Context, name string) (agent.ProviderWorker, error) {
	req := createSandboxRequest{
		Name:               name,
		RepoURL:            c.cfg.RepoURL,
		Agent:              c.cfg.Agent,
		AutoStopInterval:   autoStopInterval(c.cfg.AutoStop),
		AutoDeleteInterval: autoDeleteOff,
	}
	var s sandbox
	if err := c.do(ctx, http.MethodPost, "/sandboxes", req, &s); err != nil {
		return agent.ProviderWorker{}, err
	}
	return agent.ProviderWorker{Name: firstNonEmpty(s.Name, name), Ref: s.ID}, nil
}

// WorkerReady reads the sandbox and classifies its (un-enumerated) state
// defensively (states.go). An auto-stopped worker is woken and reported
// not-ready-yet; a terminally errored sandbox is an error the machine exhausts
// into a failed turn (05 §5, §6).
func (c *Client) WorkerReady(ctx context.Context, w agent.ProviderWorker) (bool, error) {
	ref := workerRef(w)
	var s sandbox
	if err := c.do(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(ref), nil, &s); err != nil {
		return false, err
	}
	switch classifyState(s.State) {
	case sbReady:
		return true, nil
	case sbStopped:
		if err := c.startSandbox(ctx, ref); err != nil {
			return false, err
		}
		return false, nil // starting; ready on a later poll
	case sbErrored:
		return false, fmt.Errorf("%w: worker %s (state=%q): %s", errWorkerErrored, w.Name, s.State, s.ErrorMessage)
	case sbProvisioning:
		return false, nil // provisioning or unknown ⇒ keep polling
	default:
		return false, nil
	}
}

// DestroyWorker deletes the sandbox; an already-absent one (404) is success
// (05 §2.3).
func (c *Client) DestroyWorker(ctx context.Context, w agent.ProviderWorker) error {
	err := c.do(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(workerRef(w)), nil, nil)
	if statusIs(err, http.StatusNotFound) {
		return nil
	}
	return err
}

// StartTurn enqueues an async agent-send job (never the synchronous send).
// fresh opens a new conversation; otherwise the recorded session id is
// continued (omitted when empty ⇒ Amika continues the sandbox's current
// session). A continuation whose session the provider no longer recognises is
// mapped to agent.ErrConversationLost so the machine falls back to a fresh
// conversation, same message — context lost, workspace kept (05 §3).
func (c *Client) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	req := agentSendJobRequest{Message: message, NewSession: fresh}
	if !fresh && conversation != "" {
		req.SessionID = conversation
	}
	var job agentSendJob
	path := "/sandboxes/" + url.PathEscape(workerRef(w)) + "/agent-send-jobs"
	if err := c.do(ctx, http.MethodPost, path, req, &job); err != nil {
		if !fresh && isConversationLost(err) {
			return agent.TurnRef{}, fmt.Errorf("amika: worker %s continuation: %w", w.Name, agent.ErrConversationLost)
		}
		return agent.TurnRef{}, err
	}
	// The session id is the conversation handle recorded for the next turn.
	// v0beta1 may leave it null at enqueue time (the job assigns it as it
	// runs); keep the passed conversation so a continuation's record stays
	// stable, and CheckTurn's later reads surface the real one.
	return agent.TurnRef{
		Conversation: firstNonEmpty(job.AgentSessionID, conversation),
		Turn:         job.JobID,
	}, nil
}

// CheckTurn polls one job to its terminal outcome (05 §2.3). Terminal
// detection is defensive over the un-enumerated job state (states.go); a
// mechanically-failed or agent-errored job surfaces IsError with a description
// so the brain owns what it means (05 §2.2, D3).
func (c *Client) CheckTurn(ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	var job agentSendJob
	path := "/sandboxes/" + url.PathEscape(workerRef(w)) + "/agent-send-jobs/" + url.PathEscape(ref.Turn)
	if err := c.do(ctx, http.MethodGet, path, nil, &job); err != nil {
		return agent.TurnStatus{}, err
	}
	phase := classifyJob(job)
	if phase == jobRunning {
		return agent.TurnStatus{Running: true}, nil
	}
	isErr := job.IsError || phase == jobFailed
	output := job.ResultText
	if isErr && strings.TrimSpace(output) == "" {
		output = fmt.Sprintf("agent turn failed (state=%q)", job.State)
	}
	return agent.TurnStatus{Running: false, Output: output, IsError: isErr, CostUSD: job.CostUSD}, nil
}

// startSandbox wakes an auto-stopped sandbox. A 409 (already
// starting/running) is not an error — the state read that follows will report
// readiness (05 §6).
func (c *Client) startSandbox(ctx context.Context, ref string) error {
	err := c.do(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(ref)+"/start", nil, nil)
	if statusIs(err, http.StatusConflict) {
		return nil
	}
	return err
}

// do issues one JSON request and decodes the response. body nil sends no body;
// out nil discards the response body. Any status >= 400 is decoded into the
// v0beta1 error envelope (*APIError) — the single mapping layer (05 §6).
func (c *Client) do(ctx context.Context, method, path string, body, out any) (err error) {
	req, err := c.buildRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("amika: %s %s: %w", method, path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("amika: close %s %s: %w", method, path, cerr)
		}
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return decodeAPIError(resp)
	}
	if out == nil {
		return nil
	}
	if derr := json.NewDecoder(resp.Body).Decode(out); derr != nil {
		return fmt.Errorf("amika: decode %s %s: %w", method, path, derr)
	}
	return nil
}

// buildRequest marshals body (nil ⇒ no body) and stamps the Bearer auth and
// JSON headers (05 §6, §9).
func (c *Client) buildRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("amika: marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("amika: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// decodeAPIError reads the uniform error envelope; a body that isn't the
// envelope still yields an *APIError carrying the status and raw text so no
// failure is swallowed (05 §6).
func decodeAPIError(resp *http.Response) error {
	e := &APIError{Status: resp.StatusCode}
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if rerr != nil {
		e.Message = http.StatusText(resp.StatusCode)
		return e
	}
	if uerr := json.Unmarshal(raw, e); uerr != nil {
		// Body wasn't the envelope — fall back to raw text below.
		e.Code, e.Message, e.TraceID = "", "", ""
	}
	if e.Message == "" {
		if txt := strings.TrimSpace(string(raw)); txt != "" {
			e.Message = txt
		} else {
			e.Message = http.StatusText(resp.StatusCode)
		}
	}
	return e
}

// isConversationLost reports whether a StartTurn continuation failed because
// the provider no longer has the session — a 4xx that names the session. Maps
// to agent.ErrConversationLost (05 §3). A hardening point: v0beta1 does not
// document per-error codes, so this matches on the session mention.
func isConversationLost(err error) bool {
	apiErr := new(APIError)
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Status {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict:
		return strings.Contains(strings.ToLower(apiErr.Code+" "+apiErr.Message), "session")
	}
	return false
}

// statusIs reports whether err is an *APIError with the given HTTP status.
func statusIs(err error, status int) bool {
	apiErr := new(APIError)
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// workerRef prefers the provider id, falling back to the deterministic name
// (GET/DELETE /sandboxes/{id} accept either — 05 §6).
func workerRef(w agent.ProviderWorker) string {
	if w.Ref != "" {
		return w.Ref
	}
	return w.Name
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
