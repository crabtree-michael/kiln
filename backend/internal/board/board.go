// Package board is the authoritative state of the project's board and the
// mechanical rules over it: invariants, the deterministic pull, and the
// transactional-outbox side effects. Spec: docs/specs/03-board-mechanics.md
// (realizing 01 §5 via 02 §5).
//
// Boundary (03 I8): nothing outside this module reads or writes the board
// tables. Brain and runtime reach board state only through Service (the Board
// API); the Store port is implemented by ./postgres and consumed only here.
package board
