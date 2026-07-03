// Command kiln is the single composition root for the Kiln backend monolith.
//
// This is the ONLY place concrete infrastructure adapters (Postgres repository,
// LLM client, Amika client, push/STT/TTS clients) are constructed and injected
// upward into services as port interfaces. Modules never construct their own
// infrastructure — see docs/specs/02-initial-technical-architecture.md §2.
//
// v1 is a modular monolith: api, runtime, brain, board and amika share this one
// process today, wired here, but talk to each other only through explicit
// interfaces so they can later be split into separate services.
//
// Wiring shape (04 §8): board.Service, agent.Service, brain.Service, and
// runtime.Service are each constructed over their own ports; where a
// consumer's port doesn't match a service's method set exactly (a different
// Event/id type, an extra return value), adapters.go supplies a one-method
// wrapper — see that file's doc comment for the full inventory and which
// pairs need no adapter at all.
//
// The runtime↔brain and runtime↔agent construction cycles are resolved by
// late-binding two adapter fields (adapters.go): the runtime is built with a
// still-empty brainAdapter and the agent with a still-empty agentEventAdapter,
// then each adapter's target is filled once the service it points at exists.
// No service needs a post-construction setter — see wiring.go's buildGraph.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

// version is stamped at build time via -ldflags "-X main.version=..."; it
// defaults to "dev" for local builds.
var version = "dev"

// Config is the composition root's environment configuration (04 §8; 05 §9
// AGENT_MODE; 06 §2 KILN_BRAIN_MODEL): every concrete adapter's dependency in
// one place, read once at startup. Loading/validation is solution-phase
// wiring logic; the shape is fixed here so it can be reviewed alongside the
// schema and the ports it feeds.
type Config struct {
	DatabaseURL     string // DATABASE_URL — board + runtime + agent tables (02 §3)
	AgentMode       string // AGENT_MODE: "mock" (dev/e2e default) or "amika" (05 §9)
	AmikaBaseURL    string // AMIKA_BASE_URL, when AgentMode == "amika" (05 §9)
	AnthropicAPIKey string // ANTHROPIC_API_KEY — the brain's LLM adapter (06 §2)
	BrainModel      string // KILN_BRAIN_MODEL, default brain.DefaultModel (06 §2)
	HTTPAddr        string // KILN_HTTP_ADDR — address the api server binds (04 §7)
	LogLevel        string // KILN_LOG_LEVEL (docker-compose.yml)
	WorkerCount     int    // KILN_WORKER_COUNT — board WIP cap / worker slots (03 §2.3)
	MigrationsDir   string // KILN_MIGRATIONS_DIR — base dir for module migrations
}

// Defaults for the composition root's configuration.
const (
	defaultAgentMode     = "mock"
	defaultHTTPAddr      = ":8080"
	defaultLogLevel      = "info"
	defaultWorkerCount   = 3
	defaultMigrationsDir = "."
)

// loadConfig reads the composition root's environment (04 §8), applying
// defaults. It never fails: validation of the values happens where they are
// used (newProvider, serve).
func loadConfig() Config {
	return Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		AgentMode:       getenvDefault("AGENT_MODE", defaultAgentMode),
		AmikaBaseURL:    os.Getenv("AMIKA_BASE_URL"),
		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		BrainModel:      os.Getenv("KILN_BRAIN_MODEL"),
		HTTPAddr:        getenvDefault("KILN_HTTP_ADDR", defaultHTTPAddr),
		LogLevel:        getenvDefault("KILN_LOG_LEVEL", defaultLogLevel),
		WorkerCount:     getenvInt("KILN_WORKER_COUNT", defaultWorkerCount),
		MigrationsDir:   getenvDefault("KILN_MIGRATIONS_DIR", defaultMigrationsDir),
	}
}

// getenvDefault returns the environment value for key, or def when unset.
func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getenvInt returns the integer environment value for key, or def when unset
// or unparseable.
func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("kiln exited with error", "err", err)
		os.Exit(1)
	}
}

// run holds the real startup so it is testable and returns errors instead of
// calling os.Exit. Wiring of modules and adapters lands here as surface areas are
// built (board §5, brain §6, runtime/api §7, amika §8).
func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("kiln starting", "version", version)

	cfg := loadConfig()
	if cfg.DatabaseURL == "" {
		// No store configured (e.g. the unit smoke test): there is nothing to
		// wire, so idle until shutdown rather than fail. A real deployment
		// always sets DATABASE_URL (docker-compose.yml).
		log.Info("kiln idle: DATABASE_URL unset, nothing wired")
		<-ctx.Done()
		log.Info("kiln shutting down")
		return nil
	}

	if err := serve(ctx, cfg, log); err != nil {
		return err
	}
	log.Info("kiln shutting down")
	return nil
}
