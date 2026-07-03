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
// Open composition problem (left for the solution phase): runtime.Service's
// Brain port must be backed by brain.Service, and brain.Service's Say +
// ConversationReader ports must be backed by runtime.Service — a genuine
// construction cycle (neither can be built fully-formed before the other).
// Resolving it (a settable field post-construction, a lazy indirection type,
// or restructuring Say/ConversationReader over the transcript store directly
// instead of over *runtime.Service) is a solution-phase decision, not a
// scaffolding one.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
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
	HTTPAddr        string // address the api server binds (04 §7)
	LogLevel        string // KILN_LOG_LEVEL (docker-compose.yml)
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

	// No modules wired yet: the harness is built before the product (02 §4).
	// A future composition root constructs infra adapters and injects them into
	// each module's services here.

	<-ctx.Done()
	log.Info("kiln shutting down")
	return nil
}
