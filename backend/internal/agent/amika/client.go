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
//	StartTurn     — POST /sandboxes/{id}/sessions (fresh: mint a conversation id)
//	                then fire-and-forget POST …/agent-send into it. TEMPORARY: the
//	                async agent-send-jobs endpoint 500s org-wide (2026-07), so we
//	                use the working synchronous send with a bounded wait — the turn
//	                keeps running server-side past the deadline.
//	CheckTurn     — GET …/sessions/{session_id}: the turn is done once a new
//	                assistant message appears in metadata.messages; its content is
//	                the output. This path reports no is_error/cost.
//	ReadLatestOutput — GET …/sessions/latest: the transcript is materialized only
//	                at turn completion, so the last assistant message is exactly
//	                the latest finished turn (05 §2).
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
	"strconv"
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

// agentSendTimeout bounds the client's wait on /agent-send; a longer coding turn
// just trips it and keeps running server-side (see StartTurn).
const agentSendTimeout = 12 * time.Second

// errWorkerErrored is the sentinel WorkerReady wraps when a sandbox reports a
// terminal provisioning failure; the machine exhausts it into a failed turn
// (05 §5).
var errWorkerErrored = errors.New("amika: worker errored")

// errNoSessionID is wrapped when POST …/sessions returns without an id (05 §6).
var errNoSessionID = errors.New("amika: created session has no id")

// Config is read at the composition root (05 §9, 04 §8); the API key never
// leaves /backend (02 §2).
type Config struct {
	BaseURL  string        // AMIKA_BASE_URL, default DefaultBaseURL
	APIKey   string        // AMIKA_API_KEY (secret) — Bearer auth
	RepoURL  string        // AMIKA_REPO_URL — the project repo every worker clones
	Snapshot string        // AMIKA_SNAPSHOT — snapshot every worker starts from; omitted when unset
	Agent    string        // KILN_AGENT, default DefaultAgent
	AutoStop time.Duration // KILN_WORKER_AUTO_STOP, default DefaultAutoStop; auto_delete stays OFF (05 D6)
	// ClaudeCredID (AMIKA_CLAUDE_CRED_ID) is the org agent-credential id attached
	// to every created sandbox so the coding agent can authenticate. Without it,
	// API-key-created sandboxes get no credential and the agent command fails.
	ClaudeCredID string
	// WorkerPrefix (KILN_WORKER_PREFIX, default agent.WorkerNamePrefix) scopes
	// which sandboxes ListWorkers reports as this instance's — per-environment
	// isolation on a shared Amika account (05 §4, amended 2026-07-05). Must match
	// the prefix the Service builds names with (agent.WithWorkerPrefix).
	WorkerPrefix string
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
	if cfg.WorkerPrefix == "" {
		cfg.WorkerPrefix = agent.WorkerNamePrefix
	}
	return &Client{cfg: cfg, hc: hc}
}

// Close releases the idle keep-alive connections in this client's HTTP
// connection pool. It satisfies io.Closer so a per-project tenant.Providers can
// tear a superseded bundle down on credential rebuild (11 §3). Safe to call more
// than once and while requests are in flight — CloseIdleConnections only reaps
// idle connections, and in-use ones are unaffected. A no-op when the client was
// built over http.DefaultClient (the process-wide pool is left alone).
func (c *Client) Close() error {
	if c.hc != nil && c.hc != http.DefaultClient {
		c.hc.CloseIdleConnections()
	}
	return nil
}

// ListWorkers lists every sandbox and keeps the ones this instance owns
// (Config.WorkerPrefix — per-environment scope on a shared account). GET
// /sandboxes accepts no local state — adoption is pure list-and-match
// (05 §4, D5).
func (c *Client) ListWorkers(ctx context.Context) ([]agent.ProviderWorker, error) {
	var list []sandbox
	if err := c.do(ctx, http.MethodGet, "/sandboxes", nil, &list); err != nil {
		return nil, err
	}
	out := make([]agent.ProviderWorker, 0, len(list))
	for _, s := range list {
		if strings.HasPrefix(s.Name, c.cfg.WorkerPrefix) {
			out = append(out, agent.ProviderWorker{
				Name:   s.Name,
				Ref:    s.ID,
				Status: runStatus(classifyState(s.State)),
			})
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
		Snapshot:           c.cfg.Snapshot,
		Agent:              c.cfg.Agent,
		AutoStopInterval:   autoStopInterval(c.cfg.AutoStop),
		AutoDeleteInterval: autoDeleteOff,
	}
	if c.cfg.ClaudeCredID != "" {
		req.AgentCredentials = []agentCredential{{Kind: c.cfg.Agent, ID: c.cfg.ClaudeCredID}}
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

// StartTurn fires one turn on w and returns the handle CheckTurn reads.
//
// TEMPORARY BRIDGE (2026-07): Amika's async POST …/agent-send-jobs endpoint 500s
// org-wide ("Agent launch failed") while the synchronous POST …/agent-send works.
// A fresh turn first mints the conversation up front — POST …/sessions returns a
// session id without running anything — so every send is a pure fire-and-forget
// continuation into a known session, with no handle to recover afterwards. The
// client wait is bounded (agentSendTimeout); Amika keeps running the turn
// server-side past the deadline (verified), and CheckTurn reads the reply from the
// session transcript. A continuation whose session is gone maps to
// agent.ErrConversationLost, so the machine retries fresh — a new session (05 §3).
// The session id is the conversation handle the machinery persists per binding
// (agent_turns), reset to fresh on release — one session per ticket's active life.
// Revert to agent-send-jobs once Amika fixes it (05 D6).
func (c *Client) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	ref := workerRef(w)

	// Resolve the conversation: a fresh turn mints a new (empty ⇒ baseline 0)
	// session; a continuation reuses the recorded one, its existing replies the
	// baseline so CheckTurn reports only this turn's new message.
	session, baseline := conversation, 0
	if fresh {
		s, err := c.createSession(ctx, ref)
		if err != nil {
			return agent.TurnRef{}, err
		}
		session = s
	} else if msgs, err := c.assistantMessages(ctx, ref, session); err == nil {
		baseline = len(msgs)
	}

	// Fire-and-forget: bound the wait, discard the body. A tripped deadline is the
	// expected path for a real coding turn — the reply lands in the session either
	// way. Only a genuine API error (incl. a lost session) fails the turn.
	sendCtx, cancel := context.WithTimeout(ctx, agentSendTimeout)
	defer cancel()
	req := agentSendRequest{Message: message, NewSession: false, SessionID: session}
	err := c.do(sendCtx, http.MethodPost, "/sandboxes/"+url.PathEscape(ref)+"/agent-send", req, nil)
	if err != nil && !isDeadline(err) {
		if !fresh && isConversationLost(err) {
			return agent.TurnRef{}, fmt.Errorf("amika: worker %s continuation: %w", w.Name, agent.ErrConversationLost)
		}
		return agent.TurnRef{}, err
	}
	return agent.TurnRef{Conversation: session, Turn: strconv.Itoa(baseline)}, nil
}

// CheckTurn resolves a turn by reading its session transcript (the sync bridge —
// see StartTurn). The turn is done once an assistant message appears beyond the
// baseline StartTurn recorded; that message's content is the output. This path
// carries no error flag or cost, so both are reported best-effort (05 §2.2).
func (c *Client) CheckTurn(ctx context.Context, w agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	if ref.Conversation == "" {
		return agent.TurnStatus{Running: true}, nil // session not resolved yet
	}
	msgs, err := c.assistantMessages(ctx, workerRef(w), ref.Conversation)
	if err != nil {
		if statusIs(err, http.StatusNotFound) {
			return agent.TurnStatus{Running: true}, nil // session not visible yet
		}
		return agent.TurnStatus{}, err
	}
	baseline := 0
	if n, aerr := strconv.Atoi(ref.Turn); aerr == nil {
		baseline = n
	}
	if len(msgs) <= baseline {
		return agent.TurnStatus{Running: true}, nil
	}
	var b strings.Builder
	for _, m := range msgs[baseline:] {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.Content)
	}
	return agent.TurnStatus{Running: false, Output: b.String()}, nil
}

// ReadLatestOutput reads the worker's current conversation transcript
// (GET …/sessions/latest) and returns the last assistant message as the
// worker's latest completed output (05 §2). Amika materializes the transcript
// only at turn completion (verified 2026-07-04: no mid-turn/partial output),
// so this is exactly the latest finished turn. A missing session (404) or empty
// metadata is "nothing yet" — empty TurnOutput, not an error.
func (c *Client) ReadLatestOutput(ctx context.Context, w agent.ProviderWorker) (agent.TurnOutput, error) {
	ref := workerRef(w)
	var s sessionObject
	path := "/sandboxes/" + url.PathEscape(ref) + "/sessions/latest"
	if err := c.do(ctx, http.MethodGet, path, nil, &s); err != nil {
		if statusIs(err, http.StatusNotFound) {
			return agent.TurnOutput{}, nil
		}
		return agent.TurnOutput{}, err
	}
	var last *sessionMessage
	for i := range s.Metadata.Messages {
		if s.Metadata.Messages[i].Role == roleAssistant {
			last = &s.Metadata.Messages[i]
		}
	}
	if last == nil {
		return agent.TurnOutput{}, nil
	}
	at, _ := time.Parse(time.RFC3339, last.Timestamp) //nolint:errcheck // best-effort; zero At on failure
	return agent.TurnOutput{Output: last.Content, At: at}, nil
}

// createSession opens a new agent conversation on the sandbox up front (05 §6):
// POST …/sessions returns an id without running a turn, so the send that follows
// is a clean fire-and-forget continuation. agent_name is the configured agent.
func (c *Client) createSession(ctx context.Context, ref string) (string, error) {
	var s sessionObject
	req := createSessionRequest{AgentName: c.cfg.Agent}
	if err := c.do(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(ref)+"/sessions", req, &s); err != nil {
		return "", err
	}
	if s.ID == "" {
		return "", fmt.Errorf("amika: worker %s: %w", ref, errNoSessionID)
	}
	return s.ID, nil
}

// assistantMessages returns the assistant turns in a session's transcript
// (metadata.messages) — how CheckTurn recovers a fired turn's output.
func (c *Client) assistantMessages(ctx context.Context, ref, session string) ([]sessionMessage, error) {
	var s sessionObject
	path := "/sandboxes/" + url.PathEscape(ref) + "/sessions/" + url.PathEscape(session)
	if err := c.do(ctx, http.MethodGet, path, nil, &s); err != nil {
		return nil, err
	}
	out := make([]sessionMessage, 0, len(s.Metadata.Messages))
	for _, m := range s.Metadata.Messages {
		if m.Role == roleAssistant {
			out = append(out, m)
		}
	}
	return out, nil
}

// isDeadline reports whether err is (wraps) a context deadline — the signal that
// the bounded agent-send wait tripped while the turn runs on server-side.
func isDeadline(err error) bool { return errors.Is(err, context.DeadlineExceeded) }

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
