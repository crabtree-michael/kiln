package board_test

// The deterministic pull (03 §5, I6): fires only when a ready ticket AND a
// free worker exist; pull order priority DESC, ready_at ASC, id ASC (D9);
// idempotent under re-run (03 §5 "duplicate or coalesced pull.evaluate
// entries are harmless"); WIP cap is the worker-row count with busy derived
// from active tickets (D2). RunPull is exercised directly here — it is
// exported so the runtime can call it, but no Board API operation reaches
// ready->working any other way (I6); that's cross-checked in
// service_transitions_test.go, where every other operation's precondition
// excludes `ready` as a valid to-state input for that edge.

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

func TestRunPull_NoReadyTicket_NoOp(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull with no ready ticket: unexpected error: %v", err)
	}
	if len(store.outboxSnapshot()) != 0 {
		t.Error("RunPull must not emit anything when no ready ticket exists")
	}
}

func TestRunPull_NoFreeWorker_NoOp(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateReady, ReadyAt: &rt})
	// No workers seeded at all: WorkerTotal == 0, so nothing can ever be free.

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull with no free worker: unexpected error: %v", err)
	}
	tk, _ := store.ticket("t1")
	if tk.State != board.StateReady {
		t.Errorf("ticket state = %q, want unchanged ready (no free worker to pull it)", tk.State)
	}
}

func TestRunPull_BindsReadyTicketToFreeWorker(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	workers := store.seedWorkers(projA, 1)
	rt := store.now()
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "Do the thing", Body: "details", State: board.StateReady, ReadyAt: &rt,
	})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	tk, ok := store.ticket("t1")
	if !ok {
		t.Fatal("ticket t1 vanished")
	}
	if tk.State != board.StateWorking {
		t.Errorf("state = %q, want working after pull (03 §2.1 ready -> working)", tk.State)
	}
	if tk.WorkerID == nil || *tk.WorkerID != workers[0] {
		t.Errorf("WorkerID = %v, want bound to %q", tk.WorkerID, workers[0])
	}

	ems := store.outboxSnapshot()
	sends := emissionsWithTopic(ems, board.TopicAgentSend)
	if len(sends) != 1 {
		t.Fatalf("RunPull must emit exactly one agent.send per pulled ticket, got: %+v", ems)
	}
	payload, ok := sends[0].Payload.(board.SendPayload)
	if !ok {
		t.Fatalf("agent.send payload type = %T, want board.SendPayload", sends[0].Payload)
	}
	if payload.TicketID != "t1" || payload.WorkerID != workers[0] {
		t.Errorf("agent.send payload = %+v", payload)
	}
	if len(emissionsWithTopic(ems, board.TopicBoardUpdated)) == 0 {
		t.Error("RunPull must emit board.updated for the pulled ticket (03 §4: every mutation does)")
	}
}

func TestRunPull_OrderPriorityDescThenReadyAtAscThenIDAsc(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1) // exactly one slot: only the single highest-order ticket can be pulled

	base := store.now()
	// Same priority, different ready_at: earlier ready_at must win the tie.
	store.seedTicket(projA, board.Ticket{
		ID: "low-priority", Title: "T", State: board.StateReady, Priority: 1, ReadyAt: &base,
	})
	laterReady := base.Add(time.Hour)
	store.seedTicket(projA, board.Ticket{
		ID: "same-priority-later", Title: "T", State: board.StateReady, Priority: 5, ReadyAt: &laterReady,
	})
	earlierReady := base.Add(-time.Hour)
	store.seedTicket(projA, board.Ticket{
		ID: "same-priority-earlier", Title: "T", State: board.StateReady, Priority: 5, ReadyAt: &earlierReady,
	})
	store.seedTicket(projA, board.Ticket{
		ID: "highest-priority", Title: "T", State: board.StateReady, Priority: 9, ReadyAt: &base,
	})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}

	pulled := boundWorkingTicketID(t, store)
	if pulled != "highest-priority" {
		t.Errorf("RunPull picked %q, want %q (D9: priority DESC first)", pulled, "highest-priority")
	}
}

func TestRunPull_TieBreakByReadyAtThenID(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)

	tie := store.now()
	// Equal priority AND equal ready_at: id ASC must decide (D9).
	store.seedTicket(projA, board.Ticket{ID: "z-ticket", Title: "T", State: board.StateReady, Priority: 3, ReadyAt: &tie})
	store.seedTicket(projA, board.Ticket{ID: "a-ticket", Title: "T", State: board.StateReady, Priority: 3, ReadyAt: &tie})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	pulled := boundWorkingTicketID(t, store)
	if pulled != "a-ticket" {
		t.Errorf("RunPull picked %q, want %q (D9: id ASC breaks equal priority+ready_at ties)", pulled, "a-ticket")
	}
}

func TestRunPull_LoopsUntilExhausted(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 3)

	rt := store.now()
	for _, id := range []board.TicketID{"t1", "t2", "t3"} {
		store.seedTicket(projA, board.Ticket{ID: id, Title: "T", State: board.StateReady, ReadyAt: &rt})
	}

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	for _, id := range []board.TicketID{"t1", "t2", "t3"} {
		tk, _ := store.ticket(id)
		if tk.State != board.StateWorking {
			t.Errorf("ticket %s state = %q, want working — RunPull must loop until no (ready, free) pair remains (03 §5)",
				id, tk.State)
		}
	}
	ems := store.outboxSnapshot()
	if got := len(emissionsWithTopic(ems, board.TopicAgentSend)); got != 3 {
		t.Errorf("agent.send count = %d, want 3 (one per pulled ticket)", got)
	}
}

func TestRunPull_MoreReadyThanWorkers_LeavesRemainderReady(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)

	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateReady, Priority: 5, ReadyAt: &rt})
	store.seedTicket(projA, board.Ticket{ID: "t2", Title: "T", State: board.StateReady, Priority: 1, ReadyAt: &rt})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	t1, _ := store.ticket("t1")
	t2, _ := store.ticket("t2")
	if t1.State != board.StateWorking {
		t.Errorf("higher-priority ticket t1 state = %q, want working", t1.State)
	}
	if t2.State != board.StateReady {
		t.Errorf("t2 state = %q, want left ready — WIP cap is the worker row count (I2/D2)", t2.State)
	}
}

func TestRunPull_Idempotent_RerunDoesNotDoubleBind(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateReady, ReadyAt: &rt})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("first RunPull: unexpected error: %v", err)
	}
	firstSendCount := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicAgentSend))

	// A duplicate/re-delivered pull.evaluate must converge to a no-op:
	// at-least-once drain + idempotent RunPull is the safety story (03 §5).
	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("second RunPull: unexpected error: %v", err)
	}
	secondSendCount := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicAgentSend))
	if secondSendCount != firstSendCount {
		t.Errorf("re-running RunPull emitted %d more agent.send (double-bind), want unchanged at %d",
			secondSendCount-firstSendCount, firstSendCount)
	}

	tk, _ := store.ticket("t1")
	if tk.State != board.StateWorking {
		t.Errorf("ticket state = %q, want still working after the idempotent re-run", tk.State)
	}
}

func TestRunPull_BusyWorkerIsDerivedNotStored(t *testing.T) {
	// D2: a worker with no `status` column — busy iff an active ticket
	// references it. Seed one worker already bound to a working ticket
	// (simulating a prior pull) and one ready ticket: RunPull must find zero
	// free workers, because the only worker is derived-busy.
	store := newFakeStore()
	svc := board.NewService(store)
	worker := store.seedWorkers(projA, 1)[0]
	store.seedTicket(projA, board.Ticket{ID: "already-working", Title: "T", State: board.StateWorking, WorkerID: &worker})
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "t-ready", Title: "T", State: board.StateReady, ReadyAt: &rt})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	tk, _ := store.ticket("t-ready")
	if tk.State != board.StateReady {
		t.Errorf("ready ticket state = %q, want still ready — the sole worker is derived-busy (03 D2)", tk.State)
	}
}

// boundWorkingTicketID asserts exactly one ticket in the store transitioned
// to working and returns its id.
func boundWorkingTicketID(t *testing.T, store *fakeStore) board.TicketID {
	t.Helper()
	var found []board.TicketID
	for id, tk := range store.tickets {
		if tk.State == board.StateWorking {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one working ticket after RunPull, got %v", found)
	}
	return found[0]
}
