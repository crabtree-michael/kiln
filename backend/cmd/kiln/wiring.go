package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sort"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	sentryhttp "github.com/getsentry/sentry-go/http"
	_ "github.com/lib/pq" // Postgres driver for database/sql.

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/amika"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	agentpg "github.com/crabtree-michael/kiln/backend/internal/agent/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/beta"
	betapg "github.com/crabtree-michael/kiln/backend/internal/beta/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	boardpg "github.com/crabtree-michael/kiln/backend/internal/board/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
	identitypg "github.com/crabtree-michael/kiln/backend/internal/identity/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/identity/verify"
	"github.com/crabtree-michael/kiln/backend/internal/obs"
	"github.com/crabtree-michael/kiln/backend/internal/push"
	pushpg "github.com/crabtree-michael/kiln/backend/internal/push/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/repo"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	runtimepg "github.com/crabtree-michael/kiln/backend/internal/runtime/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/steward"
	stewardpg "github.com/crabtree-michael/kiln/backend/internal/steward/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/tenant"
	"github.com/crabtree-michael/kiln/backend/internal/voice/assemblyai"
	"github.com/crabtree-michael/kiln/backend/internal/web"
)

// errBadConfig marks a fatal misconfiguration at startup (e.g. an unknown
// AGENT_MODE) — the process cannot serve, so run returns it.
var errBadConfig = errors.New("kiln: invalid configuration")

// Startup constants.
const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 15 * time.Second
)

// migrationSet is one module's embedded migrations plus the stable ledger-key
// prefix recorded in schema_migrations (the original relative path, so the
// ledger is unaffected by the move to go:embed).
type migrationSet struct {
	key  string // ledger-key prefix, e.g. "internal/board/postgres/migrations"
	fsys fs.FS  // rooted at the module's .sql files
}

// moduleMigrations lists each module's embedded migrations in dependency order
// (identity first — its users/projects tables now exist before anything
// references them by value on a fresh DB, 11 §3 — then board's outbox's
// FK-free tables, then runtime's events/messages, then agent_turns, then
// steward, then push's subscriptions). The ledger tracks applied migrations
// by file path, so reordering module sets is safe for existing databases:
// already-applied files are skipped by name regardless of set order.
// Embedding (see each module's migrations.go) ships them in the single
// static binary, so there is no runtime file dependency (backend/Dockerfile).
func moduleMigrations() ([]migrationSet, error) {
	mods := []struct {
		key string
		emb fs.FS
	}{
		{"internal/identity/postgres/migrations", identitypg.Migrations},
		{"internal/board/postgres/migrations", boardpg.Migrations},
		{"internal/runtime/postgres/migrations", runtimepg.Migrations},
		{"internal/agent/postgres/migrations", agentpg.Migrations},
		{"internal/steward/postgres/migrations", stewardpg.Migrations},
		{"internal/push/postgres/migrations", pushpg.Migrations},
		{"internal/beta/postgres/migrations", betapg.Migrations},
	}
	sets := make([]migrationSet, 0, len(mods))
	for _, m := range mods {
		sub, err := fs.Sub(m.emb, "migrations")
		if err != nil {
			return nil, fmt.Errorf("kiln: sub migrations %s: %w", m.key, err)
		}
		sets = append(sets, migrationSet{key: m.key, fsys: sub})
	}
	return sets, nil
}

// serve builds the full object graph and runs it until ctx is cancelled
// (04 §8): open the store, migrate, wire the four modules through their
// narrow ports, reconcile the worker pool, then start the two queue workers
// and the HTTP server.
func serve(ctx context.Context, cfg Config, log *slog.Logger) error {
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("kiln: open db: %w", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			log.Error("kiln: close db", "err", cerr)
		}
	}()
	if err = db.PingContext(ctx); err != nil {
		return fmt.Errorf("kiln: ping db: %w", err)
	}
	if err = applyMigrations(ctx, db); err != nil {
		return err
	}

	// Identity is built once here (not again in buildGraph) so bootstrap can use
	// it to know whether the identity surface is mounted and to seed the owner
	// user/project/config. A nil idSvc means unconfigured — bootstrap then only
	// runs the NOT NULL finalizer.
	idSvc, err := buildIdentity(cfg, db, log)
	if err != nil {
		return err
	}
	if err = bootstrap(ctx, db, idSvc, bootstrapInputFromConfig(cfg), log); err != nil {
		return err
	}

	g, err := buildGraph(cfg, db, idSvc, log)
	if err != nil {
		return err
	}
	return g.run(ctx, cfg, log)
}

// bootstrapInputFromConfig resolves the env-shaped values boot-time adoption
// seeds from: repo_url prefers GITHUB_REPO_URL, falling back to AMIKA_REPO_URL,
// and the AMIKA_* credentials are the same plain env vars newProvider reads.
func bootstrapInputFromConfig(cfg Config) bootstrapInput {
	repoURL := cfg.GitHubRepoURL
	if repoURL == "" {
		repoURL = os.Getenv("AMIKA_REPO_URL")
	}
	return bootstrapInput{
		GitHubUser:        cfg.BootstrapGitHubUser,
		RepoURL:           repoURL,
		AmikaSnapshot:     os.Getenv("AMIKA_SNAPSHOT"),
		BrainModel:        cfg.BrainModel,
		WorkerCount:       cfg.WorkerCount,
		AnthropicAPIKey:   cfg.AnthropicAPIKey,
		AmikaAPIKey:       os.Getenv("AMIKA_API_KEY"),
		AmikaClaudeCredID: os.Getenv("AMIKA_CLAUDE_CRED_ID"),
		GitHubAuthToken:   cfg.GitHubAuthToken,
	}
}

// graph is the wired, ready-to-run object graph: the HTTP server plus the two
// serial queue workers (04 §4).
type graph struct {
	server   *api.Server
	events   *runtime.Worker
	outbox   *runtime.Worker
	agent    *agent.Service
	steward  *steward.Service
	registry *tenant.Registry // per-project provider cache; closed on drain (11 §3)
}

// errIdentityNotConfigured is what every tenant resolver returns when the
// identity surface is unmounted (no OAuth/secrets config, 11 §3): a project's
// config cannot be resolved, so its events dead-letter feed-visibly and the
// reconciler idles. The server still boots — app routes just 401/404.
var errIdentityNotConfigured = errors.New("kiln: identity not configured")

// errBrainType guards the tenant.Providers.Brain type assertion in
// brainResolver: the registry always stores a *brainAdapter (a runtime.Brain),
// so this only fires if that contract is ever broken.
var errBrainType = errors.New("kiln: project brain is not a runtime.Brain")

// buildGraph constructs every singleton service and adapter and the per-project
// tenant registry (11 §3). Two construction cycles are broken by late-binding
// adapter fields (adapters.go): runtime↔agent via agentEventAdapter.rt, and the
// registry's build closure captures the still-nil rtSvc/agentSvc pointers,
// which are assigned before any event can trigger a lazy build.
func buildGraph(cfg Config, db *sql.DB, idSvc *identity.Service, log *slog.Logger) (graph, error) {
	if cfg.AgentMode != "mock" && cfg.AgentMode != "amika" {
		return graph{}, fmt.Errorf("%w: unknown AGENT_MODE %q", errBadConfig, cfg.AgentMode)
	}

	boardStore := boardpg.New(db)
	boardSvc := board.NewService(boardStore)
	clock := realClock{}
	hub := api.NewHub(boardSvc)

	// Late-bound: filled once runtime.Service exists (runtime↔agent cycle).
	agentEvents := &agentEventAdapter{}

	// Forward declarations so the registry's build closure — invoked lazily, per
	// project, after these are assigned — can close over the singleton
	// runtime/agent services (11 §3).
	var (
		rtSvc    *runtime.Service
		agentSvc *agent.Service
	)

	// The per-project provider cache (11 §3): resolve a project's decrypted
	// config through identity, then build its worker seeding, brain, and agent
	// provider once and cache them. It captures &rtSvc/&agentSvc so its lazy
	// build closure sees them once assigned below (the construction cycle).
	registry := newRegistry(cfg, idSvc, boardStore, boardSvc, &rtSvc, &agentSvc)
	if idSvc != nil {
		// A dashboard credential/project write drops the cached providers so the
		// next event rebuilds from fresh config — no restart (11 §3).
		idSvc.SetInvalidator(registry.Invalidate)
	}

	// Resolvers over the registry/identity the singleton services depend on.
	projects := &projectsResolver{idSvc: idSvc}
	owner := &ownerResolver{idSvc: idSvc}

	// The agent runtime resolves each project's provider + worker prefix per
	// sweep/turn (11 §3); its cross-project liveness loop nudges the hub to
	// re-push (adapters.go's boardRefreshAdapter). Reverse edge (hub reading
	// agent status) is late-bound below.
	agentSvc = agent.NewService(
		agentpg.New(db), &providerResolver{registry: registry}, projects,
		agentEvents, &slotsAdapter{store: boardStore}, clock,
		&boardRefreshAdapter{hub: hub, projects: projects},
	)
	hub.SetAgentInspector(&agentStatusAdapter{inner: agentSvc})

	// notify.send executor (02 §10): a real Web Push sender when the operator has
	// configured a VAPID key pair, else the log-only fallback. The runtime asks
	// per project; the notifier and mode reader resolve owner→user via the
	// registry (11 §3).
	pushStore := pushpg.New(db)
	rtSvc = runtime.NewService(
		runtimepg.New(db), runtimepg.New(db), &brainResolver{registry: registry}, boardSvc,
		&blockerAdapter{inner: boardSvc}, &agentRuntimeAdapter{inner: agentSvc},
		newNotifier(cfg, pushStore, owner, log),
		hub, hub,
		runtimepg.New(db), &boardViewAdapter{inner: boardSvc}, hub, hub,
		owner,
	)
	agentEvents.rt = rtSvc // close the runtime↔agent cycle.

	// STT token minter (09 §6): the api handler mints a short-lived AssemblyAI
	// streaming token; the browser opens the STT socket directly, so the key
	// never leaves the backend (02 §2).
	voiceMinter := assemblyai.New(assemblyai.Config{
		APIKey:  cfg.AssemblyAIAPIKey,
		BaseURL: cfg.AssemblyAIBaseURL,
	})

	server := api.NewServer(boardSvc, rtSvc, rtSvc, rtSvc, rtSvc, hub, voiceMinter)
	enableServerRoutes(server, cfg, db, boardSvc, rtSvc, agentSvc, boardStore, idSvc, pushStore, betapg.New(db))

	events, outbox := rtSvc.Workers(clock)
	return graph{
		server:   server,
		events:   events,
		outbox:   outbox,
		agent:    agentSvc,
		steward:  newSteward(cfg, db, clock, projects, boardSvc, agentSvc, rtSvc),
		registry: registry,
	}, nil
}

// newRegistry constructs the per-project provider cache (11 §3). Its resolve
// closure decrypts a project's config through identity (a nil idSvc — identity
// unconfigured — fails every resolve with errIdentityNotConfigured); its build
// closure dereferences rtSvc/agentSvc, which buildGraph assigns before any event
// can trigger a lazy build, so the runtime↔registry construction cycle is safe.
func newRegistry(
	cfg Config, idSvc *identity.Service, boardStore *boardpg.Store, boardSvc *board.Service,
	rtSvc **runtime.Service, agentSvc **agent.Service,
) *tenant.Registry {
	return tenant.New(
		func(rctx context.Context, projectID string) (identity.RuntimeConfig, error) {
			if idSvc == nil {
				return identity.RuntimeConfig{}, errIdentityNotConfigured
			}
			return idSvc.RuntimeConfig(rctx, projectID)
		},
		func(bctx context.Context, rc identity.RuntimeConfig) (*tenant.Providers, error) {
			return buildTenantProviders(bctx, cfg, rc, boardStore, boardSvc, *rtSvc, *agentSvc)
		},
	)
}

// buildTenantProviders is the tenant registry's per-project build closure body
// (11 §3): from one project's decrypted RuntimeConfig it seeds the board worker
// pool, constructs the project's brain (over projectID-injecting adapters and an
// Anthropic client keyed by the deployment-global ANTHROPIC_API_KEY + the
// project's model) and its coding-agent provider, and returns them bundled for
// the registry to cache.
// Extracted from buildGraph's closure to keep both within the complexity budget.
func buildTenantProviders(
	ctx context.Context, cfg Config, rc identity.RuntimeConfig,
	boardStore *boardpg.Store, boardSvc *board.Service, rtSvc *runtime.Service, agentSvc *agent.Service,
) (*tenant.Providers, error) {
	pid := rc.Project.ID

	// Seed this project's worker-slot pool to its configured size (03 §8, 11 §3)
	// — the per-project replacement for the removed global boot reconcile.
	if err := boardStore.ReconcileWorkers(ctx, pid, rc.Project.WorkerCount); err != nil {
		return nil, fmt.Errorf("kiln: reconcile workers for project %s: %w", pid, err)
	}

	prefix := cfg.WorkerPrefix + workerPrefixScope(pid) + "-"

	// The coding-agent provider: the in-memory mock, or the Amika HTTP client
	// built from this project's owner credentials (11 §3). AGENT_MODE is
	// validated once in buildGraph, so a non-mock mode here is always amika.
	var provider agent.Provider
	if cfg.AgentMode == "mock" {
		provider = mock.New()
	} else {
		// A per-tenant HTTP client so each project's connection pool is isolated
		// and tenant.Providers.Close can release it on a credential rebuild
		// (11 §3) without touching the shared http.DefaultClient.
		provider = amika.New(amika.Config{
			BaseURL:      cfg.AmikaBaseURL,
			APIKey:       rc.AmikaAPIKey,
			RepoURL:      rc.Project.RepoURL,
			Snapshot:     rc.Project.AmikaSnapshot,
			ClaudeCredID: rc.AmikaClaudeCredID,
			WorkerPrefix: prefix,
			Secrets:      amikaSecretRefs(rc.AmikaSecrets),
		}, &http.Client{})
	}

	// The brain's repo-inspection shell: a maintained clone under a per-project
	// directory, from the project repo with its owner's token. repo.New is
	// non-fatal — an unconfigured/failed clone yields a disabled shell.
	repoShell := repo.New(ctx, repo.Config{
		RepoURL:   rc.Project.RepoURL,
		AuthToken: rc.GitHubAuthToken,
		Dir:       cfg.RepoDir + "/" + pid,
	})

	model := rc.Project.BrainModel
	if model == "" {
		model = brain.DefaultModel
	}
	gateMode := brain.GateMode(rc.Project.MergeGateMode)
	llm := newBrainLLM(cfg, model)

	brainSvc := brain.NewService(
		&boardAPIAdapter{svc: boardSvc, projectID: pid},
		&boardReaderAdapter{svc: boardSvc, projectID: pid},
		&sayAdapter{rt: rtSvc, projectID: pid},
		&notificationsAdapter{rt: rtSvc, projectID: pid},
		&feedReaderAdapter{rt: rtSvc, projectID: pid},
		&convoAdapter{rt: rtSvc, projectID: pid},
		&agentInspectorAdapter{inner: agentSvc, projectID: pid},
		&repoShellAdapter{inner: repoShell},
		llm,
		brain.Config{Model: model, GateMode: gateMode},
	)

	return &tenant.Providers{
		ProjectID:    pid,
		OwnerUserID:  rc.OwnerUserID,
		WorkerPrefix: prefix,
		WorkerCount:  rc.Project.WorkerCount,
		Brain:        &brainAdapter{inner: brainSvc},
		Agent:        provider,
	}, nil
}

// newBrainLLM builds the brain's Anthropic adapter for a project's model. The
// Anthropic key is a deployment-global setting (ANTHROPIC_API_KEY via Config),
// not per-user config: every project's brain drives the same key rather than
// rc.AnthropicAPIKey (dormant — kept for a future per-user path). An empty key
// falls back to the SDK's own env/credential lookup, so the "unconfigured boot"
// behavior is unchanged.
func newBrainLLM(cfg Config, model string) *brain.Adapter {
	if cfg.AnthropicAPIKey != "" {
		return brain.NewAdapterWithClient(brain.Config{Model: model}, option.WithAPIKey(cfg.AnthropicAPIKey))
	}
	return brain.NewAdapter(brain.Config{Model: model})
}

// amikaSecretRefs maps a project's decrypted secrets (RuntimeConfig, plaintext)
// onto the amika adapter's config type (02 §8) — the one place the identity
// domain type meets the provider adapter, keeping the amika package free of an
// identity import. Plaintext name+value crosses here for in-process injection.
func amikaSecretRefs(secrets []identity.AmikaSecretValue) []amika.SecretRef {
	if len(secrets) == 0 {
		return nil
	}
	out := make([]amika.SecretRef, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, amika.SecretRef{Name: s.Name, Value: s.Value})
	}
	return out
}

// workerPrefixScopeLen is how many leading project-uuid characters seed the
// per-project worker-name prefix (11 §3) — enough to disambiguate tenants on a
// shared provider account without carrying the whole 36-char uuid into a name.
const workerPrefixScopeLen = 8

// workerPrefixScope is the per-project segment of the provider worker-name
// prefix (11 §3): the first workerPrefixScopeLen chars of the project uuid (or
// the whole id when shorter), so each project's sandboxes are name-isolated on a
// shared provider account.
func workerPrefixScope(projectID string) string {
	if len(projectID) < workerPrefixScopeLen {
		return projectID
	}
	return projectID[:workerPrefixScopeLen]
}

// projectsResolver enumerates the live project set for the cross-project
// singletons — the agent reconciler/liveness loop (agent.Projects), the steward
// sweep (steward.Projects), and the board-refresh nudge (projectLister). A nil
// idSvc (identity unconfigured) yields no projects, so those loops idle (11 §3).
type projectsResolver struct{ idSvc *identity.Service }

func (p *projectsResolver) ProjectIDs(ctx context.Context) ([]string, error) {
	if p.idSvc == nil {
		return nil, nil // identity unconfigured: no tenants to sweep
	}
	ids, err := p.idSvc.ListProjectIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("kiln: list project ids: %w", err)
	}
	return ids, nil
}

var (
	_ agent.Projects   = (*projectsResolver)(nil)
	_ steward.Projects = (*projectsResolver)(nil)
	_ projectLister    = (*projectsResolver)(nil)
)

// providerResolver satisfies agent.ProviderResolver over the tenant registry
// (11 §3): a project's coding-agent Provider plus the worker-name prefix that
// scopes its sandboxes.
type providerResolver struct{ registry *tenant.Registry }

func (r *providerResolver) For(ctx context.Context, projectID string) (agent.Provider, string, error) {
	p, err := r.registry.For(ctx, projectID)
	if err != nil {
		return nil, "", fmt.Errorf("kiln: resolve provider for project %s: %w", projectID, err)
	}
	return p.Agent, p.WorkerPrefix, nil
}

var _ agent.ProviderResolver = (*providerResolver)(nil)

// brainResolver satisfies runtime.BrainResolver over the tenant registry
// (11 §3): each project's own brain, built over its credentials/config. The
// registry stores the brain as a brainAdapter (runtime.Brain) so no assertion
// can fail at runtime, but the type-check guards a future Providers.Brain shape
// change.
type brainResolver struct{ registry *tenant.Registry }

func (r *brainResolver) For(ctx context.Context, projectID string) (runtime.Brain, error) {
	p, err := r.registry.For(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("kiln: resolve brain for project %s: %w", projectID, err)
	}
	b, ok := p.Brain.(runtime.Brain)
	if !ok {
		return nil, fmt.Errorf("%w: project %s (%T)", errBrainType, projectID, p.Brain)
	}
	return b, nil
}

var _ runtime.BrainResolver = (*brainResolver)(nil)

// ownerResolver resolves a project to its owning user id (11 §3) — the
// notifier path's tenant→recipient hop. Over identity's cheap GetProject (not
// the tenant registry) so a notification for an as-yet-unbuilt project maps
// projectID→owner without triggering a full provider build (repo clone,
// ReconcileWorkers, client construction). Satisfies both runtime.Owner and the
// adapters' ownerLookup.
type ownerResolver struct{ idSvc *identity.Service }

// errIdentityUnconfigured is returned when an owner lookup is attempted with no
// identity service — an unconfigured boot has no tenants, so a notifier path
// reaching here is a wiring bug, not a runtime condition.
var errIdentityUnconfigured = errors.New("kiln: identity not configured")

func (r *ownerResolver) Owner(ctx context.Context, projectID string) (string, error) {
	if r.idSvc == nil {
		return "", fmt.Errorf("kiln: resolve owner for project %s: %w", projectID, errIdentityUnconfigured)
	}
	p, err := r.idSvc.GetProject(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("kiln: resolve owner for project %s: %w", projectID, err)
	}
	return p.OwnerUserID, nil
}

var (
	_ runtime.Owner = (*ownerResolver)(nil)
	_ ownerLookup   = (*ownerResolver)(nil)
)

// newSteward builds the mechanical stall watchdog: a deterministic sweep over
// Working tickets that pokes an idle/stopped agent and escalates a genuine stall
// (re-stall or post-poke error) to Blocked. It reaches the board, agent runtime,
// and feed only through its own narrow ports (adapters.go); no brain judgment.
// newSteward builds the mechanical stall watchdog: a per-project deterministic
// sweep over Working tickets that pokes an idle/stopped agent and escalates a
// genuine stall to Blocked. Under multi-tenancy (11 §3) it enumerates the live
// projects via the same resolver the agent reconciler uses, reaching each
// project's board/agent/feed only through its own narrow ports (adapters.go).
func newSteward(
	cfg Config, db *sql.DB, clock realClock, projects *projectsResolver,
	boardSvc *board.Service, agentSvc *agent.Service, rtSvc *runtime.Service,
) *steward.Service {
	return steward.NewService(
		projects,
		&stewardBoardAdapter{inner: boardSvc},
		&stewardAgentAdapter{inner: agentSvc},
		&stewardFeedAdapter{inner: rtSvc},
		stewardpg.New(db),
		clock,
		steward.Config{Stall: cfg.PokeStall, Interval: cfg.PokeInterval},
	)
}

// buildIdentity constructs the dashboard-auth service (11 §2) when
// GITHUB_OAUTH_CLIENT_ID, GITHUB_OAUTH_CLIENT_SECRET, and KILN_SECRETS_KEY
// are ALL set, returning a nil *identity.Service (not mounted) when all three
// are unset — an unconfigured boot is today's boot. A malformed
// KILN_SECRETS_KEY fails hard (11 §3): a half-working cipher must never
// silently store plaintext. Any partial subset (one or two of the three set)
// is a misconfiguration too incomplete to run identity from — mounting on
// ClientID+SecretsKey alone would serve a working-looking /auth/github/login
// whose callback always fails the token exchange (final review, Minor #3) —
// so it logs a warning and stays unmounted rather than erroring.
func buildIdentity(cfg Config, db *sql.DB, log *slog.Logger) (*identity.Service, error) {
	switch {
	case cfg.GitHubOAuthClientID != "" && cfg.GitHubOAuthClientSecret != "" && cfg.SecretsKey != "":
		cipher, err := identity.NewCipher(cfg.SecretsKey)
		if err != nil {
			return nil, fmt.Errorf("kiln: identity cipher: %w", err)
		}
		gh := githubapi.New(githubapi.Config{
			ClientID:     cfg.GitHubOAuthClientID,
			ClientSecret: cfg.GitHubOAuthClientSecret,
		}, nil)
		idSvc := identity.NewService(identitypg.New(db), cipher, gh, cfg.AllowedGitHubUsers)
		idSvc.SetVerifier(verify.New(resolveAmikaBaseURL(cfg)))
		return idSvc, nil
	case cfg.GitHubOAuthClientID != "" || cfg.GitHubOAuthClientSecret != "" || cfg.SecretsKey != "":
		log.Warn("identity disabled: need all of GITHUB_OAUTH_CLIENT_ID, GITHUB_OAUTH_CLIENT_SECRET, and KILN_SECRETS_KEY")
	}
	//nolint:nilnil // a nil *identity.Service with a nil error IS "not configured" — the
	// caller's idSvc != nil check is exactly this contract, not an ambiguous failure.
	return nil, nil
}

// resolveAmikaBaseURL is the platform-global Amika base URL (AMIKA_BASE_URL,
// 11 §3 amended 2026-07-06): an unset env var falls back to the amika
// adapter's own default rather than leaving the verifier unconfigured.
func resolveAmikaBaseURL(cfg Config) string {
	if cfg.AmikaBaseURL != "" {
		return cfg.AmikaBaseURL
	}
	return amika.DefaultBaseURL
}

// enableServerRoutes wires the api server's non-core routes: the unconditional
// reset affordance and health/SPA, plus the dev-only seed routes when
// KILN_DEV_ENDPOINTS is set. Extracted from buildGraph to keep that function
// within the complexity budget.
func enableServerRoutes(
	server *api.Server, cfg Config, db *sql.DB,
	boardSvc *board.Service, rtSvc *runtime.Service,
	agentSvc *agent.Service, boardStore *boardpg.Store, idSvc *identity.Service,
	pushStore push.Store, betaStore beta.Store,
) {
	// Web Push registration (02 §10): the subscribe route is always mounted (the
	// store always exists); the VAPID public key is served only when configured,
	// else GET /api/push/key 404s and the client hides the notifications toggle.
	server.EnablePush(&pushRegistrarAdapter{store: pushStore}, cfg.VAPIDPublicKey)
	// Beta-signup collection: the pre-launch landing page's "Join the beta" form
	// posts an email to POST /api/beta-signup, always mounted (the store always
	// exists) since the marketing page depends on it.
	server.EnableBeta(&betaRegistrarAdapter{store: betaStore})
	// The /debug "Reset session" button's endpoint (POST /api/dev/reset) is wired
	// unconditionally — it is a developer affordance, not gated on DevEndpoints.
	// It re-seeds the worker pool to the project's configured worker count (so a
	// fresh session mirrors the dashboard setting, not the deployment default),
	// falling back to cfg.WorkerCount when identity is unconfigured.
	var resetWorkerCount func(ctx context.Context, projectID string) (int, error)
	if idSvc != nil {
		resetWorkerCount = func(ctx context.Context, projectID string) (int, error) {
			p, err := idSvc.GetProject(ctx, projectID)
			if err != nil {
				return 0, err
			}
			return p.WorkerCount, nil
		}
	}
	server.EnableReset(newResetCoordinator(db, agentSvc, boardStore, cfg.WorkerCount, resetWorkerCount))
	// GET /healthz probes the DB (Render health check + first-curl diagnostic);
	// the embedded SPA is the "/" catch-all so the client is served same-origin
	// with the API (design 2026-07-05).
	server.EnableHealthz(version, db.PingContext)
	server.EnableSPA(web.Handler())
	// Dashboard auth (11 §2, §4): mounted only when idSvc was constructed
	// (both GITHUB_OAUTH_CLIENT_ID and KILN_SECRETS_KEY set) — an unconfigured
	// boot leaves /auth/* and /api/me absent (dark-when-unconfigured).
	if idSvc != nil {
		server.EnableIdentity(idSvc, idSvc)
		// Every app route is project-scoped (11 phase 2): withProject resolves the
		// caller's single project through identity's ProjectFor. Mounted with
		// identity — unconfigured boots leave s.projects nil, and the app routes
		// stay behind withSession's 401 (no session can be minted anyway).
		server.EnableTenancy(idSvc)
		if cfg.DevEndpoints {
			// Dev/e2e only (11 §7): mint a session straight from a GitHub login,
			// bypassing the real OAuth dance, so an e2e can sign in deterministically.
			server.EnableDevSession(idSvc)
		}
	}
	if cfg.DevEndpoints {
		// Dev/e2e only: seed a ticket into any state (POST /api/dev/tickets) and
		// post a feed notification (POST /api/dev/notifications), both without the
		// brain — deterministic e2e preconditions.
		server.EnableDevTickets(boardSvc)
		server.EnableDevNotifications(rtSvc)
	}
}

// run starts the two workers and the HTTP server, then blocks until ctx is
// cancelled and shuts the server down gracefully.
func (g graph) run(ctx context.Context, cfg Config, log *slog.Logger) error {
	go g.runWorker(ctx, "events", g.events, log)
	go g.runWorker(ctx, "outbox", g.outbox, log)
	// The agent-runtime loops: an initial worker-pool reconcile, then the poller
	// (advances turns → provider StartTurn/CheckTurn) and reconciler sweep (05 §4–§5).
	// Without this the pool is never provisioned and agent.send turns never reach Amika.
	go g.runAgent(ctx, log)
	// The mechanical stall watchdog's sweep loop: poke idle/stopped Working-ticket
	// agents, escalate genuine stalls to Blocked. Deterministic, no brain.
	go g.runSteward(ctx, log)

	// Wrap the mux with Sentry's HTTP middleware for request tracing + panic
	// capture. Its ResponseWriter proxy preserves http.Flusher (it returns an
	// httpFancyWriter for a Flusher-capable HTTP/1 writer), so the SSE board
	// stream (hub.go) keeps flushing. When Sentry is disabled the mux is used
	// bare — no wrapper, no cost. Repanic lets net/http's own per-request
	// recovery resume after Sentry has captured the panic.
	handler := g.server.Handler()
	if obs.SentryEnabled() {
		handler = sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(handler)
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("kiln: http server", "err", err)
		}
	}()
	log.Info("kiln serving", "addr", cfg.HTTPAddr, "agent_mode", cfg.AgentMode)

	<-ctx.Done()

	// Detach from the (now-cancelled) parent for a bounded graceful drain.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("kiln: http shutdown: %w", err)
	}
	// Release every tenant's per-project resources (HTTP connection pools) now
	// that no new event can build a bundle (11 §3).
	if g.registry != nil {
		g.registry.Close()
	}
	return nil
}

// runWorker runs one queue worker, logging a non-shutdown error. The deferred
// recover is a process-level safety net: an unrecovered panic in a background
// goroutine would otherwise take the whole process down. Per-entry panics are
// already caught inside the worker (runtime.Worker.process) and turned into a
// retryable error; this only fires for a panic outside a handler.
func (g graph) runWorker(ctx context.Context, name string, w *runtime.Worker, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			obs.Capture(ctx, r)
		}
	}()
	if err := w.Run(ctx); err != nil {
		log.Error("kiln: worker exited", "worker", name, "err", err)
	}
}

// runAgent runs the agent-runtime reconciler+poller loop, logging a non-shutdown
// error. A panic in the loop is captured to Sentry and re-logged rather than
// crashing the process (design 2026-07-05).
func (g graph) runAgent(ctx context.Context, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			obs.Capture(ctx, r)
		}
	}()
	if err := g.agent.Run(ctx); err != nil {
		log.Error("kiln: agent runtime exited", "err", err)
	}
}

// runSteward runs the mechanical stall watchdog's sweep loop, logging a
// non-shutdown error. Like runAgent, a panic is captured to Sentry and re-logged
// rather than crashing the process; Service.Run itself never returns an error.
func (g graph) runSteward(ctx context.Context, log *slog.Logger) {
	defer func() {
		if r := recover(); r != nil {
			obs.Capture(ctx, r)
		}
	}()
	if err := g.steward.Run(ctx); err != nil {
		log.Error("kiln: steward exited", "err", err)
	}
}

// applyMigrations applies every module's migrations in order (04 §5's
// "run migrations" startup step). A per-file ledger (schema_migrations) makes
// it idempotent, so a restart is a no-op.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("kiln: ensure migration ledger: %w", err)
	}
	sets, err := moduleMigrations()
	if err != nil {
		return err
	}
	for _, set := range sets {
		if err := applyMigrationDir(ctx, db, set); err != nil {
			return err
		}
	}
	return nil
}

// applyMigrationDir applies the .sql files in one module's embedded set, in
// filename order, skipping any already recorded in the ledger.
func applyMigrationDir(ctx context.Context, db *sql.DB, set migrationSet) error {
	entries, err := fs.ReadDir(set.fsys, ".")
	if err != nil {
		return fmt.Errorf("kiln: read migrations %s: %w", set.key, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyMigrationFile(ctx, db, set.fsys, path.Join(set.key, name), name); err != nil {
			return err
		}
	}
	return nil
}

// applyMigrationFile applies one migration unless the ledger already has it.
func applyMigrationFile(ctx context.Context, db *sql.DB, fsys fs.FS, key, name string) error {
	var applied bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = $1)`, key).
		Scan(&applied); err != nil {
		return fmt.Errorf("kiln: check migration %s: %w", key, err)
	}
	if applied {
		return nil
	}
	body, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("kiln: read migration %s: %w", key, err)
	}
	if _, err := db.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("kiln: apply migration %s: %w", key, err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations (filename) VALUES ($1)`, key); err != nil {
		return fmt.Errorf("kiln: record migration %s: %w", key, err)
	}
	return nil
}
