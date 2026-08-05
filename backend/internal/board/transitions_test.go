package board_test

// Tests for the state-precondition table (transitions.go): the per-state
// allowed set the brain surfaces on get_ticket / list_tickets, and — the point
// of the exercise — that the set agrees with what the operations themselves do.
// A transition advertised as available must not come back
// ErrInvalidTransition, and one left out must.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// allStates is the five stored positions (03 §2.1), so every table below is
// exhaustive over them rather than over whichever ones a case remembered.
var allStates = []board.State{
	board.StateShaping,
	board.StateReady,
	board.StateWorking,
	board.StateBlocked,
	board.StateDone,
}

// TestAllowedOps_PerState is the golden per-state table — what a ticket in each
// state accepts, in report order. Changing it is a deliberate diff, since it is
// what the model is told it can do.
func TestAllowedOps_PerState(t *testing.T) {
	want := map[board.State][]board.Operation{
		board.StateShaping: {
			board.OpShapeTicket, board.OpRequestApproval, board.OpMarkReady, board.OpArchiveTicket,
		},
		board.StateReady: {
			board.OpShapeTicket, board.OpArchiveTicket,
		},
		board.StateWorking: {
			board.OpSendToAgent, board.OpMarkBlocked, board.OpAcceptToDone,
			board.OpKillSandbox, board.OpReassignSandbox,
		},
		board.StateBlocked: {
			board.OpSendToAgent, board.OpAcceptToDone, board.OpArchiveTicket,
			board.OpKillSandbox, board.OpReassignSandbox,
		},
		board.StateDone: {
			board.OpArchiveTicket,
		},
	}

	for _, state := range allStates {
		got := state.AllowedOps()
		if !slices.Equal(got, want[state]) {
			t.Errorf("State(%q).AllowedOps() = %v, want %v", state, got, want[state])
		}
	}
}

// TestAllowedOps_AgreesWithEachOperationsPrecondition is the anti-drift test:
// for every (state, operation) pair it runs the real Board API against a ticket
// seeded in that state and asserts the outcome matches what AllowedOps
// advertised. An operation in the allowed set may still fail its non-state
// preconditions (a spent commit, no free slot), so the assertion is specifically
// about ErrInvalidTransition — the refusal the brain would otherwise have to
// discover by trying.
func TestAllowedOps_AgreesWithEachOperationsPrecondition(t *testing.T) {
	for _, state := range allStates {
		allowed := state.AllowedOps()
		for op, invoke := range boardOperations() {
			t.Run(string(state)+"/"+string(op), func(t *testing.T) {
				svc, store := newTestService()
				// Two slots: one for the ticket to hold while active, one for
				// ReassignSandbox to move onto (its own precondition, not a state one).
				store.seedWorkers(projA, 2)
				seedTicketInState(store, state)

				err := invoke(context.Background(), svc)

				var it *board.ErrInvalidTransition
				gotRefusal := errors.As(err, &it)
				if wantAllowed := slices.Contains(allowed, op); wantAllowed == gotRefusal {
					t.Fatalf("state %q: AllowedOps says %s is allowed=%t, but the operation returned %v",
						state, op, wantAllowed, err)
				}
				if gotRefusal && (it.From != state || it.Attempted != string(op)) {
					t.Errorf("refusal = ErrInvalidTransition{From: %q, Attempted: %q}, want {%q, %q} — "+
						"the allowed set and the refusal must name the same operation",
						it.From, it.Attempted, state, op)
				}
			})
		}
	}
}

// seedTicketInState plants transitionTicket in the given state, honoring the
// invariants that state carries: a bound worker for the active states (03 I3)
// and a reason for blocked (03 I4).
func seedTicketInState(store *fakeStore, state board.State) {
	tk := board.Ticket{ID: transitionTicket, Title: "T", Body: "B", State: state}
	if state.Active() {
		worker := board.WorkerID("w1")
		tk.WorkerID = &worker
	}
	if state == board.StateBlocked {
		reason := "seeded blocker"
		tk.BlockedReason = &reason
	}
	store.seedTicket(projA, tk)
}

const transitionTicket = board.TicketID("t1")

// boardOperations invokes each state-gated operation with arguments that
// satisfy everything *except* its state precondition, so the only reason any of
// them can return ErrInvalidTransition is the state under test.
func boardOperations() map[board.Operation]func(context.Context, *board.Service) error {
	newTitle := "revised"
	return map[board.Operation]func(context.Context, *board.Service) error{
		board.OpShapeTicket: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.ShapeTicket(ctx, projA, transitionTicket, board.ShapePatch{Title: &newTitle}))
		},
		board.OpRequestApproval: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.RequestApproval(ctx, projA, transitionTicket))
		},
		board.OpMarkReady: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.MarkReady(ctx, projA, transitionTicket))
		},
		board.OpSendToAgent: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.SendToAgent(ctx, projA, transitionTicket, "carry on"))
		},
		board.OpMarkBlocked: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.MarkBlocked(ctx, projA, transitionTicket, "needs a decision"))
		},
		board.OpAcceptToDone: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.AcceptToDone(ctx, projA, transitionTicket, board.CompletionLink{}, "abc1234"))
		},
		board.OpArchiveTicket: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.ArchiveTicket(ctx, projA, transitionTicket))
		},
		board.OpKillSandbox: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.KillSandbox(ctx, projA, transitionTicket))
		},
		board.OpReassignSandbox: func(ctx context.Context, svc *board.Service) error {
			return refusal(svc.ReassignSandbox(ctx, projA, transitionTicket))
		},
	}
}

// refusal keeps the error half of a Board API result and drops the ticket,
// unwrapped: the test asserts on the typed refusal itself, so nothing may be
// layered over it here.
func refusal(_ board.Ticket, err error) error { return err }
