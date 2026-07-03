// Package brain is the (board state + event) -> actions decision step: wake on
// one event, load state, reason once with the LLM, and emit actions from a fixed
// tool set mapped onto the Board API plus notify/speak.
//
// Spec: docs/specs/02-initial-technical-architecture.md §6 (Orchestrator brain),
// realizing docs/specs/01-initial.md §6 (The orchestrator brain).
//
// The tool schema exposed to the LLM is the brain's contract. Replaying the same
// event must not double-apply actions (idempotency is a hard requirement).
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — the queue-event handler the runtime invokes once per event.
//	services    — prompt assembly, the decide step, action validation/application;
//	              depends on the LLM and Board API only through injected ports.
//	infra       — the Anthropic Go SDK client behind an LLM port, wired at the
//	              composition root and injected upward. Tests use a scripted fake.
package brain
