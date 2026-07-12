// Package devin is the Devin (Cognition) v1 HTTP adapter behind the agent
// module's Provider port (multi-provider design §4) — the one place Devin
// vocabulary is legal. Bearer auth, the port↔API mapping, error-envelope
// translation, and status/cost classification; nothing else. Every rule
// (machine, retries, dedupe, recovery) lives in the generic machinery (05 §5,
// §7), unchanged.
//
// Devin is the *virtual-worker* provider (design §4, D5): it has no
// caller-managed sandbox to list, create, or destroy — the execution
// environment lives inside an ephemeral session. So the four worker-lifecycle
// port calls are satisfied virtually, and the session is created lazily on the
// first StartTurn(fresh):
//
//	ListWorkers   — returns EMPTY (Devin has no persistent workspace to
//	                enumerate). The reconciler's adopt-first sweep then always
//	                "creates" (a no-op) and never has an orphan to destroy.
//	CreateWorker  — NO remote call; returns a synthetic ProviderWorker{Name,
//	                Ref: ""}. The Devin session is minted on StartTurn(fresh).
//	WorkerReady   — always true (nothing to wake).
//	DestroyWorker — no-op (best-effort): the worker carries no session handle —
//	                the session id lives in the turn's TurnRef, not the worker —
//	                so there is nothing to end here. A release recorded by the
//	                machine makes the next StartTurn fresh, which opens a clean
//	                session; "fresh workspace" is automatic (Devin sessions are
//	                ephemeral).
//	StartTurn     — fresh: POST /v1/sessions {prompt, idempotent, snapshot_id,
//	                max_acu_limit}, recording session_id as the conversation
//	                handle; else POST /v1/sessions/{conv}/messages.
//	CheckTurn     — GET /v1/sessions/{id}: classify status_enum
//	                (running/blocked-done/expired/errored — states.go), surface
//	                structured_output / PR as output, estimate cost from ACUs.
//	ReadLatestOutput — returns empty (the worker carries no session handle to
//	                read a conversation from); "nothing yet", not an error.
//
// Devin has native idempotency (the `idempotent` create flag + is_new_session),
// so the module's agent_turns dedupe is redundant for it but harmless — it stays
// (design "what already exists"). No webhooks: the module's poller drives it.
package devin

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

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// Defaults for the composition-root config (design §6).
const (
	// DefaultBaseURL is Devin's hosted API root; endpoints hang off /v1.
	DefaultBaseURL = "https://api.devin.ai"
	// DefaultACUToUSD is the best-effort ACU→USD rate used to fill
	// TurnStatus.CostUSD (design: ACU↔USD normalization beyond an estimate is out
	// of scope). Overridable per deployment; a live rate should track Devin's
	// current pricing.
	DefaultACUToUSD = 2.25
)

// maxErrorBody caps how much of an error response we read before giving up on
// the envelope — a defensive bound, envelopes are tiny.
const maxErrorBody = 1 << 20

// errNoSessionID is wrapped when POST /v1/sessions returns without a session id.
var errNoSessionID = errors.New("devin: created session has no session_id")

// Config is read at the composition root (design §6, §7). The API key never
// leaves /backend (02 §2).
type Config struct {
	// BaseURL is DEVIN_BASE_URL, default DefaultBaseURL.
	BaseURL string
	// APIKey is the Devin bearer token — a `cog_…` service key OR a personal
	// access token; both authenticate identically (design §4), so the adapter
	// makes no distinction. Secret; Bearer auth.
	APIKey string
	// Snapshot is the snapshot_id every created session starts from; omitted when
	// unset (Devin's Snapshots capability).
	Snapshot string
	// MaxACULimit caps a session's ACU spend (max_acu_limit); 0 omits it, leaving
	// Devin's account default.
	MaxACULimit int
	// ACUToUSD is the USD-per-ACU rate for the best-effort cost estimate; <= 0
	// falls back to DefaultACUToUSD.
	ACUToUSD float64
}

// APIError is Devin's error envelope over the 4xx/5xx responses this adapter
// sees — the one error-mapping layer (design §4). Devin's error bodies are not
// uniformly documented, so decodeAPIError is defensive: it keeps the status and
// whatever message/code it can read.
type APIError struct {
	Status  int    `json:"-"` // HTTP status (set from the response, not the body)
	Code    string `json:"error_code"`
	Message string `json:"error"`
	Detail  string `json:"detail"`
}

func (e *APIError) Error() string {
	msg := firstNonEmpty(e.Message, e.Detail)
	return fmt.Sprintf("devin: %d %s: %s", e.Status, e.Code, msg)
}

// ProviderErrorFields exposes the scrub-safe diagnostics — HTTP status and error
// code — to the provider-neutral core for logging, omitting the free-text
// message. Satisfies agent.ProviderErrorFields.
func (e *APIError) ProviderErrorFields() (int, string, string) {
	return e.Status, e.Code, ""
}

// Client implements agent.Provider over Devin v1. Session ids stay inside
// agent.TurnRef as opaque handles — never visible outside the agent module
// (05 abstraction rule).
type Client struct {
	cfg Config
	hc  *http.Client
}

var (
	_ agent.Provider           = (*Client)(nil)
	_ agent.CapabilityReporter = (*Client)(nil)
)

// New builds the adapter; hc nil means http.DefaultClient. Empty BaseURL /
// ACUToUSD fall back to defaults, and a trailing slash is trimmed so path joins
// are clean.
func New(cfg Config, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.ACUToUSD <= 0 {
		cfg.ACUToUSD = DefaultACUToUSD
	}
	return &Client{cfg: cfg, hc: hc}
}

// Capabilities declares Devin's shape to the provider-neutral core (design §5):
// NO managed sandbox (sessions are ephemeral, not caller-managed workspaces), a
// best-effort cost estimate, snapshot support, and no caller-supplied secret
// injection. The core reads these to hide sandbox-only operator affordances for
// Devin without ever naming it (05 abstraction rule).
func (c *Client) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		ManagedSandbox: false,
		ReportsCost:    true,
		Snapshots:      true,
		SecretsInject:  false,
	}
}

// Close releases idle keep-alive connections so a per-project tenant.Providers
// can tear a superseded bundle down on credential rebuild (11 §3). A no-op over
// http.DefaultClient.
func (c *Client) Close() error {
	if c.hc != nil && c.hc != http.DefaultClient {
		c.hc.CloseIdleConnections()
	}
	return nil
}

// ListWorkers returns empty: Devin has no persistent workspace to enumerate
// (design §4). Adoption then always "creates" (a virtual no-op) and the orphan
// sweep has nothing to destroy — the reconciler degrades cleanly.
func (c *Client) ListWorkers(context.Context) ([]agent.ProviderWorker, error) {
	return []agent.ProviderWorker{}, nil
}

// CreateWorker makes NO remote call (design §4, D5): it returns a synthetic
// virtual worker with an empty Ref. The Devin session is minted lazily on the
// first StartTurn(fresh), matching Devin's ephemeral, per-conversation model.
func (c *Client) CreateWorker(_ context.Context, name string) (agent.ProviderWorker, error) {
	return agent.ProviderWorker{Name: name, Ref: "", Status: agent.RunReady}, nil
}

// WorkerReady is always true: a virtual worker has nothing to wake (design §4).
func (c *Client) WorkerReady(context.Context, agent.ProviderWorker) (bool, error) {
	return true, nil
}

// DestroyWorker is a best-effort no-op (design §4). The worker carries no
// session handle — the session id lives in the turn's TurnRef, not the
// ProviderWorker — so there is nothing to end here. The machine records the
// release, which makes the next StartTurn fresh; that opens a clean session, so
// the fresh-workspace semantics hold automatically (Devin sessions are
// ephemeral). Success, like an already-absent worker (05 §2.3).
func (c *Client) DestroyWorker(context.Context, agent.ProviderWorker) error {
	return nil
}

// StartTurn fires one turn on w and returns the handle CheckTurn reads. fresh
// mints a new Devin session from the message (POST /v1/sessions), recording its
// session_id as the conversation handle; a continuation posts the message into
// the recorded session (POST /v1/sessions/{id}/messages). A continuation whose
// session Devin no longer has maps to agent.ErrConversationLost, so the machine
// retries fresh — a new session, context lost, never a failed ticket (05 §3).
func (c *Client) StartTurn(
	ctx context.Context, _ agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	if fresh {
		return c.startFresh(ctx, message)
	}
	return c.startContinuation(ctx, conversation, message)
}

// CheckTurn polls the session to its terminal outcome (design §4). status_enum
// classifies running vs done vs terminal-failure (states.go); a done session's
// output is its structured_output / PR / last message, and cost is the
// best-effort ACU→USD estimate. An expired session mid-turn is reported as a
// terminal error outcome (the turn completes as an error event; the brain
// decides what it means), not a transient error the machine burns retries on.
func (c *Client) CheckTurn(ctx context.Context, _ agent.ProviderWorker, ref agent.TurnRef) (agent.TurnStatus, error) {
	if ref.Conversation == "" {
		return agent.TurnStatus{Running: true}, nil // session not resolved yet
	}
	var d sessionDetail
	path := "/v1/sessions/" + url.PathEscape(ref.Conversation)
	if err := c.do(ctx, http.MethodGet, path, nil, &d); err != nil {
		if statusIs(err, http.StatusNotFound) {
			return agent.TurnStatus{Running: true}, nil // not visible yet; keep polling
		}
		return agent.TurnStatus{}, err
	}
	switch classifyStatus(d.statusValue()) {
	case devinRunning:
		return agent.TurnStatus{Running: true}, nil
	case devinDone:
		return agent.TurnStatus{Running: false, Output: sessionOutput(d), CostUSD: c.costUSD(d)}, nil
	case devinExpired:
		return agent.TurnStatus{
			Running: false, IsError: true,
			Output: "the agent session expired before finishing", CostUSD: c.costUSD(d),
		}, nil
	case devinErrored:
		return agent.TurnStatus{
			Running: false, IsError: true,
			Output: firstNonEmpty(sessionOutput(d), "the agent session failed"), CostUSD: c.costUSD(d),
		}, nil
	default:
		return agent.TurnStatus{Running: true}, nil
	}
}

// ReadLatestOutput returns empty for Devin: a ProviderWorker carries no session
// handle (the session id lives in the turn's TurnRef), so the adapter cannot map
// a worker back to a conversation to read. Empty TurnOutput is "no completed
// turn yet", never an error (05 §2) — the neutral inspector degrades to
// status-only for a session-shaped provider.
func (c *Client) ReadLatestOutput(context.Context, agent.ProviderWorker) (agent.TurnOutput, error) {
	return agent.TurnOutput{}, nil
}

// startFresh mints a session from the prompt (design §4). idempotent lets a
// replayed create reuse the same session; is_new_session reports which happened
// (not load-bearing here — the recorded handle is the same either way).
func (c *Client) startFresh(ctx context.Context, message string) (agent.TurnRef, error) {
	// idempotent is always on: Devin's native create-dedupe makes a replayed
	// StartTurn(fresh) reuse the same session rather than open a duplicate
	// (design §4), a free safety net the machine's own retry budget benefits from.
	req := createSessionRequest{
		Prompt:      message,
		Idempotent:  true,
		Snapshot:    c.cfg.Snapshot,
		MaxACULimit: c.cfg.MaxACULimit,
	}
	var resp createSessionResponse
	if err := c.do(ctx, http.MethodPost, "/v1/sessions", req, &resp); err != nil {
		return agent.TurnRef{}, err
	}
	if resp.SessionID == "" {
		return agent.TurnRef{}, fmt.Errorf("%w", errNoSessionID)
	}
	return agent.TurnRef{Conversation: resp.SessionID, Turn: resp.SessionID}, nil
}

// startContinuation delivers the next message into an existing session. A 404 /
// expired-session rejection maps to agent.ErrConversationLost (05 §3).
func (c *Client) startContinuation(ctx context.Context, conversation, message string) (agent.TurnRef, error) {
	if conversation == "" {
		// No recorded session to continue — fall back to a fresh one so the turn
		// still runs (defensive; markContinuation shouldn't produce this).
		return c.startFresh(ctx, message)
	}
	path := "/v1/sessions/" + url.PathEscape(conversation) + "/messages"
	if err := c.do(ctx, http.MethodPost, path, messageRequest{Message: message}, nil); err != nil {
		if isConversationLost(err) {
			return agent.TurnRef{}, fmt.Errorf("devin: session %s continuation: %w", conversation, agent.ErrConversationLost)
		}
		return agent.TurnRef{}, err
	}
	return agent.TurnRef{Conversation: conversation, Turn: conversation}, nil
}

// sessionOutput renders a done session's turn output for the agent.turn_completed
// event, preferring the richest signal available: Devin's structured_output, then
// the opened pull request's URL, then the last assistant/Devin message. Empty
// when the session produced none of these.
func sessionOutput(d sessionDetail) string {
	if out := renderStructured(d.StructuredOutput); out != "" {
		return out
	}
	if d.PullRequest != nil && d.PullRequest.URL != "" {
		return d.PullRequest.URL
	}
	return lastMessage(d.Messages)
}

// renderStructured turns Devin's structured_output (an arbitrary JSON value)
// into a string: a JSON string is unquoted to its content; anything else is
// rendered as compact JSON. Empty for a null/absent value.
func renderStructured(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// lastMessage returns the content of the last agent-authored message in the
// session, tolerating the two shapes Devin has used (message vs content, typed
// vs role-tagged). Empty when there is none.
func lastMessage(msgs []sessionMsg) string {
	last := ""
	for _, m := range msgs {
		if text := firstNonEmpty(m.Message, m.Content); text != "" {
			last = text
		}
	}
	return last
}

// costUSD converts the session's consumed ACUs to a best-effort USD figure
// (design: ACU→USD is an estimate; richer usage modelling is out of scope). 0
// when Devin reported no ACU usage.
func (c *Client) costUSD(d sessionDetail) float64 {
	return acuUsed(d) * c.cfg.ACUToUSD
}

// do issues one JSON request and decodes the response. body nil sends no body;
// out nil discards the response body. Any status >= 400 is decoded into
// *APIError, with a credit-exhaustion rejection mapped to the neutral
// agent.ErrOutOfCredits sentinel so the machine fails the turn now rather than
// retrying a doomed call (05 §5).
func (c *Client) do(ctx context.Context, method, path string, body, out any) (err error) {
	req, err := c.buildRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("devin: %s %s: %w", method, path, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("devin: close %s %s: %w", method, path, cerr)
		}
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		apiErr := decodeAPIError(resp)
		if isOutOfCredits(apiErr) {
			return fmt.Errorf("devin: %s %s: %w: %w", method, path, agent.ErrOutOfCredits, apiErr)
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if derr := json.NewDecoder(resp.Body).Decode(out); derr != nil {
		return fmt.Errorf("devin: decode %s %s: %w", method, path, derr)
	}
	return nil
}

// buildRequest marshals body (nil ⇒ no body) and stamps the Bearer auth and
// JSON headers. Auth is Authorization: Bearer <APIKey> — a cog_ service key or a
// PAT, indistinguishable to the adapter (design §4).
func (c *Client) buildRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("devin: marshal %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("devin: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// decodeAPIError reads the error body defensively; a body that isn't the
// envelope still yields an *APIError carrying the status and raw text so no
// failure is swallowed.
func decodeAPIError(resp *http.Response) error {
	e := &APIError{Status: resp.StatusCode}
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if rerr != nil {
		e.Message = http.StatusText(resp.StatusCode)
		return e
	}
	if uerr := json.Unmarshal(raw, e); uerr != nil {
		e.Code, e.Message, e.Detail = "", "", ""
	}
	if e.Message == "" && e.Detail == "" {
		if txt := strings.TrimSpace(string(raw)); txt != "" {
			e.Message = txt
		} else {
			e.Message = http.StatusText(resp.StatusCode)
		}
	}
	return e
}

// isConversationLost reports whether a StartTurn continuation failed because
// Devin no longer has the session — a 404, or a 400/409/410 that names the
// session/expiry. Maps to agent.ErrConversationLost (05 §3). A hardening point:
// tighten against the real not-found envelope once observed.
func isConversationLost(err error) bool {
	apiErr := new(APIError)
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Status {
	case http.StatusNotFound, http.StatusGone:
		return true
	case http.StatusBadRequest, http.StatusConflict:
		hay := strings.ToLower(apiErr.Code + " " + apiErr.Message + " " + apiErr.Detail)
		return strings.Contains(hay, "session") || strings.Contains(hay, "expired")
	default:
		return false
	}
}

// isOutOfCredits reports whether err is a Devin rejection for exhausted ACU
// budget / billing — a 402 Payment Required, or any envelope whose code/message
// names an ACU/credit/quota/billing stop. Maps to agent.ErrOutOfCredits (05 §5).
// A hardening point: tighten against the real ACU-limit envelope once observed.
func isOutOfCredits(err error) bool {
	apiErr := new(APIError)
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status == http.StatusPaymentRequired {
		return true
	}
	hay := strings.ToLower(apiErr.Code + " " + apiErr.Message + " " + apiErr.Detail)
	for _, kw := range []string{"acu", "credit", "quota", "insufficient", "balance", "billing", "limit reached"} {
		if strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}

// statusIs reports whether err is an *APIError with the given HTTP status.
func statusIs(err error, status int) bool {
	apiErr := new(APIError)
	return errors.As(err, &apiErr) && apiErr.Status == status
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
