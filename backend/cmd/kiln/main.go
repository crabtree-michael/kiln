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
