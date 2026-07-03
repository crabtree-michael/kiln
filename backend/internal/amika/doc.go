// Package amika is the bridge to the agent platform: dispatch an agent into a
// sandbox, instruct a running/blocked agent, receive a turn's result, and expose
// the queue the runtime triggers off of. It recovers safely across deploys.
//
// Spec: docs/specs/02-initial-technical-architecture.md §8 (Agent-platform
// integration), realizing docs/specs/01-initial.md §4.
//
// Amika's real API/SDK is DEFERRED (01 §11). This package is defined as an
// interface (port) with a mock implementation, so the rest of the system can be
// built and the end-to-end loop tested without real agents. Do not couple other
// modules to a concrete Amika client — depend on the interface.
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — the dispatch / instruct / receive-result contract other modules
//	              depend on (the port).
//	services    — sandbox-lifecycle <-> board-binding mapping and dispatch-failure
//	              surfacing.
//	infra       — the mock adapter today; the real Amika client when its docs land.
package amika
