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
	"strings"
	"syscall"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/obs"
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
	AgentMode       string // AGENT_MODE: "amika" (default) or "mock" (05 §9)
	AmikaBaseURL    string // AMIKA_BASE_URL, when AgentMode == "amika" (05 §9)
	AnthropicAPIKey string // ANTHROPIC_API_KEY — the brain's LLM adapter (06 §2)
	BrainModel      string // KILN_BRAIN_MODEL, default brain.DefaultModel (06 §2)
	HTTPAddr        string // KILN_HTTP_ADDR — address the api server binds (04 §7)
	LogLevel        string // KILN_LOG_LEVEL (docker-compose.yml)
	WorkerCount     int    // KILN_WORKER_COUNT — board WIP cap / worker slots (03 §2.3)
	WorkerPrefix    string // KILN_WORKER_PREFIX — per-environment provider worker-name scope (05 §4)
	DevEndpoints    bool   // KILN_DEV_ENDPOINTS=1 — mount dev-only seed routes (local/e2e)

	AssemblyAIAPIKey  string // ASSEMBLYAI_API_KEY — the STT provider's token-mint credential (09 §6)
	AssemblyAIBaseURL string // ASSEMBLYAI_BASE_URL — override the streaming host (default in-adapter)

	// The brain's repo-inspection bash tool (design 2026-07-04): a maintained
	// local clone the brain runs allowlisted git/gh/rg commands in to verify an
	// agent's work is pushed before accept_to_done.
	GitHubRepoURL   string // GITHUB_REPO_URL — the project repo cloned for the brain to inspect
	GitHubAuthToken string // GITHUB_AUTH_TOKEN — token embedded into the https clone + GH_TOKEN
	RepoDir         string // KILN_REPO_DIR — where the clone lives (default defaultRepoDir)

	// Sentry observability (design 2026-07-05). Both empty locally, so the SDK
	// is a no-op: make up and tests are unaffected.
	SentryDSN         string // SENTRY_BACKEND_DSN — backend project DSN; empty ⇒ Sentry disabled
	SentryEnvironment string // SENTRY_ENVIRONMENT — deployment env label (e.g. "production")
}

// Defaults for the composition root's configuration.
const (
	defaultAgentMode   = "amika"
	defaultHTTPAddr    = ":8080"
	defaultLogLevel    = "info"
	defaultWorkerCount = 3
	defaultRepoDir     = "/var/lib/kiln/repo"
)

// resolveWorkerPrefix reads KILN_WORKER_PREFIX — the per-environment scope for
// provider-side worker names (05 §4, amended 2026-07-05). Instances sharing one
// Amika account (prod, local dev, e2e) must each own a distinct prefix, or one
// instance's orphan sweep destroys another's live agents. A missing trailing
// dash is appended so the prefix never fuses with the slot uuid; unset falls
// back to the historical shared default.
func resolveWorkerPrefix() string {
	p := getenvDefault("KILN_WORKER_PREFIX", agent.WorkerNamePrefix)
	if !strings.HasSuffix(p, "-") {
		p += "-"
	}
	return p
}

// resolveHTTPAddr honors a platform-provided PORT (Render/Heroku convention)
// when set, otherwise KILN_HTTP_ADDR, otherwise the default. PORT wins so a
// managed host can assign and route to the bind port without app changes.
func resolveHTTPAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return getenvDefault("KILN_HTTP_ADDR", defaultHTTPAddr)
}

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
		HTTPAddr:        resolveHTTPAddr(),
		LogLevel:        getenvDefault("KILN_LOG_LEVEL", defaultLogLevel),
		WorkerCount:     getenvInt("KILN_WORKER_COUNT", defaultWorkerCount),
		WorkerPrefix:    resolveWorkerPrefix(),
		DevEndpoints:    os.Getenv("KILN_DEV_ENDPOINTS") == "1",

		AssemblyAIAPIKey:  os.Getenv("ASSEMBLYAI_API_KEY"),
		AssemblyAIBaseURL: os.Getenv("ASSEMBLYAI_BASE_URL"),

		GitHubRepoURL:   os.Getenv("GITHUB_REPO_URL"),
		GitHubAuthToken: os.Getenv("GITHUB_AUTH_TOKEN"),
		RepoDir:         getenvDefault("KILN_REPO_DIR", defaultRepoDir),

		SentryDSN:         os.Getenv("SENTRY_BACKEND_DSN"),
		SentryEnvironment: os.Getenv("SENTRY_ENVIRONMENT"),
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
	// os.Exit is isolated here so main() holds no defers; the real startup runs
	// in start(), whose deferred Sentry flush is therefore guaranteed to run
	// before the process exits (gocritic exitAfterDefer).
	os.Exit(start())
}

// start performs process startup and returns the exit code. Its deferred
// Sentry flush runs on every return path — including a failed run — so buffered
// events reach Sentry before exit.
func start() int {
	// Initialize Sentry before anything else so panics report even in the idle
	// (no DATABASE_URL) path, and before NewLogger so the slog→Sentry-Logs bridge
	// can compose. Empty SENTRY_BACKEND_DSN ⇒ a no-op that flushes nothing.
	cfg := loadConfig()
	flush := obs.InitSentry(obs.SentryConfig{
		DSN:         cfg.SentryDSN,
		Environment: cfg.SentryEnvironment,
		Release:     version,
	})
	defer flush()

	log, err := obs.NewLogger()
	if err != nil {
		// The logger is usable (stdout) even when the durable file sink could
		// not be opened; report the degradation rather than start blind.
		log.Warn("kiln: log file sink unavailable; logging to stdout only", "err", err)
	}
	// Make this the process default so every module's slog.*Context call flows
	// through the turn-id context handler (obs.Handler) as JSON lines.
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("kiln exited with error", "err", err)
		return 1
	}
	return 0
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
