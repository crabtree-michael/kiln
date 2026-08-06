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
	// (anthropic, amika, devin, repo — 11 §4).
	verifyCheckCount = 4

	// statusSkipped is the CheckResult.Status for an unconfigured credential
	// group (no verifier wired in, or the group has no secret/repo set).
	statusSkipped = "skipped"
)

// GitHub is the service's port onto the GitHub App — satisfied directly by
// *githubapi.Client (the consumer declares the interface, 02 §2).
type GitHub interface {
	// InstallURL builds the install redirect for a state nonce — the page where
	// GitHub renders the "All repositories / Only select repositories" chooser.
	// With user authorization enabled on the App, one trip through it yields
	// both the installation and a user-authorization code.
	InstallURL(state string) string
	// ExchangeCode returns the USER access token for a callback code. It is the
	// same endpoint the OAuth App used, authenticated with the App's own client
	// id and secret; the scope string it used to return went away with the
	// scopes themselves — a GitHub App's reach is its installation permissions,
	// decided at registration, not a per-grant scope list.
	ExchangeCode(ctx context.Context, code string) (string, error)
	FetchUser(ctx context.Context, accessToken string) (githubapi.GitHubUser, error)
	// ListRepos returns the repos a raw access token can reach — the picker's
	// source for a hand-typed PAT or bootstrap token, which has no installation
	// to narrow. A token GitHub rejects yields githubapi.ErrUnauthorized.
	ListRepos(ctx context.Context, accessToken string) ([]githubapi.Repo, error)
	// ListInstallationRepos returns the repos of ONE installation the user can
	// themselves see — the picker's source once an installation exists, and the
	// user-visible payoff of the App: it lists exactly what they ticked on
	// GitHub's chooser. accessToken is the USER token, not a minted one.
	ListInstallationRepos(ctx context.Context, accessToken string, installationID int64) ([]githubapi.Repo, error)
	// ConfigureURL is GitHub's own settings page for one installation, where the
	// repository selection is changed. Kiln links out to it rather than
	// reimplementing a screen only GitHub can render.
	ConfigureURL(installationID int64) string
}

// Verifier is the service's port onto live connection checks — satisfied by
// *verify.Verifier (the consumer declares the interface, 02 §2). Every method
// reports its outcome as a CheckResult and never returns a Go error.
type Verifier interface {
	VerifyAnthropic(ctx context.Context, apiKey string) CheckResult
	VerifyAmika(ctx context.Context, apiKey string) CheckResult
	VerifyDevin(ctx context.Context, apiKey string) CheckResult
	// VerifyRepo probes repo reachability with a credential resolved AT PROBE
	// TIME: an installation token is minted per use and would be stale if the
	// caller had resolved it earlier.
	VerifyRepo(ctx context.Context, repoURL string, token TokenSource) CheckResult
}

// Service is identity's domain service (11 §2–§4): login, sessions, config.
type Service struct {
	store      Store
	cipher     *Cipher
	gh         GitHub
	tokens     *InstallationTokens
	verifier   Verifier
	allowed    map[string]bool
	now        func() time.Time
	invalidate func(projectID string)
	// devGitHubToken / devGitHubInstallationID are the synthetic GitHub
	// connection DevSignIn stores, both zero (the default) outside a keyless
	// stack — see SetDevGitHubConnection.
	devGitHubToken          string
	devGitHubInstallationID int64
}

// NewService builds the domain service. tokens mints installation credentials
// (nil-safe: without it an installation resolves to no credential, which is the
// unconfigured-App state rather than a crash) and is wired back to the service
// so a mint GitHub rejects is recorded against the user it belongs to — the one
// place a runtime failure becomes the dashboard's "reconnect" prompt.
func NewService(store Store, cipher *Cipher, gh GitHub, tokens *InstallationTokens, allowedLogins []string) *Service {
	allowed := make(map[string]bool, len(allowedLogins))
	for _, l := range allowedLogins {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			allowed[l] = true
		}
	}
	s := &Service{store: store, cipher: cipher, gh: gh, tokens: tokens, allowed: allowed, now: time.Now}
	if tokens != nil {
		tokens.OnUnavailable(s.recordInstallationRevoked)
	}
	return s
}

// ConnectURL starts THE GitHub flow — there is still exactly one (11 §2,
// amended 2026-08-03 and again by the GitHub App migration). Only its
// destination changed: it now points at the App's installation page, where
// GitHub renders the repository chooser, instead of an OAuth authorize screen
// that could only grant blanket `repo`.
//
// Signing in and connecting GitHub remain the same act. With user authorization
// enabled on the App, one pass through this URL returns both halves — the
// installation (which repositories) and a user-authorization code (who) — so the
// migration needed no second route, no second callback, and no second thing for
// a button to point at by mistake.
func (s *Service) ConnectURL(state string) string {
	return s.gh.InstallURL(state)
}

// CompleteConnect completes the single flow (ConnectURL): exchange the code for
// a user token, enforce the allowlist on every sign-in (11 §2), find-or-create
// the user, and persist BOTH halves — the installation id, which is what git and
// `gh` will mint credentials against, and the user token, which is what answers
// "which of this installation's repos can this person see" for the picker.
//
// installationID is GitHub's `installation_id` callback parameter. A callback
// that authorized the user but installed nothing (0) returns
// ErrInstallationRequired alongside a POPULATED user. Returning both is
// deliberate: GitHub really did authenticate an allowlisted account, so the
// caller signs them in and refuses only the repository half — withholding the
// user would lock someone out of the very dashboard the retry lives on.
//
// setup_action is deliberately not a parameter. `install` and `update` differ
// only in whether an installation already existed, and this path is idempotent
// either way: it re-records the installation and re-stores the token, which is
// exactly what a user returning from GitHub's Configure screen with a changed
// repository selection needs.
func (s *Service) CompleteConnect(ctx context.Context, code string, installationID int64) (User, error) {
	user, token, err := s.completeOAuth(ctx, code, "complete connect")
	if err != nil {
		return User{}, err
	}
	if installationID == 0 {
		return user, ErrInstallationRequired
	}
	if err := s.storeGitHubConnection(ctx, user.ID, installationID, token, user.GitHubLogin); err != nil {
		return User{}, fmt.Errorf("identity: complete connect: %w", err)
	}
	return user, nil
}

// AttachInstallation records an installation against an ALREADY SIGNED-IN user.
// It is the second way into the callback: someone who installs Kiln from
// GitHub's own Apps/Marketplace page arrives with an `installation_id` and no
// `code`, because they never passed through Kiln's authorize step. There is
// nothing to exchange and no new user token, so the stored one is left alone.
//
// Without this the flow would dead-end for a perfectly ordinary path — the user
// has installed the App, GitHub has sent them to Kiln, and Kiln would say "not
// connected".
func (s *Service) AttachInstallation(ctx context.Context, userID string, installationID int64) error {
	if installationID == 0 {
		return ErrInstallationRequired
	}
	if err := s.storeGitHubConnection(ctx, userID, installationID, "", ""); err != nil {
		return fmt.Errorf("identity: attach installation: %w", err)
	}
	return nil
}

// gitHubConnection derives the account view's repo-credential state from the
// stored row. The ordering encodes what each state is FOR:
//
//   - an installation GitHub has rejected reads needs_reconnect, so a grant that
//     died since it was made surfaces on the card rather than at the next agent
//     turn;
//   - an installation nothing has reported dead reads connected;
//   - a stored token with NO installation reads unknown — a hand-typed PAT or the
//     deployment's bootstrap token, deliberately treated as working, since it was
//     configured on purpose and Kiln simply did not grant it;
//   - nothing at all reads disconnected.
//
// It is a pure read of the stored row: no network call hides in a dashboard
// render. What GitHub thinks is learned when a credential is actually used, and
// recorded (recordInstallationRevoked) for this function to find later.
func gitHubConnection(cfg UserConfig, configureURL func(int64) string) GitHubConnection {
	if cfg.GitHubInstallationID == 0 {
		if len(cfg.GitHubTokenEnc) == 0 {
			return GitHubConnection{Status: GitHubDisconnected}
		}
		return GitHubConnection{Status: GitHubUnknownScopes, Login: cfg.GitHubConnectedLogin}
	}
	conn := GitHubConnection{
		Status:         GitHubConnected,
		Login:          cfg.GitHubConnectedLogin,
		InstallationID: cfg.GitHubInstallationID,
	}
	if configureURL != nil {
		conn.ConfigureURL = configureURL(cfg.GitHubInstallationID)
	}
	if cfg.GitHubInstallationRevokedAt != nil {
		conn.Status = GitHubNeedsReconnect
	}
	return conn
}

// ListGitHubRepos returns the repos the caller's connected GitHub account can
// reach — the source of the settings repo picker, replacing hand-typed repo URLs.
//
// With an installation this lists ONLY the repositories the user ticked on
// GitHub's chooser, which is the user-visible payoff of the App migration: the
// picker stops offering every repository the account can reach. Without one it
// falls back to the token's own reach, for the hand-typed PAT and bootstrap
// paths that have no installation to narrow.
//
// It reads the SAME row the Integrations card reports on, so the picker and the
// card can never disagree about whether the account is connected. Returns
// ErrGitHubNotConnected when there is no credential at all, or when GitHub
// rejects the one there is — both mean "run the Connect GitHub flow", which is
// the remedy the picker offers.
func (s *Service) ListGitHubRepos(ctx context.Context, userID string) ([]Repo, error) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list github repos: %w", err)
	}
	token := s.decrypt(cfg.GitHubTokenEnc)
	if token == "" {
		return nil, ErrGitHubNotConnected
	}
	found, err := s.listRepos(ctx, cfg, token)
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

// GitHubTokenSource resolves the repo credential for one user as a FUNCTION,
// not a value (design §3.3). An installation token lives about an hour, so a
// string captured when a project's providers were built would be dead long
// before that project's next agent turn; every call site therefore re-resolves
// per git/`gh` invocation, and the cache behind this makes that cheap.
//
// The branch is the credential model, not a migration shim: an installation
// mints, and anything else — a hand-typed PAT, the bootstrap GITHUB_AUTH_TOKEN,
// the keyless stack's synthetic credential — is already a usable token and is
// handed back as-is.
func (s *Service) GitHubTokenSource(cfg UserConfig) TokenSource {
	if cfg.GitHubInstallationID == 0 {
		return StaticTokenSource(s.decrypt(cfg.GitHubTokenEnc))
	}
	installationID := cfg.GitHubInstallationID
	if s.tokens == nil {
		// No minter wired in (the App is unconfigured): report it as an
		// unavailable installation rather than silently falling back to a user
		// token that cannot clone, so the failure names its own cause.
		return func(context.Context) (string, error) {
			return "", fmt.Errorf("identity: installation %d: %w", installationID, githubapi.ErrNoAppCredentials)
		}
	}
	return func(ctx context.Context) (string, error) {
		return s.tokens.Token(ctx, installationID)
	}
}

// DevSignIn is the KILN_DEV_ENDPOINTS-only seam (11 §7): find-or-create with
// NO allowlist check, so e2e can mint sessions without real OAuth. It shares
// EnsureUser's find-or-create mechanics.
//
// When a dev GitHub connection is configured (SetDevGitHubConnection — keyless
// stacks only), it is stored for the user the way CompleteConnect stores a real
// one, so a dev-minted session reaches the settings repo picker already
// connected. Without that call this is a pure find-or-create and touches no
// credential — a real-service e2e run can never have its stored GitHub
// connection clobbered by a synthetic one.
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
		s.ensureDevGitHubConnection(ctx, user.ID, user.GitHubLogin)
	}
	return user, nil
}

// SetDevGitHubConnection configures the synthetic GitHub connection DevSignIn
// stores (keyless e2e): the mock installation the credential path mints
// against, and the mock user token the repo picker lists with. Setter, not a
// constructor arg, for the same reason as SetVerifier — and so it is off unless
// a composition root explicitly opts in.
//
// It stores an INSTALLATION, not just a token, so the keyless lane exercises the
// real App credential path (mint, cache, installation-scoped listing) rather
// than the raw-token fallback beside it.
func (s *Service) SetDevGitHubConnection(installationID int64, token string) {
	s.devGitHubInstallationID = installationID
	s.devGitHubToken = token
}

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
		GitHub:            gitHubConnection(cfg, s.configureURL),
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
	// A GitHub token written through THIS path — the API's back-compat field, the
	// bootstrap seed, the keyless stack's synthetic credential — is a raw token
	// nobody installed an App for. It must not inherit the previous credential's
	// provenance: clear the installation and the recorded account so the card
	// reads "stored, not granted by Kiln" rather than claiming an installation
	// this token has nothing to do with. CompleteConnect never comes through here.
	if upd.GitHubToken != "" {
		cfg.GitHubInstallationID = 0
		cfg.GitHubInstallationRevokedAt = nil
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
	// GitHubToken resolves the repo credential AT USE TIME (see
	// GitHubTokenSource). It is a function rather than a string because an
	// installation token expires within the hour: a value resolved when this
	// bundle was built would be dead by the time a long agent turn used it.
	// Never nil — an unconfigured credential resolves to "".
	GitHubToken TokenSource
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
		GitHubToken:       s.GitHubTokenSource(cfg),
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

// recordInstallationRevoked marks every user on an installation GitHub has just
// listRepos picks the listing endpoint for one stored row: the installation's
// recordInstallationRevoked marks every user on an installation GitHub has just
// rejected, so the Integrations card can prompt a re-install instead of leaving
// the failure to resurface as a mysteriously broken agent turn.
//
// Best-effort and fire-and-forget by design: it runs on the failure path of a
// credential mint, where the caller already has a real error to return, and a
// failed bookkeeping write must never mask it. It is also idempotent — the
// store only writes rows that are not already marked — so the hourly retry of a
// dead installation does not rewrite the row every time.
func (s *Service) recordInstallationRevoked(ctx context.Context, installationID int64) {
	if err := s.store.MarkInstallationRevoked(ctx, installationID, s.now()); err != nil {
		slog.ErrorContext(ctx, "identity: record installation revoked",
			"installation_id", installationID, "err", err)
	}
}

// listRepos picks the listing endpoint for one stored row: the installation's
// (narrowed to the user's own access within it) when there is an installation,
// the token's own reach otherwise.
func (s *Service) listRepos(ctx context.Context, cfg UserConfig, token string) ([]githubapi.Repo, error) {
	if cfg.GitHubInstallationID != 0 {
		found, err := s.gh.ListInstallationRepos(ctx, token, cfg.GitHubInstallationID)
		if err != nil {
			return nil, fmt.Errorf("identity: list installation repos: %w", err)
		}
		return found, nil
	}
	found, err := s.gh.ListRepos(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("identity: list repos: %w", err)
	}
	return found, nil
}

// configureURL is the nil-safe accessor for the adapter's installation-settings
// link, so gitHubConnection can be a pure function over the stored row and still
// be called on a service built without a GitHub adapter (unit tests, and the
// unconfigured deployment).
func (s *Service) configureURL(installationID int64) string {
	if s.gh == nil {
		return ""
	}
	return s.gh.ConfigureURL(installationID)
}

// completeOAuth is the identity half of the callback: exchange the code, read
// the profile, enforce the allowlist, find-or-create the user. It returns the
// user access token alongside the user so CompleteConnect can store it. The
// token is a live secret — never log it.
func (s *Service) completeOAuth(ctx context.Context, code, op string) (User, string, error) {
	token, err := s.gh.ExchangeCode(ctx, code)
	if err != nil {
		return User{}, "", fmt.Errorf("identity: %s: %w", op, err)
	}
	ghUser, err := s.gh.FetchUser(ctx, token)
	if err != nil {
		return User{}, "", fmt.Errorf("identity: %s: %w", op, err)
	}
	login := strings.ToLower(ghUser.Login)
	if !s.allowed[login] {
		return User{}, "", ErrNotAllowed
	}
	user, err := s.upsertFromGitHub(ctx, ghUser)
	if err != nil {
		return User{}, "", err
	}
	return user, token, nil
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

// storeGitHubConnection persists a freshly granted connection: the installation
// id (what git and `gh` mint against) plus, when the flow carried one, the user
// access token and the account it belongs to. An empty token/login leaves the
// stored ones untouched — that is the AttachInstallation path, where the user
// installed from GitHub's own page and there was never a code to exchange.
//
// It clears any recorded revocation: the user has just come back through
// GitHub's install screen, so whatever killed the previous installation has been
// answered, and leaving the flag set would show a reconnect prompt to somebody
// who has this second reconnected.
//
// It also forgets the installation's cached token. The repository selection may
// have changed on the screen the user just left, and a token minted against the
// old selection would keep working against repos they have just removed —
// exactly the blanket access this migration exists to end.
func (s *Service) storeGitHubConnection(ctx context.Context, userID string, installationID int64,
	token, login string,
) error {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		return fmt.Errorf("identity: store github connection: %w", err)
	}
	cfg.UserID = userID
	if err := s.mergeSecret(&cfg.GitHubTokenEnc, token); err != nil {
		return err
	}
	cfg.GitHubInstallationID = installationID
	cfg.GitHubInstallationRevokedAt = nil
	if login != "" {
		cfg.GitHubConnectedLogin = login
	}
	if err := s.store.UpsertUserConfig(ctx, cfg); err != nil {
		return fmt.Errorf("identity: store github connection: %w", err)
	}
	if s.tokens != nil {
		s.tokens.Forget(installationID)
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
	// The repo check gets a token SOURCE, not a token: under an installation the
	// credential is minted by the probe itself. That also makes verify the one
	// user-facing action that discovers a dead installation — the mint's failure
	// is recorded (recordInstallationRevoked), so the card flips to "reconnect"
	// on the very run that reports the repo unreachable.
	ghToken := s.GitHubTokenSource(cfg)
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

// ensureDevGitHubConnection stores the configured dev connection only for a user
// who has none, so repeat sign-ins cause no config write (and therefore no
// tenant invalidation). Best-effort: a failure leaves the user unconnected,
// which the dashboard renders as the connect prompt, and is never fatal to
// signing in.
func (s *Service) ensureDevGitHubConnection(ctx context.Context, userID, login string) {
	cfg, err := s.store.GetUserConfig(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "identity: read config for dev github connection", "user_id", userID, "err", err)
		return
	}
	if cfg.GitHubInstallationID != 0 || len(cfg.GitHubTokenEnc) > 0 {
		return
	}
	serr := s.storeGitHubConnection(ctx, userID, s.devGitHubInstallationID, s.devGitHubToken, login)
	if serr != nil {
		slog.ErrorContext(ctx, "identity: store dev github connection", "user_id", userID, "err", serr)
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
