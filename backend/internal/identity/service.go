package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	sessionTTL        = 30 * 24 * time.Hour
	sessionRenewBelow = 15 * 24 * time.Hour

	// sessionTokenBytes is the amount of CSPRNG entropy in a raw session
	// token before base64url encoding.
	sessionTokenBytes = 32

	// maxPositiveInt63 masks off the sign bit so a DevSignIn-derived GitHub
	// id (an fnv64a hash) always fits int64 as a positive value.
	maxPositiveInt63 = 0x7fffffffffffffff

	// verifyCheckCount is the fixed number of checks Verify always returns
	// (anthropic, amika, repo — 11 §4).
	verifyCheckCount = 3

	// statusSkipped is the CheckResult.Status for an unconfigured credential
	// group (no verifier wired in, or the group has no secret/repo set).
	statusSkipped = "skipped"
)

// GitHub is the service's port onto the OAuth provider — satisfied directly
// by *githubapi.Client (the consumer declares the interface, 02 §2).
type GitHub interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchUser(ctx context.Context, accessToken string) (githubapi.GitHubUser, error)
}

// Verifier is the service's port onto live connection checks — satisfied by
// *verify.Verifier (the consumer declares the interface, 02 §2). Every method
// reports its outcome as a CheckResult and never returns a Go error.
type Verifier interface {
	VerifyAnthropic(ctx context.Context, apiKey string) CheckResult
	VerifyAmika(ctx context.Context, apiKey string) CheckResult
	VerifyRepo(ctx context.Context, repoURL, token string) CheckResult
}

// Service is identity's domain service (11 §2–§4): login, sessions, config.
type Service struct {
	store      Store
	cipher     *Cipher
	gh         GitHub
	verifier   Verifier
	allowed    map[string]bool
	now        func() time.Time
	invalidate func(projectID string)
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
// NO allowlist check, so e2e can mint sessions without real OAuth. It shares
// EnsureUser's find-or-create mechanics.
func (s *Service) DevSignIn(ctx context.Context, login string) (User, error) {
	return s.EnsureUser(ctx, login)
}

// EnsureUser finds-or-creates a user by GitHub login WITHOUT the allowlist
// check — the shared find-or-create used by DevSignIn (11 §7) and the phase-2
// bootstrap-from-env path. A deterministic fnv64a hash of the login stands in
// for the GitHub id (not cryptographic). Real OAuth logins still go through
// CompleteLogin, which enforces the allowlist on every login (11 §2).
func (s *Service) EnsureUser(ctx context.Context, login string) (User, error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.ToLower(login)))
	u, err := s.store.EnsureUserByLogin(ctx, User{
		GitHubID:    int64(h.Sum64() & maxPositiveInt63), // deterministic dev id, not crypto
		GitHubLogin: strings.ToLower(login),
		DisplayName: login,
	})
	if err != nil {
		return User{}, fmt.Errorf("identity: ensure user: %w", err)
	}
	return u, nil
}

func (s *Service) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, sessionTokenBytes)
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
// under half the TTL remaining ⇒ slide the window (11 §2). The returned
// time.Time is the session's CURRENT expiry — the renewed one when the
// window slid, else the existing (unchanged) one — so the caller (the api's
// withSession) can re-issue the session cookie to match the DB row and keep
// the "sliding" expiry visible to the browser, not just server-side.
func (s *Service) ResolveSession(ctx context.Context, token string) (User, time.Time, error) {
	if token == "" {
		return User{}, time.Time{}, ErrNoSession
	}
	sess, user, err := s.store.GetSessionUser(ctx, hashToken(token))
	if err != nil {
		return User{}, time.Time{}, ErrNoSession
	}
	now := s.now()
	if now.After(sess.ExpiresAt) {
		if derr := s.store.DeleteSession(ctx, sess.TokenHash); derr != nil {
			slog.ErrorContext(ctx, "identity: delete expired session", "err", derr)
		}
		return User{}, time.Time{}, ErrNoSession
	}
	expiresAt := sess.ExpiresAt
	if sess.ExpiresAt.Sub(now) < sessionRenewBelow {
		expiresAt = now.Add(sessionTTL)
		if err := s.store.TouchSession(ctx, sess.TokenHash, expiresAt); err != nil {
			return User{}, time.Time{}, fmt.Errorf("identity: touch session: %w", err)
		}
	}
	return user, expiresAt, nil
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
	if upd.AmikaClaudeCredID != "" {
		cfg.AmikaClaudeCredID = upd.AmikaClaudeCredID
	}
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		return fmt.Errorf("identity: update settings: %w", err)
	}
	// Config is per-user; invalidate the owner's project runtime if they have
	// one. A user who hasn't onboarded a project yet has nothing to invalidate
	// (not an error).
	switch proj, perr := s.store.GetProjectByOwner(ctx, userID); {
	case perr == nil:
		s.fireInvalidate(proj.ID)
	case errors.Is(perr, ErrNotFound): // no project yet — nothing to invalidate
	default:
		return fmt.Errorf("identity: update settings: %w", perr)
	}
	return nil
}

// minWorkerCount and maxWorkerCount mirror the DB's CHECK (worker_count
// between 1 and 10); defaultWorkerCount is used when the caller omits it.
const (
	minWorkerCount     = 1
	maxWorkerCount     = 10
	defaultWorkerCount = 3
)

// UpsertProject creates or updates the caller's project (one per owner in
// phase 1), validating required fields and the worker-count range.
func (s *Service) UpsertProject(ctx context.Context, userID string, upd ProjectUpdate) (Project, error) {
	if upd.WorkerCount == 0 {
		upd.WorkerCount = defaultWorkerCount
	}
	if upd.Name == "" || upd.RepoURL == "" || upd.WorkerCount < minWorkerCount || upd.WorkerCount > maxWorkerCount {
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
	s.fireInvalidate(p.ID)
	return p, nil
}

// SetInvalidator registers a hook fired after a successful config write
// (UpdateSettings for the owner's project, UpsertProject) with the affected
// project id, so the runtime's per-project registry can rebuild that project.
// Setter, not a constructor arg, to keep NewService's signature stable and the
// hook optional (nil-safe when unset).
func (s *Service) SetInvalidator(f func(projectID string)) { s.invalidate = f }

// ProjectFor returns the owner's project, wrapping the store's
// GetProjectByOwner for runtime callers. Returns ErrNotFound before onboarding
// creates it (detectable with errors.Is through the wrap).
func (s *Service) ProjectFor(ctx context.Context, userID string) (Project, error) {
	p, err := s.store.GetProjectByOwner(ctx, userID)
	if err != nil {
		return Project{}, fmt.Errorf("identity: project for owner: %w", err)
	}
	return p, nil
}

// GetProject resolves a project by id to its plaintext metadata — including
// OwnerUserID — WITHOUT touching the credential store or the cipher. It is the
// cheap owner/config-metadata lookup the notifier path uses (11 §3): unlike
// RuntimeConfig it decrypts nothing and reads no user_config, so a notification
// never pays a secret-decrypt (or, at the composition root, a provider build).
// Returns ErrNotFound (through the wrap) when the project doesn't exist.
func (s *Service) GetProject(ctx context.Context, projectID string) (Project, error) {
	p, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, fmt.Errorf("identity: get project: %w", err)
	}
	return p, nil
}

// ListProjectIDs returns every project's id (created_at order), for the
// runtime to enumerate the tenants it must stand up at startup.
func (s *Service) ListProjectIDs(ctx context.Context) ([]string, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity: list project ids: %w", err)
	}
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// RuntimeConfig is the fully-decrypted, in-process credential bundle the
// runtime needs to drive one project's brain/board/agents. It carries
// PLAINTEXT secrets and therefore MUST NEVER be returned over the wire,
// serialized into any API/DTO, or logged — in-process use only. (There is
// deliberately no String()/wire mapping for this type.)
type RuntimeConfig struct {
	Project           Project
	OwnerUserID       string
	AnthropicAPIKey   string // decrypted; empty = unset
	AmikaAPIKey       string
	AmikaClaudeCredID string
	GitHubAuthToken   string
}

// RuntimeConfig resolves a project to its owner's decrypted credentials:
// project → owner user → user_config → cipher.Decrypt per set column. An unset
// (NULL) credential column decrypts to "" rather than erroring. Returns
// ErrNotFound when the project doesn't exist.
//
// The result carries plaintext secrets for in-process use ONLY — never log it
// or expose it via a wire type (see the RuntimeConfig type doc).
func (s *Service) RuntimeConfig(ctx context.Context, projectID string) (RuntimeConfig, error) {
	proj, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("identity: runtime config: %w", err)
	}
	// Resolve the owner (guards against a dangling owner_user_id).
	if _, uerr := s.store.GetUser(ctx, proj.OwnerUserID); uerr != nil {
		return RuntimeConfig{}, fmt.Errorf("identity: runtime config: %w", uerr)
	}
	cfg, err := s.store.GetUserConfig(ctx, proj.OwnerUserID)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("identity: runtime config: %w", err)
	}
	return RuntimeConfig{
		Project:           proj,
		OwnerUserID:       proj.OwnerUserID,
		AnthropicAPIKey:   s.decrypt(cfg.AnthropicKeyEnc),
		AmikaAPIKey:       s.decrypt(cfg.AmikaKeyEnc),
		AmikaClaudeCredID: cfg.AmikaClaudeCredID,
		GitHubAuthToken:   s.decrypt(cfg.GitHubTokenEnc),
	}, nil
}

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
	anthropicKey := s.decrypt(cfg.AnthropicKeyEnc)
	amikaKey := s.decrypt(cfg.AmikaKeyEnc)
	ghToken := s.decrypt(cfg.GitHubTokenEnc)
	repoURL := ""
	if p, err := s.store.GetProjectByOwner(ctx, userID); err == nil {
		repoURL = p.RepoURL
	}
	checks := make([]CheckResult, 0, verifyCheckCount)
	checks = append(checks, s.check(ctx, "anthropic", anthropicKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAnthropic(ctx, anthropicKey)
	}))
	checks = append(checks, s.check(ctx, "amika", amikaKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAmika(ctx, amikaKey)
	}))
	checks = append(checks, s.check(ctx, "repo", repoURL != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyRepo(ctx, repoURL, ghToken)
	}))
	return checks, nil
}

// check runs one live check when its credential group is configured and a
// verifier is wired in; otherwise it reports "skipped" without touching the
// network.
func (s *Service) check(
	ctx context.Context, name string, configured bool, run func(context.Context) CheckResult,
) CheckResult {
	if !configured || s.verifier == nil {
		return CheckResult{Name: name, Status: statusSkipped, Message: "not configured"}
	}
	res := run(ctx)
	res.Name = name
	return res
}

// fireInvalidate calls the registered invalidator (if any) for a non-empty
// project id.
func (s *Service) fireInvalidate(projectID string) {
	if s.invalidate != nil && projectID != "" {
		s.invalidate(projectID)
	}
}

// decrypt is nil-safe (an unset ciphertext yields "") and swallows a
// corrupt/undecryptable ciphertext to "" too — Verify then reports that
// credential group as unconfigured rather than surfacing a decrypt error,
// mirroring secretStatus's own set-but-unreadable handling.
func (s *Service) decrypt(enc []byte) string {
	if len(enc) == 0 {
		return ""
	}
	plain, err := s.cipher.Decrypt(enc)
	if err != nil {
		return ""
	}
	return plain
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

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
