// Package brain is the (board state + event) -> actions decision step
// (01 §6, 02 §6): wake on one event, load fresh context, run one bounded
// tool-calling pass with the LLM, and emit actions from a fixed seven-tool
// set mapped onto the Board API plus say. Spec:
// docs/specs/06-orchestrator-brain.md (resolving every 02 §6 open decision).
//
// Stateless (06 §9): nothing survives between events — no tables, no
// migrations. Every pass is built fresh from three ports: the board snapshot
// (BoardReader), the last 20 transcript messages (ConversationReader), and
// the triggering event, which this module decodes itself.
//
// Module boundary: this module reaches the board only through BoardAPI /
// BoardReader (ports.go), the transcript only through ConversationReader, and
// the user only through Say. It deliberately does not import
// internal/runtime — mirroring the agent module's rule (05 §2.2 D3) that a
// decision/execution module never depends on the orchestrating shell that
// drives it; the composition root (backend/cmd/kiln) adapts runtime.Event to
// this module's own Event type and adapts this module's HandleEvent to the
// runtime's Brain port (04 §2). It does import internal/board directly for
// the Board API's public entity and error types (Ticket, TicketID, Snapshot,
// ShapePatch, ErrNotFound, ErrInvalidTransition) — board is the stable
// domain contract the brain is defined in terms of (03 §4), not an
// orchestrating layer, so no adapter is needed there: *board.Service
// satisfies BoardAPI/BoardReader directly (see the compile-time assertions
// in ports.go).
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — HandleEvent(ctx, event), the runtime's Brain port (04 §2),
//	              and the seven tool definitions (tools.go) that are this
//	              module's entire action surface (06 §4).
//	services    — context assembly (types.go, prompt.go), the bounded tool
//	              loop (service.go, 06 §5), and the tool→port dispatch
//	              (tools.go), all written once against the ports below.
//	infra       — the Anthropic Go SDK client behind the LLM port (llm.go),
//	              wired at the composition root. Tests use a scripted fake
//	              that plays back a fixed response sequence (06 §9); the SDK
//	              dependency itself is added in the solution phase, not here
//	              (see the wire-in note on Adapter in llm.go).
//
// Files: doc.go (this file); service.go (Service, HandleEvent, the pass
// loop); ports.go (BoardAPI, BoardReader, Say, ConversationReader — the
// ports consumed); tools.go (the seven ToolDef schemas, the registry, and the
// tool-call → port-method dispatch); prompt.go (the Go-template system
// prompt, D7); llm.go (model config, the LLM port + request/response
// shapes, and the not-yet-wired Anthropic adapter); types.go (the per-pass
// input contract, transcript Message, and event/payload shapes, 06 §3).
package brain
