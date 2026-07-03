// Command kiln is the backend binary and the composition root (02 §7,
// 04 §8, D9) — the only place concrete adapters exist together. Startup is
// deliberately the whole recovery story (04 §5): run migrations, start the
// two workers, serve; the first poll re-finds whatever was pending when the
// process last died.
//
// Wiring, as the modules land (04 §8):
//
//	Postgres pool → board/postgres store → board.Service
//	             → runtime/postgres store ┐
//	LLM client → brain (02 §6)            ├→ runtime.Service → the two workers
//	Amika adapter, real or mock by config │
//	push + STT/TTS adapters               ┘
//	api.Hub + api.Server (04 §7) → HTTP server, nudges wired back to the workers
package main

import (
	"fmt"
	"os"
)

func main() {
	// Scaffold: composition arrives with the module implementations.
	fmt.Fprintln(os.Stderr, "kiln: not implemented (scaffold) — see docs/specs/04-runtime-and-api.md §8")
	os.Exit(1)
}
