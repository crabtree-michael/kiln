package board_test

// The manual sandbox controls (KillSandbox, ReassignSandbox): the user's direct
// override for a wedged or corrupted workspace, the thing that previously only
// the orchestrator could clear up. Both are per-ticket, both act on the sandbox
// behind a slot rather than on the ticket's place on the board, and both
// deliberately ignore the KeepSandbox option — so these tests pin the emissions
// each one owes, the state each one leaves, and the one refusal each one has.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// Kill recycles the ticket's own slot and leaves everything else alone: same
// state, same worker, one agent.release, and mutate's board.updated so open
// clients re-read the slot's status.
func TestKillSandbox_ReleasesTheTicketsWorkerAndKeepsTheBinding(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	got, err := svc.KillSandbox(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("KillSandbox: unexpected error: %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("state = %q, want working — killing a sandbox is not a board transition", got.State)
	}
	if got.WorkerID == nil || *got.WorkerID != worker {
		t.Errorf("worker = %v, want the ticket to keep slot %q", got.WorkerID, worker)
	}

	ems := store.outboxSnapshot()
	releases := emissionsWithTopic(ems, board.TopicAgentRelease)
	if len(releases) != 1 {
		t.Fatalf("agent.release emissions = %d, want 1", len(releases))
	}
	if p, ok := releases[0].Payload.(board.ReleasePayload); !ok || p.WorkerID != worker {
		t.Errorf("release payload = %+v, want the ticket's worker %q", releases[0].Payload, worker)
	}
	if got := len(emissionsWithTopic(ems, board.TopicBoardUpdated)); got != 1 {
		t.Errorf("board.updated emissions = %d, want 1", got)
	}
	if len(ems) != 2 {
		t.Errorf("emissions = %+v, want agent.release + board.updated alone", ems)
	}
}

// A blocked ticket is the likeliest place to reach for the control — the agent
// is stalled and its workspace is suspect — so the kill has to work there too,
// without disturbing the blocker.
func TestKillSandbox_WorksOnABlockedTicketWithoutUnblockingIt(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	reason := "needs a decision"
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked, WorkerID: &worker, BlockedReason: &reason,
	})

	got, err := svc.KillSandbox(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("KillSandbox: unexpected error: %v", err)
	}
	if got.State != board.StateBlocked || got.BlockedReason == nil {
		t.Errorf("ticket = {state:%q reason:%v}, want the blocker untouched", got.State, got.BlockedReason)
	}
	if got := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicAgentRelease)); got != 1 {
		t.Errorf("agent.release emissions = %d, want 1", got)
	}
}

// The override beats the option. Every automatic exit from Developing honors
// KeepSandbox and skips the release; pressing Kill is the user asking for the
// recycle in front of them, and a saved sandbox is exactly the case where a
// silent no-op would be worst.
func TestKillSandbox_IgnoresKeepSandbox(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker, KeepSandbox: true,
	})

	if _, err := svc.KillSandbox(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("KillSandbox: unexpected error: %v", err)
	}
	if got := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicAgentRelease)); got != 1 {
		t.Fatalf("agent.release emissions = %d, want 1 — an explicit kill overrides the saved sandbox", got)
	}
}

// There is no sandbox behind a ticket that never reached a worker, so the
// operation is refused loudly rather than emitting a release for nothing.
func TestKillSandbox_RefusesATicketWithNoWorker(t *testing.T) {
	for _, state := range []board.State{board.StateShaping, board.StateReady, board.StateDone} {
		svc, store := newTestService()
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: state})

		_, err := svc.KillSandbox(context.Background(), projA, "t1")
		requireInvalidTransition(t, err, state, "KillSandbox")
		if ems := store.outboxSnapshot(); len(ems) != 0 {
			t.Errorf("state %q: emissions = %+v, want none — a refused kill writes nothing", state, ems)
		}
	}
}

func TestKillSandbox_UnknownIsNotFound(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projB, worker)
	store.seedTicket(projB, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.KillSandbox(context.Background(), projA, "t1"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("KillSandbox on another project's ticket = %v, want ErrNotFound", err)
	}
}

// The recovery half: the ticket lands on a different slot, the old sandbox is
// recycled, and the new slot gets the full work order — the same message RunPull
// sends, because a fresh sandbox knows nothing about the ticket.
func TestReassignSandbox_MovesToAFreeSlotAndRebriefsIt(t *testing.T) {
	svc, store := newTestService()
	workers := store.seedWorkers(projA, 2)
	old := workers[0]
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "Fix the parser", Body: "It drops trailing commas.",
		State: board.StateWorking, WorkerID: &old,
	})

	got, err := svc.ReassignSandbox(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("ReassignSandbox: unexpected error: %v", err)
	}
	if got.WorkerID == nil || *got.WorkerID == old {
		t.Fatalf("worker = %v, want a different slot than %q", got.WorkerID, old)
	}
	fresh := *got.WorkerID
	if stored, _ := store.ticket("t1"); stored.WorkerID == nil || *stored.WorkerID != fresh {
		t.Errorf("persisted worker = %v, want the new slot %q", stored.WorkerID, fresh)
	}

	ems := store.outboxSnapshot()
	releases := emissionsWithTopic(ems, board.TopicAgentRelease)
	if len(releases) != 1 {
		t.Fatalf("agent.release emissions = %d, want 1", len(releases))
	}
	if p, ok := releases[0].Payload.(board.ReleasePayload); !ok || p.WorkerID != old {
		t.Errorf("release payload = %+v, want the vacated slot %q", releases[0].Payload, old)
	}
	sends := emissionsWithTopic(ems, board.TopicAgentSend)
	if len(sends) != 1 {
		t.Fatalf("agent.send emissions = %d, want 1", len(sends))
	}
	p, ok := sends[0].Payload.(board.SendPayload)
	if !ok {
		t.Fatalf("send payload = %T, want board.SendPayload", sends[0].Payload)
	}
	if p.WorkerID != fresh || p.TicketID != "t1" {
		t.Errorf("send payload = %+v, want ticket t1 on the new slot %q", p, fresh)
	}
	if p.Message != "Fix the parser\n\nIt drops trailing commas." {
		t.Errorf("send message = %q, want the ticket's full work order", p.Message)
	}
	// Free capacity is unchanged — one slot vacated, one taken — so nothing
	// became pullable and no pull.evaluate is owed.
	if got := len(emissionsWithTopic(ems, board.TopicPullEvaluate)); got != 0 {
		t.Errorf("pull.evaluate emissions = %d, want 0 — reassign trades one slot for another", got)
	}
	if got := len(emissionsWithTopic(ems, board.TopicBoardUpdated)); got != 1 {
		t.Errorf("board.updated emissions = %d, want 1", got)
	}
}

// Reassigning a blocked ticket restarts it: it is briefed on the new sandbox, so
// it is working again and the stale blocker goes — exactly what SendToAgent
// leaves behind, because the same thing just happened.
func TestReassignSandbox_RestartsABlockedTicket(t *testing.T) {
	svc, store := newTestService()
	workers := store.seedWorkers(projA, 2)
	old := workers[0]
	reason := "the working tree is corrupted"
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked, WorkerID: &old, BlockedReason: &reason,
	})

	got, err := svc.ReassignSandbox(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("ReassignSandbox: unexpected error: %v", err)
	}
	if got.State != board.StateWorking {
		t.Errorf("state = %q, want working — the ticket is briefed and running again", got.State)
	}
	if got.BlockedReason != nil {
		t.Errorf("blocked_reason = %q, want it cleared", *got.BlockedReason)
	}
}

// The old sandbox is being abandoned on the user's explicit instruction, so the
// save option does not hold it back any more than it does a kill.
func TestReassignSandbox_IgnoresKeepSandbox(t *testing.T) {
	svc, store := newTestService()
	workers := store.seedWorkers(projA, 2)
	old := workers[0]
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &old, KeepSandbox: true,
	})

	if _, err := svc.ReassignSandbox(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("ReassignSandbox: unexpected error: %v", err)
	}
	if got := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicAgentRelease)); got != 1 {
		t.Fatalf("agent.release emissions = %d, want 1 — an explicit reassign discards the old sandbox", got)
	}
}

// With every slot busy there is nowhere to move to. Saying so is the honest
// answer — silently re-running on the same sandbox is precisely what the user
// reached for this control to escape.
func TestReassignSandbox_NoFreeSlotIsRefused(t *testing.T) {
	svc, store := newTestService()
	workers := store.seedWorkers(projA, 2)
	first, second := workers[0], workers[1]
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &first})
	store.seedTicket(projA, board.Ticket{ID: "t2", Title: "U", State: board.StateWorking, WorkerID: &second})

	if _, err := svc.ReassignSandbox(context.Background(), projA, "t1"); !errors.Is(err, board.ErrNoFreeWorker) {
		t.Fatalf("ReassignSandbox with every slot busy = %v, want ErrNoFreeWorker", err)
	}
	if ems := store.outboxSnapshot(); len(ems) != 0 {
		t.Errorf("emissions = %+v, want none — a refused reassign rolls back whole", ems)
	}
	if stored, _ := store.ticket("t1"); stored.WorkerID == nil || *stored.WorkerID != first {
		t.Errorf("worker = %v, want the ticket still on its original slot %q", stored.WorkerID, first)
	}
}

// A slot the liveness reconciler marked errored is not somewhere to move a
// ticket to — it is the kind of slot the user is trying to get off.
func TestReassignSandbox_SkipsAnErroredFreeSlot(t *testing.T) {
	svc, store := newTestService()
	workers := store.seedWorkers(projA, 2)
	old, sick := workers[0], workers[1]
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &old})
	if err := store.SetWorkerHealth(context.Background(), projA, []string{string(sick)}); err != nil {
		t.Fatalf("SetWorkerHealth: %v", err)
	}

	if _, err := svc.ReassignSandbox(context.Background(), projA, "t1"); !errors.Is(err, board.ErrNoFreeWorker) {
		t.Fatalf("ReassignSandbox onto an errored slot = %v, want ErrNoFreeWorker", err)
	}
}

// Same precondition as the kill: with no worker bound there is nothing to move.
func TestReassignSandbox_RefusesATicketWithNoWorker(t *testing.T) {
	for _, state := range []board.State{board.StateShaping, board.StateReady, board.StateDone} {
		svc, store := newTestService()
		store.seedWorkers(projA, 1)
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: state})

		_, err := svc.ReassignSandbox(context.Background(), projA, "t1")
		requireInvalidTransition(t, err, state, "ReassignSandbox")
		if ems := store.outboxSnapshot(); len(ems) != 0 {
			t.Errorf("state %q: emissions = %+v, want none", state, ems)
		}
	}
}

func TestReassignSandbox_UnknownIsNotFound(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projB, worker)
	store.seedTicket(projB, board.Ticket{ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker})

	if _, err := svc.ReassignSandbox(context.Background(), projA, "t1"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("ReassignSandbox on another project's ticket = %v, want ErrNotFound", err)
	}
}
