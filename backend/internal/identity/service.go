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
	VerifyAmika(ctx context.Context, baseURL, apiKey string) CheckResult
	VerifyRepo(ctx context.Context, repoURL, token string) CheckResult
}

// Service is identity's domain service (11 §2–§4): login, sessions, config.
type Service struct {
	store    Store
	cipher   *Cipher
	gh       GitHub
	verifier Verifier
	allowed  map[string]bool
	now      func() time.Time
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
		ID:    int64(h.Sum64() & maxPositiveInt63), // deterministic dev id, not crypto
		Login: login,
		Name:  login,
	})
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
		if derr := s.store.DeleteSession(ctx, sess.TokenHash); derr != nil {
			slog.ErrorContext(ctx, "identity: delete expired session", "err", derr)
		}
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
	return p, nil
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
		return s.verifier.VerifyAmika(ctx, cfg.AmikaBaseURL, amikaKey)
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
