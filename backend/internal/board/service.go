package board

import (
	"context"
	"fmt"
	"time"
)

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
// Precondition: title non-empty (ErrEmptyTitle otherwise, before any write).
func (s *Service) CreateTicket(ctx context.Context, title, body string) (Ticket, error) {
	if title == "" {
		return Ticket{}, ErrEmptyTitle
	}
	var out Ticket
	err := s.store.Tx(ctx, func(tx Tx) error {
		created, err := tx.InsertTicket(ctx, Ticket{
			Title: title,
			Body:  body,
			State: StateShaping,
		})
		if err != nil {
			return fmt.Errorf("board: insert ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		out = created
		return nil
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("board: create ticket: %w", err)
	}
	return out, nil
}

// ShapeTicket updates a ticket's fields while it is still in Backlog; the
// state is unchanged (03 §4).
// Precondition: state ∈ {shaping, ready}.
func (s *Service) ShapeTicket(ctx context.Context, id TicketID, patch ShapePatch) (Ticket, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateShaping && t.State != StateReady {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "ShapeTicket"}
		}
		if patch.Title != nil {
			t.Title = *patch.Title
		}
		if patch.Body != nil {
			t.Body = *patch.Body
		}
		if patch.Priority != nil {
			t.Priority = *patch.Priority
		}
		updated, err := tx.UpdateTicket(ctx, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		return updated, nil
	})
}

// MarkReady moves shaping → ready and sets ready_at (03 §4).
// Precondition: state = shaping. Emits pull.evaluate.
func (s *Service) MarkReady(ctx context.Context, id TicketID) (Ticket, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateShaping {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "MarkReady"}
		}
		now := time.Now().UTC()
		t.State = StateReady
		t.ReadyAt = &now
		updated, err := tx.UpdateTicket(ctx, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicPullEvaluate}); err != nil {
			return Ticket{}, fmt.Errorf("board: append pull.evaluate: %w", err)
		}
		return updated, nil
	})
}

// SendToAgent covers both 01 §5 rows — Blocked→Working (resume with the
// user's answer) and Working→Working (a new turn). Result state is working;
// blocked_reason is cleared (03 §4).
// Precondition: state ∈ {working, blocked}. Emits agent.send.
func (s *Service) SendToAgent(ctx context.Context, id TicketID, instruction string) (Ticket, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if !t.State.Active() || t.WorkerID == nil {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "SendToAgent"}
		}
		worker := *t.WorkerID
		t.State = StateWorking
		t.BlockedReason = nil
		updated, err := tx.UpdateTicket(ctx, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicAgentSend, Payload: SendPayload{
			TicketID: updated.ID,
			WorkerID: worker,
			Message:  instruction,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append agent.send: %w", err)
		}
		return updated, nil
	})
}

// MarkBlocked moves working → blocked with the reason the user must decide on
// — or the failure being surfaced when the runtime calls it mechanically
// (crash/timeout, exhausted delivery retries — 03 §7.3, 04 §3).
// Precondition: state = working. Emits notify.send.
func (s *Service) MarkBlocked(ctx context.Context, id TicketID, reason string) (Ticket, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if t.State != StateWorking {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "MarkBlocked"}
		}
		r := reason
		t.State = StateBlocked
		t.BlockedReason = &r
		updated, err := tx.UpdateTicket(ctx, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicNotifySend, Payload: NotifyPayload{
			TicketID: updated.ID,
			Title:    updated.Title,
			Reason:   reason,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append notify.send: %w", err)
		}
		return updated, nil
	})
}

// AcceptToDone moves working|blocked → done, clearing the worker binding
// and blocked_reason (03 §4).
// Precondition: state ∈ {working, blocked}. Emits pull.evaluate and
// agent.release (recycle the freed worker — 05 §4).
func (s *Service) AcceptToDone(ctx context.Context, id TicketID) (Ticket, error) {
	return s.mutate(ctx, id, func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error) {
		if !t.State.Active() || t.WorkerID == nil {
			return Ticket{}, &ErrInvalidTransition{From: t.State, Attempted: "AcceptToDone"}
		}
		worker := *t.WorkerID
		t.State = StateDone
		t.WorkerID = nil
		t.BlockedReason = nil
		updated, err := tx.UpdateTicket(ctx, *t)
		if err != nil {
			return Ticket{}, fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicPullEvaluate}); err != nil {
			return Ticket{}, fmt.Errorf("board: append pull.evaluate: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicAgentRelease, Payload: ReleasePayload{
			WorkerID: worker,
		}}); err != nil {
			return Ticket{}, fmt.Errorf("board: append agent.release: %w", err)
		}
		return updated, nil
	})
}

// GetBoard returns the full snapshot (03 §4).
func (s *Service) GetBoard(ctx context.Context) (Snapshot, error) {
	snap, err := s.store.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("board: get board: %w", err)
	}
	return snap, nil
}

// RunPull is the deterministic pull (03 §5) — a system action, never a brain
// decision (03 I6). It loops, one transaction per binding (pullOnce), until no
// (ready ticket, free worker) pair remains: lock both with SKIP LOCKED,
// move ready → working, bind the worker, emit agent.send with the work
// order. Idempotent by construction, so duplicate pull.evaluate triggers and
// at-least-once delivery are safe; the one_active_ticket_per_worker index
// (03 I2) is the backstop against double-binding.
func (s *Service) RunPull(ctx context.Context) error {
	for {
		bound, err := s.pullOnce(ctx)
		if err != nil {
			return err
		}
		if !bound {
			return nil
		}
	}
}

// pullOnce binds at most one (ready ticket, free worker) pair in a single
// transaction, reporting whether a binding happened. When either side is
// exhausted it commits an empty transaction and reports bound=false, which
// stops RunPull's loop.
func (s *Service) pullOnce(ctx context.Context) (bool, error) {
	bound := false
	err := s.store.Tx(ctx, func(tx Tx) error {
		ticket, ok, err := tx.NextReadyTicket(ctx)
		if err != nil {
			return fmt.Errorf("board: next ready ticket: %w", err)
		}
		if !ok {
			return nil
		}
		worker, ok, err := tx.FreeWorker(ctx)
		if err != nil {
			return fmt.Errorf("board: free worker: %w", err)
		}
		if !ok {
			return nil
		}
		wid := worker.ID
		ticket.State = StateWorking
		ticket.WorkerID = &wid
		updated, err := tx.UpdateTicket(ctx, ticket)
		if err != nil {
			return fmt.Errorf("board: update ticket: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicAgentSend, Payload: SendPayload{
			TicketID: updated.ID,
			WorkerID: wid,
			Message:  workOrder(updated),
		}}); err != nil {
			return fmt.Errorf("board: append agent.send: %w", err)
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		bound = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("board: run pull: %w", err)
	}
	return bound, nil
}

// mutate runs the common lock-then-check transaction shape (03 §6): lock the
// target ticket, hand it to apply for precondition-check + state change +
// emissions, then append the universal board.updated signal (03 §4). apply
// returns the persisted ticket; any error rolls back the whole transaction so
// no partial write or emission survives a failed precondition (03 I7, D8).
func (s *Service) mutate(
	ctx context.Context,
	id TicketID,
	apply func(ctx context.Context, tx Tx, t *Ticket) (Ticket, error),
) (Ticket, error) {
	var out Ticket
	err := s.store.Tx(ctx, func(tx Tx) error {
		locked, err := tx.LockTicket(ctx, id)
		if err != nil {
			return fmt.Errorf("board: lock ticket %s: %w", id, err)
		}
		updated, err := apply(ctx, tx, &locked)
		if err != nil {
			return err
		}
		if err := tx.AppendOutbox(ctx, Emission{Topic: TopicBoardUpdated}); err != nil {
			return fmt.Errorf("board: append board.updated: %w", err)
		}
		out = updated
		return nil
	})
	if err != nil {
		return Ticket{}, fmt.Errorf("board: mutate ticket %s: %w", id, err)
	}
	return out, nil
}

// workOrder is RunPull's agent.send message — the ticket's title and body as
// the work instruction (03 §7.1). SendToAgent supplies its own instruction
// instead; the agent-runtime module derives first-message-vs-continuation.
func workOrder(t Ticket) string {
	if t.Body == "" {
		return t.Title
	}
	return t.Title + "\n\n" + t.Body
}
