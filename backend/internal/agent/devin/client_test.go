package devin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

const (
	sessID = "devin-session-1"
	//nolint:gosec // G101: a fake bearer token for the httptest server, not a real credential.
	testToken = "cog_test_key"
)

var (
	pathSessions = "/v1/sessions"
	pathSession  = pathSessions + "/" + sessID
	pathMessages = pathSession + "/messages"
)

// route keys a handler by "METHOD /path"; an unrouted request fails the test
// loudly so a wrong method/path can't pass silently (mirrors the amika harness).
type route struct{ method, path string }

func newClient(t *testing.T, cfg Config, routes map[route]http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := routes[route{r.Method, r.URL.Path}]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	if cfg.APIKey == "" {
		cfg.APIKey = testToken
	}
	return New(cfg, srv.Client())
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func decodeBody(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Errorf("decode request body: %v", err)
	}
}

// --- Virtual worker lifecycle: no remote calls (design §4, D5). ---

func TestVirtualWorkerLifecycleMakesNoRemoteCalls(t *testing.T) {
	// No routes registered: any HTTP call fails the test. The four lifecycle calls
	// must be entirely local for a session-only provider.
	c := newClient(t, Config{}, map[route]http.HandlerFunc{})
	ctx := context.Background()

	ws, err := c.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(ws) != 0 {
		t.Errorf("ListWorkers must be empty for Devin, got %+v", ws)
	}

	w, err := c.CreateWorker(ctx, "kiln-worker-slot-1")
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	if w.Name != "kiln-worker-slot-1" || w.Ref != "" {
		t.Errorf("CreateWorker = %+v, want a synthetic worker with empty Ref", w)
	}

	ready, err := c.WorkerReady(ctx, w)
	if err != nil || !ready {
		t.Errorf("WorkerReady = (%v, %v), want (true, nil)", ready, err)
	}
	if err := c.DestroyWorker(ctx, w); err != nil {
		t.Errorf("DestroyWorker = %v, want nil (best-effort no-op)", err)
	}
}

func TestCapabilitiesReportSessionShape(t *testing.T) {
	got := agent.CapabilitiesOf(New(Config{}, nil))
	want := agent.Capabilities{ManagedSandbox: false, ReportsCost: true, Snapshots: true, SecretsInject: false}
	if got != want {
		t.Fatalf("devin capabilities = %+v, want %+v", got, want)
	}
}

// --- StartTurn: lazy session create + continuation (design §4). ---

func TestStartTurnFreshMintsSessionFromPrompt(t *testing.T) {
	var body createSessionRequest
	c := newClient(t, Config{Snapshot: "snap-9", MaxACULimit: 20}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSessions}: func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
				t.Errorf("auth header = %q, want Bearer %s", got, testToken)
			}
			decodeBody(t, r, &body)
			writeJSON(t, w, http.StatusOK, createSessionResponse{SessionID: sessID, IsNewSession: true})
		},
	})

	ref, err := c.StartTurn(context.Background(), agent.ProviderWorker{Name: "w1"}, "", "build the thing", true)
	if err != nil {
		t.Fatalf("StartTurn fresh: %v", err)
	}
	if ref.Conversation != sessID {
		t.Errorf("conversation handle = %q, want the session id %q", ref.Conversation, sessID)
	}
	if body.Prompt != "build the thing" {
		t.Errorf("prompt = %q, want the message", body.Prompt)
	}
	if !body.Idempotent {
		t.Errorf("idempotent flag = false, want true by default")
	}
	if body.Snapshot != "snap-9" || body.MaxACULimit != 20 {
		t.Errorf("snapshot/max_acu = %q/%d, want snap-9/20 from config", body.Snapshot, body.MaxACULimit)
	}
}

func TestStartTurnFreshWithoutSessionIDErrors(t *testing.T) {
	c := newClient(t, Config{}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSessions}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusOK, createSessionResponse{}) // no session_id
		},
	})
	_, err := c.StartTurn(context.Background(), agent.ProviderWorker{}, "", "m", true)
	if !errors.Is(err, errNoSessionID) {
		t.Fatalf("StartTurn err = %v, want errNoSessionID", err)
	}
}

func TestStartTurnContinuationPostsMessage(t *testing.T) {
	var body messageRequest
	c := newClient(t, Config{}, map[route]http.HandlerFunc{
		{http.MethodPost, pathMessages}: func(w http.ResponseWriter, r *http.Request) {
			decodeBody(t, r, &body)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	ref, err := c.StartTurn(context.Background(), agent.ProviderWorker{}, sessID, "keep going", false)
	if err != nil {
		t.Fatalf("StartTurn continuation: %v", err)
	}
	if ref.Conversation != sessID {
		t.Errorf("continuation conversation = %q, want %q", ref.Conversation, sessID)
	}
	if body.Message != "keep going" {
		t.Errorf("message body = %q, want the continuation message", body.Message)
	}
}

func TestStartTurnContinuationLostMapsToSentinel(t *testing.T) {
	c := newClient(t, Config{}, map[route]http.HandlerFunc{
		{http.MethodPost, pathMessages}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusNotFound, map[string]string{"error": "session not found"})
		},
	})
	_, err := c.StartTurn(context.Background(), agent.ProviderWorker{}, sessID, "m", false)
	if !errors.Is(err, agent.ErrConversationLost) {
		t.Fatalf("continuation-lost err = %v, want agent.ErrConversationLost", err)
	}
}

// --- CheckTurn: status_enum classification + output + cost (design §4). ---

func TestCheckTurnRunningKeepsPolling(t *testing.T) {
	c := newClient(t, Config{}, sessionRoute(t, sessionDetail{SessionID: sessID, StatusEnum: "running"}))
	st, err := c.CheckTurn(context.Background(), agent.ProviderWorker{}, agent.TurnRef{Conversation: sessID})
	if err != nil {
		t.Fatalf("CheckTurn: %v", err)
	}
	if !st.Running {
		t.Errorf("running session must report Running=true, got %+v", st)
	}
}

func TestCheckTurnBlockedIsDoneWithOutputAndCost(t *testing.T) {
	// "blocked" = the agent finished and is waiting for the user — a completed
	// turn, NOT an error. structured_output is the output; ACUs → USD cost.
	detail := sessionDetail{
		SessionID:        sessID,
		StatusEnum:       "blocked",
		StructuredOutput: json.RawMessage(`"opened PR #42"`),
		ACUUsed:          3,
	}
	c := newClient(t, Config{ACUToUSD: 2}, sessionRoute(t, detail))
	st, err := c.CheckTurn(context.Background(), agent.ProviderWorker{}, agent.TurnRef{Conversation: sessID})
	if err != nil {
		t.Fatalf("CheckTurn: %v", err)
	}
	if st.Running || st.IsError {
		t.Errorf("blocked session must be a completed, non-error turn, got %+v", st)
	}
	if st.Output != "opened PR #42" {
		t.Errorf("output = %q, want the structured_output string", st.Output)
	}
	if st.CostUSD != 6 { // 3 ACU * $2
		t.Errorf("cost = %v, want 6 (3 ACU * $2)", st.CostUSD)
	}
}

func TestCheckTurnFallsBackToPullRequestURL(t *testing.T) {
	detail := sessionDetail{SessionID: sessID, StatusEnum: "finished", PullRequest: &pullRequest{URL: "https://gh/pr/7"}}
	c := newClient(t, Config{}, sessionRoute(t, detail))
	st, err := c.CheckTurn(context.Background(), agent.ProviderWorker{}, agent.TurnRef{Conversation: sessID})
	if err != nil {
		t.Fatalf("CheckTurn: %v", err)
	}
	if st.Output != "https://gh/pr/7" {
		t.Errorf("output = %q, want the PR url when there is no structured_output", st.Output)
	}
}

func TestCheckTurnExpiredIsTerminalError(t *testing.T) {
	c := newClient(t, Config{}, sessionRoute(t, sessionDetail{SessionID: sessID, StatusEnum: "expired"}))
	st, err := c.CheckTurn(context.Background(), agent.ProviderWorker{}, agent.TurnRef{Conversation: sessID})
	if err != nil {
		t.Fatalf("CheckTurn: %v", err)
	}
	if st.Running || !st.IsError {
		t.Errorf("expired session must be a terminal error outcome, got %+v", st)
	}
}

func TestCheckTurnUnknownSessionKeepsPolling(t *testing.T) {
	c := newClient(t, Config{}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	st, err := c.CheckTurn(context.Background(), agent.ProviderWorker{}, agent.TurnRef{Conversation: sessID})
	if err != nil {
		t.Fatalf("CheckTurn 404: %v", err)
	}
	if !st.Running {
		t.Errorf("a not-yet-visible session (404) must keep polling, got %+v", st)
	}
}

// --- Fail-fast billing: ACU exhaustion → ErrOutOfCredits (05 §5). ---

func TestCreateOutOfCreditsMapsToSentinel(t *testing.T) {
	c := newClient(t, Config{}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSessions}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusPaymentRequired, map[string]string{"error": "ACU limit reached"})
		},
	})
	_, err := c.StartTurn(context.Background(), agent.ProviderWorker{}, "", "m", true)
	if !errors.Is(err, agent.ErrOutOfCredits) {
		t.Fatalf("out-of-credits err = %v, want agent.ErrOutOfCredits", err)
	}
}

// --- ReadLatestOutput degrades to empty for a session-only provider. ---

func TestReadLatestOutputIsEmpty(t *testing.T) {
	c := newClient(t, Config{}, map[route]http.HandlerFunc{})
	out, err := c.ReadLatestOutput(context.Background(), agent.ProviderWorker{Name: "w1"})
	if err != nil {
		t.Fatalf("ReadLatestOutput: %v", err)
	}
	if out != (agent.TurnOutput{}) {
		t.Errorf("ReadLatestOutput = %+v, want empty (no session handle on the worker)", out)
	}
}

// --- State classification unit table (states.go). ---

func TestClassifyStatus(t *testing.T) {
	cases := map[string]turnPhase{
		"running":          devinRunning,
		"working":          devinRunning,
		"":                 devinRunning, // unknown ⇒ keep polling (safe default)
		"something_new":    devinRunning,
		"blocked":          devinDone,
		"waiting_for_user": devinDone,
		"finished":         devinDone,
		"stopped":          devinDone,
		"expired":          devinExpired,
		"terminated":       devinExpired,
		"failed":           devinErrored,
		"cancelled":        devinErrored,
	}
	for status, want := range cases {
		if got := classifyStatus(status); got != want {
			t.Errorf("classifyStatus(%q) = %d, want %d", status, got, want)
		}
	}
}

// sessionRoute is the common GET /v1/sessions/{id} handler returning detail.
func sessionRoute(t *testing.T, detail sessionDetail) map[route]http.HandlerFunc {
	t.Helper()
	return map[route]http.HandlerFunc{
		{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusOK, detail)
		},
	}
}
