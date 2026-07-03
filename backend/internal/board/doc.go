// Package board owns the authoritative state of one project's board — tickets,
// columns, zones, sandbox bindings — plus the mechanical rules that govern it:
// invariants, the deterministic pull, and the side-effect transitions.
//
// Spec: docs/specs/02-initial-technical-architecture.md §5 (Board mechanism),
// realizing docs/specs/01-initial.md §5 (The board).
//
// This is the single source of truth for board state; nothing else mutates it
// directly. Neighbours reach it only through the Board API defined in this
// package.
//
// Layering (see 02 §2 "Internal layering of a backend module"). Outer layers
// depend inward, never the reverse:
//
//	interfaces  — thin route/queue-event handlers: transport in/out, then delegate.
//	services    — business logic over entities (Ticket, Sandbox, column, zone, event);
//	              depends on infrastructure only through injected port interfaces.
//	infra       — concrete port implementations (the Postgres repository), wired at
//	              the composition root (cmd/kiln) and injected upward.
package board
