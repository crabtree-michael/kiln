package amika

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
)

// Shared literals — kept as constants so the routing table and assertions read
// the same handle everywhere.
const (
	sbID   = "sb-1"
	sbID9  = "sb-9"
	sessID = "sess-1"

	testRepoURL = "https://example.com/repo.git"

	keyCode = "error_code"
	keyMsg  = "message"
)

var (
	pathSandboxes = "/sandboxes"
	pathSandbox   = "/sandboxes/" + sbID
	pathStart     = pathSandbox + "/start"
	pathSessions  = pathSandbox + "/sessions"
	pathSession   = pathSessions + "/" + sessID
	pathSend      = pathSandbox + "/agent-send"
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
		RepoURL:  testRepoURL,
		Snapshot: "snap-42",
		Agent:    "claude",
		AutoStop: 30 * time.Minute,
	}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			var body createSandboxRequest
			decodeBody(t, r, &body)
			if body.Name != agent.WorkerName("w1") {
				t.Errorf("name = %q", body.Name)
			}
			if body.RepoURL != testRepoURL {
				t.Errorf("repo_url = %q", body.RepoURL)
			}
			if body.Snapshot != "snap-42" {
				t.Errorf("snapshot = %q", body.Snapshot)
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
			writeJSON(t, w, http.StatusAccepted, sandbox{ID: sbID9, Name: body.Name, State: "provisioning"})
		},
	})

	got, err := c.CreateWorker(context.Background(), agent.WorkerName("w1"))
	if err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}
	if want := (agent.ProviderWorker{Name: agent.WorkerName("w1"), Ref: sbID9}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An unset Snapshot (env var absent) must be omitted from the wire, not sent as
// an empty string — omitempty keeps the field optional (task: unused if unset).
func TestCreateWorkerOmitsSnapshotWhenUnset(t *testing.T) {
	c := newClient(t, Config{APIKey: "k", RepoURL: testRepoURL}, map[route]http.HandlerFunc{
		{http.MethodPost, pathSandboxes}: func(w http.ResponseWriter, r *http.Request) {
			var raw map[string]json.RawMessage
			decodeBody(t, r, &raw)
			if _, present := raw["snapshot"]; present {
				t.Errorf("snapshot key present when unset: %v", raw)
			}
			writeJSON(t, w, http.StatusAccepted, sandbox{ID: sbID9, Name: agent.WorkerName("w1")})
		},
	})

	if _, err := c.CreateWorker(context.Background(), agent.WorkerName("w1")); err != nil {
		t.Fatalf("CreateWorker: %v", err)
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

	t.Run("fresh mints a session then fires the send", func(t *testing.T) {
		var created, sent bool
		c := newClient(t, Config{APIKey: "k", Agent: DefaultAgent}, map[route]http.HandlerFunc{
			{http.MethodPost, pathSessions}: func(w http.ResponseWriter, r *http.Request) {
				var body createSessionRequest
				decodeBody(t, r, &body)
				if body.AgentName != DefaultAgent {
					t.Errorf("agent_name = %q, want %s", body.AgentName, DefaultAgent)
				}
				created = true
				writeJSON(t, w, http.StatusCreated, sessionObject{ID: sessID})
			},
			{http.MethodPost, pathSend}: func(w http.ResponseWriter, r *http.Request) {
				var body agentSendRequest
				decodeBody(t, r, &body)
				if body.NewSession {
					t.Error("send must continue the minted session (new_session=false)")
				}
				if body.SessionID != sessID {
					t.Errorf("session_id = %q, want %s", body.SessionID, sessID)
				}
				if body.Message != "do the thing" {
					t.Errorf("message = %q", body.Message)
				}
				sent = true
				writeJSON(t, w, http.StatusOK, map[string]any{})
			},
		})
		ref, err := c.StartTurn(context.Background(), worker, "", "do the thing", true)
		if err != nil {
			t.Fatalf("StartTurn: %v", err)
		}
		if !created || !sent {
			t.Fatalf("created=%v sent=%v, want both true", created, sent)
		}
		if want := (agent.TurnRef{Conversation: sessID, Turn: "0"}); ref != want {
			t.Errorf("ref = %+v, want %+v", ref, want)
		}
	})

	t.Run("continuation reuses the recorded session with a baseline", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			// One prior assistant reply in the transcript ⇒ baseline 1.
			{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sessionObject{ID: sessID, Metadata: sessionMetadata{Messages: []sessionMessage{
					{Role: "user", Content: "earlier"}, {Role: roleAssistant, Content: "prior"},
				}}})
			},
			{http.MethodPost, pathSend}: func(w http.ResponseWriter, r *http.Request) {
				var body agentSendRequest
				decodeBody(t, r, &body)
				if body.NewSession {
					t.Error("continuation must set new_session=false")
				}
				if body.SessionID != sessID {
					t.Errorf("session_id = %q, want %s", body.SessionID, sessID)
				}
				writeJSON(t, w, http.StatusOK, map[string]any{})
			},
		})
		ref, err := c.StartTurn(context.Background(), worker, sessID, "more work", false)
		if err != nil {
			t.Fatalf("StartTurn: %v", err)
		}
		if want := (agent.TurnRef{Conversation: sessID, Turn: "1"}); ref != want {
			t.Errorf("ref = %+v, want %+v", ref, want)
		}
	})

	t.Run("lost session maps to ErrConversationLost", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("session_not_found", "gone"))
			},
			{http.MethodPost, pathSend}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("session_not_found", "unknown session id"))
			},
		})
		_, err := c.StartTurn(context.Background(), worker, sessID, "continue", false)
		if !errors.Is(err, agent.ErrConversationLost) {
			t.Fatalf("err = %v, want ErrConversationLost", err)
		}
	})

	t.Run("non-session 404 is a plain error, not conversation loss", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sessionObject{ID: sessID})
			},
			{http.MethodPost, pathSend}: func(w http.ResponseWriter, r *http.Request) {
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
	// check runs CheckTurn against a session whose transcript the case supplies,
	// with the given baseline (assistant-message count recorded at StartTurn).
	check := func(t *testing.T, baseline int, msgs []sessionMessage) agent.TurnStatus {
		t.Helper()
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusOK, sessionObject{ID: sessID, Metadata: sessionMetadata{Messages: msgs}})
			},
		})
		ref := agent.TurnRef{Conversation: sessID, Turn: strconv.Itoa(baseline)}
		st, err := c.CheckTurn(context.Background(), worker, ref)
		if err != nil {
			t.Fatalf("CheckTurn: %v", err)
		}
		return st
	}
	user := func(s string) sessionMessage { return sessionMessage{Role: "user", Content: s} }
	asst := func(s string) sessionMessage { return sessionMessage{Role: roleAssistant, Content: s} }

	t.Run("no new assistant message keeps running", func(t *testing.T) {
		st := check(t, 1, []sessionMessage{user("q"), asst("prior")})
		if !st.Running {
			t.Errorf("want Running, got %+v", st)
		}
	})

	t.Run("fresh turn's first reply is the output", func(t *testing.T) {
		st := check(t, 0, []sessionMessage{user("build it"), asst("all done")})
		want := agent.TurnStatus{Running: false, Output: "all done"}
		if st != want {
			t.Errorf("got %+v, want %+v", st, want)
		}
	})

	t.Run("continuation reports only the new reply", func(t *testing.T) {
		st := check(t, 1, []sessionMessage{user("q1"), asst("prior"), user("q2"), asst("second reply")})
		if st.Running || st.Output != "second reply" {
			t.Errorf("got %+v, want done with 'second reply'", st)
		}
	})

	t.Run("missing session keeps polling", func(t *testing.T) {
		c := newClient(t, Config{APIKey: "k"}, map[route]http.HandlerFunc{
			{http.MethodGet, pathSession}: func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusNotFound, errEnvelope("session_not_found", "nope"))
			},
		})
		st, err := c.CheckTurn(context.Background(), worker, agent.TurnRef{Conversation: sessID, Turn: "0"})
		if err != nil {
			t.Fatalf("CheckTurn: %v", err)
		}
		if !st.Running {
			t.Errorf("want Running on missing session, got %+v", st)
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
