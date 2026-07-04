# Voice Pipeline (spec 09) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add speech input in front of the existing `POST /api/message` seam — AssemblyAI streaming STT driven from the client, with a backend-minted temporary token — so the user can open the app and talk. **Kiln does not speak** (spec 09 §10 A1: no TTS anywhere).

**Architecture:** The client opens AssemblyAI's Universal-Streaming WebSocket directly (audio never transits our backend); the only backend addition is `POST /api/voice/token`, which mints a short-lived AssemblyAI token so the API key never leaves `/backend` (02 §2 trust boundary). On AssemblyAI's automatic end-of-turn, the client POSTs the final transcript to the unchanged `/api/message` — a voice utterance becomes the same `human.message` event a typed message produces.

**Tech Stack:** Go (net/http, api module + new `internal/voice/assemblyai` infra client); TypeScript/React (new `frontend/src/voice/` module: pure commit state machine + AssemblyAI socket/mic client + dock wiring); wire contract in `/schema` (OpenAPI → generated Go + TS).

## Global Constraints

- **Hard gate is a wall** (02 §4): `make check` = lint → typecheck/build → unit + integration must be green before commit. Never weaken a check.
- **Wire contract lives in `/schema`** — never hand-edit `backend/internal/wire/generated.go` or `frontend/src/schema/generated.ts`; change `schema/openapi.yaml` and run `make schema` (02 §3, §4).
- **Frontend escape-hatch ban** (02 §4b): no `any`, no `as`. Narrow `unknown` with runtime type guards, mirroring `transport.ts`.
- **Work behind interfaces** (02 §2): the api handler depends on a narrow port; the concrete AssemblyAI client lives in infra, wired only at the composition root (`cmd/kiln`).
- **STT provider is AssemblyAI** (09 §10 D1). Token TTL ≤ 10 min (09 §6). Client streams to AssemblyAI directly with the temp token (09 §2, D2).
- **Real-service test hygiene** (memory): the gated smoke test uses the real key from `.env`; it never runs in the default gate.

### AssemblyAI API reference (verified 2026-07)

- **Token mint (backend → AssemblyAI):** `GET https://streaming.assemblyai.com/v3/token?expires_in_seconds=<1..600>`, header `Authorization: <ASSEMBLYAI_API_KEY>`. Response `{"token": string, "expires_in_seconds": integer}`.
- **Client WebSocket:** `wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le&token=<token>&format_turns=true`. Client sends binary PCM16 mono 16 kHz frames. Receives JSON messages: `{"type":"Begin",...}`; `{"type":"Turn","transcript":string,"end_of_turn":bool,"turn_is_formatted":bool,"words":[...]}`. Close with `{"type":"Terminate"}`.
- End-of-turn commit trigger = a `Turn` with `end_of_turn=true && turn_is_formatted=true` (the formatted final). Everything else with a `transcript` is a partial.

---

## File structure

**Schema (Task 1 — serial, first):**
- Modify `schema/openapi.yaml` — add `POST /api/voice/token` path + `VoiceToken` schema.
- Regenerated (do not hand-edit): `backend/internal/wire/generated.go`, `frontend/src/schema/generated.ts`.

**Backend (Task 2 — parallel):**
- Create `backend/internal/voice/assemblyai/client.go` — the AssemblyAI token infra adapter (the only file that knows AssemblyAI's HTTP protocol).
- Create `backend/internal/voice/assemblyai/client_test.go` — table-driven against an `httptest.Server`.
- Modify `backend/internal/api/routes.go` — `VoiceTokenMinter` port, `handleVoiceToken`, route mount, `NewServer` param.
- Modify `backend/internal/api/routes_test.go`, `backend/internal/api/stream_test.go`, `backend/internal/api/fakes_test.go` — pass a `fakeVoiceTokenMinter`; add `handleVoiceToken` tests.
- Modify `backend/cmd/kiln/main.go` — `Config.AssemblyAIAPIKey` / `AssemblyAIBaseURL`, load from env.
- Modify `backend/cmd/kiln/wiring.go` — construct the AssemblyAI client, pass to `NewServer`.
- Modify `docker-compose.yml` — pass `ASSEMBLYAI_API_KEY` to backend env.

**Frontend (Task 3 — parallel):**
- Create `frontend/src/voice/commit-machine.ts` — pure reducer + types (the unit-test target).
- Create `frontend/src/voice/commit-machine.test.ts` — the §8 scripted-provider cases.
- Create `frontend/src/voice/assemblyai-client.ts` — getUserMedia + AudioWorklet PCM16 downsample + WS send/decode → neutral `VoiceProviderEvent`.
- Create `frontend/src/voice/pcm-worklet.ts` — the AudioWorklet processor (downsample Float32 → PCM16).
- Create `frontend/src/voice/voice-store.tsx` + `frontend/src/voice/voice-context.ts` — React glue: owns the machine via `useReducer`, mic lifecycle side-effects (token fetch, `visibilitychange`), exposes `{micState, settledText, tailText, pause, resume, cancel}`.
- Modify `frontend/src/transport/transport.ts` — add `fetchVoiceToken()` + `VoiceToken` type + guard.
- Modify `frontend/src/components/Dock.tsx` — consume the voice store; render the four states, transcript, X. Keep the existing `data-role`/`aria-label="Talk"` selectors working.
- Modify `frontend/src/components/PrimaryScreen.tsx` (or `App.tsx`) — wrap the tree in `VoiceProvider` so `Dock` has context.
- Create `frontend/src/components/Dock.test.tsx` — state rendering + interaction (pause/cancel).

**E2E (Task 4 — serial, last, gated):**
- Create `tests/tests/voice-token-mints.spec.ts` — gated real-service smoke: mint a token, open the socket with a canned PCM clip, assert a `human.message` lands with non-empty text.

---

## Task 1: Wire contract — `/api/voice/token` + `VoiceToken` (serial, first)

**Files:**
- Modify: `schema/openapi.yaml`
- Regenerate: `backend/internal/wire/generated.go`, `frontend/src/schema/generated.ts`

**Produces:** Go `wire.VoiceToken{Token string; ExpiresAt time.Time}`; TS `components['schemas']['VoiceToken']` = `{ token: string; expires_at: string }`.

- [ ] **Step 1:** Add the path under `paths:` in `schema/openapi.yaml` (after `/api/tickets/{id}/accept`, before `/api/stream`):

```yaml
  /api/voice/token:
    post:
      operationId: postVoiceToken
      summary: Mint a short-lived AssemblyAI streaming token (09 §6).
      description: >
        The client calls this, then opens AssemblyAI's Universal-Streaming
        WebSocket directly with the returned token — audio never transits the
        Kiln backend, and the real API key never leaves /backend (09 §2, 02 §2).
        Thin handler over the STT-provider port; the concrete AssemblyAI client
        lives in infra and is wired at the composition root. Token expiry is
        <= 10 min (09 §6).
      responses:
        '200':
          description: A temporary streaming token and its absolute expiry.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/VoiceToken'
        '502':
          description: The STT provider failed to mint a token.
```

- [ ] **Step 2:** Add the schema under `components/schemas:` (after `SayEvent`):

```yaml
    VoiceToken:
      type: object
      description: >
        POST /api/voice/token's 200 body (09 §2, §6): a short-lived AssemblyAI
        Universal-Streaming token and its absolute expiry. The client opens the
        STT WebSocket with `token` and refreshes proactively before `expires_at`.
      required: [token, expires_at]
      properties:
        token:
          type: string
          description: The temporary AssemblyAI streaming token (bearer for the wss connection).
        expires_at:
          type: string
          format: date-time
          description: Absolute expiry (backend-computed from the provider's redemption window).
      additionalProperties: false
```

- [ ] **Step 3:** Regenerate both sides.

Run: `cd /Users/mac/Desktop/kiln/.claude/worktrees/voice-pipeline-09 && make schema`
Expected: `backend/internal/wire/generated.go` gains a `VoiceToken` struct; `frontend/src/schema/generated.ts` gains a `VoiceToken` schema. No other diff.

- [ ] **Step 4:** Verify it compiles / typechecks and commit.

Run: `cd backend && go build ./...` (PASS) and `cd ../frontend && pnpm run typecheck` (PASS).

```bash
git add schema/openapi.yaml backend/internal/wire/generated.go frontend/src/schema/generated.ts
git commit -m "feat(09): add /api/voice/token wire contract"
```

---

## Task 2: Backend — AssemblyAI token client + route + wiring (parallel with Task 3)

**Interfaces:**
- Consumes: `wire.VoiceToken` (Task 1).
- Produces: `api.VoiceTokenMinter` port `MintStreamingToken(ctx) (token string, expiresAt time.Time, err error)`; `assemblyai.New(assemblyai.Config) *Client` satisfying it; `NewServer(..., minter VoiceTokenMinter)` new trailing param.

### 2a. AssemblyAI infra client

- [ ] **Step 1: Write the failing test** — `backend/internal/voice/assemblyai/client_test.go`. Cover happy path (200 → token + expiresAt ≈ now+ttl), provider non-200 → error, missing `token` in body → error. Use `httptest.NewServer`; assert the request is `GET /v3/token`, `expires_in_seconds` query present, `Authorization` header == the key.

```go
package assemblyai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/voice/assemblyai"
)

func TestMintStreamingToken_HappyPath(t *testing.T) {
	var gotAuth, gotQuery, gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("expires_in_seconds")
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok-abc","expires_in_seconds":480}`))
	}))
	defer ts.Close()

	c := assemblyai.New(assemblyai.Config{APIKey: "key-123", BaseURL: ts.URL, TTL: 480 * time.Second})
	before := time.Now()
	tok, exp, err := c.MintStreamingToken(context.Background())
	if err != nil {
		t.Fatalf("MintStreamingToken: %v", err)
	}
	if tok != "tok-abc" {
		t.Errorf("token = %q, want tok-abc", tok)
	}
	if gotMethod != http.MethodGet || gotPath != "/v3/token" {
		t.Errorf("request = %s %s, want GET /v3/token", gotMethod, gotPath)
	}
	if gotAuth != "key-123" {
		t.Errorf("Authorization = %q, want key-123", gotAuth)
	}
	if gotQuery != "480" {
		t.Errorf("expires_in_seconds = %q, want 480", gotQuery)
	}
	if exp.Before(before.Add(470*time.Second)) || exp.After(time.Now().Add(490*time.Second)) {
		t.Errorf("expiresAt = %v, not ~now+480s", exp)
	}
}

func TestMintStreamingToken_ProviderError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()
	c := assemblyai.New(assemblyai.Config{APIKey: "bad", BaseURL: ts.URL, TTL: 480 * time.Second})
	if _, _, err := c.MintStreamingToken(context.Background()); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestMintStreamingToken_NoToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"expires_in_seconds":480}`))
	}))
	defer ts.Close()
	c := assemblyai.New(assemblyai.Config{APIKey: "k", BaseURL: ts.URL, TTL: 480 * time.Second})
	if _, _, err := c.MintStreamingToken(context.Background()); err == nil {
		t.Fatal("expected error on empty token, got nil")
	}
}
```

- [ ] **Step 2: Run it, watch it fail** (`cd backend && go test ./internal/voice/...` → build failure: package doesn't exist).

- [ ] **Step 3: Implement** `backend/internal/voice/assemblyai/client.go`:

```go
// Package assemblyai is the AssemblyAI Universal-Streaming HTTP adapter behind
// the api module's VoiceTokenMinter port (09 §6) — the one place AssemblyAI
// vocabulary is legal. It mints the short-lived streaming token the client
// uses to open the STT WebSocket directly; audio never transits our backend
// (09 §2), and the API key never leaves /backend (02 §2).
//
// Endpoint (v3): GET {BaseURL}/v3/token?expires_in_seconds=<ttl>, Authorization
// header = the raw API key. Response {token, expires_in_seconds}; expiresAt is
// computed here (now + provider window) so the port returns an absolute time.
package assemblyai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// DefaultBaseURL is AssemblyAI's streaming host (09 §2). DefaultTTL is the
// token redemption window; <= 10 min per 09 §6.
const (
	DefaultBaseURL = "https://streaming.assemblyai.com"
	DefaultTTL     = 8 * time.Minute
)

// maxErrorBody caps how much of a provider error response we read.
const maxErrorBody = 1 << 20

// Config is read at the composition root (04 §8); the API key never leaves
// /backend (02 §2).
type Config struct {
	APIKey  string        // ASSEMBLYAI_API_KEY (secret) — Authorization header
	BaseURL string        // ASSEMBLYAI_BASE_URL, default DefaultBaseURL
	TTL     time.Duration // token redemption window, default DefaultTTL (<= 10 min)
}

// Client is the AssemblyAI token adapter. It satisfies api.VoiceTokenMinter.
type Client struct {
	apiKey  string
	baseURL string
	ttl     time.Duration
	http    *http.Client
}

// New builds a Client, applying defaults for BaseURL and TTL.
func New(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Client{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		ttl:     ttl,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// tokenResponse is AssemblyAI's GET /v3/token body.
type tokenResponse struct {
	Token           string `json:"token"`
	ExpiresInSecond int    `json:"expires_in_seconds"`
}

// MintStreamingToken mints a short-lived streaming token (09 §6). expiresAt is
// now + the requested TTL — the absolute expiry the client refreshes before.
func (c *Client) MintStreamingToken(ctx context.Context) (string, time.Time, error) {
	secs := int(c.ttl / time.Second)
	if secs < 1 {
		secs = 1
	}
	u := fmt.Sprintf("%s/v3/token?%s", c.baseURL, url.Values{
		"expires_in_seconds": {strconv.Itoa(secs)},
	}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assemblyai: build token request: %w", err)
	}
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assemblyai: mint token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return "", time.Time{}, fmt.Errorf("assemblyai: mint token: status %d: %s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", time.Time{}, fmt.Errorf("assemblyai: decode token: %w", err)
	}
	if tr.Token == "" {
		return "", time.Time{}, fmt.Errorf("assemblyai: mint token: empty token in response")
	}
	return tr.Token, time.Now().Add(c.ttl), nil
}
```

- [ ] **Step 4: Run it, watch it pass** (`cd backend && go test ./internal/voice/...` → PASS).

### 2b. api port + handler

- [ ] **Step 5: Write the failing handler test** in `backend/internal/api/routes_test.go`. Add a `fakeVoiceTokenMinter` to `fakes_test.go` and a test that `POST /api/voice/token` returns 200 with the wire shape, and that a mint error → 502. Follow the existing `routes_test.go` httptest helper style. Add the minter to every `api.NewServer(...)` call site (`routes_test.go`, `stream_test.go`).

```go
// fakes_test.go — add:
type fakeVoiceTokenMinter struct {
	token string
	exp   time.Time
	err   error
}

func (f *fakeVoiceTokenMinter) MintStreamingToken(context.Context) (string, time.Time, error) {
	return f.token, f.exp, f.err
}
```

```go
// routes_test.go — add (adjust the shared server helper to take a minter, or add one):
func TestVoiceToken_HappyPath(t *testing.T) {
	exp := time.Now().Add(8 * time.Minute).UTC()
	minter := &fakeVoiceTokenMinter{token: "tok-xyz", exp: exp}
	srv := api.NewServer(&fakeBoardReader{}, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, minter) // hub per existing helper
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/voice/token", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got wire.VoiceToken
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Token != "tok-xyz" {
		t.Errorf("token = %q, want tok-xyz", got.Token)
	}
}

func TestVoiceToken_MintError(t *testing.T) {
	minter := &fakeVoiceTokenMinter{err: errors.New("provider down")}
	srv := api.NewServer(&fakeBoardReader{}, &fakeMessagePoster{}, &fakeMessagesReader{},
		&fakeFeedReader{}, &fakeSeenAcker{}, hub, minter)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, _ := http.Post(ts.URL+"/api/voice/token", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}
```

(Note: match the exact fake constructor names already in `fakes_test.go`/`routes_test.go`; the helper may build boards/hub differently — reuse it, only threading the new minter param.)

- [ ] **Step 6: Run it, watch it fail** (compile error: `NewServer` arity, no `handleVoiceToken`).

- [ ] **Step 7: Implement** in `backend/internal/api/routes.go`:
  - Add the port near the other ports:

```go
// VoiceTokenMinter is the api's port onto the STT provider's temporary-token
// mint (09 §6): a short-lived AssemblyAI streaming token the client uses to
// open the STT socket directly, so the real API key never leaves the backend
// (09 §2, 02 §2). One method, so tests fake it trivially and a provider swap
// touches one adapter. Satisfied by *voice/assemblyai.Client.
type VoiceTokenMinter interface {
	MintStreamingToken(ctx context.Context) (token string, expiresAt time.Time, err error)
}
```

  - Add `voice VoiceTokenMinter` to the `Server` struct; add it as the trailing `NewServer` param and assign it.
  - Mount the route in `Handler()`: `mux.HandleFunc("POST /api/voice/token", s.handleVoiceToken)`.
  - Add the handler:

```go
// handleVoiceToken mints a short-lived AssemblyAI streaming token (09 §6) and
// returns it with its absolute expiry. The client opens the STT WebSocket
// directly with this token; audio never transits our backend (09 §2). A
// provider mint failure is a 502 — the client's one silent reconnect then
// Retry surface handles it (09 §5).
func (s *Server) handleVoiceToken(w http.ResponseWriter, r *http.Request) {
	token, expiresAt, err := s.voice.MintStreamingToken(r.Context())
	if err != nil {
		slog.Error("api: mint voice token", "err", err)
		http.Error(w, "mint voice token", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, wire.VoiceToken{Token: token, ExpiresAt: expiresAt})
}
```

  - Add `"time"` to imports.

- [ ] **Step 8: Run it, watch it pass** (`cd backend && go test ./internal/api/...` → PASS).

### 2c. Composition root + compose

- [ ] **Step 9:** `backend/cmd/kiln/main.go` — add to `Config`:

```go
	AssemblyAIAPIKey  string // ASSEMBLYAI_API_KEY — the STT provider's token-mint credential (09 §6)
	AssemblyAIBaseURL string // ASSEMBLYAI_BASE_URL — override the streaming host (default in-adapter)
```

  and in `loadConfig()`:

```go
		AssemblyAIAPIKey:  os.Getenv("ASSEMBLYAI_API_KEY"),
		AssemblyAIBaseURL: os.Getenv("ASSEMBLYAI_BASE_URL"),
```

- [ ] **Step 10:** `backend/cmd/kiln/wiring.go` — in `buildGraph`, construct the client and pass it to `NewServer`:

```go
	voiceMinter := assemblyai.New(assemblyai.Config{
		APIKey:  cfg.AssemblyAIAPIKey,
		BaseURL: cfg.AssemblyAIBaseURL,
	})
	server := api.NewServer(boardSvc, rtSvc, rtSvc, rtSvc, rtSvc, hub, voiceMinter)
```

  Add the import `"github.com/crabtree-michael/kiln/backend/internal/voice/assemblyai"`.

- [ ] **Step 11:** `docker-compose.yml` — under `backend.environment`, add (next to the AMIKA keys):

```yaml
      # STT provider (09 §2, §6). The backend mints a short-lived AssemblyAI
      # streaming token; the browser opens the STT socket directly. Key never
      # leaves the backend (02 §2). Sourced from .env.
      ASSEMBLYAI_API_KEY: ${ASSEMBLYAI_API_KEY:-}
```

- [ ] **Step 12: Run backend gate + commit.**

Run: `cd backend && gofmt -l . && go build ./... && go test ./...` (all PASS, gofmt clean).

```bash
git add backend/ docker-compose.yml
git commit -m "feat(09): backend AssemblyAI token mint + /api/voice/token route"
```

---

## Task 3: Frontend — commit state machine, AssemblyAI client, dock wiring (parallel with Task 2)

**Interfaces:**
- Consumes: `wire.VoiceToken` (Task 1) via `fetchVoiceToken(): Promise<{ token: string; expires_at: string }>`.
- Produces: `commit-machine.ts` reducer + types; `useVoice()` hook returning `{ micState, settledText, tailText, pause, resume, cancel }`; a functional `Dock` consuming it.

### 3a. Commit state machine (pure — the unit-test target, spec §8)

- [ ] **Step 1: Write the failing test** `frontend/src/voice/commit-machine.test.ts`. The machine is a pure reducer over provider events + user actions. States: `listening | paused | denied | retry`. It emits **commit intents** (the reducer returns a `commit?: string` side-effect field the caller acts on) rather than doing I/O. §8 cases:

```ts
import { describe, it, expect } from 'vitest';
import { initialVoiceState, voiceReducer } from '@/voice/commit-machine';

describe('commit machine', () => {
  it('partials then formatted final -> exactly one commit', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello wor' } });
    expect(s.tailText).toBe('hello wor');
    expect(s.commit).toBeUndefined();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello world' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello, world.' } });
    expect(s.commit).toBe('Hello, world.');
    expect(s.settledText).toBe('Hello, world.');
    expect(s.tailText).toBe('');
    // next tick: caller clears the consumed commit
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.commit).toBeUndefined();
  });

  it('empty / whitespace final -> no commit', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: '   ' } });
    expect(s.commit).toBeUndefined();
  });

  it('X during tail -> no commit, tail cleared', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'never mind' } });
    s = voiceReducer(s, { type: 'cancel' });
    expect(s.commit).toBeUndefined();
    expect(s.tailText).toBe('');
    expect(s.micState).toBe('listening');
  });

  it('socket drop -> retry, preserves un-committed transcript', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'half a thought' } });
    s = voiceReducer(s, { type: 'providerFailed' }); // after the one silent reconnect already failed
    expect(s.micState).toBe('retry');
    expect(s.tailText).toBe('half a thought');
  });

  it('pause survives background/foreground', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'pause' });
    expect(s.micState).toBe('paused');
    s = voiceReducer(s, { type: 'background' });
    s = voiceReducer(s, { type: 'foreground' });
    expect(s.micState).toBe('paused'); // explicit pause is sticky
  });

  it('foreground from an auto-stopped (background) listen resumes listening', () => {
    let s = initialVoiceState(); // starts listening
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'background' });
    expect(s.micState).toBe('listening'); // still the desired state; socket closed by the store
    s = voiceReducer(s, { type: 'foreground' });
    expect(s.micState).toBe('listening');
  });

  it('permission denied -> denied state', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'denied' });
    expect(s.micState).toBe('denied');
  });
});
```

- [ ] **Step 2: Run it, watch it fail** (`cd frontend && pnpm test commit-machine` → module not found).

- [ ] **Step 3: Implement** `frontend/src/voice/commit-machine.ts`:

```ts
// The voice commit state machine (09 §3–§4): pure logic, no I/O — the unit-test
// target. It owns the mic states and the utterance-commit rules, consuming
// neutral provider events + user actions. It never calls the network: on an
// end-of-turn final it stamps `commit` with the text to POST; the store acts on
// it and dispatches `commitConsumed`. The provider client (assemblyai-client)
// and the store (voice-store) supply the I/O around this reducer.

/** The four mic states (09 §3). `listening` is the resting/default state. */
export type MicState = 'listening' | 'paused' | 'denied' | 'retry';

/** Neutral provider events — the AssemblyAI protocol is decoded to these
 *  (09 §7 provider client) so the machine is provider-agnostic and testable. */
export type VoiceProviderEvent =
  | { kind: 'open' }
  | { kind: 'partial'; text: string } // still-forming turn -> ghosted tail
  | { kind: 'final'; text: string } // formatted end-of-turn transcript -> settle + commit
  | { kind: 'error' }
  | { kind: 'close' };

/** User + lifecycle actions the store dispatches. */
export type VoiceAction =
  | { type: 'provider'; event: VoiceProviderEvent }
  | { type: 'providerFailed' } // the one silent reconnect (09 §5) already failed
  | { type: 'pause' }
  | { type: 'resume' }
  | { type: 'cancel' } // the X — discard the un-committed utterance (09 §4)
  | { type: 'denied' }
  | { type: 'background' } // visibilitychange -> hidden (09 §3)
  | { type: 'foreground' } // visibilitychange -> visible
  | { type: 'commitConsumed' }; // the store POSTed the pending commit

export interface VoiceState {
  micState: MicState;
  settledText: string; // committed/finalized text, in ink
  tailText: string; // still-forming partial, ghosted
  /** Set for one tick when an utterance is ready to POST; the store consumes it. */
  commit?: string;
}

export function initialVoiceState(): VoiceState {
  // Mic on by default (09 §3 D3): the app opens straight into Listening.
  return { micState: 'listening', settledText: '', tailText: '' };
}

export function voiceReducer(state: VoiceState, action: VoiceAction): VoiceState {
  switch (action.type) {
    case 'provider':
      return onProviderEvent(state, action.event);
    case 'providerFailed':
      // Preserve any un-committed transcript on screen (09 §5).
      return { ...state, micState: 'retry', commit: undefined };
    case 'pause':
      return { ...state, micState: 'paused', tailText: '', commit: undefined };
    case 'resume':
      return { ...state, micState: 'listening', commit: undefined };
    case 'cancel':
      // The X discards the un-committed utterance; nothing was sent (09 §4).
      return { ...state, tailText: '', commit: undefined };
    case 'denied':
      return { ...state, micState: 'denied', tailText: '', commit: undefined };
    case 'background':
      // The store closes the socket; the desired state is unchanged so
      // foregrounding resumes it. An explicit pause stays paused.
      return state;
    case 'foreground':
      return state;
    case 'commitConsumed':
      return { ...state, commit: undefined };
    default:
      return state;
  }
}

function onProviderEvent(state: VoiceState, event: VoiceProviderEvent): VoiceState {
  // Ignore provider chatter while paused/denied — the store shouldn't feed it,
  // but be defensive.
  if (state.micState === 'paused' || state.micState === 'denied') {
    return state;
  }
  switch (event.kind) {
    case 'open':
      return { ...state, micState: 'listening' };
    case 'partial':
      return { ...state, micState: 'listening', tailText: event.text };
    case 'final': {
      const text = event.text.trim();
      if (text === '') {
        // Empty/whitespace finals never post (09 §4).
        return { ...state, tailText: '' };
      }
      return { ...state, settledText: text, tailText: '', commit: text };
    }
    case 'error':
      return state; // the store decides reconnect-then-retry; no state change here
    case 'close':
      return state;
    default:
      return state;
  }
}
```

- [ ] **Step 4: Run it, watch it pass** (`cd frontend && pnpm test commit-machine` → PASS).

### 3b. Transport — token fetch

- [ ] **Step 5:** In `frontend/src/transport/transport.ts` add the type, guard, and fetch (mirroring `postMessage`/`isMessagePostResponse`):

```ts
export type VoiceToken = components['schemas']['VoiceToken'];

function isVoiceToken(value: unknown): value is VoiceToken {
  return (
    isRecord(value) && typeof value.token === 'string' && typeof value.expires_at === 'string'
  );
}

/** `POST /api/voice/token` — mint a short-lived AssemblyAI streaming token (09 §2). */
export async function fetchVoiceToken(): Promise<VoiceToken> {
  const response = await fetch('/api/voice/token', { method: 'POST' });
  if (!response.ok) {
    throw new Error('fetchVoiceToken: mint failed');
  }
  const payload: unknown = await response.json();
  if (!isVoiceToken(payload)) {
    throw new Error('fetchVoiceToken: unexpected response shape');
  }
  return payload;
}
```

### 3c. AssemblyAI provider client (I/O) + PCM worklet

- [ ] **Step 6:** Create `frontend/src/voice/pcm-worklet.ts` — an `AudioWorkletProcessor` that downsamples the mic's Float32 frames to 16 kHz PCM16 and posts `ArrayBuffer`s to the main thread. (Loaded via a Vite `?url`/Blob; keep it dependency-free and self-contained.) Include a source-rate → 16k linear resample and Float32→Int16 clamp.

- [ ] **Step 7:** Create `frontend/src/voice/assemblyai-client.ts` — the only file that knows AssemblyAI's protocol (09 §7). Responsibilities:
  - `startVoiceStream({ getToken, onEvent }): { stop(): void }` where `getToken: () => Promise<VoiceToken>` and `onEvent: (e: VoiceProviderEvent) => void`.
  - `getUserMedia({ audio: {...} })`; on `NotAllowedError` → `onEvent({kind:'error'})` and surface a distinct denied path (the store maps permission denial → `denied`). Wire an `AudioContext` + the PCM worklet; send each PCM `ArrayBuffer` as a binary WS frame.
  - Open `wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le&format_turns=true&token=<token>`.
  - Decode messages with a runtime guard (no `as`): `Begin` → `{kind:'open'}`; `Turn` with `end_of_turn && turn_is_formatted` → `{kind:'final', text: transcript}`; other `Turn` with a `transcript` → `{kind:'partial', text: transcript}`; socket `onerror`/`onclose` → `{kind:'error'}`/`{kind:'close'}`.
  - `stop()` sends `{"type":"Terminate"}`, closes the socket, stops the mic tracks and the AudioContext.
  - This file has no pure-logic branches worth unit-testing beyond the message decode; extract `decodeAssemblyMessage(data: string): VoiceProviderEvent | null` as a pure exported function and unit-test it (Begin, formatted final, partial, unknown → null). Add `frontend/src/voice/assemblyai-client.test.ts` for `decodeAssemblyMessage`.

- [ ] **Step 8:** Add `frontend/src/voice/assemblyai-client.test.ts` for the pure decoder:

```ts
import { describe, it, expect } from 'vitest';
import { decodeAssemblyMessage } from '@/voice/assemblyai-client';

describe('decodeAssemblyMessage', () => {
  it('Begin -> open', () => {
    expect(decodeAssemblyMessage(JSON.stringify({ type: 'Begin', id: 'x' }))).toEqual({ kind: 'open' });
  });
  it('formatted end-of-turn -> final', () => {
    const msg = JSON.stringify({ type: 'Turn', transcript: 'Hello.', end_of_turn: true, turn_is_formatted: true });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'final', text: 'Hello.' });
  });
  it('mid-turn -> partial', () => {
    const msg = JSON.stringify({ type: 'Turn', transcript: 'hello', end_of_turn: false, turn_is_formatted: false });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'partial', text: 'hello' });
  });
  it('unformatted end-of-turn -> partial (wait for the formatted final)', () => {
    const msg = JSON.stringify({ type: 'Turn', transcript: 'hello', end_of_turn: true, turn_is_formatted: false });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'partial', text: 'hello' });
  });
  it('garbage -> null', () => {
    expect(decodeAssemblyMessage('not json')).toBeNull();
    expect(decodeAssemblyMessage(JSON.stringify({ type: 'Nope' }))).toBeNull();
  });
});
```

### 3d. Voice store (React glue) + Dock wiring

- [ ] **Step 9:** Create `frontend/src/voice/voice-context.ts` + `frontend/src/voice/voice-store.tsx`:
  - `VoiceProvider` owns the reducer via `useReducer(voiceReducer, undefined, initialVoiceState)`.
  - On mount (and on `resume`/`retry`→resume): fetch a token, `startVoiceStream`, feed provider events via `dispatch({type:'provider', event})`. On the first provider `error`, attempt exactly one silent reconnect (fresh token); if that also fails, `dispatch({type:'providerFailed'})` (→ retry). On `getUserMedia` `NotAllowedError`, `dispatch({type:'denied'})`.
  - Effect watching `state.commit`: when set, `postMessage(state.commit)` then `dispatch({type:'commitConsumed'})`. On POST failure, keep the text and surface an inline retry (reuse the dock's retry affordance; `07 §8` rule — never a modal).
  - `visibilitychange`: hidden → stop the stream + `dispatch({type:'background'})`; visible → if not paused, restart + `dispatch({type:'foreground'})`.
  - `pause()` stops the stream + `dispatch({type:'pause'})`; `resume()` restarts + `dispatch({type:'resume'})`; `cancel()` → `dispatch({type:'cancel'})`.
  - Expose `{ micState, settledText, tailText, pause, resume, cancel }` via context; `useVoice()` reads it.
  - **Token refresh:** schedule a proactive refresh before `expires_at` (09 §5) — reconnect with a fresh token, preserving transcript.

- [ ] **Step 10:** Rewrite `frontend/src/components/Dock.tsx` to consume `useVoice()`. Preserve the existing `data-role="dock"`, `data-role="dock-talk"`, `aria-label` and mic-glyph sub-elements so `PrimaryScreen.css` and existing selectors keep working, but:
  - `data-dock-state` reflects `micState` (`listening|paused|denied|retry`).
  - Render the live transcript region: `settledText` in ink + `tailText` ghosted + a caret (per design 5a), shown while listening.
  - Mic button label/behavior per state table (09 §3): Listening → "Listening…" + amber (tapping pauses); Paused/Retry/Denied → grey with the state's copy (tap resumes/retries).
  - Render the **X** (cancel) beside the mic while there is an un-committed transcript; `onClick` → `cancel()`.
  - Keep it a presentational consumer — logic stays in the store/machine.

- [ ] **Step 11:** Wrap the app tree in `VoiceProvider`. In `frontend/src/components/PrimaryScreen.tsx` (the composing wrapper) or `App.tsx`, add `<VoiceProvider>` around the subtree that renders `<Dock/>`. Keep `PrimaryScreenView` pure — `Dock` reads context itself (like the stores).

- [ ] **Step 12:** Add `frontend/src/components/Dock.test.tsx` — render `Dock` inside a test `VoiceProvider` (or mock `useVoice`) and assert: Listening shows "Listening…" and the amber state; tapping the mic calls `pause`; Paused shows "Tap to talk"; the X calls `cancel`; a transcript renders settled + tail. Mock `@/voice/voice-store`'s `useVoice` for deterministic states.

- [ ] **Step 13:** Update the DOM-snapshot coverage (spec §8 "dock in Listening/Paused/Blocked/Retry"). Since pixel snapshots are DOM-structure snapshots here (07 §9 D4), add cases to `PrimaryScreenView.test.tsx` or a new `Dock.snapshot.test.tsx` rendering the Dock in each state with fixture transcript text; commit the `.snap`.

- [ ] **Step 14: Run the frontend gate + commit.**

Run: `cd frontend && pnpm run lint && pnpm run format && pnpm run typecheck && pnpm test` (all PASS).

```bash
git add frontend/
git commit -m "feat(09): frontend voice module — commit machine, AssemblyAI client, dock"
```

---

## Task 4: Gated real-service smoke test (serial, last)

**Files:** Create `tests/tests/voice-token-mints.spec.ts` (+ a tiny canned PCM16 clip fixture, or synthesize silence/tone in-test).

- [ ] **Step 1:** Write a Playwright test that: (a) `POST /api/voice/token` against the running stack and asserts a non-empty `token` + future `expires_at`; (b) opens the AssemblyAI WS with a canned 16 kHz PCM clip of speech and asserts a `Turn` with non-empty `transcript` arrives, then that `POST /api/message` with that transcript lands (a `human.message` — assert via `GET /api/messages`). Gate it so it only runs when explicitly invoked (e.g. `test.skip(!process.env.KILN_VOICE_SMOKE, ...)`), per real-service test hygiene — never in the default gate.

- [ ] **Step 2:** Document the run recipe in `tests/README.md` and in the `voice-pipeline` skill's "How to run" section.

- [ ] **Step 3: Commit.**

```bash
git add tests/ docs/ .agents/ .claude/skills/
git commit -m "test(09): gated AssemblyAI voice smoke + skill notes"
```

---

## Final: full gate + skill update

- [ ] Run the whole wall: `make check` (lint → typecheck → unit + integration, both surfaces) → green.
- [ ] Run `make schema-verify` → generated types not stale.
- [ ] Fold gotchas learned into `.claude/skills/voice-pipeline/SKILL.md` (provider config, where the socket/mic code lives vs the token route, the PCM16/end-of-turn decode rule).
- [ ] Present finishing options (merge/PR) via `superpowers:finishing-a-development-branch`.
