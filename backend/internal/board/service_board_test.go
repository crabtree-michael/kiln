package board_test

// GetBoard (03 §4) and the D1 derived-grouping guarantee: column/zone are
// never stored fields — the Snapshot's per-state slice membership is the
// only grouping, computed purely from Ticket.State. These tests exercise
// Service.GetBoard's delegation to Store.Snapshot and its error
// propagation; the exact SQL ORDER BY within each group is a Postgres-adapter
// concern proven against real Postgres in postgres/store_integration_test.go
// — the fakeStore.Snapshot used here (fakes_test.go) is a same-shape
// stand-in, not the thing D9's ordering guarantee is ultimately pinned on.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

func TestGetBoard_GroupsPurelyByState(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	worker := store.seedWorkers(projA, 1)[0]

	store.seedTicket(projA, board.Ticket{ID: "shaping-1", Title: "T", State: board.StateShaping})
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "ready-1", Title: "T", State: board.StateReady, ReadyAt: &rt})
	store.seedTicket(projA, board.Ticket{ID: "working-1", Title: "T", State: board.StateWorking, WorkerID: &worker})
	store.seedTicket(projA, board.Ticket{ID: "done-1", Title: "T", State: board.StateDone})

	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: unexpected error: %v", err)
	}

	assertIDs(t, "Shaping", snap.Shaping, "shaping-1")
	assertIDs(t, "Ready", snap.Ready, "ready-1")
	assertIDs(t, "Working", snap.Working, "working-1")
	assertIDs(t, "Done", snap.Done, "done-1")
	if len(snap.Blocked) != 0 {
		t.Errorf("Blocked = %v, want empty (no blocked ticket seeded)", snap.Blocked)
	}
}

func TestGetBoard_BlockedStackedSeparatelyFromWorking(t *testing.T) {
	// D1: state is the single source of truth for grouping — a blocked
	// ticket must land in Blocked, never Working, purely because its State
	// field says blocked (there is no separate zone field to consult).
	store := newFakeStore()
	svc := board.NewService(store)
	worker := store.seedWorkers(projA, 1)[0]
	store.seedTicket(projA, board.Ticket{
		ID: "blocked-1", Title: "T", State: board.StateBlocked,
		WorkerID: &worker, BlockedReason: new("reason"),
	})

	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: unexpected error: %v", err)
	}
	assertIDs(t, "Blocked", snap.Blocked, "blocked-1")
	if len(snap.Working) != 0 {
		t.Errorf("Working = %v, want empty — a blocked ticket must not appear in Working", snap.Working)
	}
}

func TestGetBoard_WorkerCounts(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	workers := store.seedWorkers(projA, 2)
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &workers[0]})

	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: unexpected error: %v", err)
	}
	if snap.WorkerTotal != 2 {
		t.Errorf("WorkerTotal = %d, want 2 (03 §2.3: WIP cap is the worker row count)", snap.WorkerTotal)
	}
	if snap.WorkerFree != 1 {
		t.Errorf("WorkerFree = %d, want 1 (one worker bound to an active ticket, one derived free — D2)", snap.WorkerFree)
	}
}

func TestGetBoard_PropagatesStoreError(t *testing.T) {
	svc := board.NewService(&errorStore{err: errBoom})
	_, err := svc.GetBoard(context.Background(), projA)
	if !errors.Is(err, errBoom) {
		t.Fatalf("GetBoard must surface the store's error, got: %v", err)
	}
}

func assertIDs(t *testing.T, group string, tickets []board.Ticket, want ...board.TicketID) {
	t.Helper()
	if len(tickets) != len(want) {
		t.Fatalf("%s group has %d tickets, want %d (%v)", group, len(tickets), len(want), want)
	}
	seen := map[board.TicketID]bool{}
	for _, tk := range tickets {
		seen[tk.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("%s group missing expected ticket %q", group, id)
		}
	}
}

// errBoom is a sentinel used only to prove Service.GetBoard forwards a store
// failure rather than swallowing it.
var errBoom = errors.New("boom: store unavailable")

// errorStore is a minimal board.Store whose every method fails; it exists
// solely to test Service's error-propagation path without touching
// fakeStore's happy-path plumbing.
type errorStore struct{ err error }

func (s *errorStore) Tx(ctx context.Context, fn func(board.Tx) error) error { return s.err }
func (s *errorStore) Snapshot(ctx context.Context, projectID string) (board.Snapshot, error) {
	return board.Snapshot{}, s.err
}

func (s *errorStore) GetTicket(ctx context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	return board.Ticket{}, s.err
}
