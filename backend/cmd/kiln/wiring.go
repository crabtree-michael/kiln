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

// migrationDirs are the three modules' migration directories, relative to
// Config.MigrationsDir, applied in dependency order (board first — the outbox
// FK-free tables, then runtime's events/messages, then agent_turns).
var migrationDirs = []string{
	"internal/board/postgres/migrations",
	"internal/runtime/postgres/migrations",
	"internal/agent/postgres/migrations",
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
	if err = applyMigrations(ctx, db, cfg.MigrationsDir); err != nil {
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
	)
	agentEvents.rt = rtSvc // close the runtime↔agent cycle.

	brainSvc := brain.NewService(
		boardSvc, boardSvc, rtSvc, &convoAdapter{rt: rtSvc},
		brain.NewAdapter(brain.Config{Model: cfg.BrainModel}),
		brain.Config{Model: cfg.BrainModel}, brain.CurrentPromptVersion,
	)
	brainPort.inner = brainSvc // close the runtime↔brain cycle.

	events, outbox := rtSvc.Workers(clock)
	return graph{
		server: api.NewServer(boardSvc, rtSvc, rtSvc, hub),
		events: events,
		outbox: outbox,
	}, nil
}

// run starts the two workers and the HTTP server, then blocks until ctx is
// cancelled and shuts the server down gracefully.
func (g graph) run(ctx context.Context, cfg Config, log *slog.Logger) error {
	go g.runWorker(ctx, "events", g.events, log)
	go g.runWorker(ctx, "outbox", g.outbox, log)

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

// newProvider selects the agent provider by AGENT_MODE (05 §9): the in-memory
// mock (dev/e2e default) or the Amika HTTP client.
func newProvider(cfg Config) (agent.Provider, error) {
	switch cfg.AgentMode {
	case "mock":
		return mock.New(), nil
	case "amika":
		return amika.New(amika.Config{
			BaseURL: cfg.AmikaBaseURL,
			APIKey:  os.Getenv("AMIKA_API_KEY"),
			RepoURL: os.Getenv("KILN_REPO_URL"),
		}, nil), nil
	default:
		return nil, fmt.Errorf("%w: unknown AGENT_MODE %q", errBadConfig, cfg.AgentMode)
	}
}

// applyMigrations applies every module's migrations in order (04 §5's
// "run migrations" startup step). A per-file ledger (schema_migrations) makes
// it idempotent, so a restart is a no-op.
func applyMigrations(ctx context.Context, db *sql.DB, base string) error {
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("kiln: ensure migration ledger: %w", err)
	}
	for _, dir := range migrationDirs {
		if err := applyMigrationDir(ctx, db, path.Join(base, dir)); err != nil {
			return err
		}
	}
	return nil
}

// applyMigrationDir applies the .sql files in one directory, in filename
// order, skipping any already recorded in the ledger.
func applyMigrationDir(ctx context.Context, db *sql.DB, dir string) error {
	fsys := os.DirFS(dir)
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("kiln: read migrations %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyMigrationFile(ctx, db, fsys, path.Join(dir, name), name); err != nil {
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
