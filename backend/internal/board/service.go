package board

import (
	"context"
	"errors"
)

// errNotImplemented marks scaffold stubs. Implementations follow
// docs/specs/03-board-mechanics.md; remove this once the last stub is gone.
var errNotImplemented = errors.New("board: not implemented (scaffold)")

// Service is the Board API (03 §4) — the only mutation surface for board
// state. Callers: the brain (02 §6) for every operation except RunPull, which
// is never a brain tool (03 I6); the runtime for RunPull (driven by
// pull.evaluate entries) and for the mechanical failure path of MarkBlocked
// (03 §7.3, 04 §3).
//
// Every mutation is one transaction — lock the ticket, verify the
// precondition, apply the change, append outbox rows, commit (03 §6) — and,
// beyond the emissions noted per operation, every mutation emits
// board.updated. Precondition failures are typed errors (ErrNotFound,
// ErrInvalidTransition), never partial writes or silent no-ops (03 D8).
type Service struct {
	store Store
}

// NewService wires the Board API over its persistence port.
func NewService(store Store) *Service { return &Service{store: store} }

// ShapePatch is ShapeTicket's input; nil fields are left unchanged.
type ShapePatch struct {
	Title    *string
	Body     *string
	Priority *int // higher pulls first; there is no separate reprioritize operation (03 §4)
}

// CreateTicket creates a ticket in shaping (03 §4).
// Precondition: title non-empty.
func (s *Service) CreateTicket(ctx context.Context, title, body string) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// ShapeTicket updates a ticket's fields while it is still in Backlog; the
// state is unchanged (03 §4).
// Precondition: state ∈ {shaping, ready}.
func (s *Service) ShapeTicket(ctx context.Context, id TicketID, patch ShapePatch) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// MarkReady moves shaping → ready and sets ready_at (03 §4).
// Precondition: state = shaping. Emits pull.evaluate.
func (s *Service) MarkReady(ctx context.Context, id TicketID) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// SendToAgent covers both 01 §5 rows — Blocked→Working (resume with the
// user's answer) and Working→Working (a new turn). Result state is working;
// blocked_reason is cleared (03 §4).
// Precondition: state ∈ {working, blocked}. Emits amika.instruct.
func (s *Service) SendToAgent(ctx context.Context, id TicketID, instruction string) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// MarkBlocked moves working → blocked with the reason the user must decide on
// — or the failure being surfaced when the runtime calls it mechanically
// (crash/timeout, exhausted delivery retries — 03 §7.3, 04 §3).
// Precondition: state = working. Emits notify.send.
func (s *Service) MarkBlocked(ctx context.Context, id TicketID, reason string) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// AcceptToDone moves working|blocked → done, clearing the sandbox binding
// (releasing the sandbox) and blocked_reason (03 §4).
// Precondition: state ∈ {working, blocked}. Emits pull.evaluate.
func (s *Service) AcceptToDone(ctx context.Context, id TicketID) (Ticket, error) {
	return Ticket{}, errNotImplemented
}

// GetBoard returns the full snapshot (03 §4).
func (s *Service) GetBoard(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, errNotImplemented
}

// RunPull is the deterministic pull (03 §5) — a system action, never a brain
// decision (03 I6). It loops, one transaction per binding, until no
// (ready ticket, free sandbox) pair remains: lock both with SKIP LOCKED,
// move ready → working, bind the sandbox, emit amika.dispatch. Idempotent by
// construction, so duplicate pull.evaluate triggers and at-least-once
// delivery are safe; the one_active_ticket_per_sandbox index (03 I2) is the
// backstop against double-binding.
func (s *Service) RunPull(ctx context.Context) error {
	return errNotImplemented
}
