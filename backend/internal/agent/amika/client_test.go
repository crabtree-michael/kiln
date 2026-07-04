package amika

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// Shared literals — kept as constants so the routing table and assertions read
// the same handle everywhere.
const (
	sbID   = "sb-1"
	jobID  = "job-1"
	sessID = "sess-1"

	keyCode = "error_code"
	keyMsg  = "message"
)

var (
	pathSandboxes = "/sandboxes"
	pathSandbox   = "/sandboxes/" + sbID
	pathStart     = pathSandbox + "/start"
	pathJobs      = pathSandbox + "/agent-send-jobs"
	pathJob       = pathJobs + "/" + jobID
)

// route keys a handler by "METHOD /path". A test server answers exactly the
// routes a case sets up; an unrouted request fails the test loudly so a wrong
// method/path can't pass silently.
type route struct {
	method, path string
}

// newClient spins an httptest server over the given routes and returns a Client
// pointed at it. Handlers close over the case's own *testing.T.
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

func errEnvelope(code, msg string) map[string]any {
	return map[string]any{keyCode: code, keyMsg: msg}
}

func TestListWorkersFiltersPrefixAndMapsID(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer k" {
				t.Errorf("auth header = %q, want Bearer k", got)
			}
			writeJSON(t, w, http.StatusOK, []sandbox{
				{ID: sbID, Name: agent.WorkerName("worker-a")},
				{ID: "sb-2", Name: "someone-elses-sandbox"},
				{ID: "sb-3", Name: agent.WorkerName("worker-b")},
			})
		},
	})

	got, err := c.ListWorkers(context.Background())
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	want := []agent.ProviderWorker{
		{Name: agent.WorkerName("worker-a"), Ref: sbID},
		{Name: agent.WorkerName("worker-b"), Ref: "sb-3"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d workers, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("worker[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCreateWorkerSendsConventionBody(t *testing.T) {
	c := newClient(t, Config{
		APIKey:   "k",
		RepoURL:  "https://example.com/repo.git",
		Agent:    "claude",
		AutoStop: 30 * time.Minute,
	}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			var body createSandboxRequest
			decodeBody(t, r, &body)
			if body.Name != agent.WorkerName("w1") {
				t.Errorf("name = %q", body.Name)
			}
			if body.RepoURL != "https://example.com/repo.git" {
				t.Errorf("repo_url = %q", body.RepoURL)
			}
			if body.Agent != "claude" {
				t.Errorf("agent = %q", body.Agent)
			}
			if body.AutoStopInterval != 30 {
				t.Errorf("auto_stop_interval = %d, want 30", body.AutoStopInterval)
			}
			if body.AutoDeleteInterval != autoDeleteOff {
				t.Errorf("auto_delete_interval = %d, want %d (OFF)", body.AutoDeleteInterval, autoDeleteOff)
			}
			writeJSON(t, w, http.StatusAccepted, sandbox{ID: "sb-9", Name: body.Name, State: "provisioning"})
		},
	})

	got, err := c.CreateWorker(context.Background(), agent.WorkerName("w1"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	if want := (agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: "sb-9"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestWorkerReadyStates(t *testing.T) {
	worker := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID}

	t.Run("ready", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "running"})
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err != nil || !ready {
			t.Fatalf("ready=%v err=%v, want true nil", ready, err)
		}
	})

	t.Run("provisioning keeps polling", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "provisioning"})
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err != nil || ready {
			t.Fatalf("ready=%v err=%v, want false nil", ready, err)
		}
	})

	t.Run("unknown state keeps polling", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "some_future_state"})
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err != nil || ready {
			t.Fatalf("ready=%v err=%v, want false nil (defensive)", ready, err)
		}
	})

	t.Run("stopped is woken", func(t *testing.T) {
		started := false
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "stopped"})
			},
			{http.MethodPost, pathStart}: func(w http.ResponseWriter, r *http.Request) {
				started = true
				writeJSON(t, w, http.StatusAccepted, map[string]bool{"ok": true})
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err != nil || ready {
			t.Fatalf("ready=%v err=%v, want false nil", ready, err)
		}
		if !started {
			t.Error("expected POST …/start on a stopped worker")
		}
	})

	t.Run("stopped tolerates 409 on start", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "stopped"})
			},
			{http.MethodPost, pathStart}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusConflict, errEnvelope("conflict", "already starting"))
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err != nil || ready {
			t.Fatalf("ready=%v err=%v, want false nil (409 tolerated)", ready, err)
		}
	})

	t.Run("errored surfaces error", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sandbox{ID: sbID, State: "errored", ErrorMessage: "boom"})
			},
		})
		ready, err := c.WorkerReady(context.Background(), worker)
		if err == nil || ready {
			t.Fatalf("ready=%v err=%v, want false + error", ready, err)
		}
		if !errors.Is(err, errWorkerErrored) {
			t.Errorf("err = %v, want wrapped errWorkerErrored", err)
		}
	})
}

func TestDestroyWorker(t *testing.T) {
	worker := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID}

	t.Run("ok", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodDelete, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, map[string]bool{"ok": true})
			},
		})
		if err := c.DestroyWorker(context.Background(), worker); err != nil {
			t.Fatalf("DestroyWorker: %v", err)
		}
	})

	t.Run("404 is success", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodDelete, pathSandbox}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("not_found", "gone"))
			},
		})
		if err := c.DestroyWorker(context.Background(), worker); err != nil {
			t.Fatalf("404 should be success, got %v", err)
		}
	})
}

func TestStartTurnFreshAndContinuation(t *testing.T) {
	worker := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID}

	t.Run("fresh opens new session", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodPost, pathJobs}: func(w http.ResponseWriter, r *http.Request) {
				var body agentSendJobRequest
				decodeBody(t, r, &body)
				if !body.NewSession {
					t.Error("fresh turn must set new_session=true")
				}
				if body.SessionID != "" {
					t.Errorf("fresh turn must not send session_id, got %q", body.SessionID)
				}
				if body.Message != "do the thing" {
					t.Errorf("message = %q", body.Message)
				}
				writeJSON(t, w, http.StatusAccepted, agentSendJob{JobID: jobID, State: "queued", AgentSessionID: sessID})
			},
		})
		ref, err := c.StartTurn(context.Background(), worker, "", "do the thing", true)
		if err != nil {
			t.Fatalf("StartTurn: %v", err)
		}
		if want := (agent.TurnRef{Conversation: sessID, Turn: jobID}); ref != want {
			t.Errorf("ref = %+v, want %+v", ref, want)
		}
	})

	t.Run("continuation sends recorded session", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodPost, pathJobs}: func(w http.ResponseWriter, r *http.Request) {
				var body agentSendJobRequest
				decodeBody(t, r, &body)
				if body.NewSession {
					t.Error("continuation must set new_session=false")
				}
				if body.SessionID != sessID {
					t.Errorf("session_id = %q, want %s", body.SessionID, sessID)
				}
				// 202 with a null session id — keep the recorded one.
				writeJSON(t, w, http.StatusAccepted, agentSendJob{JobID: "job-2", State: "queued"})
			},
		})
		ref, err := c.StartTurn(context.Background(), worker, sessID, "more work", false)
		if err != nil {
			t.Fatalf("StartTurn: %v", err)
		}
		if want := (agent.TurnRef{Conversation: sessID, Turn: "job-2"}); ref != want {
			t.Errorf("ref = %+v, want %+v", ref, want)
		}
	})

	t.Run("lost session maps to ErrConversationLost", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodPost, pathJobs}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("session_not_found", "unknown session id"))
			},
		})
		_, err := c.StartTurn(context.Background(), worker, "sess-gone", "continue", false)
		if !errors.Is(err, agent.ErrConversationLost) {
			t.Fatalf("err = %v, want ErrConversationLost", err)
		}
	})

	t.Run("non-session 404 is a plain error, not conversation loss", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodPost, pathJobs}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("sandbox_not_found", "no such sandbox"))
			},
		})
		_, err := c.StartTurn(context.Background(), worker, sessID, "continue", false)
		if err == nil || errors.Is(err, agent.ErrConversationLost) {
			t.Fatalf("err = %v, want a plain error (not ErrConversationLost)", err)
		}
	})
}

func TestCheckTurn(t *testing.T) {
	worker := agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID}
	check := func(t *testing.T, job agentSendJob) agent.TurnStatus {
		t.Helper()
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathJob}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, job)
			},
		})
		st, err := c.CheckTurn(context.Background(), worker, agent.TurnRef{Conversation: sessID, Turn: jobID})
		if err != nil {
			t.Fatalf("CheckTurn: %v", err)
		}
		return st
	}

	t.Run("running", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "running"})
		if !st.Running {
			t.Errorf("want Running, got %+v", st)
		}
	})

	t.Run("success", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "completed", ResultText: "all done", CostUSD: 0.42})
		want := agent.TurnStatus{Running: false, Output: "all done", IsError: false, CostUSD: 0.42}
		if st != want {
			t.Errorf("got %+v, want %+v", st, want)
		}
	})

	t.Run("agent errored with description", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "completed", IsError: true, ResultText: "tests failed"})
		if !st.IsError || st.Running || st.Output != "tests failed" {
			t.Errorf("got %+v, want error turn with output", st)
		}
	})

	t.Run("mechanical failure synthesizes a description", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "failed"})
		if !st.IsError || st.Running || st.Output == "" {
			t.Errorf("got %+v, want error turn with a synthesized description", st)
		}
	})

	t.Run("unknown state with a result is terminal", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "weird", ResultText: "output present"})
		if st.Running || st.IsError || st.Output != "output present" {
			t.Errorf("got %+v, want terminal success", st)
		}
	})

	t.Run("unknown state with no signal keeps polling", func(t *testing.T) {
		st := check(t, agentSendJob{JobID: jobID, State: "weird"})
		if !st.Running {
			t.Errorf("got %+v, want Running (defensive)", st)
		}
	})
}

func TestAPIErrorEnvelopeDecoded(t *testing.T) {
	c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
		{http.MethodGet, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			writeJSON(t, w, http.StatusBadGateway, map[string]any{
				"type":     "error",
				keyCode:    "upstream_unavailable",
				keyMsg:     "provider down",
				"trace_id": "tr-42",
			})
		},
	})

	_, err := c.ListWorkers(context.Background())
	apiErr := new(APIError)
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", apiErr.Status)
	}
	if apiErr.Code != "upstream_unavailable" || apiErr.Message != "provider down" || apiErr.TraceID != "tr-42" {
		t.Errorf("envelope not fully decoded: %+v", apiErr)
	}
}

func TestNewNormalizesConfig(t *testing.T) {
	c := New(Config{BaseURL: "https://host/api/", APIKey: "k"}, nil)
	if c.cfg.BaseURL != "https://host/api" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", c.cfg.BaseURL)
	}
	if c.cfg.Agent != DefaultAgent {
		t.Errorf("Agent = %q, want default %q", c.cfg.Agent, DefaultAgent)
	}
	empty := New(Config{}, nil)
	if empty.cfg.BaseURL != DefaultBaseURL {
		t.Errorf("empty BaseURL = %q, want default", empty.cfg.BaseURL)
	}
	if empty.cfg.AutoStop != DefaultAutoStop {
		t.Errorf("empty AutoStop = %v, want default %v (auto_stop stays on)", empty.cfg.AutoStop, DefaultAutoStop)
	}
}
