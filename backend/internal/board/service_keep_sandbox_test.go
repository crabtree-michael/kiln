// The per-ticket sandbox option (KeepSandbox): the user's "save this ticket's
// sandbox" choice, set from the ticket detail sheet. Its whole mechanical effect
// is negative — the agent.release a ticket owes when it gives up its worker is
// NOT emitted — so these tests pin both halves: the flag round-trips through
// SetKeepSandbox, and each exit from Developing (AcceptToDone, ArchiveTicket on
// a blocked ticket) drops the release while every other emission stays.
package board_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// SetKeepSandbox records the option and emits only the universal board.updated —
// it is a setting, not a transition, so nothing else fires and the state is
// untouched.
func TestSetKeepSandbox_RecordsOptionAndEmitsOnlyBoardUpdated(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateShaping})

	got, err := svc.SetKeepSandbox(context.Background(), projA, "t1", true)
	if err != nil {
		t.Fatalf("SetKeepSandbox: unexpected error: %v", err)
	}
	if !got.KeepSandbox {
		t.Error("SetKeepSandbox(true) must set KeepSandbox on the returned ticket")
	}
	if got.State != board.StateShaping {
		t.Errorf("state = %q, want shaping — the option is not a transition", got.State)
	}
	if stored, _ := store.ticket("t1"); !stored.KeepSandbox {
		t.Error("SetKeepSandbox(true) must persist KeepSandbox")
	}

	ems := store.outboxSnapshot()
	if got := len(emissionsWithTopic(ems, board.TopicBoardUpdated)); got != 1 {
		t.Errorf("board.updated emissions = %d, want 1", got)
	}
	if len(ems) != 1 {
		t.Errorf("emissions = %+v, want board.updated alone", ems)
	}
}

// The option is a plain toggle: setting it back to false restores the default
// recycle.
func TestSetKeepSandbox_FalseClearsTheOption(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "T", State: board.StateShaping, KeepSandbox: true})

	got, err := svc.SetKeepSandbox(context.Background(), projA, "t1", false)
	if err != nil {
		t.Fatalf("SetKeepSandbox: unexpected error: %v", err)
	}
	if got.KeepSandbox {
		t.Error("SetKeepSandbox(false) must clear KeepSandbox")
	}
}

// An unknown id — including one owned by another project — is ErrNotFound, so
// the route can answer 404 without leaking the other tenant's ticket.
func TestSetKeepSandbox_UnknownIsNotFound(t *testing.T) {
	svc, store := newTestService()
	store.seedTicket(projB, board.Ticket{ID: "t1", Title: "T", State: board.StateShaping})

	if _, err := svc.SetKeepSandbox(context.Background(), projA, "t1", true); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("SetKeepSandbox on another project's ticket = %v, want ErrNotFound", err)
	}
}

// The point of the option: accepting a ticket whose sandbox is saved emits NO
// agent.release, so the worker is never destroyed-and-recreated and the workspace
// survives for the next turn on that slot. Everything else about the transition
// is unchanged — the binding still clears and the slot is still offered to the
// pull.
func TestAcceptToDone_KeepSandboxSuppressesRelease(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateWorking, WorkerID: &worker, KeepSandbox: true,
	})

	got, err := svc.AcceptToDone(context.Background(), projA, "t1", board.CompletionLink{}, "")
	if err != nil {
		t.Fatalf("AcceptToDone: unexpected error: %v", err)
	}
	if got.State != board.StateDone || got.WorkerID != nil {
		t.Errorf("ticket = {state:%q worker:%v}, want done with no worker", got.State, got.WorkerID)
	}
	if !got.KeepSandbox {
		t.Error("AcceptToDone must not clear the ticket's sandbox option")
	}

	ems := store.outboxSnapshot()
	if got := len(emissionsWithTopic(ems, board.TopicAgentRelease)); got != 0 {
		t.Fatalf("agent.release emissions = %d, want 0 — a saved sandbox is never recycled", got)
	}
	// The rest of the transition is untouched.
	for _, topic := range []board.Topic{
		board.TopicPullEvaluate, board.TopicFeedUpdated, board.TopicActivityToast,
		board.TopicFeedCompletion, board.TopicNotifySend, board.TopicBoardUpdated,
	} {
		if got := len(emissionsWithTopic(ems, topic)); got != 1 {
			t.Errorf("%s emissions = %d, want 1", topic, got)
		}
	}
}

// The option holds on the other exit from Developing too: deleting a blocked
// ticket frees the slot but leaves its sandbox alone.
func TestArchiveTicket_KeepSandboxSuppressesRelease(t *testing.T) {
	svc, store := newTestService()
	worker := board.WorkerID("w1")
	reason := "needs a decision"
	store.seedWorker(projA, worker)
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "T", State: board.StateBlocked, WorkerID: &worker,
		BlockedReason: &reason, KeepSandbox: true,
	})

	got, err := svc.ArchiveTicket(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("ArchiveTicket: unexpected error: %v", err)
	}
	if got.WorkerID != nil {
		t.Errorf("archived ticket still bound to worker %v, want nil", *got.WorkerID)
	}

	ems := store.outboxSnapshot()
	if got := len(emissionsWithTopic(ems, board.TopicAgentRelease)); got != 0 {
		t.Fatalf("agent.release emissions = %d, want 0 — a saved sandbox is never recycled", got)
	}
	// The freed slot is still offered to the pull, and the card still leaves.
	for _, topic := range []board.Topic{
		board.TopicPullEvaluate, board.TopicFeedUpdated, board.TopicBoardUpdated,
	} {
		if got := len(emissionsWithTopic(ems, topic)); got != 1 {
			t.Errorf("%s emissions = %d, want 1", topic, got)
		}
	}
}
