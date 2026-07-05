# Multi-User Phase 1 — Auth + Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship phase 1 of `docs/specs/11-multi-user.md`: GitHub OAuth sign-in gated by a username allowlist, cookie sessions, `users`/`sessions`/`user_config`/`projects` tables with AES-GCM-encrypted secrets, four new session-protected endpoints, and a desktop `/dashboard` SPA surface — with the current app at `/` and `/debug` provably untouched.

**Architecture:** A new `internal/identity` backend module (service + store interface + postgres store, mirroring `internal/board`'s shape) owns users, sessions, encrypted config, and projects. A GitHub OAuth adapter (`internal/identity/githubapi`) and a connection verifier (`internal/identity/verify`) follow the amika/assemblyai outbound-client pattern. `internal/api` gains cookie/session helpers, three browser auth routes, and four JSON endpoints — mounted only when `EnableIdentity` is called, which `cmd/kiln` gates on the new env vars, so an unconfigured boot is byte-for-byte today's behavior. The frontend adds one route (`/dashboard/*`), one context store, and screens under `src/dashboard/`; nothing under `PrimaryScreen`/`App` changes. The runtime does NOT consume stored config in phase 1 (spec 11 §1, D6).

**Tech Stack:** Go 1.26 stdlib (`net/http` ServeMux, `database/sql`+lib/pq, `crypto/aes|cipher|rand|sha256`), OpenAPI 3.0.3 + oapi-codegen + openapi-typescript, React 18 + React Router v7 (already installed), plain CSS, Vitest + Testing Library, Playwright.

## Global Constraints

- **Hard gate before every commit:** `make check` from repo root (backend gofmt+golangci-lint, frontend eslint+prettier-check, `go build ./...`, `tsc --noEmit`, `go test ./...`, `go test -tags=integration ./...` — needs `TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable` exported and compose db up — and `pnpm run test`). Pre-push hook runs it anyway; do not weaken any check.
- **Commit to main is OK** (AGENTS.md); commit after every task. Only commit your own files.
- **Never hand-edit** `frontend/src/schema/generated.ts` or `backend/internal/wire/generated.go` — edit `schema/openapi.yaml`, run `make schema` (PATH must include `$(go env GOPATH)/bin` for oapi-codegen). Schema + both generated files land in the same commit.
- **Never `cat`/dump `.env`** (AGENTS.md). Reference variables by name only.
- **The current app is untouched:** no edits under `frontend/src/components/`, `frontend/src/stores/` (except a new file), `frontend/src/App.tsx`, and no behavior change to any existing `/api/*` route. Existing tests must pass unmodified — that is the proof (spec 11 §8).
- **Auth guards only the new routes** in phase 1 (spec 11 §2): `/api/me`, `PUT /api/settings`, `PUT /api/project`, `POST /api/settings/verify`. Nothing existing gains a session requirement.
- **Go error style:** static sentinel errors (`var ErrX = errors.New(...)`) wrapped with `fmt.Errorf("identity: <op>: %w", err)` — golangci-lint enforces err113.
- **TS style:** no `any`, no `as` (repo ban) — narrow `unknown` through hand-written type guards like `transport.ts` does.
- **CSS:** plain CSS scoped under `[data-role='dashboard']`; selectors key off `data-*` attributes, not class names.
- **Secrets hygiene:** stored secrets are never logged, never returned by any endpoint after write (fingerprint-only reads), and the master key is read once at boot.
- **Recorded deviations from spec 11:** (1) `PUT /api/settings` treats empty/omitted as "unchanged" and does NOT support clearing a credential (overwrite instead) — nullable-vs-absent is not distinguishable through oapi-codegen 3.0 models and phase 1 has no need; (2) `/auth/*` browser redirect endpoints are not in `openapi.yaml` (they carry no JSON contract — same precedent as `/api/dev/*`); (3) the dashboard's session/me store lives in `frontend/src/dashboard/` rather than spec §9's `frontend/src/stores/session` — nothing outside the dashboard consumes it in phase 1, so it stays inside the surface that owns it.

## File Structure

**Backend — create:**
- `backend/internal/identity/doc.go` — module doc: owns who-you-are + what-you-configured (spec 11 §2–§3)
- `backend/internal/identity/entities.go` — `User`, `Session`, `Project`, `UserConfig` (encrypted-at-rest row), `Me`, `SettingsUpdate`, `ProjectUpdate`, `CheckResult`
- `backend/internal/identity/errors.go` — `ErrNotFound`, `ErrNotAllowed`, `ErrNoSession`
- `backend/internal/identity/cipher.go` — AES-256-GCM `Cipher` + `Tail` fingerprint helper
- `backend/internal/identity/cipher_test.go`
- `backend/internal/identity/store.go` — `Store` port interface
- `backend/internal/identity/service.go` — `Service` (login, sessions, me, settings, project, verify)
- `backend/internal/identity/service_test.go` + `backend/internal/identity/fakes_test.go` — in-memory `fakeStore`, `fakeGitHub`, `fakeVerifier`
- `backend/internal/identity/postgres/store.go` — `New(db *sql.DB) *Store`
- `backend/internal/identity/postgres/migrations.go` — `//go:embed migrations/*.sql`
- `backend/internal/identity/postgres/migrations/0001_identity.sql`
- `backend/internal/identity/postgres/store_integration_test.go` — `//go:build integration`
- `backend/internal/identity/githubapi/client.go` — OAuth code exchange + `GET /user` (amika-client pattern)
- `backend/internal/identity/githubapi/client_test.go` — httptest
- `backend/internal/identity/verify/verify.go` — Anthropic/Amika/repo live checks
- `backend/internal/identity/verify/verify_test.go` — httptest + local bare git repo
- `backend/internal/api/session.go` — cookie names/helpers, `randomToken`, `withSession` middleware
- `backend/internal/api/auth_handlers.go` — `/auth/github/login|callback`, `/auth/logout`, invite-only page
- `backend/internal/api/identity_handlers.go` — me/settings/project/verify + `POST /api/dev/session`
- `backend/internal/api/identity_handlers_test.go`, `backend/internal/api/auth_handlers_test.go`

**Backend — modify:**
- `backend/internal/api/routes.go` — new port interfaces + `EnableIdentity`/`EnableDevSession` + route mounts in `Handler()`
- `backend/internal/api/fakes_test.go` — fake identity ports for route tests
- `backend/cmd/kiln/main.go` — `Config` gains `GitHubOAuthClientID/Secret`, `AllowedGitHubUsers`, `SecretsKey`
- `backend/cmd/kiln/wiring.go` — register identity migrations in `moduleMigrations()`; construct identity graph in `enableServerRoutes` path
- `backend/internal/wire/generated.go` — regenerated (never by hand)

**Schema — modify:**
- `schema/openapi.yaml` — 4 paths + 9 component schemas

**Frontend — create:**
- `frontend/src/dashboard/Dashboard.tsx` — route root: loading → SignIn / Onboarding / Settings
- `frontend/src/dashboard/dashboard-context.ts` + `frontend/src/dashboard/dashboard-store.tsx` — context store (repo two-file pattern)
- `frontend/src/dashboard/SignIn.tsx`, `frontend/src/dashboard/Onboarding.tsx`, `frontend/src/dashboard/Settings.tsx`, `frontend/src/dashboard/ConfigFields.tsx` (shared field components)
- `frontend/src/dashboard/Dashboard.css`
- `frontend/src/dashboard/Dashboard.test.tsx`, `frontend/src/dashboard/dashboard-store.test.tsx`

**Frontend — modify:**
- `frontend/src/main.tsx` — one added `<Route path="/dashboard/*" ...>`
- `frontend/src/transport/transport.ts` — `fetchMe`, `putSettings`, `putProject`, `postVerify`, `postLogout` + guards
- `frontend/src/transport/transport.test.ts` (if present; else co-located new test)
- `frontend/src/schema/generated.ts` — regenerated
- `frontend/vite.config.ts` — add `'/auth'` proxy key

**Infra/tests — modify/create:**
- `docker-compose.yml`, `.env.example` — new env vars
- `tests/tests/dashboard-config.spec.ts` — e2e: dev session → onboard → `/dashboard` reflects config
- `.agents/skills/runtime-and-api/SKILL.md`, `.agents/skills/web-client/SKILL.md` — skill updates (AGENTS.md rule)

---

### Task 1: Wire schema — new endpoints and types

**Files:**
- Modify: `schema/openapi.yaml`
- Generated: `backend/internal/wire/generated.go`, `frontend/src/schema/generated.ts`

**Interfaces:**
- Produces: Go `wire.Me`, `wire.MeUser`, `wire.MeProject`, `wire.MeSettings`, `wire.SecretStatus`, `wire.SettingsUpdateRequest`, `wire.ProjectUpdateRequest`, `wire.VerifyResponse`, `wire.VerifyCheck`; TS `components['schemas']['Me']` etc. Every later task uses these exact names.

- [ ] **Step 1: Add paths to `schema/openapi.yaml`** (append under `paths:`, following the file's existing compact style):

```yaml
  /api/me:
    get:
      operationId: getMe
      summary: Current user, project, and config status (11 §4). Session-protected.
      responses:
        '200':
          description: The signed-in user's account view.
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Me' }
        '401':
          description: No valid session.
  /api/settings:
    put:
      operationId: putSettings
      summary: Partial credential update (11 §4). Secrets are write-only; empty/omitted fields are unchanged.
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/SettingsUpdateRequest' }
      responses:
        '200':
          description: Updated account view (same shape as GET /api/me).
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Me' }
        '400':
          description: Invalid request body.
        '401':
          description: No valid session.
  /api/project:
    put:
      operationId: putProject
      summary: Create-or-update the caller's single project (11 §3–§4).
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/ProjectUpdateRequest' }
      responses:
        '200':
          description: Updated account view (same shape as GET /api/me).
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Me' }
        '400':
          description: Invalid request body.
        '401':
          description: No valid session.
  /api/settings/verify:
    post:
      operationId: postSettingsVerify
      summary: Live connection checks for stored credentials (11 §4).
      responses:
        '200':
          description: Per-check results; unconfigured checks report status "skipped".
          content:
            application/json:
              schema: { $ref: '#/components/schemas/VerifyResponse' }
        '401':
          description: No valid session.
```

- [ ] **Step 2: Add component schemas** (append under `components: schemas:`):

```yaml
    Me:
      type: object
      description: The signed-in user's account view (11 §4). Secret values never appear.
      required: [user, settings]
      properties:
        user: { $ref: '#/components/schemas/MeUser' }
        project:
          allOf: [{ $ref: '#/components/schemas/MeProject' }]
          description: Absent until the user creates their project.
        settings: { $ref: '#/components/schemas/MeSettings' }
    MeUser:
      type: object
      required: [github_login, display_name, avatar_url]
      properties:
        github_login: { type: string }
        display_name: { type: string }
        avatar_url: { type: string }
    MeProject:
      type: object
      required: [name, repo_url, amika_snapshot, brain_model, worker_count]
      properties:
        name: { type: string }
        repo_url: { type: string }
        amika_snapshot: { type: string }
        brain_model: { type: string }
        worker_count: { type: integer }
    MeSettings:
      type: object
      description: Config status — secrets as presence+fingerprint only (11 §3 D7).
      required: [anthropic_api_key, amika_api_key, github_auth_token, amika_base_url, amika_claude_cred_id]
      properties:
        anthropic_api_key: { $ref: '#/components/schemas/SecretStatus' }
        amika_api_key: { $ref: '#/components/schemas/SecretStatus' }
        github_auth_token: { $ref: '#/components/schemas/SecretStatus' }
        amika_base_url: { type: string }
        amika_claude_cred_id: { type: string }
    SecretStatus:
      type: object
      required: [set, tail]
      properties:
        set: { type: boolean }
        tail:
          type: string
          description: Last 4 characters of the stored secret; empty when unset.
    SettingsUpdateRequest:
      type: object
      description: All fields optional; empty or omitted means unchanged (write-only secrets).
      properties:
        anthropic_api_key: { type: string }
        amika_api_key: { type: string }
        github_auth_token: { type: string }
        amika_base_url: { type: string }
        amika_claude_cred_id: { type: string }
    ProjectUpdateRequest:
      type: object
      required: [name, repo_url]
      properties:
        name: { type: string }
        repo_url: { type: string }
        amika_snapshot: { type: string }
        brain_model: { type: string }
        worker_count: { type: integer }
    VerifyCheck:
      type: object
      required: [name, status, message]
      properties:
        name: { type: string, enum: [anthropic, amika, repo] }
        status: { type: string, enum: [ok, failed, skipped] }
        message: { type: string }
    VerifyResponse:
      type: object
      required: [checks]
      properties:
        checks:
          type: array
          items: { $ref: '#/components/schemas/VerifyCheck' }
```

- [ ] **Step 3: Regenerate both sides**

Run from repo root: `export PATH="$PATH:$(go env GOPATH)/bin" && make schema`
Expected: both generated files change; `git diff --stat` shows exactly `schema/openapi.yaml`, `backend/internal/wire/generated.go`, `frontend/src/schema/generated.ts`.

- [ ] **Step 4: Verify gate pieces that schema touches**

Run: `make schema-verify && cd backend && go build ./... && cd ../frontend && pnpm run typecheck`
Expected: all green (nothing consumes the new types yet).

- [ ] **Step 5: Commit**

```bash
git add schema/openapi.yaml backend/internal/wire/generated.go frontend/src/schema/generated.ts
git commit -m "feat(schema): wire types for /api/me, settings, project, verify (spec 11 §4)"
```

---

### Task 2: identity module core — entities, errors, cipher

**Files:**
- Create: `backend/internal/identity/doc.go`, `entities.go`, `errors.go`, `cipher.go`
- Test: `backend/internal/identity/cipher_test.go`

**Interfaces:**
- Produces: `identity.User`, `identity.Session`, `identity.Project`, `identity.UserConfig`, `identity.Me`, `identity.MeSettings`, `identity.SecretStatus`, `identity.SettingsUpdate`, `identity.ProjectUpdate`, `identity.CheckResult`; `identity.NewCipher(hexKey string) (*Cipher, error)`, `(*Cipher).Encrypt(string) ([]byte, error)`, `(*Cipher).Decrypt([]byte) (string, error)`, `identity.Tail(string) string`; sentinels `ErrNotFound`, `ErrNotAllowed`, `ErrNoSession`, `ErrBadKey`, `ErrBadCiphertext`.

- [ ] **Step 1: Write the failing cipher test** — `backend/internal/identity/cipher_test.go` (package `identity_test`):

```go
package identity_test

import (
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// 32 bytes of hex — a valid master key shape (KILN_SECRETS_KEY, 11 §3 D7).
const testKey = "3f9c2b8a71d04e5f6a1b2c3d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f50617"

func TestCipherRoundTrip(t *testing.T) {
	c, err := identity.NewCipher(testKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	box, err := c.Encrypt("sk-ant-secret-x4Kd")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(string(box), "sk-ant") {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := c.Decrypt(box)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != "sk-ant-secret-x4Kd" {
		t.Fatalf("round trip = %q", got)
	}
}

func TestCipherDistinctNonces(t *testing.T) {
	c, _ := identity.NewCipher(testKey)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if string(a) == string(b) {
		t.Fatal("two encryptions of the same plaintext must differ (random nonce)")
	}
}

func TestNewCipherRejectsBadKeys(t *testing.T) {
	for _, k := range []string{"", "abc", "zz" + testKey[2:], testKey[:32]} {
		if _, err := identity.NewCipher(k); err == nil {
			t.Fatalf("NewCipher(%q) accepted a malformed key", k)
		}
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	c, _ := identity.NewCipher(testKey)
	if _, err := c.Decrypt([]byte("short")); err == nil {
		t.Fatal("Decrypt accepted truncated ciphertext")
	}
	box, _ := c.Encrypt("secret")
	box[len(box)-1] ^= 0xFF
	if _, err := c.Decrypt(box); err == nil {
		t.Fatal("Decrypt accepted tampered ciphertext")
	}
}

func TestTail(t *testing.T) {
	if got := identity.Tail("sk-ant-secret-x4Kd"); got != "x4Kd" {
		t.Fatalf("Tail = %q", got)
	}
	if got := identity.Tail("ab"); got != "ab" {
		t.Fatalf("Tail short = %q", got)
	}
	if got := identity.Tail(""); got != "" {
		t.Fatalf("Tail empty = %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd backend && go test ./internal/identity/...`
Expected: FAIL — package does not exist / undefined symbols.

- [ ] **Step 3: Write `doc.go`, `errors.go`, `entities.go`, `cipher.go`**

`backend/internal/identity/doc.go`:

```go
// Package identity owns who the user is and what they have configured
// (docs/specs/11-multi-user.md §2–§4, phase 1): GitHub-derived users, cookie
// sessions, per-user encrypted credentials, and the (single, for now) project.
//
// The runtime does NOT consume this module in phase 1 (11 §1, D6): env vars
// still drive the brain/agent. This module is the dashboard's config store.
package identity
```

`backend/internal/identity/errors.go`:

```go
package identity

import "errors"

var (
	// ErrNotFound is returned by stores when the row does not exist.
	ErrNotFound = errors.New("identity: not found")
	// ErrNotAllowed rejects a GitHub login not on KILN_ALLOWED_GITHUB_USERS (11 §2).
	ErrNotAllowed = errors.New("identity: github user not on the allowlist")
	// ErrNoSession rejects a missing/expired/unknown session token.
	ErrNoSession = errors.New("identity: no valid session")
)
```

`backend/internal/identity/entities.go`:

```go
package identity

import "time"

// User is a signed-up GitHub identity (11 §3).
type User struct {
	ID          string
	GitHubID    int64
	GitHubLogin string // stored lower-cased
	DisplayName string
	AvatarURL   string
	CreatedAt   time.Time
}

// Session is a server-side cookie session; only the token's hash is stored (11 §2).
type Session struct {
	TokenHash string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// UserConfig is the encrypted-at-rest credentials row (11 §3 D4, D7).
// *Enc fields hold AES-GCM ciphertext (nil = unset); the two non-secret
// fields are stored in the clear.
type UserConfig struct {
	UserID            string
	AnthropicKeyEnc   []byte
	AmikaKeyEnc       []byte
	GitHubTokenEnc    []byte
	AmikaBaseURL      string
	AmikaClaudeCredID string
}

// Project parameterizes one brain/board (11 §3 D5): the repo it works on and
// the brain-shaped knobs. One per user in phase 1.
type Project struct {
	ID            string
	OwnerUserID   string
	Name          string
	RepoURL       string
	AmikaSnapshot string
	BrainModel    string
	WorkerCount   int
	CreatedAt     time.Time
}

// SecretStatus is the fingerprint-only read shape for a stored secret (11 §3 D7).
type SecretStatus struct {
	Set  bool
	Tail string
}

// MeSettings is the config-status view: never secret values.
type MeSettings struct {
	AnthropicKey      SecretStatus
	AmikaKey          SecretStatus
	GitHubToken       SecretStatus
	AmikaBaseURL      string
	AmikaClaudeCredID string
}

// Me is everything GET /api/me returns (11 §4).
type Me struct {
	User     User
	Project  *Project // nil until onboarding creates it
	Settings MeSettings
}

// SettingsUpdate is a partial credential write; empty string = leave unchanged.
type SettingsUpdate struct {
	AnthropicKey      string
	AmikaKey          string
	GitHubToken       string
	AmikaBaseURL      string
	AmikaClaudeCredID string
}

// ProjectUpdate creates or updates the caller's project.
type ProjectUpdate struct {
	Name          string
	RepoURL       string
	AmikaSnapshot string
	BrainModel    string
	WorkerCount   int
}

// CheckResult is one live connection check (POST /api/settings/verify, 11 §4).
type CheckResult struct {
	Name    string // "anthropic" | "amika" | "repo"
	Status  string // "ok" | "failed" | "skipped"
	Message string
}
```

`backend/internal/identity/cipher.go`:

```go
package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Cipher seals per-user secrets with AES-256-GCM under one master key
// (KILN_SECRETS_KEY — 11 §3, D7). Ciphertext layout: nonce || sealed.
type Cipher struct{ aead cipher.AEAD }

var (
	// ErrBadKey rejects a KILN_SECRETS_KEY that is not 64 hex chars (32 bytes).
	ErrBadKey = errors.New("identity: master key must be 64 hex chars (32 bytes)")
	// ErrBadCiphertext rejects truncated or tampered ciphertext.
	ErrBadCiphertext = errors.New("identity: ciphertext invalid")
)

// NewCipher parses the hex master key and prepares the AEAD.
func NewCipher(hexKey string) (*Cipher, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, ErrBadKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("identity: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("identity: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext under a fresh random nonce.
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("identity: nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt opens nonce||sealed ciphertext produced by Encrypt.
func (c *Cipher) Decrypt(box []byte) (string, error) {
	if len(box) < c.aead.NonceSize() {
		return "", ErrBadCiphertext
	}
	nonce, sealed := box[:c.aead.NonceSize()], box[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrBadCiphertext
	}
	return string(plain), nil
}

// Tail is the last-4 fingerprint shown by the API ("configured · …x4Kd").
func Tail(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd backend && go test ./internal/identity/...`
Expected: PASS (5 tests).

- [ ] **Step 5: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/identity/... && cd ..
git add backend/internal/identity
git commit -m "feat(identity): module core — entities, sentinels, AES-GCM cipher (spec 11 §3)"
```

---

### Task 3: identity postgres store + migration

**Files:**
- Create: `backend/internal/identity/store.go`, `backend/internal/identity/postgres/store.go`, `backend/internal/identity/postgres/migrations.go`, `backend/internal/identity/postgres/migrations/0001_identity.sql`
- Test: `backend/internal/identity/postgres/store_integration_test.go`
- Modify: `backend/cmd/kiln/wiring.go` (`moduleMigrations()` — add the identity set)

**Interfaces:**
- Consumes: Task 2 entities.
- Produces: `identity.Store` interface (below) and `identitypg.New(db *sql.DB) *Store` satisfying it. Task 5's service and Task 10's wiring depend on these exact signatures.

- [ ] **Step 1: Write `backend/internal/identity/store.go`** — the port first, so both the fake (Task 5) and postgres impl build against it:

```go
package identity

import (
	"context"
	"time"
)

// Store is identity's persistence port (02 §2: modules own their interfaces;
// the postgres adapter lives in identity/postgres).
type Store interface {
	// UpsertUser finds-or-creates by GitHubID, refreshing login/name/avatar
	// on every login (GitHub users can rename).
	UpsertUser(ctx context.Context, u User) (User, error)

	InsertSession(ctx context.Context, s Session) error
	// GetSession returns ErrNotFound for unknown hashes; expiry is the
	// service's business rule, so expired rows ARE returned.
	GetSession(ctx context.Context, tokenHash string) (Session, error)
	// TouchSession extends expiry (sliding window, 11 §2).
	TouchSession(ctx context.Context, tokenHash string, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	// GetSessionUser resolves a session's user in one call.
	GetSessionUser(ctx context.Context, tokenHash string) (Session, User, error)

	// GetUserConfig returns a zero-value UserConfig (not ErrNotFound) when the
	// user has never written config — callers treat absent as all-unset.
	GetUserConfig(ctx context.Context, userID string) (UserConfig, error)
	UpsertUserConfig(ctx context.Context, cfg UserConfig) error

	// GetProjectByOwner returns ErrNotFound before onboarding creates it.
	GetProjectByOwner(ctx context.Context, ownerUserID string) (Project, error)
	UpsertProject(ctx context.Context, p Project) (Project, error)
}
```

- [ ] **Step 2: Write the migration** — `backend/internal/identity/postgres/migrations/0001_identity.sql`:

```sql
-- Identity & per-user config: docs/specs/11-multi-user.md §3 (phase 1).
CREATE TABLE users (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  github_id    bigint NOT NULL UNIQUE,
  github_login text NOT NULL UNIQUE
               CHECK (github_login = lower(github_login) AND github_login <> ''),
  display_name text NOT NULL DEFAULT '',
  avatar_url   text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash text PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_by_user ON sessions (user_id);

-- Credentials follow the person (11 §3 D4); *_enc columns are AES-GCM
-- ciphertext (11 §3 D7), NULL = unset. Non-secrets stored in the clear.
CREATE TABLE user_config (
  user_id               uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  anthropic_api_key_enc bytea,
  amika_api_key_enc     bytea,
  github_auth_token_enc bytea,
  amika_base_url        text NOT NULL DEFAULT '',
  amika_claude_cred_id  text NOT NULL DEFAULT '',
  updated_at            timestamptz NOT NULL DEFAULT now()
);

-- One brain per project (11 §3 D5); repo + brain knobs ride the project.
CREATE TABLE projects (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           text NOT NULL CHECK (name <> ''),
  repo_url       text NOT NULL DEFAULT '',
  amika_snapshot text NOT NULL DEFAULT '',
  brain_model    text NOT NULL DEFAULT '',
  worker_count   int  NOT NULL DEFAULT 3 CHECK (worker_count BETWEEN 1 AND 10),
  created_at     timestamptz NOT NULL DEFAULT now()
);
-- One project per user in phase 1 (11 §3): drop this index — no data
-- migration — when multi-project lands.
CREATE UNIQUE INDEX one_project_per_owner ON projects (owner_user_id);
```

`backend/internal/identity/postgres/migrations.go` (mirror board's verbatim pattern):

```go
package postgres

import "embed"

// Migrations holds the identity module's schema migrations, embedded so kiln
// ships as a single static binary (same pattern as board/runtime/agent).
//
//go:embed migrations/*.sql
var Migrations embed.FS
```

- [ ] **Step 3: Register the migration set** in `backend/cmd/kiln/wiring.go` `moduleMigrations()` — append after the agent entry, importing `identitypg "github.com/crabtree-michael/kiln/backend/internal/identity/postgres"`:

```go
	{
		key: "internal/identity/postgres/migrations",
		fs:  subFS(identitypg.Migrations),
	},
```

(Match the exact struct literal shape used by the three existing entries — read them first; the `key` string is the ledger prefix and must never change once deployed.)

- [ ] **Step 4: Write the integration test** — `backend/internal/identity/postgres/store_integration_test.go`. First line `//go:build integration`, package `postgres_test`. Copy the board store's `testDB(t)` helper shape: skip unless `TEST_DATABASE_URL` is set; apply `./migrations` only if `users` is missing from `information_schema.tables`; truncate only `users, sessions, user_config, projects` (CASCADE). Tests:

```go
func TestUpsertUserFindsOrCreates(t *testing.T)   // same github_id twice → one row, refreshed login/name
func TestSessionLifecycle(t *testing.T)           // insert → GetSessionUser returns session+user → touch extends → delete → ErrNotFound
func TestUserConfigZeroThenUpsert(t *testing.T)   // Get before write → zero value; upsert enc bytes + clear fields → read back equal; second upsert overwrites
func TestProjectUpsertAndUniqueOwner(t *testing.T) // ErrNotFound before create; upsert creates then updates in place (same id); worker_count persisted
```

Each test asserts exact field round-trips (bytea comes back byte-equal). Use `t.Cleanup` + the truncate helper between tests.

- [ ] **Step 5: Write `backend/internal/identity/postgres/store.go`**

Package `postgres`, `type Store struct{ db *sql.DB }`, `func New(db *sql.DB) *Store`, `var _ identity.Store = (*Store)(nil)`. Follow board/postgres conventions exactly: `QueryRowContext(...).Scan(...)`, errors wrapped `fmt.Errorf("identity/postgres: <op>: %w", err)`, `sql.ErrNoRows` → `identity.ErrNotFound`. Key statements:

```sql
-- UpsertUser
INSERT INTO users (github_id, github_login, display_name, avatar_url)
VALUES ($1, lower($2), $3, $4)
ON CONFLICT (github_id) DO UPDATE
  SET github_login = EXCLUDED.github_login,
      display_name = EXCLUDED.display_name,
      avatar_url   = EXCLUDED.avatar_url
RETURNING id, github_id, github_login, display_name, avatar_url, created_at

-- GetSessionUser
SELECT s.token_hash, s.user_id, s.created_at, s.expires_at,
       u.id, u.github_id, u.github_login, u.display_name, u.avatar_url, u.created_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1

-- UpsertUserConfig (write all columns; service does read-modify-write merge)
INSERT INTO user_config (user_id, anthropic_api_key_enc, amika_api_key_enc,
                         github_auth_token_enc, amika_base_url, amika_claude_cred_id, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (user_id) DO UPDATE
  SET anthropic_api_key_enc = EXCLUDED.anthropic_api_key_enc,
      amika_api_key_enc     = EXCLUDED.amika_api_key_enc,
      github_auth_token_enc = EXCLUDED.github_auth_token_enc,
      amika_base_url        = EXCLUDED.amika_base_url,
      amika_claude_cred_id  = EXCLUDED.amika_claude_cred_id,
      updated_at            = now()

-- UpsertProject
INSERT INTO projects (owner_user_id, name, repo_url, amika_snapshot, brain_model, worker_count)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (owner_user_id) DO UPDATE
  SET name = EXCLUDED.name, repo_url = EXCLUDED.repo_url,
      amika_snapshot = EXCLUDED.amika_snapshot,
      brain_model = EXCLUDED.brain_model, worker_count = EXCLUDED.worker_count
RETURNING id, owner_user_id, name, repo_url, amika_snapshot, brain_model, worker_count, created_at
```

`GetUserConfig` scans `sql.ErrNoRows` into a zero `UserConfig{UserID: userID}` and returns nil error (per the port doc). `TouchSession`/`DeleteSession` are single UPDATE/DELETE statements; `InsertSession` a plain INSERT; `GetSession` selects the sessions row only.

- [ ] **Step 6: Run integration tests**

Run (compose db must be up: `docker compose up -d db`, and create the test DB once: `docker compose exec db psql -U kiln -c 'CREATE DATABASE kiln_test' || true`):
```
cd backend && TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
  go test -tags=integration ./internal/identity/postgres/...
```
Expected: PASS (4 tests). Also run `go test ./...` — unit suites stay green.

- [ ] **Step 7: Boot check — migrations apply cleanly**

Run: `docker compose up -d --build backend && docker compose logs backend | tail -20`
Expected: backend healthy, `schema_migrations` gains `internal/identity/postgres/migrations/0001_identity.sql` (check: `docker compose exec db psql -U kiln -d kiln -c "SELECT filename FROM schema_migrations ORDER BY filename" | grep identity`).

- [ ] **Step 8: Commit**

```bash
git add backend/internal/identity backend/cmd/kiln/wiring.go
git commit -m "feat(identity): postgres store + 0001_identity migration (spec 11 §3)"
```

---

### Task 4: GitHub OAuth client (`githubapi`)

**Files:**
- Create: `backend/internal/identity/githubapi/client.go`
- Test: `backend/internal/identity/githubapi/client_test.go`

**Interfaces:**
- Produces:
  - `githubapi.Config{ClientID, ClientSecret, OAuthBaseURL, APIBaseURL string}` (base URLs default to `https://github.com` / `https://api.github.com`; overridable for tests)
  - `githubapi.New(cfg Config, hc *http.Client) *Client` (nil hc → 10s-timeout default)
  - `(*Client).AuthorizeURL(state string) string` — `{OAuthBaseURL}/login/oauth/authorize?client_id=...&state=...` (no scopes, no redirect_uri: GitHub uses the OAuth app's registered callback — spec 11 §2)
  - `(*Client).ExchangeCode(ctx context.Context, code string) (string, error)` — returns the access token
  - `(*Client).FetchUser(ctx context.Context, accessToken string) (GitHubUser, error)`
  - `githubapi.GitHubUser{ID int64 "json:\"id\""; Login string "json:\"login\""; Name string "json:\"name\""; AvatarURL string "json:\"avatar_url\""}`
  - Sentinels: `ErrExchange`, `ErrFetchUser`

- [ ] **Step 1: Write failing tests** — `client_test.go` (package `githubapi_test`), each spinning an `httptest.NewServer`:

```go
func TestAuthorizeURL(t *testing.T)          // contains client_id + state, path /login/oauth/authorize, no scope param
func TestExchangeCodeSuccess(t *testing.T)   // fake server asserts: POST /login/oauth/access_token, Accept: application/json, form/JSON body carries client_id+client_secret+code; responds {"access_token":"gho_x","token_type":"bearer"}; client returns "gho_x"
func TestExchangeCodeOAuthError(t *testing.T) // 200 with {"error":"bad_verification_code","error_description":"..."} → error wrapping ErrExchange containing the description (GitHub returns 200 for OAuth errors!)
func TestExchangeCodeHTTPError(t *testing.T)  // 503 → error wrapping ErrExchange
func TestFetchUserSuccess(t *testing.T)       // asserts GET /user with Authorization: Bearer gho_x; responds {"id":123,"login":"Crabtree-Michael","name":"Michael","avatar_url":"https://..."} → struct populated verbatim (case preserved; lowering is the service's job)
func TestFetchUserUnauthorized(t *testing.T)  // 401 → error wrapping ErrFetchUser
```

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/identity/githubapi/...` → FAIL (undefined).

- [ ] **Step 3: Implement `client.go`.** Follow `internal/voice/assemblyai/client.go`'s shape (Config, New with defaults + trailing-slash trim, 10s `http.Client`, `maxErrorBody = 1<<20` LimitReader on error bodies, sentinel-wrapped errors). Key mechanics:

```go
// ExchangeCode: POST {OAuthBaseURL}/login/oauth/access_token
//   Headers: Accept: application/json, Content-Type: application/json
//   Body:    {"client_id":..., "client_secret":..., "code": code}
// Decode: struct{ AccessToken string `json:"access_token"`; Error string `json:"error"`; ErrorDescription string `json:"error_description"` }
// resp.StatusCode >= 400 → fmt.Errorf("githubapi: exchange: %w: http %d", ErrExchange, code)
// body.Error != ""       → fmt.Errorf("githubapi: exchange: %w: %s (%s)", ErrExchange, body.Error, body.ErrorDescription)
// body.AccessToken == "" → wrap ErrExchange

// FetchUser: GET {APIBaseURL}/user
//   Headers: Authorization: Bearer <token>, Accept: application/vnd.github+json
// non-200 → wrap ErrFetchUser; else decode GitHubUser.
```

The package doc comment declares this the one place GitHub-OAuth vocabulary is legal, and that `ClientSecret` never leaves the backend (02 §2).

- [ ] **Step 4: Run tests** — `cd backend && go test ./internal/identity/githubapi/...` → PASS (6 tests).

- [ ] **Step 5: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/identity/... && cd ..
git add backend/internal/identity/githubapi
git commit -m "feat(identity): github oauth client — exchange + user fetch (spec 11 §2)"
```

---

### Task 5: identity Service — login, allowlist, sessions

**Files:**
- Create: `backend/internal/identity/service.go`, `backend/internal/identity/fakes_test.go`
- Test: `backend/internal/identity/service_test.go`

**Interfaces:**
- Consumes: `identity.Store` (Task 3), `githubapi.GitHubUser` shape (Task 4 — but through a local port, see below).
- Produces (Tasks 6–10 depend on these exact signatures):

```go
// GitHub is the service's port onto the OAuth provider — satisfied directly
// by *githubapi.Client (the consumer declares the interface, 02 §2).
type GitHub interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchUser(ctx context.Context, accessToken string) (githubapi.GitHubUser, error)
}

func NewService(store Store, cipher *Cipher, gh GitHub, allowedLogins []string) *Service

func (s *Service) LoginURL(state string) string
func (s *Service) CompleteLogin(ctx context.Context, code string) (User, error)   // ErrNotAllowed when not allowlisted
func (s *Service) CreateSession(ctx context.Context, userID string) (token string, expiresAt time.Time, err error)
func (s *Service) ResolveSession(ctx context.Context, token string) (User, error) // ErrNoSession; sliding renewal
func (s *Service) Logout(ctx context.Context, token string) error
func (s *Service) DevSignIn(ctx context.Context, login string) (User, error)      // dev-endpoint only: find-or-create, NO allowlist check
```

Constants: `sessionTTL = 30 * 24 * time.Hour`, `sessionRenewBelow = 15 * 24 * time.Hour`. `Service` has a `now func() time.Time` field defaulting to `time.Now` (tests override — matches board's clock discipline).

- [ ] **Step 1: Write `fakes_test.go`** — package `identity_test`: `fakeStore` (maps + `sync.Mutex`, implements every `Store` method; `UpsertUser` keys on GitHubID and assigns `fmt.Sprintf("user-%d", n)` ids; `GetUserConfig` returns zero-value when absent), `fakeGitHub` (fields `token string`, `user githubapi.GitHubUser`, `exchangeErr, fetchErr error`; records the code/token it was called with). Include `var _ identity.Store = (*fakeStore)(nil)`.

- [ ] **Step 2: Write failing service tests** — `service_test.go`:

```go
func TestCompleteLoginAllowlisted(t *testing.T)
// gh returns Login "Crabtree-Michael"; allowlist []string{"crabtree-michael"} (case-insensitive both sides)
// → user created with GitHubLogin "crabtree-michael"; second login same GitHubID → same user ID (find-or-create)

func TestCompleteLoginNotAllowlisted(t *testing.T)
// allowlist {"someone-else"} → errors.Is(err, identity.ErrNotAllowed); store has no user

func TestCompleteLoginEmptyAllowlist(t *testing.T)
// allowlist nil → EVERY login rejected (empty var ⇒ nobody signs up, spec 11 §8)

func TestAllowlistCheckedOnEveryLogin(t *testing.T)
// login OK; rebuild service with the login removed from allowlist; CompleteLogin again → ErrNotAllowed

func TestSessionRoundTrip(t *testing.T)
// CreateSession → token is ≥ 40 chars, expiresAt ≈ now+30d
// ResolveSession(token) → same user; store holds sha256 hash, NOT the raw token (assert raw token absent from fakeStore keys)

func TestResolveSessionExpired(t *testing.T)
// now := base; create; advance now beyond 30d → ErrNoSession (and session deleted from store)

func TestResolveSessionSlides(t *testing.T)
// advance now +20d (remaining 10d < renewBelow 15d) → resolve succeeds AND expires_at extended to now+30d

func TestResolveSessionUnknownOrEmpty(t *testing.T) // "" and "nope" → ErrNoSession

func TestLogout(t *testing.T) // create → logout → resolve → ErrNoSession; logout of unknown token → nil (idempotent)

func TestDevSignInBypassesAllowlist(t *testing.T)
// allowlist nil; DevSignIn(ctx, "e2e-user") → user created (github_id derived deterministically, e.g. hash of login; login lower-cased)
```

- [ ] **Step 3: Run to verify failure** — `cd backend && go test ./internal/identity/` → FAIL.

- [ ] **Step 4: Implement the login/session half of `service.go`:**

```go
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	sessionTTL        = 30 * 24 * time.Hour
	sessionRenewBelow = 15 * 24 * time.Hour
)

// Service is identity's domain service (11 §2–§4): login, sessions, config.
type Service struct {
	store   Store
	cipher  *Cipher
	gh      GitHub
	allowed map[string]bool
	now     func() time.Time
	verifier Verifier // set via SetVerifier (Task 7); nil ⇒ Verify returns skipped checks
}

func NewService(store Store, cipher *Cipher, gh GitHub, allowedLogins []string) *Service {
	allowed := make(map[string]bool, len(allowedLogins))
	for _, l := range allowedLogins {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			allowed[l] = true
		}
	}
	return &Service{store: store, cipher: cipher, gh: gh, allowed: allowed, now: time.Now}
}

func (s *Service) LoginURL(state string) string { return s.gh.AuthorizeURL(state) }

// CompleteLogin exchanges the OAuth code, enforces the allowlist on every
// login (11 §2), and finds-or-creates the user.
func (s *Service) CompleteLogin(ctx context.Context, code string) (User, error) {
	token, err := s.gh.ExchangeCode(ctx, code)
	if err != nil {
		return User{}, fmt.Errorf("identity: complete login: %w", err)
	}
	ghUser, err := s.gh.FetchUser(ctx, token)
	if err != nil {
		return User{}, fmt.Errorf("identity: complete login: %w", err)
	}
	login := strings.ToLower(ghUser.Login)
	if !s.allowed[login] {
		return User{}, ErrNotAllowed
	}
	return s.upsertFromGitHub(ctx, ghUser)
}

// DevSignIn is the KILN_DEV_ENDPOINTS-only seam (11 §7): find-or-create with
// NO allowlist check, so e2e can mint sessions without real OAuth.
func (s *Service) DevSignIn(ctx context.Context, login string) (User, error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(login)))
	return s.upsertFromGitHub(ctx, githubapi.GitHubUser{
		ID:    int64(h.Sum64() & 0x7fffffffffffffff), //nolint:gosec // deterministic dev id, not crypto
		Login: login,
		Name:  login,
	})
}

func (s *Service) upsertFromGitHub(ctx context.Context, gh githubapi.GitHubUser) (User, error) {
	u, err := s.store.UpsertUser(ctx, User{
		GitHubID:    gh.ID,
		GitHubLogin: strings.ToLower(gh.Login),
		DisplayName: gh.Name,
		AvatarURL:   gh.AvatarURL,
	})
	if err != nil {
		return User{}, fmt.Errorf("identity: upsert user: %w", err)
	}
	return u, nil
}

func (s *Service) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("identity: session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.now().Add(sessionTTL)
	err := s.store.InsertSession(ctx, Session{TokenHash: hashToken(token), UserID: userID, ExpiresAt: expires})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("identity: create session: %w", err)
	}
	return token, expires, nil
}

// ResolveSession authenticates a request: unknown/expired ⇒ ErrNoSession;
// under half the TTL remaining ⇒ slide the window (11 §2).
func (s *Service) ResolveSession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNoSession
	}
	sess, user, err := s.store.GetSessionUser(ctx, hashToken(token))
	if err != nil {
		return User{}, ErrNoSession
	}
	now := s.now()
	if now.After(sess.ExpiresAt) {
		_ = s.store.DeleteSession(ctx, sess.TokenHash)
		return User{}, ErrNoSession
	}
	if sess.ExpiresAt.Sub(now) < sessionRenewBelow {
		if err := s.store.TouchSession(ctx, sess.TokenHash, now.Add(sessionTTL)); err != nil {
			return User{}, fmt.Errorf("identity: touch session: %w", err)
		}
	}
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.store.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("identity: logout: %w", err)
	}
	return nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
```

(`fakeStore.DeleteSession` must return nil for unknown hashes so logout is idempotent; the postgres DELETE already behaves that way.)

- [ ] **Step 5: Run tests** — `cd backend && go test ./internal/identity/` → PASS (10 new + Task 2's 5).

- [ ] **Step 6: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/identity/... && cd ..
git add backend/internal/identity
git commit -m "feat(identity): service — oauth login, allowlist, sliding sessions (spec 11 §2)"
```

---

### Task 6: identity Service — Me, UpdateSettings, UpsertProject

**Files:**
- Modify: `backend/internal/identity/service.go`
- Test: `backend/internal/identity/service_test.go` (extend), `backend/internal/identity/fakes_test.go` (extend if needed)

**Interfaces:**
- Produces (Task 9's handlers depend on these):

```go
func (s *Service) Me(ctx context.Context, userID string) (Me, error)
func (s *Service) UpdateSettings(ctx context.Context, userID string, upd SettingsUpdate) error
func (s *Service) UpsertProject(ctx context.Context, userID string, upd ProjectUpdate) (Project, error)
```

- [ ] **Step 1: Write failing tests** (extend `service_test.go`; build a `User` via `DevSignIn` in each):

```go
func TestMeEmpty(t *testing.T)
// fresh user → Me.Project == nil; every SecretStatus{Set:false, Tail:""}; clear fields ""

func TestUpdateSettingsWriteAndStatus(t *testing.T)
// UpdateSettings{AnthropicKey:"sk-ant-abcx4Kd", AmikaBaseURL:"https://api.amika.dev"}
// → Me shows AnthropicKey{Set:true, Tail:"x4Kd"}, AmikaBaseURL round-trips in clear,
//   AmikaKey{Set:false}; stored bytes in fakeStore are NOT the plaintext (encrypted)

func TestUpdateSettingsPartialMerge(t *testing.T)
// write anthropic; then UpdateSettings{AmikaKey:"amk-999zZ"} with empty AnthropicKey
// → anthropic unchanged (Set:true, same tail), amika now set (empty = unchanged, 11 §4)

func TestUpdateSettingsOverwrite(t *testing.T)
// write anthropic "aaaa1111"; overwrite "bbbb2222" → Tail "2222"

func TestUpsertProjectCreatesThenUpdates(t *testing.T)
// UpsertProject{Name:"kiln", RepoURL:"https://github.com/x/y", WorkerCount:0}
// → WorkerCount defaulted to 3; Me.Project non-nil
// second call {Name:"kiln", RepoURL:"...", BrainModel:"claude-haiku-4-5-20251001", WorkerCount:5} → same project ID, fields updated

func TestUpsertProjectValidates(t *testing.T)
// empty Name or empty RepoURL → error; WorkerCount 11 → error (matches DB CHECK 1..10)
```

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/identity/` → FAIL.

- [ ] **Step 3: Implement.** Append to `service.go`:

```go
// Me assembles the account view: fingerprints only, never secret values (11 §3 D7).
func (s *Service) Me(ctx context.Context, userID string) (Me, error) {
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return Me{}, fmt.Errorf("identity: me: %w", err)
	}
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return Me{}, fmt.Errorf("identity: me: %w", err)
	}
	me := Me{User: user, Settings: MeSettings{
		AnthropicKey:      s.secretStatus(cfg.AnthropicKeyEnc),
		AmikaKey:          s.secretStatus(cfg.AmikaKeyEnc),
		GitHubToken:       s.secretStatus(cfg.GitHubTokenEnc),
		AmikaBaseURL:      cfg.AmikaBaseURL,
		AmikaClaudeCredID: cfg.AmikaClaudeCredID,
	}}
	proj, err := s.store.GetProjectByOwner(ctx, userID)
	switch {
	case err == nil:
		me.Project = &proj
	case errors.Is(err, ErrNotFound): // onboarding not done yet
	default:
		return Me{}, fmt.Errorf("identity: me: %w", err)
	}
	return me, nil
}

func (s *Service) secretStatus(enc []byte) SecretStatus {
	if len(enc) == 0 {
		return SecretStatus{}
	}
	plain, err := s.cipher.Decrypt(enc)
	if err != nil { // wrong master key / corrupt row: surface as set-but-unreadable
		return SecretStatus{Set: true, Tail: "????"}
	}
	return SecretStatus{Set: true, Tail: Tail(plain)}
}
```

Wait — `Store` (Task 3) has no `GetUser`. **Add it to the interface, the postgres store (`SELECT ... FROM users WHERE id = $1`, ErrNotFound on no rows), the fake, and one integration-test assertion in Task 3's file.** (This is the deliberate seam-fix; do it as part of this task.)

```go
// UpdateSettings merges non-empty fields over the stored row (read-modify-write;
// empty = unchanged — recorded deviation, no clear operation in phase 1).
func (s *Service) UpdateSettings(ctx context.Context, userID string, upd SettingsUpdate) error {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: update settings: %w", err)
	}
	cfg.UserID = userID
	if err := s.mergeSecret(&cfg.AnthropicKeyEnc, upd.AnthropicKey); err != nil {
		return err
	}
	if err := s.mergeSecret(&cfg.AmikaKeyEnc, upd.AmikaKey); err != nil {
		return err
	}
	if err := s.mergeSecret(&cfg.GitHubTokenEnc, upd.GitHubToken); err != nil {
		return err
	}
	if upd.AmikaBaseURL != "" {
		cfg.AmikaBaseURL = upd.AmikaBaseURL
	}
	if upd.AmikaClaudeCredID != "" {
		cfg.AmikaClaudeCredID = upd.AmikaClaudeCredID
	}
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		return fmt.Errorf("identity: update settings: %w", err)
	}
	return nil
}

func (s *Service) mergeSecret(dst *[]byte, plaintext string) error {
	if plaintext == "" {
		return nil
	}
	enc, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("identity: encrypt secret: %w", err)
	}
	*dst = enc
	return nil
}

var ErrInvalidProject = errors.New("identity: project needs a name, a repo_url, and worker_count 1-10")

func (s *Service) UpsertProject(ctx context.Context, userID string, upd ProjectUpdate) (Project, error) {
	if upd.WorkerCount == 0 {
		upd.WorkerCount = 3
	}
	if upd.Name == "" || upd.RepoURL == "" || upd.WorkerCount < 1 || upd.WorkerCount > 10 {
		return Project{}, ErrInvalidProject
	}
	p, err := s.store.UpsertProject(ctx, Project{
		OwnerUserID:   userID,
		Name:          upd.Name,
		RepoURL:       upd.RepoURL,
		AmikaSnapshot: upd.AmikaSnapshot,
		BrainModel:    upd.BrainModel,
		WorkerCount:   upd.WorkerCount,
	})
	if err != nil {
		return Project{}, fmt.Errorf("identity: upsert project: %w", err)
	}
	return p, nil
}
```

(Move `ErrInvalidProject` into `errors.go` with the other sentinels.)

- [ ] **Step 4: Run tests** — `cd backend && go test ./internal/identity/` → PASS; also rerun the integration suite from Task 3 step 6 (now including `GetUser`).

- [ ] **Step 5: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/identity/... && cd ..
git add backend/internal/identity
git commit -m "feat(identity): me/settings/project — encrypted write-only config (spec 11 §3-§4)"
```

---

### Task 7: connection verifier + Service.Verify

**Files:**
- Create: `backend/internal/identity/verify/verify.go`
- Test: `backend/internal/identity/verify/verify_test.go`
- Modify: `backend/internal/identity/service.go` (+`service_test.go`, `fakes_test.go`)

**Interfaces:**
- Produces:
  - In `identity`: `type Verifier interface { VerifyAnthropic(ctx context.Context, apiKey string) CheckResult; VerifyAmika(ctx context.Context, baseURL, apiKey string) CheckResult; VerifyRepo(ctx context.Context, repoURL, token string) CheckResult }`, `(*Service).SetVerifier(v Verifier)`, `(*Service).Verify(ctx context.Context, userID string) ([]CheckResult, error)`
  - In `verify`: `verify.New() *Verifier` (struct with `hc *http.Client{Timeout: 10s}`, `AnthropicBaseURL`/default `https://api.anthropic.com`), satisfying the identity port. `var _ identity.Verifier = (*Verifier)(nil)`.

- [ ] **Step 1: Write failing verifier tests** — `verify_test.go` (package `verify_test`):

```go
func TestVerifyAnthropicOK(t *testing.T)     // httptest: asserts GET /v1/models with x-api-key + anthropic-version headers; 200 → {name:"anthropic", status:"ok"}
func TestVerifyAnthropicBadKey(t *testing.T) // 401 → status "failed", message contains "401"
func TestVerifyAmikaOK(t *testing.T)         // httptest: asserts GET /sandboxes with Authorization: Bearer; 200 → ok  (same call tests/global-teardown.ts uses)
func TestVerifyAmikaBadKey(t *testing.T)     // 401 → failed
func TestVerifyAmikaNoBaseURL(t *testing.T)  // baseURL "" → failed with "amika_base_url not set"
func TestVerifyRepoOK(t *testing.T)          // create a local bare repo: git init --bare + one commit pushed via a temp clone (t.TempDir, exec git); VerifyRepo("file:///...", "") → ok
func TestVerifyRepoMissing(t *testing.T)     // file:// URL to an empty dir → failed
func TestAuthedCloneURL(t *testing.T)        // exported helper: ("https://github.com/x/y", "tok") → "https://x-access-token:tok@github.com/x/y"; non-https or empty token → unchanged; result never logged
```

Skip the two git tests with `t.Skip` if `exec.LookPath("git")` fails.

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/identity/verify/...` → FAIL.

- [ ] **Step 3: Implement `verify.go`.**
  - `VerifyAnthropic`: `GET {AnthropicBaseURL}/v1/models`, headers `x-api-key: <key>`, `anthropic-version: 2023-06-01` — free, no tokens billed. 200 → ok; else failed with `http <code>`.
  - `VerifyAmika`: `GET {baseURL}/sandboxes`, `Authorization: Bearer <key>`. 200 → ok.
  - `VerifyRepo`: `exec.CommandContext(ctx, "git", "ls-remote", AuthedCloneURL(repoURL, token), "HEAD")` with `cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")` and a 15s context timeout. Success → ok "repo reachable". Failure message must be the static `"git ls-remote failed"` + exit code — NEVER the command output or URL (the token is embedded in the URL).
  - Every check catches its own errors and returns a `CheckResult` — `Verify*` methods never return Go errors; the HTTP layer always gets a full slice.

- [ ] **Step 4: Wire into the service** (modify `service.go`; extend `service_test.go` with a `fakeVerifier` recording calls):

```go
// SetVerifier injects the live-check adapter (nil-safe: without it every
// check reports skipped). Setter, not constructor arg, to keep NewService's
// signature stable for tests that don't verify.
func (s *Service) SetVerifier(v Verifier) { s.verifier = v }

// Verify runs live checks for each configured credential group; unconfigured
// groups report "skipped" (11 §4). Order is fixed: anthropic, amika, repo.
func (s *Service) Verify(ctx context.Context, userID string) ([]CheckResult, error) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: verify: %w", err)
	}
	anthropicKey, _ := s.decrypt(cfg.AnthropicKeyEnc)
	amikaKey, _ := s.decrypt(cfg.AmikaKeyEnc)
	ghToken, _ := s.decrypt(cfg.GitHubTokenEnc)
	repoURL := ""
	if p, err := s.store.GetProjectByOwner(ctx, userID); err == nil {
		repoURL = p.RepoURL
	}
	checks := make([]CheckResult, 0, 3)
	checks = append(checks, s.check(ctx, "anthropic", anthropicKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAnthropic(ctx, anthropicKey)
	}))
	checks = append(checks, s.check(ctx, "amika", amikaKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAmika(ctx, cfg.AmikaBaseURL, amikaKey)
	}))
	checks = append(checks, s.check(ctx, "repo", repoURL != "" ,func(ctx context.Context) CheckResult {
		return s.verifier.VerifyRepo(ctx, repoURL, ghToken)
	}))
	return checks, nil
}

func (s *Service) check(ctx context.Context, name string, configured bool, run func(context.Context) CheckResult) CheckResult {
	if !configured || s.verifier == nil {
		return CheckResult{Name: name, Status: "skipped", Message: "not configured"}
	}
	res := run(ctx)
	res.Name = name
	return res
}

func (s *Service) decrypt(enc []byte) (string, error) {
	if len(enc) == 0 {
		return "", nil
	}
	return s.cipher.Decrypt(enc)
}
```

Service tests: `TestVerifySkipsUnconfigured` (fresh user → 3 skipped), `TestVerifyRunsConfigured` (set anthropic key + project repo → fakeVerifier called with the DECRYPTED key and the project's repo URL; amika skipped).

- [ ] **Step 5: Run + lint + commit**

Run: `cd backend && go test ./internal/identity/... && gofmt -l . && golangci-lint run ./internal/identity/...` → PASS/clean.

```bash
git add backend/internal/identity
git commit -m "feat(identity): live connection verifier — anthropic/amika/repo (spec 11 §4)"
```

---

### Task 8: api — sessions, cookies, and the /auth routes

**Files:**
- Create: `backend/internal/api/session.go`, `backend/internal/api/auth_handlers.go`
- Test: `backend/internal/api/auth_handlers_test.go`
- Modify: `backend/internal/api/routes.go` (ports + `EnableIdentity` + mounts), `backend/internal/api/fakes_test.go` (fake ports)

**Interfaces:**
- Consumes: `identity.User`, `identity.ErrNotAllowed`, `identity.ErrNoSession` (api already imports domain packages for board; same precedent).
- Produces (Task 9 + Task 10 depend on these):

```go
// routes.go — ports satisfied directly by *identity.Service, no adapters:
type Authenticator interface {
	LoginURL(state string) string
	CompleteLogin(ctx context.Context, code string) (identity.User, error)
	CreateSession(ctx context.Context, userID string) (string, time.Time, error)
	ResolveSession(ctx context.Context, token string) (identity.User, error)
	Logout(ctx context.Context, token string) error
}
func (s *Server) EnableIdentity(auth Authenticator, account AccountService) // AccountService defined in Task 9

// session.go:
const sessionCookie = "kiln_session"
const stateCookie = "kiln_oauth_state"
func randomToken() (string, error)                       // 32 bytes crypto/rand, base64.RawURLEncoding
func (s *Server) withSession(next func(http.ResponseWriter, *http.Request, identity.User)) http.HandlerFunc
func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) // HttpOnly, SameSite=Lax, Path=/, Secure when r.TLS!=nil or X-Forwarded-Proto==https
func clearCookie(w http.ResponseWriter, r *http.Request, name string)
```

- [ ] **Step 1: Write failing route tests** — `auth_handlers_test.go`, package `api_test`, using `httptest.NewServer(server.Handler())` like `routes_test.go`. Add to `fakes_test.go` a `fakeAuth` implementing `Authenticator` (+ a minimal `fakeAccount` stub for `EnableIdentity`'s second arg — full version in Task 9):

```go
func TestAuthLoginRedirects(t *testing.T)
// GET /auth/github/login (client with CheckRedirect → ErrUseLastResponse)
// → 302; Location == fakeAuth.LoginURL(state); Set-Cookie kiln_oauth_state present,
//   HttpOnly, Max-Age ≤ 600; the state in the cookie equals the state query param in Location

func TestAuthCallbackSuccess(t *testing.T)
// GET /auth/github/callback?code=c1&state=<state> with the state cookie set
// → fakeAuth.CompleteLogin called with "c1"; 302 → /dashboard;
//   Set-Cookie kiln_session=<token from fakeAuth.CreateSession>, HttpOnly, SameSite=Lax;
//   state cookie cleared (Max-Age=0 or expired)

func TestAuthCallbackStateMismatch(t *testing.T)
// cookie state ≠ query state → 400, CompleteLogin NOT called

func TestAuthCallbackMissingStateCookie(t *testing.T) // no cookie → 400

func TestAuthCallbackNotAllowlisted(t *testing.T)
// fakeAuth.CompleteLogin returns identity.ErrNotAllowed
// → 403, body contains "invite-only", NO session cookie set

func TestAuthCallbackGitHubDown(t *testing.T)
// CompleteLogin returns a plain error → 502

func TestLogout(t *testing.T)
// POST /auth/logout with session cookie → 204; fakeAuth.Logout called with the raw token; cookie cleared
// POST /auth/logout without cookie → 204 (idempotent)

func TestIdentityRoutesAbsentWhenDisabled(t *testing.T)
// server WITHOUT EnableIdentity: GET /auth/github/login → 404 (SPA/no route), proving unconfigured boot unchanged
```

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/api/` → FAIL (undefined).

- [ ] **Step 3: Implement `session.go` + `auth_handlers.go` + mounts.**

`session.go` per the Produces block. `withSession`:

```go
// withSession authenticates the request via the kiln_session cookie and hands
// the user to the wrapped handler; 401 otherwise. Phase 1 guards ONLY the
// identity routes (11 §2) — do not wrap existing handlers.
func (s *Server) withSession(next func(http.ResponseWriter, *http.Request, identity.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		user, err := s.auth.ResolveSession(r.Context(), c.Value)
		if err != nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next(w, r, user)
	}
}
```

`auth_handlers.go`: `handleAuthLogin` (randomToken → state cookie 10 min → 302 to `s.auth.LoginURL(state)`), `handleAuthCallback` (constant-time-compare state cookie vs query via `subtle.ConstantTimeCompare`; `CompleteLogin`; `errors.Is(err, identity.ErrNotAllowed)` → 403 with a small inline HTML page — plain string const `inviteOnlyPage` saying "Kiln is invite-only. Ask for your GitHub username to be added."; other errors → 502; success → `CreateSession` → session cookie for `expires.Sub(now)` → 302 `/dashboard`), `handleLogout` (read cookie if present → `s.auth.Logout` → clear → 204).

`routes.go`: add fields `auth Authenticator`, `account AccountService`; `EnableIdentity` sets both; in `Handler()`:

```go
	if s.auth != nil {
		mux.HandleFunc("GET /auth/github/login", s.handleAuthLogin)
		mux.HandleFunc("GET /auth/github/callback", s.handleAuthCallback)
		mux.HandleFunc("POST /auth/logout", s.handleLogout)
	}
```

(Explicit routes beat the `"/"` SPA catch-all, so prod serving needs no web/embed change.)

- [ ] **Step 4: Run tests** — `cd backend && go test ./internal/api/` → PASS (8 new; all pre-existing route tests untouched and green).

- [ ] **Step 5: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/api/... && cd ..
git add backend/internal/api
git commit -m "feat(api): github oauth routes + cookie sessions (spec 11 §2)"
```

---

### Task 9: api — /api/me, settings, project, verify + dev session

**Files:**
- Create: `backend/internal/api/identity_handlers.go`
- Test: `backend/internal/api/identity_handlers_test.go`
- Modify: `backend/internal/api/routes.go` (AccountService port, DevSessionMinter, mounts), `backend/internal/api/fakes_test.go` (full `fakeAccount`)

**Interfaces:**
- Consumes: `wire.Me`, `wire.SettingsUpdateRequest`, `wire.ProjectUpdateRequest`, `wire.VerifyResponse` (Task 1); `withSession` (Task 8); identity types.
- Produces:

```go
type AccountService interface {
	Me(ctx context.Context, userID string) (identity.Me, error)
	UpdateSettings(ctx context.Context, userID string, upd identity.SettingsUpdate) error
	UpsertProject(ctx context.Context, userID string, upd identity.ProjectUpdate) (identity.Project, error)
	Verify(ctx context.Context, userID string) ([]identity.CheckResult, error)
}
type DevSessionMinter interface {
	DevSignIn(ctx context.Context, login string) (identity.User, error)
	CreateSession(ctx context.Context, userID string) (string, time.Time, error)
}
func (s *Server) EnableDevSession(m DevSessionMinter)
func meToWire(me identity.Me) wire.Me   // the single domain→wire mapper, next to boardToWire's precedent
```

- [ ] **Step 1: Write failing tests** — `identity_handlers_test.go`:

```go
func TestMeRequiresSession(t *testing.T)        // GET /api/me, no cookie → 401
func TestMeReturnsAccountView(t *testing.T)     // with cookie (fakeAuth resolves) → 200; JSON: user.github_login, project absent (null), settings.anthropic_api_key.set == false
func TestMeWithProjectAndSecrets(t *testing.T)  // fakeAccount primed → project fields + {set:true, tail:"x4Kd"} serialized; assert raw secret NEVER in body
func TestPutSettings(t *testing.T)              // PUT /api/settings {"anthropic_api_key":"sk-x"} → fakeAccount received SettingsUpdate{AnthropicKey:"sk-x"}; 200 body is refreshed Me
func TestPutSettingsBadBody(t *testing.T)       // malformed JSON → 400
func TestPutProject(t *testing.T)               // PUT /api/project {"name":"kiln","repo_url":"https://github.com/x/y","worker_count":5} → mapped ProjectUpdate; 200 Me
func TestPutProjectInvalid(t *testing.T)        // fakeAccount returns identity.ErrInvalidProject → 400
func TestVerify(t *testing.T)                   // POST /api/settings/verify → 200 {"checks":[...3 items...]} in anthropic/amika/repo order
func TestDevSessionMintsCookie(t *testing.T)    // EnableDevSession on: POST /api/dev/session {"github_login":"e2e-user"} → 200, Set-Cookie kiln_session, body {token, expires_at}
func TestDevSessionAbsentByDefault(t *testing.T) // without EnableDevSession → 404
```

- [ ] **Step 2: Run to verify failure** — `cd backend && go test ./internal/api/` → FAIL.

- [ ] **Step 3: Implement `identity_handlers.go`.** Handlers all wrapped `s.withSession(...)`; decode into wire types; map wire↔identity field-for-field (`derefOr(ptr, "")` helper for oapi-codegen's optional `*string`/`*int` fields); errors: `ErrInvalidProject` → 400, else 500 via slog+http.Error per repo convention. `meToWire` maps `identity.Me` → `wire.Me` including `Project` nil-ness and each `SecretStatus`. PUT handlers re-fetch `account.Me` after the write and return it (one round-trip refresh, matches Task 1 contract). Dev session handler (mounted only when `s.devSession != nil`, inside the same `if s.seeder != nil` dev gating style):

```go
mux.HandleFunc("GET /api/me", s.withSession(s.handleMe))
mux.HandleFunc("PUT /api/settings", s.withSession(s.handlePutSettings))
mux.HandleFunc("PUT /api/project", s.withSession(s.handlePutProject))
mux.HandleFunc("POST /api/settings/verify", s.withSession(s.handleVerify))
// dev-only (KILN_DEV_ENDPOINTS=1 AND identity enabled):
if s.devSession != nil {
	mux.HandleFunc("POST /api/dev/session", s.handleDevSession)
}
```

All four identity mounts live inside the `if s.auth != nil` block from Task 8.

- [ ] **Step 4: Run tests** — `cd backend && go test ./internal/api/` → PASS.

- [ ] **Step 5: Lint + commit**

```bash
cd backend && gofmt -l . && golangci-lint run ./internal/api/... && cd ..
git add backend/internal/api
git commit -m "feat(api): me/settings/project/verify endpoints + dev session mint (spec 11 §4, §7)"
```

---

### Task 10: composition root — wiring, env, compose

**Files:**
- Modify: `backend/cmd/kiln/main.go` (Config), `backend/cmd/kiln/wiring.go` (identity construction), `docker-compose.yml`, `.env.example`

**Interfaces:**
- Consumes: `identity.NewService/NewCipher/SetVerifier`, `identitypg.New`, `githubapi.New`, `verify.New`, `server.EnableIdentity/EnableDevSession` (Tasks 3–9).
- Produces: env contract — `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, `KILN_ALLOWED_GITHUB_USERS`, `KILN_SECRETS_KEY`. (`KILN_BOOTSTRAP_GITHUB_USER` is phase 2 — do NOT add it.)

- [ ] **Step 1: Extend `Config` + `loadConfig()`** in `main.go`:

```go
	// Multi-user phase 1 (11 §2, §7): dashboard auth. All four unset ⇒ the
	// identity surface is not mounted and the binary behaves exactly as before.
	GitHubOAuthClientID     string   // GITHUB_OAUTH_CLIENT_ID
	GitHubOAuthClientSecret string   // GITHUB_OAUTH_CLIENT_SECRET
	AllowedGitHubUsers      []string // KILN_ALLOWED_GITHUB_USERS — comma-separated logins
	SecretsKey              string   // KILN_SECRETS_KEY — 64 hex chars; malformed-but-set ⇒ refuse boot
```

In `loadConfig()`: `AllowedGitHubUsers: splitCSV(os.Getenv("KILN_ALLOWED_GITHUB_USERS"))` with a small `splitCSV` helper (split, trim, drop empties).

- [ ] **Step 2: Construct identity in `buildGraph`** (before `api.NewServer`; enable after):

```go
	// Identity / dashboard auth (11 §2): mounted only when configured, so an
	// unconfigured boot is today's boot. Malformed KILN_SECRETS_KEY fails hard
	// (11 §3) — a half-working cipher must never silently store plaintext.
	var idSvc *identity.Service
	if cfg.GitHubOAuthClientID != "" && cfg.SecretsKey != "" {
		cipher, err := identity.NewCipher(cfg.SecretsKey)
		if err != nil {
			return graph{}, fmt.Errorf("wiring: %w", err)
		}
		gh := githubapi.New(githubapi.Config{
			ClientID:     cfg.GitHubOAuthClientID,
			ClientSecret: cfg.GitHubOAuthClientSecret,
		}, nil)
		idSvc = identity.NewService(identitypg.New(db), cipher, gh, cfg.AllowedGitHubUsers)
		idSvc.SetVerifier(verify.New())
	} else if cfg.SecretsKey != "" || cfg.GitHubOAuthClientID != "" {
		log.Warn("identity disabled: need both GITHUB_OAUTH_CLIENT_ID and KILN_SECRETS_KEY")
	}
```

In `enableServerRoutes` (pass `idSvc` through): `if idSvc != nil { server.EnableIdentity(idSvc, idSvc); if cfg.DevEndpoints { server.EnableDevSession(idSvc) } }`.

- [ ] **Step 3: Compose + env example.** `docker-compose.yml` backend environment block gains:

```yaml
      # Multi-user phase 1 (spec 11 §2, §7): dashboard GitHub sign-in. All
      # optional locally — unset ⇒ /dashboard shows sign-in but auth 404s.
      GITHUB_OAUTH_CLIENT_ID: ${GITHUB_OAUTH_CLIENT_ID:-}
      GITHUB_OAUTH_CLIENT_SECRET: ${GITHUB_OAUTH_CLIENT_SECRET:-}
      KILN_ALLOWED_GITHUB_USERS: ${KILN_ALLOWED_GITHUB_USERS:-}
      KILN_SECRETS_KEY: ${KILN_SECRETS_KEY:-}
```

`.env.example` gains (mirroring its comment style):

```
# Multi-user phase 1 — dashboard auth (spec 11 §2, §7).
# OAuth app: github.com/settings/applications/new, callback
# http://localhost:5173/auth/github/callback for dev.
GITHUB_OAUTH_CLIENT_ID=
GITHUB_OAUTH_CLIENT_SECRET=
# Comma-separated GitHub logins allowed to sign in (empty = nobody).
KILN_ALLOWED_GITHUB_USERS=
# 32-byte hex master key for per-user secrets: openssl rand -hex 32
KILN_SECRETS_KEY=
```

Also append to the real `.env` (do NOT read it — append only):
`KILN_ALLOWED_GITHUB_USERS=crabtree-michael` and `KILN_SECRETS_KEY=$(openssl rand -hex 32)` via `echo ... >> .env`; the OAuth vars are already there.

- [ ] **Step 4: Boot smoke test**

Run: `docker compose up -d --build backend db && sleep 3 && curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/api/me && curl -s -o /dev/null -w '%{http_code}\n' -I localhost:8080/auth/github/login`
Expected: `401` (identity mounted, no session) and `302`. Then `docker compose logs backend | grep -i 'identity\|error'` — no errors. Also verify an unconfigured boot: `docker compose run --rm -e GITHUB_OAUTH_CLIENT_ID= -e KILN_SECRETS_KEY= backend ...` or temporarily unset in compose — `/api/me` → 404 (route absent), proving the dark-when-unconfigured contract.

- [ ] **Step 5: Full backend gate + commit**

Run: `cd backend && go build ./... && go test ./... && TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable go test -tags=integration ./... && gofmt -l . && golangci-lint run ./...`
Expected: all green.

```bash
git add backend/cmd/kiln docker-compose.yml .env.example
git commit -m "feat(kiln): wire identity service — env-gated dashboard auth (spec 11 §7)"
```

---

### Task 11: frontend transport + dashboard store

**Files:**
- Modify: `frontend/src/transport/transport.ts`
- Create: `frontend/src/dashboard/dashboard-context.ts`, `frontend/src/dashboard/dashboard-store.tsx`
- Test: `frontend/src/dashboard/dashboard-store.test.tsx`, transport guard tests co-located if a transport test file exists (else cover via store tests' mocked fetch)

**Interfaces:**
- Consumes: `components['schemas']['Me'|'SettingsUpdateRequest'|'ProjectUpdateRequest'|'VerifyResponse'|'VerifyCheck']` from `@/schema/generated` (Task 1).
- Produces:

```ts
// transport.ts additions (exact exports):
export type Me = components['schemas']['Me'];
export type MeProject = components['schemas']['MeProject'];
export type SettingsUpdateRequest = components['schemas']['SettingsUpdateRequest'];
export type ProjectUpdateRequest = components['schemas']['ProjectUpdateRequest'];
export type VerifyResponse = components['schemas']['VerifyResponse'];
export type VerifyCheck = components['schemas']['VerifyCheck'];
export async function fetchMe(): Promise<Me | null>;          // null on 401 (signed out) — never throws for 401
export async function putSettings(body: SettingsUpdateRequest): Promise<Me>;
export async function putProject(body: ProjectUpdateRequest): Promise<Me>;
export async function postVerify(): Promise<VerifyResponse>;
export async function postLogout(): Promise<void>;            // POST /auth/logout

// dashboard-context.ts:
export type DashboardPhase = 'loading' | 'signed-out' | 'ready';
export interface DashboardStoreValue {
  phase: DashboardPhase;
  me: Me | null;
  saving: boolean;
  error: string | null;
  verifying: boolean;
  verifyChecks: VerifyCheck[] | null;
  saveSettings: (body: SettingsUpdateRequest) => Promise<void>;
  saveProject: (body: ProjectUpdateRequest) => Promise<void>;
  runVerify: () => Promise<void>;
  signOut: () => Promise<void>;
}
export const DashboardStoreContext: React.Context<DashboardStoreValue | undefined>;
export function useDashboardStore(): DashboardStoreValue;     // throws outside provider (repo pattern)
// dashboard-store.tsx:
export function DashboardProvider({ children }: { children: ReactNode }): JSX.Element;
```

- [ ] **Step 1: Write failing store tests** — `dashboard-store.test.tsx`, mocking the transport boundary exactly like `App.integration.test.tsx` does (`vi.mock('@/transport/transport', ...)`):

```
- loads me on mount → phase 'ready', me populated
- fetchMe resolves null → phase 'signed-out'
- saveSettings calls putSettings and swaps in the returned Me (saving toggles true→false)
- saveProject likewise
- runVerify populates verifyChecks from postVerify
- transport rejection → error set, phase stays 'ready'
- signOut calls postLogout then re-fetches → 'signed-out'
```

- [ ] **Step 2: Run to verify failure** — `cd frontend && pnpm test -- src/dashboard` → FAIL.

- [ ] **Step 3: Implement.** Transport functions copy the `postMessage` pattern verbatim (fetch, `!response.ok` throw with status, `unknown` → type-guard). Guards needed: `isMe` (checks `user.github_login` is string, `settings` object with the three `SecretStatus` shapes — each `set` boolean — and optional `project`), `isVerifyResponse` (checks `checks` array of `{name,status,message}` strings). `fetchMe` special-case: `if (response.status === 401) return null;`. `putSettings`/`putProject` reuse `isMe`. The store is a straight transposition of the chat-store pattern: `useState` for the value pieces, `useCallback` actions, `useEffect` mount-load, `useMemo` context value.

- [ ] **Step 4: Run tests + gate** — `cd frontend && pnpm test -- src/dashboard && pnpm run lint && pnpm run typecheck` → PASS/clean.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/transport frontend/src/dashboard
git commit -m "feat(web): dashboard transport + store (spec 11 §5, §9)"
```

---

### Task 12: dashboard UI — screens, route, styling

**Files:**
- Create: `frontend/src/dashboard/Dashboard.tsx`, `SignIn.tsx`, `Onboarding.tsx`, `Settings.tsx`, `ConfigFields.tsx`, `Dashboard.css`
- Test: `frontend/src/dashboard/Dashboard.test.tsx`
- Modify: `frontend/src/main.tsx` (one route), `frontend/vite.config.ts` (`/auth` proxy)

**Interfaces:**
- Consumes: `DashboardProvider`/`useDashboardStore` (Task 11).
- Produces: selector contract the e2e (Task 13) binds to — **exact strings:**
  - root: `data-role="dashboard"`
  - sign-in link: `getByRole('link', { name: 'Continue with GitHub' })` → `href="/auth/github/login"`
  - project form: `data-role="project-form"`; inputs labeled `Project name`, `Repo URL`, `Amika snapshot`, `Brain model`, `Worker count`; submit `getByRole('button', { name: 'Save project' })`
  - credentials form: `data-role="settings-form"`; password inputs labeled `Anthropic API key`, `Amika API key`, `GitHub token`; text inputs `Amika base URL`, `Amika Claude credential ID`; submit `getByRole('button', { name: 'Save credentials' })`
  - secret status: `data-role="secret-status"` with `data-name="anthropic_api_key|amika_api_key|github_auth_token"`, `data-set="true|false"`, text `configured · …x4Kd` when set
  - verify: `getByRole('button', { name: 'Test connections' })`; each result `data-role="verify-check"` with `data-name="anthropic|amika|repo"` and `data-status="ok|failed|skipped"`
  - sign out: `getByRole('button', { name: 'Sign out' })`

- [ ] **Step 1: Write failing component tests** — `Dashboard.test.tsx` (mock transport; render `<MemoryRouter><Dashboard/></MemoryRouter>`):

```
- signed out (fetchMe → null) → 'Continue with GitHub' link with href /auth/github/login
- signed in, no project → onboarding heading visible ('Set up your project'), project form rendered first
- signed in with project + configured secrets → settings view: secret-status shows 'configured · …x4Kd', data-set="true"
- filling credentials form and submitting calls putSettings with only the filled fields
- 'Test connections' renders three verify-check rows with data-status from postVerify
- DOM snapshot of the settings view (toMatchSnapshot, matching PrimaryScreenView test discipline)
```

- [ ] **Step 2: Run to verify failure** — `cd frontend && pnpm test -- src/dashboard` → FAIL.

- [ ] **Step 3: Implement screens.**
  - `Dashboard.tsx`: `<DashboardProvider>` wrapping a switch on `phase`: loading → `data-role="dashboard-loading"` spinner text; `signed-out` → `<SignIn/>`; `ready` → `me.project == null ? <Onboarding/> : <Settings/>`.
  - `SignIn.tsx`: centered card, product wordmark "Kiln", one anchor `Continue with GitHub` (plain `<a href="/auth/github/login">` — full-page navigation, NOT a router Link).
  - `Onboarding.tsx`: heading `Set up your project`; the project step ONLY (`ProjectFields` + Save). No local step state: after `saveProject` succeeds the store's refreshed `me.project` is non-nil and `Dashboard` switches to `Settings`, where credentials + verify live. YAGNI: no wizard machinery.
  - `Settings.tsx`: account card (avatar `<img>`, `display_name`, `@github_login`, Sign out button) · `CredentialFields` (credentials section) · `ProjectFields` (project section) · verify section (`Test connections` button + `verify-check` rows) · footer note "Open kiln on your phone at this URL — the app itself doesn't need sign-in yet."
  - `ConfigFields.tsx`: exports `ProjectFields` and `CredentialFields` — controlled forms seeded from `me`, secret inputs `type="password"` with placeholder `configured · …x4Kd` when set (value stays empty — write-only), each form submits only non-empty fields.
  - `Dashboard.css`: scoped `[data-role='dashboard']` — desktop-first: light paper background reusing PrimaryScreen's palette vocabulary (`#f3efee` paper, ember `oklch(0.55 0.2 26)` accents, Space Grotesk headings — fonts already loaded by index.html), max-width 720px centered column of cards, `@media (max-width: 600px)` single-column fallback.
  - `main.tsx`: add `<Route path="/dashboard/*" element={<Dashboard />} />` between the existing two routes. **No other main.tsx change.**
  - `vite.config.ts`: add `'/auth': { target: process.env.KILN_PROXY_TARGET ?? 'http://localhost:8080', changeOrigin: true },` beside the `/api` entry.

- [ ] **Step 4: Run tests + full frontend gate** — `cd frontend && pnpm check` (lint + typecheck + all tests). Expected: green, and every pre-existing test file unmodified (verify: `git status frontend/src/components frontend/src/stores frontend/src/App.tsx` → clean).

- [ ] **Step 5: Visual smoke** — `docker compose up -d && open http://localhost:5173/dashboard`: sign-in card renders; clicking through GitHub works end-to-end if the OAuth app + allowlist env are set (manual; the human does the GitHub login click).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/dashboard frontend/src/main.tsx frontend/vite.config.ts
git commit -m "feat(web): /dashboard — sign-in, onboarding, settings (spec 11 §5)"
```

---

### Task 13: e2e + full gate + skill updates

**Files:**
- Create: `tests/tests/dashboard-config.spec.ts`
- Modify: `.agents/skills/runtime-and-api/SKILL.md`, `.agents/skills/web-client/SKILL.md`

**Interfaces:**
- Consumes: `POST /api/dev/session` (Task 9), dashboard selectors (Task 12), Playwright config (`baseURL` 5173, chromium project).

- [ ] **Step 1: Write the e2e spec** — `tests/tests/dashboard-config.spec.ts`:

```ts
import { test, expect } from '@playwright/test';

// Phase 1 dashboard flow (spec 11 §8): dev session → onboard → config sticks.
// Needs the compose stack up with KILN_DEV_ENDPOINTS=1 and identity env set
// (GITHUB_OAUTH_CLIENT_ID, KILN_SECRETS_KEY) — both default in local .env.
test('dashboard onboarding stores config and reflects status', async ({ page }) => {
  // page.request shares the browser context's cookie jar, so the minted
  // session cookie authenticates subsequent page navigation.
  const mint = await page.request.post('/api/dev/session', {
    data: { github_login: `e2e-dash-${Date.now()}` },
  });
  expect(mint.ok()).toBe(true);

  await page.goto('/dashboard');
  // Fresh user → onboarding.
  await expect(page.getByRole('heading', { name: 'Set up your project' })).toBeVisible();
  await page.getByLabel('Project name').fill('kiln-e2e');
  await page.getByLabel('Repo URL').fill('https://github.com/crabtree-michael/kiln');
  await page.getByRole('button', { name: 'Save project' }).click();

  // Project saved → settings view; store credentials.
  await page.getByLabel('Anthropic API key').fill('sk-ant-e2e-fake-x4Kd');
  await page.getByRole('button', { name: 'Save credentials' }).click();
  const status = page.locator('[data-role="secret-status"][data-name="anthropic_api_key"]');
  await expect(status).toHaveAttribute('data-set', 'true');
  await expect(status).toContainText('x4Kd');

  // Write-only: the raw secret never comes back over the wire.
  const me = await page.request.get('/api/me');
  expect(await me.text()).not.toContain('sk-ant-e2e-fake');

  // Verify endpoint runs live: the fake key must FAIL against real Anthropic;
  // amika reports skipped (never configured). The repo check RUNS (repo_url is
  // set) and passes — public repo, ls-remote needs no token.
  await page.getByRole('button', { name: 'Test connections' }).click();
  await expect(page.locator('[data-role="verify-check"][data-name="anthropic"]')).toHaveAttribute(
    'data-status', 'failed');
});
```

- [ ] **Step 2: Run the e2e suite** — stack up (`make up` in one shell), then `make e2e`.
Expected: the new spec passes AND every existing spec passes **unmodified** — that is phase 1's headline proof (spec 11 §8). The Amika-billing specs behave exactly as before since env still drives the runtime.

- [ ] **Step 3: Full repo gate** — `make check && make schema-verify` → green.

- [ ] **Step 4: Update skills** (AGENTS.md rule — keep your area's skill current):
  - `runtime-and-api`: add a short "Identity (spec 11 phase 1)" note — module layout, env gating (`GITHUB_OAUTH_CLIENT_ID`+`KILN_SECRETS_KEY` both required or routes absent), dev session mint for tests, write-only secrets.
  - `web-client`: note the `/dashboard` surface, the store/two-file pattern reuse, the `/auth` vite proxy, and that `/` + `/debug` remain session-free in phase 1.

- [ ] **Step 5: Commit**

```bash
git add tests/tests/dashboard-config.spec.ts .agents/skills
git commit -m "test(e2e): dashboard onboarding + config flow; skill notes (spec 11 §8)"
```

- [ ] **Step 6: Manual rollout step (human-in-the-loop).** Ask the user to open `http://localhost:5173/dashboard` and click **Continue with GitHub** signed in as `crabtree-michael` — verifies the real OAuth app + allowlist end-to-end. Prod flip (register prod OAuth app with the Render callback, set the four env vars in Render) is deliberately NOT in this plan — it is spec 11 §7 rollout step 1, done when the user says go.
