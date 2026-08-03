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
	"slices"
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
	// (anthropic, amika, devin, repo — 11 §4).
	verifyCheckCount = 4

	// statusSkipped is the CheckResult.Status for an unconfigured credential
	// group (no verifier wired in, or the group has no secret/repo set).
	statusSkipped = "skipped"
)

// GitHub is the service's port onto the OAuth provider — satisfied directly
// by *githubapi.Client (the consumer declares the interface, 02 §2).
type GitHub interface {
	// AuthorizeURL builds the authorize redirect for a state nonce and scope;
	// an empty scope is the plain sign-in grant (no `scope` param at all).
	AuthorizeURL(state, scope string) string
	// ExchangeCode returns the access token AND the scope string GitHub
	// actually granted it — which can be narrower than the one requested.
	ExchangeCode(ctx context.Context, code string) (string, string, error)
	FetchUser(ctx context.Context, accessToken string) (githubapi.GitHubUser, error)
	// ListRepos returns the repos the access token can reach, for the settings
	// repo picker. A token GitHub rejects (revoked, or one that never carried
	// the repo scope) yields githubapi.ErrUnauthorized.
	ListRepos(ctx context.Context, accessToken string) ([]githubapi.Repo, error)
	// TokenScopes reports the scopes an already-stored token carries, so a
	// credential Kiln did not mint (a PAT carried over from the old manual
	// field) can be classified without the user re-entering anything. An
	// error means "could not determine", never "no scopes".
	TokenScopes(ctx context.Context, accessToken string) (string, error)
}

// Verifier is the service's port onto live connection checks — satisfied by
// *verify.Verifier (the consumer declares the interface, 02 §2). Every method
// reports its outcome as a CheckResult and never returns a Go error.
type Verifier interface {
	VerifyAnthropic(ctx context.Context, apiKey string) CheckResult
	VerifyAmika(ctx context.Context, apiKey string) CheckResult
	VerifyDevin(ctx context.Context, apiKey string) CheckResult
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
	// devGitHubToken is the synthetic GitHub credential DevSignIn stores, empty
	// (the default) outside a keyless stack — see SetDevGitHubToken.
	devGitHubToken string
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

// ConnectURL starts THE GitHub grant — there is exactly one (11 §2, amended
// 2026-08-03). It asks for `repo`, full read/write repository access, because
// signing in and connecting GitHub are now the same act: the token it yields is
// what CompleteConnect stores as the user's GitHubToken, so it is the
// credential the brain's clone/fetch, the `gh` PR gate, and the sandbox's
// clone/push all authenticate with.
//
// It replaces a split — a scopeless LoginURL beside this one — that bought a
// consent screen without a `repo` prompt at the price of two routes which
// looked interchangeable and weren't. Every entry point (landing sign-in,
// session gate, the dashboard's Connect card) now leads here, so "which one
// does this button want?" is no longer a question anyone can get wrong.
func (s *Service) ConnectURL(state string) string {
	return s.gh.AuthorizeURL(state, githubapi.ScopeRepo)
}

// CompleteConnect completes the single grant (ConnectURL): exchange the code,
// enforce the allowlist on every sign-in (11 §2), find-or-create the user, and
// PERSIST the access token as their GitHubToken — the same encrypted slot the
// old manual token field wrote to, which is why a token stored by that field
// keeps working until replaced here.
//
// Storing the token is what makes "Connected" on the dashboard card mean the
// repo is actually reachable, rather than merely "you signed in with GitHub".
//
// A grant that came back WITHOUT `repo` returns ErrRepoScopeNotGranted
// alongside a POPULATED user. Returning both is deliberate: GitHub really did
// authenticate an allowlisted account, so the caller can still sign them in and
// refuse only the unusable token. While this was the connect-only path an empty
// user was harmless — the caller already had a session. Now that it is also the
// sign-in path, withholding the user would lock someone out of the very
// dashboard the retry lives on.
func (s *Service) CompleteConnect(ctx context.Context, code string) (User, error) {
	user, token, scope, err := s.completeOAuth(ctx, code, "complete connect")
	if err != nil {
		return User{}, err
	}
	// GitHub can hand back a NARROWER grant than the one asked for — the user
	// unticks an org, or the OAuth app isn't approved there. Storing such a
	// token would light the dashboard card up as "Connected" while every clone
	// and push still failed, so refuse it: the caller turns this into a page
	// that says what happened and offers to retry.
	if !grantsRepoScope(scope) {
		return user, ErrRepoScopeNotGranted
	}
	if err := s.storeGitHubConnection(ctx, user.ID, token, scope, user.GitHubLogin); err != nil {
		return User{}, fmt.Errorf("identity: complete connect: %w", err)
	}
	return user, nil
}

// grantsRepoScope reports whether GitHub's granted-scope string carries full
// `repo`. The string is comma-separated (e.g. "repo,gist"), and the match is
// exact per element on purpose: `public_repo` is a strictly narrower scope that
// cannot clone or push a private repo, so it must not pass for `repo`.
func grantsRepoScope(scope string) bool {
	return slices.Contains(splitScopes(scope), githubapi.ScopeRepo)
}

// splitScopes normalizes GitHub's scope string into its non-empty elements.
// GitHub uses commas in the token-exchange body and (historically) commas with
// spaces in the X-OAuth-Scopes header, so both are tolerated. An empty string
// yields an empty (never nil) slice, so the wire field is always an array.
func splitScopes(scope string) []string {
	out := []string{}
	for part := range strings.SplitSeq(scope, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// gitHubConnection derives the account view's repo-credential state from the
// stored row. The ordering encodes the carry-forward guarantee: a credential
// with NO scope record — every token carried over from the old manual field —
// reads as connected-with-unknown-scopes, never as disconnected and never as
// needing a reconnect. Only a scope list Kiln actually holds, and which
// actually lacks `repo`, downgrades the connection, so nobody is pushed through
// a fresh OAuth grant merely because of this refactor.
func gitHubConnection(cfg UserConfig) GitHubConnection {
	if len(cfg.GitHubTokenEnc) == 0 {
		return GitHubConnection{Status: GitHubDisconnected, Scopes: []string{}}
	}
	conn := GitHubConnection{Login: cfg.GitHubConnectedLogin, Scopes: splitScopes(cfg.GitHubTokenScopes)}
	switch {
	case len(conn.Scopes) == 0:
		conn.Status = GitHubUnknownScopes
	case grantsRepoScope(cfg.GitHubTokenScopes):
		conn.Status = GitHubConnected
	default:
		conn.Status = GitHubNeedsReconnect
	}
	return conn
}

// ListGitHubRepos returns the repos the caller's connected GitHub account can
// reach — the source of the settings repo picker, replacing hand-typed repo
// URLs. Private repos are included: the credential the "Connect GitHub" grant
// stores carries the repo scope.
//
// It reads the SAME credential the Integrations card reports on
// (GitHubTokenEnc), so the picker and the card can never disagree about whether
// the account is connected. Returns ErrGitHubNotConnected when the caller has
// no stored credential — the ordinary state before they connect, since signing
// in grants nothing (11 §2 D2) — or when GitHub rejects the one they have
// (revoked, or a carried-over manual token that never had the scope). Both mean
// "run the Connect GitHub grant", which is the remedy the picker offers.
func (s *Service) ListGitHubRepos(ctx context.Context, userID string) ([]Repo, error) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list github repos: %w", err)
	}
	token := s.decrypt(cfg.GitHubTokenEnc)
	if token == "" {
		return nil, ErrGitHubNotConnected
	}
	found, err := s.gh.ListRepos(ctx, token)
	if err != nil {
		if errors.Is(err, githubapi.ErrUnauthorized) {
			return nil, ErrGitHubNotConnected
		}
		return nil, fmt.Errorf("identity: list github repos: %w", err)
	}
	repos := make([]Repo, 0, len(found))
	for _, r := range found {
		repos = append(repos, Repo{FullName: r.FullName, URL: r.HTMLURL, Private: r.Private})
	}
	return repos, nil
}

// DevSignIn is the KILN_DEV_ENDPOINTS-only seam (11 §7): find-or-create with
// NO allowlist check, so e2e can mint sessions without real OAuth. It shares
// EnsureUser's find-or-create mechanics.
//
// When a dev GitHub credential is configured (SetDevGitHubToken — keyless
// stacks only), it is stored for the user the way CompleteConnect stores the
// real OAuth token, so a dev-minted session reaches the settings repo picker already
// connected. Without that call this is a pure find-or-create and touches no
// credential — a real-service e2e run can never have its stored GitHub token
// clobbered by a synthetic one.
//
// The write happens ONCE per user, only when they have no GitHub credential yet.
// That is not an optimization: a credential write invalidates every project the
// user owns, and invalidation CLOSES the tenant bundle (tenant.Registry). Specs
// share one dev login, so writing on every mint let one spec's session mint tear
// down another spec's in-flight agent turn mid-run — the completion event was
// then never delivered. Minting a session must stay side-effect-free for a user
// who is already connected.
func (s *Service) DevSignIn(ctx context.Context, login string) (User, error) {
	user, err := s.EnsureUser(ctx, login)
	if err != nil {
		return User{}, err
	}
	if s.devGitHubToken != "" {
		s.ensureDevGitHubToken(ctx, user.ID)
	}
	return user, nil
}

// SetDevGitHubToken configures the synthetic GitHub credential DevSignIn stores
// (keyless e2e). Setter, not a constructor arg, for the same reason as
// SetVerifier — and so it is off unless a composition root explicitly opts in.
func (s *Service) SetDevGitHubToken(token string) { s.devGitHubToken = token }

// EnsureUser finds-or-creates a user by GitHub login WITHOUT the allowlist
// check — the shared find-or-create used by DevSignIn (11 §7) and the phase-2
// bootstrap-from-env path. A deterministic fnv64a hash of the login stands in
// for the GitHub id (not cryptographic). Real OAuth logins still go through
// CompleteConnect, which enforces the allowlist on every sign-in (11 §2).
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
		DevinKey:          s.secretStatus(cfg.DevinKeyEnc),
		GitHubToken:       s.secretStatus(cfg.GitHubTokenEnc),
		GitHub:            gitHubConnection(cfg),
		AmikaClaudeCredID: cfg.AmikaClaudeCredID,
	}}
	views, err := s.ListProjects(ctx, userID)
	if err != nil {
		return Me{}, fmt.Errorf("identity: me: %w", err)
	}
	me.Projects = views
	return me, nil
}

// ListProjects returns the owner's live projects as ProjectViews (project +
// fingerprint-only secret statuses), oldest-first — the collection behind
// GET /api/projects and Me.projects (12 §3.1). An owner with none yields an
// empty slice (the "not onboarded" state), never an error.
func (s *Service) ListProjects(ctx context.Context, userID string) ([]ProjectView, error) {
	projects, err := s.store.ListProjectsByOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list projects: %w", err)
	}
	views := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		views = append(views, ProjectView{Project: p, Secrets: s.amikaSecretStatuses(p.AmikaSecrets)})
	}
	return views, nil
}

// UpdateSettings merges non-empty fields over the stored row (read-modify-write;
// empty = unchanged — recorded deviation, no clear operation in phase 1).
func (s *Service) UpdateSettings(ctx context.Context, userID string, upd SettingsUpdate) error {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: update settings: %w", err)
	}
	cfg.UserID = userID
	// Each write-only secret merges in place: a non-empty inbound value replaces
	// the stored ciphertext, an empty one leaves it unchanged (11 §3 D7).
	secrets := []struct {
		dst   *[]byte
		value string
	}{
		{&cfg.AnthropicKeyEnc, upd.AnthropicKey},
		{&cfg.AmikaKeyEnc, upd.AmikaKey},
		{&cfg.DevinKeyEnc, upd.DevinKey},
		{&cfg.GitHubTokenEnc, upd.GitHubToken},
	}
	for _, sec := range secrets {
		if err := s.mergeSecret(sec.dst, sec.value); err != nil {
			return err
		}
	}
	if upd.AmikaClaudeCredID != "" {
		cfg.AmikaClaudeCredID = upd.AmikaClaudeCredID
	}
	// A GitHub token written through THIS path (the API, which still accepts the
	// field for back-compat) came with no scope record, so the old token's record
	// must not be inherited by the new one — reset both to unknown and let the
	// next verify's scope probe classify it. CompleteConnect never comes through
	// here; it records the real scope itself.
	if upd.GitHubToken != "" {
		cfg.GitHubTokenScopes = ""
		cfg.GitHubConnectedLogin = ""
	}
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		return fmt.Errorf("identity: update settings: %w", err)
	}
	return s.invalidateOwnedProjects(ctx, userID, "update settings")
}

// minWorkerCount and maxWorkerCount mirror the DB's CHECK (worker_count
// between 1 and 10); defaultWorkerCount is used when the caller omits it.
const (
	minWorkerCount     = 1
	maxWorkerCount     = 10
	defaultWorkerCount = 3
)

// maxAmikaSecrets bounds the per-project secret list so a single project can't
// bloat the sandbox-create request (02 §8). Generous headroom over any real use.
const maxAmikaSecrets = 50

// normalizeMergeGateMode defaults an empty gate mode to MergeGateMain (so a
// project that never set the knob keeps the original behavior) and reports
// whether the result is a known mode (06 §7).
func normalizeMergeGateMode(m MergeGateMode) (MergeGateMode, bool) {
	if m == "" {
		m = MergeGateMain
	}
	return m, m == MergeGateMain || m == MergeGatePR
}

// CreateProject creates a new project for the caller (12 DP2), validating
// required fields and the worker-count range. Credentials are per-user (already
// set), so a second project skips the credential step — only the project fields
// are supplied here. A fresh project carries no prior secrets, so the write-only
// merge starts from nothing.
func (s *Service) CreateProject(ctx context.Context, userID string, upd ProjectUpdate) (ProjectView, error) {
	upd, err := validateProjectUpdate(upd)
	if err != nil {
		return ProjectView{}, err
	}
	secrets, err := s.mergeAmikaSecrets(upd.AmikaSecrets, nil)
	if err != nil {
		return ProjectView{}, err
	}
	p, err := s.store.CreateProject(ctx, s.projectRow(userID, "", upd, secrets))
	if err != nil {
		return ProjectView{}, fmt.Errorf("identity: create project: %w", err)
	}
	s.fireInvalidate(p.ID)
	return s.projectView(p), nil
}

// UpdateProject updates a project the caller owns (12 §3.1), carrying forward
// write-only secret values the client didn't re-enter. Ownership is enforced in
// the store's UPDATE WHERE (id + owner_user_id): a project the caller doesn't own
// (or a soft-deleted one) resolves to ErrNotFound both when loading its prior
// secrets and on the write itself, so a foreign project is never confirmed (§3.2).
func (s *Service) UpdateProject(ctx context.Context, userID, projectID string, upd ProjectUpdate) (ProjectView, error) {
	upd, err := validateProjectUpdate(upd)
	if err != nil {
		return ProjectView{}, err
	}
	// Load the target's current secrets (owner-authorized) so empty-value entries
	// carry the stored ciphertext forward (11 §3 D7).
	cur, err := s.ProjectByID(ctx, userID, projectID)
	if err != nil {
		return ProjectView{}, err
	}
	secrets, err := s.mergeAmikaSecrets(upd.AmikaSecrets, cur.AmikaSecrets)
	if err != nil {
		return ProjectView{}, err
	}
	p, err := s.store.UpdateProject(ctx, s.projectRow(userID, projectID, upd, secrets))
	if err != nil {
		return ProjectView{}, fmt.Errorf("identity: update project: %w", err)
	}
	s.fireInvalidate(p.ID)
	return s.projectView(p), nil
}

// UpsertProject is the back-compat singular write (PUT /api/project, 12 §9):
// update the caller's first project when they have one, else create it. New
// clients target the id'd create/update endpoints instead. Returns the plain
// Project (bootstrap's shape).
func (s *Service) UpsertProject(ctx context.Context, userID string, upd ProjectUpdate) (Project, error) {
	switch first, err := s.store.GetProjectByOwner(ctx, userID); {
	case err == nil:
		v, uerr := s.UpdateProject(ctx, userID, first.ID, upd)
		return v.Project, uerr
	case errors.Is(err, ErrNotFound):
		v, cerr := s.CreateProject(ctx, userID, upd)
		return v.Project, cerr
	default:
		return Project{}, fmt.Errorf("identity: upsert project: %w", err)
	}
}

// ProjectByID resolves a project by id and authorizes the caller as its owner
// (12 §3.2) — the owner-check that did not exist in phase 1 (there was never a
// foreign project to request). Returns ErrNotFound both for an unknown/soft-deleted
// id AND for a live project owned by someone else, so a non-owner can never tell
// the two apart (§3.2: 404, not 403). This is the request-path project resolver
// the api's withProject guard is built on.
func (s *Service) ProjectByID(ctx context.Context, userID, projectID string) (Project, error) {
	p, err := s.store.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, fmt.Errorf("identity: project by id: %w", err)
	}
	if p.OwnerUserID != userID {
		return Project{}, ErrNotFound // don't confirm a foreign project's existence
	}
	return p, nil
}

// SoftDeleteProject marks the caller's project deleted (12 DP6), guarded by the
// owner check in the store's UPDATE WHERE. Returns ErrNotFound when no live row
// the caller owns matches. The runtime-eviction/state-cascade around this
// (Reset, tenant Invalidate, clone removal) is the composition root's job (§5) —
// this only retires the row.
func (s *Service) SoftDeleteProject(ctx context.Context, userID, projectID string) error {
	if err := s.store.SoftDeleteProject(ctx, projectID, userID); err != nil {
		return fmt.Errorf("identity: soft delete project: %w", err)
	}
	return nil
}

// validateProjectUpdate defaults the worker count and gate mode and validates
// the required fields + ranges, returning the normalized update or
// ErrInvalidProject. Shared by create and update so both enforce the same rules.
func validateProjectUpdate(upd ProjectUpdate) (ProjectUpdate, error) {
	if upd.WorkerCount == 0 {
		upd.WorkerCount = defaultWorkerCount
	}
	gateMode, gateOK := normalizeMergeGateMode(upd.MergeGateMode)
	if upd.Name == "" || upd.RepoURL == "" || upd.WorkerCount < minWorkerCount || upd.WorkerCount > maxWorkerCount {
		return ProjectUpdate{}, ErrInvalidProject
	}
	if !gateOK {
		return ProjectUpdate{}, ErrInvalidProject
	}
	upd.MergeGateMode = gateMode
	return upd, nil
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
//
// SERVER-DERIVED projectID ONLY. This is an owner-DISCOVERING resolver
// (projectID → OwnerUserID): it does NOT and cannot verify ownership, so it
// returns any tenant's metadata for the id it's handed. Pass only a
// server-enumerated id (ListProjectIDs, or an event/outbox row's server-assigned
// project_id) — NEVER a client-supplied one. Request-path lookups must instead
// go through the owner-scoped ProjectFor (keyed by the authenticated user), which
// is the only project resolver the api package is given (its ProjectResolver
// port), so a handler structurally cannot reach this one with a client id.
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
	Project     Project
	OwnerUserID string
	// AnthropicAPIKey is decrypted but DORMANT: the brain now uses the
	// deployment-global ANTHROPIC_API_KEY env setting, not this per-user value
	// (see UserConfig.AnthropicKeyEnc). Still resolved so re-enabling a
	// per-user path is a one-line change at the composition root.
	AnthropicAPIKey string
	AmikaAPIKey     string
	// DevinAPIKey is the owner's decrypted Devin bearer, empty when unset. The
	// composition root's buildDevinProvider prefers it over the deployment
	// DEVIN_API_KEY env (multi-provider design §8). Plaintext — in-process only.
	DevinAPIKey       string
	AmikaClaudeCredID string
	GitHubAuthToken   string
	// AmikaSecrets is the project's decrypted secrets (name + value) to inject
	// into every sandbox at startup (02 §8). Plaintext — in-process use only.
	AmikaSecrets []AmikaSecretValue
}

// RuntimeConfig resolves a project to its owner's decrypted credentials:
// project → owner user → user_config → cipher.Decrypt per set column. An unset
// (NULL) credential column decrypts to "" rather than erroring. Returns
// ErrNotFound when the project doesn't exist.
//
// The result carries plaintext secrets for in-process use ONLY — never log it
// or expose it via a wire type (see the RuntimeConfig type doc).
//
// SERVER-DERIVED projectID ONLY (see GetProject): like GetProject this discovers
// the owner from the id and does not verify it against a caller, so it would
// decrypt any tenant's credentials for the id given. Its sole caller is the
// tenant registry's resolve closure, driven by server-assigned event project_ids
// — never wire this behind a handler that takes a client-supplied id.
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
		DevinAPIKey:       s.decrypt(cfg.DevinKeyEnc),
		AmikaClaudeCredID: cfg.AmikaClaudeCredID,
		GitHubAuthToken:   s.decrypt(cfg.GitHubTokenEnc),
		AmikaSecrets:      s.resolveAmikaSecrets(proj.AmikaSecrets),
	}, nil
}

// SetVerifier injects the live-check adapter (nil-safe: without it every
// check reports skipped). Setter, not constructor arg, to keep NewService's
// signature stable for tests that don't verify.
func (s *Service) SetVerifier(v Verifier) { s.verifier = v }

// Verify runs the caller's live checks against their FIRST project's repo — the
// back-compat user-scoped check (POST /api/settings/verify, 11 §4).
func (s *Service) Verify(ctx context.Context, userID string) ([]CheckResult, error) {
	repoURL := ""
	if p, err := s.store.GetProjectByOwner(ctx, userID); err == nil {
		repoURL = p.RepoURL
	}
	return s.verifyRepo(ctx, userID, repoURL)
}

// VerifyProject runs the caller's live checks against a SPECIFIC project's repo
// (12 §3.1, §6.2): the repo check uses that project's url; the Amika/Anthropic/
// Devin checks are per-user. Owner-authorized — a foreign/unknown project is
// ErrNotFound (→ 404).
func (s *Service) VerifyProject(ctx context.Context, userID, projectID string) ([]CheckResult, error) {
	proj, err := s.ProjectByID(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	return s.verifyRepo(ctx, userID, proj.RepoURL)
}

// refreshGitHubScopes classifies a stored repo credential whose scopes aren't
// recorded yet — the carried-over manual token. It asks GitHub what the token
// actually carries and persists the answer, so the dashboard can stop guessing
// and either leave the card green or prompt a reconnect.
//
// Best-effort by design, and only ever run from the verify path (never from a
// read): a probe failure means "still unknown" — a revoked or network-blocked
// token must NOT be recorded as scopeless, because that would show a reconnect
// prompt for a credential that may be perfectly fine. It also never re-probes a
// credential whose scopes are already recorded, so the common case costs
// nothing.
func (s *Service) refreshGitHubScopes(ctx context.Context, userID string, cfg UserConfig) {
	if s.gh == nil || len(cfg.GitHubTokenEnc) == 0 || cfg.GitHubTokenScopes != "" {
		return
	}
	scope, err := s.gh.TokenScopes(ctx, s.decrypt(cfg.GitHubTokenEnc))
	if err != nil || strings.TrimSpace(scope) == "" {
		// Unknown stays unknown: see the doc comment. A fine-grained PAT
		// legitimately reports no scopes, so an empty answer is not evidence
		// that the credential is insufficient.
		return
	}
	cfg.UserID = userID
	cfg.GitHubTokenScopes = scope
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		slog.Warn("identity: record github token scopes", "err", err)
	}
}

// completeOAuth is the identity half of the callback: exchange the code, read
// the profile, enforce the allowlist, find-or-create the user. It returns the
// access token and its granted scope alongside the user so CompleteConnect can
// vet and store them. The token is a live secret — never log it.
func (s *Service) completeOAuth(ctx context.Context, code, op string) (User, string, string, error) {
	token, scope, err := s.gh.ExchangeCode(ctx, code)
	if err != nil {
		return User{}, "", "", fmt.Errorf("identity: %s: %w", op, err)
	}
	ghUser, err := s.gh.FetchUser(ctx, token)
	if err != nil {
		return User{}, "", "", fmt.Errorf("identity: %s: %w", op, err)
	}
	login := strings.ToLower(ghUser.Login)
	if !s.allowed[login] {
		return User{}, "", "", ErrNotAllowed
	}
	user, err := s.upsertFromGitHub(ctx, ghUser)
	if err != nil {
		return User{}, "", "", err
	}
	return user, token, scope, nil
}

// invalidateOwnedProjects rebuilds every project the user owns after a config
// write. Config is per-user and shared by every brain the user owns (12 §2), so
// a credential change must rebuild ALL of them, not just one. A user who hasn't
// onboarded a project yet has nothing to invalidate.
func (s *Service) invalidateOwnedProjects(ctx context.Context, userID, op string) error {
	projects, err := s.store.ListProjectsByOwner(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: %s: %w", op, err)
	}
	for _, proj := range projects {
		s.fireInvalidate(proj.ID)
	}
	return nil
}

// storeGitHubConnection persists a freshly granted repo credential: the token
// itself (into the SAME encrypted column the old manual field wrote to, so the
// two paths are interchangeable), plus the scope GitHub granted it and the
// account it belongs to. Recording the scope is what lets the dashboard tell
// "connected" apart from "stored but can't reach the repo" without re-probing
// GitHub on every page load.
func (s *Service) storeGitHubConnection(ctx context.Context, userID, token, scope, login string) error {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: store github connection: %w", err)
	}
	cfg.UserID = userID
	if err := s.mergeSecret(&cfg.GitHubTokenEnc, token); err != nil {
		return err
	}
	cfg.GitHubTokenScopes = scope
	cfg.GitHubConnectedLogin = login
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		return fmt.Errorf("identity: store github connection: %w", err)
	}
	return s.invalidateOwnedProjects(ctx, userID, "store github connection")
}

// verifyRepo runs live checks for each configured credential group against the
// given repo url; unconfigured groups report "skipped" (11 §4). Order is fixed:
// anthropic, amika, devin, repo.
func (s *Service) verifyRepo(ctx context.Context, userID, repoURL string) ([]CheckResult, error) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: verify: %w", err)
	}
	anthropicKey := s.decrypt(cfg.AnthropicKeyEnc)
	amikaKey := s.decrypt(cfg.AmikaKeyEnc)
	devinKey := s.decrypt(cfg.DevinKeyEnc)
	ghToken := s.decrypt(cfg.GitHubTokenEnc)
	// Verify is the one user-facing action that is already allowed to touch the
	// network, so it is where a carried-over GitHub token gets its scopes
	// classified — turning the card's provisional "connected" into either a
	// confirmed one or a reconnect prompt. No-op once the scopes are recorded.
	s.refreshGitHubScopes(ctx, userID, cfg)
	checks := make([]CheckResult, 0, verifyCheckCount)
	checks = append(checks, s.check(ctx, "anthropic", anthropicKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAnthropic(ctx, anthropicKey)
	}))
	checks = append(checks, s.check(ctx, "amika", amikaKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyAmika(ctx, amikaKey)
	}))
	checks = append(checks, s.check(ctx, "devin", devinKey != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyDevin(ctx, devinKey)
	}))
	checks = append(checks, s.check(ctx, "repo", repoURL != "", func(ctx context.Context) CheckResult {
		return s.verifier.VerifyRepo(ctx, repoURL, ghToken)
	}))
	return checks, nil
}

// projectView pairs a stored project with its fingerprint-only secret statuses
// (decrypted names + value presence) — the shape the account API returns.
func (s *Service) projectView(p Project) ProjectView {
	return ProjectView{Project: p, Secrets: s.amikaSecretStatuses(p.AmikaSecrets)}
}

// projectRow assembles the store Project for a create/update from a validated
// update (id is "" for a create).
func (s *Service) projectRow(userID, projectID string, upd ProjectUpdate, secrets []AmikaSecret) Project {
	return Project{
		ID:            projectID,
		OwnerUserID:   userID,
		Name:          upd.Name,
		RepoURL:       upd.RepoURL,
		AgentProvider: upd.AgentProvider,
		AmikaSnapshot: upd.AmikaSnapshot,
		WorkerCount:   upd.WorkerCount,
		MergeGateMode: upd.MergeGateMode,
		AmikaSecrets:  secrets,
	}
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

// ensureDevGitHubToken stores the configured dev credential only for a user who
// has none, so repeat sign-ins cause no config write (and therefore no tenant
// invalidation). Best-effort: a failure leaves the user unconnected, which the
// dashboard renders as the connect prompt, and is never fatal to signing in.
func (s *Service) ensureDevGitHubToken(ctx context.Context, userID string) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "identity: read config for dev github token", "user_id", userID, "err", err)
		return
	}
	if len(cfg.GitHubTokenEnc) > 0 {
		return
	}
	if serr := s.UpdateSettings(ctx, userID, SettingsUpdate{GitHubToken: s.devGitHubToken}); serr != nil {
		slog.ErrorContext(ctx, "identity: store dev github token", "user_id", userID, "err", serr)
	}
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

// mergeAmikaSecrets validates and encrypts the inbound secret list against the
// currently-stored secrets. Names are required, trimmed, and unique (each is a
// distinct env var). The value is write-only (11 §3 D7): a non-empty value is
// encrypted fresh; an empty value carries the stored ciphertext forward (keyed
// by name); an empty value with nothing stored for that name is rejected (a
// secret must have a value). BOTH name and value are stored encrypted. A
// nil/empty input clears the list. A rejected list is the client's fault
// (ErrInvalidProject).
func (s *Service) mergeAmikaSecrets(in []AmikaSecretInput, existing []AmikaSecret) ([]AmikaSecret, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxAmikaSecrets {
		return nil, ErrInvalidProject
	}
	// Stored ciphertext values, keyed by decrypted name, for carry-forward of
	// unchanged (empty-value) entries.
	prior := make(map[string][]byte, len(existing))
	for _, sec := range existing {
		prior[s.decrypt(sec.NameEnc)] = sec.ValueEnc
	}
	out := make([]AmikaSecret, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, sec := range in {
		name := strings.TrimSpace(sec.Name)
		if name == "" {
			return nil, ErrInvalidProject
		}
		if _, dup := seen[name]; dup {
			return nil, ErrInvalidProject
		}
		seen[name] = struct{}{}
		nameEnc, err := s.cipher.Encrypt(name)
		if err != nil {
			return nil, fmt.Errorf("identity: encrypt amika secret name: %w", err)
		}
		valueEnc, err := s.amikaSecretValueEnc(sec, name, prior)
		if err != nil {
			return nil, err
		}
		out = append(out, AmikaSecret{NameEnc: nameEnc, ValueEnc: valueEnc})
	}
	return out, nil
}

// amikaSecretValueEnc resolves one entry's value ciphertext: a freshly typed
// value is encrypted; an empty value carries the stored ciphertext forward
// (keyed by name); an empty value with nothing stored is a client error.
func (s *Service) amikaSecretValueEnc(in AmikaSecretInput, name string, prior map[string][]byte) ([]byte, error) {
	if in.Value != "" {
		enc, err := s.cipher.Encrypt(in.Value)
		if err != nil {
			return nil, fmt.Errorf("identity: encrypt amika secret value: %w", err)
		}
		return enc, nil
	}
	if prev, ok := prior[name]; ok && len(prev) > 0 {
		return prev, nil
	}
	return nil, ErrInvalidProject // new secret carries no value
}

// amikaSecretStatuses is the fingerprint-only read view of stored secrets: the
// decrypted name (a label, safe to show) plus the value's presence+tail.
func (s *Service) amikaSecretStatuses(secrets []AmikaSecret) []AmikaSecretStatus {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]AmikaSecretStatus, 0, len(secrets))
	for _, sec := range secrets {
		out = append(out, AmikaSecretStatus{
			Name:  s.decrypt(sec.NameEnc),
			Value: s.secretStatus(sec.ValueEnc),
		})
	}
	return out
}

// resolveAmikaSecrets decrypts a project's stored secrets into plaintext
// name/value pairs for in-process sandbox injection (02 §8). A secret whose
// name fails to decrypt is dropped (it could not be injected under a usable
// env var anyway); mirrors decrypt's swallow-and-continue posture.
func (s *Service) resolveAmikaSecrets(secrets []AmikaSecret) []AmikaSecretValue {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]AmikaSecretValue, 0, len(secrets))
	for _, sec := range secrets {
		name := s.decrypt(sec.NameEnc)
		if name == "" {
			continue
		}
		out = append(out, AmikaSecretValue{Name: name, Value: s.decrypt(sec.ValueEnc)})
	}
	return out
}
