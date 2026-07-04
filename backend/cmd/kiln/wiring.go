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

	_ "github.com/lib/pq" // Postgres driver for database/sql.

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/amika"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	agentpg "github.com/crabtree-michael/kiln/backend/internal/agent/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/api"
	"github.com/crabtree-michael/kiln/backend/internal/board"
	boardpg "github.com/crabtree-michael/kiln/backend/internal/board/postgres"
	"github.com/crabtree-michael/kiln/backend/internal/brain"
	"github.com/crabtree-michael/kiln/backend/internal/runtime"
	runtimepg "github.com/crabtree-michael/kiln/backend/internal/runtime/postgres"
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

// moduleMigrations lists the three modules' embedded migrations in dependency
// order (board first — the outbox's FK-free tables — then runtime's
// events/messages, then agent_turns). Embedding (see each module's
// migrations.go) ships them in the single static binary, so there is no
// runtime file dependency (backend/Dockerfile).
func moduleMigrations() ([]migrationSet, error) {
	mods := []struct {
		key string
		emb fs.FS
	}{
		{"internal/board/postgres/migrations", boardpg.Migrations},
		{"internal/runtime/postgres/migrations", runtimepg.Migrations},
		{"internal/agent/postgres/migrations", agentpg.Migrations},
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
	server *api.Server
	events *runtime.Worker
	outbox *runtime.Worker
	agent  *agent.Service
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

	agentSvc := agent.NewService(
		agentpg.New(db), provider, agentEvents, &slotsAdapter{store: boardStore}, clock,
	)

	rtSvc := runtime.NewService(
		runtimepg.New(db), runtimepg.New(db), brainPort, boardSvc,
		&blockerAdapter{inner: boardSvc}, agentSvc, &logNotifier{log: log}, hub, hub,
		runtimepg.New(db), &boardViewAdapter{inner: boardSvc}, hub, hub,
	)
	agentEvents.rt = rtSvc // close the runtime↔agent cycle.

	brainSvc := brain.NewService(
		boardSvc, boardSvc, rtSvc, rtSvc, &convoAdapter{rt: rtSvc},
		brain.NewAdapter(brain.Config{Model: cfg.BrainModel}),
		brain.Config{Model: cfg.BrainModel}, brain.CurrentPromptVersion,
	)
	brainPort.inner = brainSvc // close the runtime↔brain cycle.

	server := api.NewServer(boardSvc, rtSvc, rtSvc, rtSvc, rtSvc, hub)
	if cfg.DevEndpoints {
		// Dev/e2e only: seed a ticket into any state (POST /api/dev/tickets) and
		// post a feed notification (POST /api/dev/notifications), both without the
		// brain — deterministic e2e preconditions.
		server.EnableDevTickets(boardSvc)
		server.EnableDevNotifications(rtSvc)
	}

	events, outbox := rtSvc.Workers(clock)
	return graph{
		server: server,
		events: events,
		outbox: outbox,
		agent:  agentSvc,
	}, nil
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

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           g.server.Handler(),
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

// runWorker runs one queue worker, logging a non-shutdown error.
func (g graph) runWorker(ctx context.Context, name string, w *runtime.Worker, log *slog.Logger) {
	if err := w.Run(ctx); err != nil {
		log.Error("kiln: worker exited", "worker", name, "err", err)
	}
}

// runAgent runs the agent-runtime reconciler+poller loop, logging a non-shutdown error.
func (g graph) runAgent(ctx context.Context, log *slog.Logger) {
	if err := g.agent.Run(ctx); err != nil {
		log.Error("kiln: agent runtime exited", "err", err)
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
