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

	sentryhttp "github.com/getsentry/sentry-go/http"
	_ "github.com/lib/pq" // Postgres driver for database/sql.

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/amika"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	agentpg "github.com/crabtree-michael/kiln/backend/internal/agent/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/api"
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
// (board first — the outbox's FK-free tables — then runtime's events/messages,
// then agent_turns, then identity's users/sessions/config, then push's
// subscriptions). Embedding (see each module's migrations.go) ships them in the
// single static binary, so there is no runtime file dependency
// (backend/Dockerfile).
func moduleMigrations() ([]migrationSet, error) {
	mods := []struct {
		key string
		emb fs.FS
	}{
		{"internal/board/postgres/migrations", boardpg.Migrations},
		{"internal/runtime/postgres/migrations", runtimepg.Migrations},
		{"internal/agent/postgres/migrations", agentpg.Migrations},
		{"internal/steward/postgres/migrations", stewardpg.Migrations},
		{"internal/identity/postgres/migrations", identitypg.Migrations},
		{"internal/push/postgres/migrations", pushpg.Migrations},
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

	g, err := buildGraph(ctx, cfg, db, log)
	if err != nil {
		return err
	}
	return g.run(ctx, cfg, log)
}

// graph is the wired, ready-to-run object graph: the HTTP server plus the two
// serial queue workers (04 §4).
type graph struct {
	server  *api.Server
	events  *runtime.Worker
	outbox  *runtime.Worker
	agent   *agent.Service
	steward *steward.Service
}

// buildGraph constructs every service and adapter and resolves the two
// construction cycles by late-binding the adapter fields (adapters.go):
// runtime↔brain (brainAdapter.inner) and runtime↔agent (agentEventAdapter.rt).
func buildGraph(ctx context.Context, cfg Config, db *sql.DB, log *slog.Logger) (graph, error) {
	boardStore := boardpg.New(db)
	boardSvc := board.NewService(boardStore)
	if err := boardStore.ReconcileWorkers(ctx, cfg.WorkerCount); err != nil {
		return graph{}, fmt.Errorf("kiln: reconcile workers: %w", err)
	}

	clock := realClock{}
	hub := api.NewHub(boardSvc)

	provider, err := newProvider(cfg)
	if err != nil {
		return graph{}, err
	}

	// The two adapters whose target is built later — filled in below once the
	// service each points at exists (breaking the cycles without any setter).
	agentEvents := &agentEventAdapter{}
	brainPort := &brainAdapter{}

	// The agent runtime nudges the hub to re-push the board when a silent
	// liveness change (e.g. a sandbox auto-stop) makes the Streams status stale
	// (amended 2026-07-05). The hub already exists, so the refresher is direct;
	// the reverse edge (hub reading agent status) is late-bound below.
	agentSvc := agent.NewService(
		agentpg.New(db), provider, agentEvents, &slotsAdapter{store: boardStore}, clock,
		&boardRefreshAdapter{hub: hub},
		agent.WithWorkerPrefix(cfg.WorkerPrefix),
	)
	// Close the hub↔agent cycle: the board snapshot now carries each live
	// worker's real session status for the Streams view.
	hub.SetAgentInspector(&agentStatusAdapter{inner: agentSvc})

	// notify.send executor (02 §10): a real Web Push sender when the operator has
	// configured a VAPID key pair, else the log-only fallback (local dev + tests).
	pushStore := pushpg.New(db)
	rtSvc := runtime.NewService(
		runtimepg.New(db), runtimepg.New(db), brainPort, boardSvc,
		&blockerAdapter{inner: boardSvc}, agentSvc, newNotifier(cfg, pushStore, log), hub, hub,
		runtimepg.New(db), &boardViewAdapter{inner: boardSvc}, hub, hub,
	)
	agentEvents.rt = rtSvc // close the runtime↔agent cycle.

	brainPort.inner = buildBrain(ctx, cfg, boardSvc, rtSvc, agentSvc) // close the runtime↔brain cycle.

	// STT token minter (09 §6): the api handler mints a short-lived AssemblyAI
	// streaming token; the browser opens the STT socket directly, so the key
	// never leaves the backend (02 §2).
	voiceMinter := assemblyai.New(assemblyai.Config{
		APIKey:  cfg.AssemblyAIAPIKey,
		BaseURL: cfg.AssemblyAIBaseURL,
	})

	idSvc, err := buildIdentity(cfg, db, log)
	if err != nil {
		return graph{}, err
	}

	server := api.NewServer(boardSvc, rtSvc, rtSvc, rtSvc, rtSvc, hub, voiceMinter)
	enableServerRoutes(server, cfg, db, boardSvc, rtSvc, agentSvc, boardStore, idSvc, pushStore)

	events, outbox := rtSvc.Workers(clock)
	return graph{
		server:  server,
		events:  events,
		outbox:  outbox,
		agent:   agentSvc,
		steward: newSteward(cfg, db, clock, boardSvc, agentSvc, rtSvc),
	}, nil
}

// newSteward builds the mechanical stall watchdog: a deterministic sweep over
// Working tickets that pokes an idle/stopped agent and escalates a genuine stall
// (re-stall or post-poke error) to Blocked. It reaches the board, agent runtime,
// and feed only through its own narrow ports (adapters.go); no brain judgment.
func newSteward(
	cfg Config, db *sql.DB, clock realClock,
	boardSvc *board.Service, agentSvc *agent.Service, rtSvc *runtime.Service,
) *steward.Service {
	return steward.NewService(
		&stewardBoardAdapter{inner: boardSvc},
		&stewardAgentAdapter{inner: agentSvc},
		&stewardFeedAdapter{inner: rtSvc},
		stewardpg.New(db),
		clock,
		steward.Config{Stall: cfg.PokeStall, Interval: cfg.PokeInterval},
	)
}

// buildBrain constructs the brain service, including its repo-inspection
// shell (design 2026-07-04): a maintained local clone the bash tool runs
// allowlisted git/gh/rg commands in, cloned once at boot. repo.New is
// non-fatal — an unconfigured/failed clone yields a disabled shell whose tool
// calls report "unavailable", and it logs the outcome itself. Extracted from
// buildGraph to keep that function within the complexity budget.
func buildBrain(
	ctx context.Context, cfg Config, boardSvc *board.Service, rtSvc *runtime.Service, agentSvc *agent.Service,
) *brain.Service {
	repoShell := repo.New(ctx, repo.Config{
		RepoURL:   cfg.GitHubRepoURL,
		AuthToken: cfg.GitHubAuthToken,
		Dir:       cfg.RepoDir,
	})
	return brain.NewService(
		boardSvc, boardSvc, rtSvc, rtSvc, &feedReaderAdapter{rt: rtSvc},
		&convoAdapter{rt: rtSvc},
		&agentInspectorAdapter{inner: agentSvc},
		&repoShellAdapter{inner: repoShell},
		brain.NewAdapter(brain.Config{Model: cfg.BrainModel}),
		brain.Config{Model: cfg.BrainModel},
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
	pushStore push.Store,
) {
	// Web Push registration (02 §10): the subscribe route is always mounted (the
	// store always exists); the VAPID public key is served only when configured,
	// else GET /api/push/key 404s and the client hides the notifications toggle.
	server.EnablePush(&pushRegistrarAdapter{store: pushStore}, cfg.VAPIDPublicKey)
	// The /debug "Reset session" button's endpoint (POST /api/dev/reset) is wired
	// unconditionally — it is a developer affordance, not gated on DevEndpoints.
	// It re-seeds the worker pool to WorkerCount, mirroring startup, so a fresh
	// session comes back up with capacity.
	server.EnableReset(newResetCoordinator(db, agentSvc, boardStore, cfg.WorkerCount))
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

// newProvider selects the agent provider by AGENT_MODE (05 §9): the Amika HTTP
// client (default) or the in-memory mock.
func newProvider(cfg Config) (agent.Provider, error) {
	switch cfg.AgentMode {
	case "mock":
		return mock.New(), nil
	case "amika":
		return amika.New(amika.Config{
			BaseURL:      cfg.AmikaBaseURL,
			APIKey:       os.Getenv("AMIKA_API_KEY"),
			RepoURL:      os.Getenv("AMIKA_REPO_URL"),
			Snapshot:     os.Getenv("AMIKA_SNAPSHOT"),
			ClaudeCredID: os.Getenv("AMIKA_CLAUDE_CRED_ID"),
			WorkerPrefix: cfg.WorkerPrefix,
		}, nil), nil
	default:
		return nil, fmt.Errorf("%w: unknown AGENT_MODE %q", errBadConfig, cfg.AgentMode)
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
