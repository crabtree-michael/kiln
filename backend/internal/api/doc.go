// Package api is the client-facing surface: the live connection that pushes board
// updates to the web client and the HTTP endpoints the client calls. It is the
// only backend module the untrusted client talks to.
//
// Spec: docs/specs/02-initial-technical-architecture.md §7 (client-facing half of
// the runtime shell) and §11 (Web client), realizing docs/specs/01-initial.md §4.
//
// Request/response and live-connection message shapes are the wire contract in
// /schema — never hand-write types that cross this boundary; change the schema and
// regenerate (see AGENTS.md).
//
// Layering (see 02 §2). Outer layers depend inward, never the reverse:
//
//	interfaces  — HTTP routes + the live-connection (WS/SSE) handlers; thin,
//	              transport in/out only.
//	services    — request handling and board-update fan-out; depends on the runtime
//	              and board only through injected ports.
//	infra       — the concrete transport server, wired at the composition root.
package api
