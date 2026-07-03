// Package runtime is the durable, deploy-resumable service shell: it receives
// events, drives the brain once per event, and coordinates board, amika and
// notifications. The orchestrator wakes on events, not a timer.
//
// Spec: docs/specs/02-initial-technical-architecture.md §7 (Orchestrator API +
// event queue / runtime), realizing docs/specs/01-initial.md §7–§8.
//
// It ingests the two event types — agent-turn-completed and human-voice-input —
// and drains a durable queue table in Postgres so a restart or deploy recovers by
// re-reading durable state rather than trusting in-process memory.
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — event ingestion + the queue-drain loop.
//	services    — the single-writer-per-project event loop, ordering/serialization
//	              of turn-completed vs voice events; depends on the durable queue,
//	              brain, board, amika and notifications only through injected ports.
//	infra       — the Postgres-backed durable queue behind a port, wired at the
//	              composition root and injected upward.
package runtime
