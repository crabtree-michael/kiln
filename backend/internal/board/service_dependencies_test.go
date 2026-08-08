package board_test

// Ticket dependencies (0013): "this ticket cannot start until those are done".
//
// The whole mechanism lives in the pull — a Ready ticket with an unmet
// dependency is skipped, keeping its place in the queue without holding a
// worker — plus the two edge operations and the cycle refusal that keeps the
// graph satisfiable. The cases below are grouped as: what the pull does, what
// makes a dependency stop counting (done, removed, archived), and what the
// board refuses to record.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// Ticket ids and titles reused across the cases below.
const (
	idBlocker      = "blocker"
	titleAbandoned = "Abandoned"
	idDoomed       = "doomed"
)

// seedReady plants a ready ticket with a ready_at stamp, the shape the pull
// orders on.
func seedReady(store *fakeStore, id board.TicketID, title string) {
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: id, Title: title, State: board.StateReady, ReadyAt: &rt})
}

// dependsOn wires id -> dependency through the real Board API, failing the test
// if the edge is refused.
func dependsOn(t *testing.T, svc *board.Service, id, dependency board.TicketID) board.Ticket {
	t.Helper()
	tk, err := svc.AddDependency(context.Background(), projA, id, dependency)
	if err != nil {
		t.Fatalf("AddDependency(%s -> %s): unexpected error: %v", id, dependency, err)
	}
	return tk
}

func requireCircular(t *testing.T, err error, wantTicket, wantDependsOn board.TicketID) []board.TicketID {
	t.Helper()
	if err == nil {
		t.Fatalf("expected ErrCircularDependency for %s -> %s, got nil error", wantTicket, wantDependsOn)
	}
	var cyc *board.ErrCircularDependency
	if !errors.As(err, &cyc) {
		t.Fatalf("expected *board.ErrCircularDependency, got %T: %v", err, err)
	}
	if cyc.Ticket != wantTicket || cyc.DependsOn != wantDependsOn {
		t.Errorf("ErrCircularDependency = {%s -> %s}, want {%s -> %s}",
			cyc.Ticket, cyc.DependsOn, wantTicket, wantDependsOn)
	}
	return cyc.Path
}

// ---- the pull skips what is still waiting ---------------------------------

// The headline: a ready ticket with an unmet dependency does not pull, and it
// does not consume the free worker either — the ticket behind it does.
func TestPull_SkipsTicketWaitingOnDependency(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)
	// Seed order is pull order (ready_at ASC), so the waiter is FIRST in the
	// queue and the single worker is exactly what it would claim if the
	// dependency were ignored. That is what makes the skip load-bearing here
	// rather than something the ordering would have produced anyway.
	seedReady(store, "waiter", "Use the new column")
	seedReady(store, idBlocker, "Land the migration")
	dependsOn(t, svc, "waiter", idBlocker)

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}

	waiter, _ := store.ticket("waiter")
	if waiter.State != board.StateReady {
		t.Errorf("waiting ticket state = %q, want it held at ready by its unmet dependency", waiter.State)
	}
	if waiter.WorkerID != nil {
		t.Error("a ticket waiting on a dependency must not hold a worker")
	}
	// The single slot went to the next pullable ticket, not to nothing: a
	// skipped ticket must not stall the queue behind it.
	blocker, _ := store.ticket(idBlocker)
	if blocker.State != board.StateWorking {
		t.Errorf("blocker state = %q, want working — the pull should move past the waiting ticket", blocker.State)
	}
}

// A dependency that is already done never held anything back.
func TestPull_DependencyAlreadyDoneDoesNotBlock(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)
	store.seedTicket(projA, board.Ticket{ID: "done", Title: "Shipped", State: board.StateDone})
	seedReady(store, "waiter", "Builds on it")
	dependsOn(t, svc, "waiter", "done")

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	waiter, _ := store.ticket("waiter")
	if waiter.State != board.StateWorking {
		t.Errorf("waiter state = %q, want working — its only dependency is already done", waiter.State)
	}
}

// Every dependency must land, not just one.
func TestPull_WaitsForEveryDependency(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 3)
	store.seedTicket(projA, board.Ticket{ID: "d1", Title: "First", State: board.StateDone})
	seedReady(store, "d2", "Second")
	seedReady(store, "waiter", "Needs both")
	dependsOn(t, svc, "waiter", "d1")
	dependsOn(t, svc, "waiter", "d2")

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull: unexpected error: %v", err)
	}
	waiter, _ := store.ticket("waiter")
	if waiter.State != board.StateReady {
		t.Errorf("waiter state = %q, want ready — d2 is not done yet", waiter.State)
	}
}

// The far end of the mechanism: finishing the last dependency releases the
// waiter on the very next pull, and AcceptToDone's own pull.evaluate is what
// triggers it — no separate wake-up is needed.
func TestAcceptToDone_ReleasesWaitingTicketOnNextPull(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	workers := store.seedWorkers(projA, 1)
	store.seedTicket(projA, board.Ticket{
		ID: idBlocker, Title: "Land it", State: board.StateWorking, WorkerID: &workers[0],
	})
	seedReady(store, "waiter", "Use it")
	dependsOn(t, svc, "waiter", idBlocker)

	// Nothing pullable yet: the only slot is busy and the waiter is held.
	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull before completion: %v", err)
	}
	if w, _ := store.ticket("waiter"); w.State != board.StateReady {
		t.Fatalf("waiter state = %q before its dependency landed, want ready", w.State)
	}

	if _, err := svc.AcceptToDone(context.Background(), projA, idBlocker, board.CompletionLink{}, ""); err != nil {
		t.Fatalf("AcceptToDone: %v", err)
	}
	if got := emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate); len(got) == 0 {
		t.Fatal("AcceptToDone must emit pull.evaluate — it is what re-runs the pull for the released ticket")
	}
	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull after completion: %v", err)
	}
	waiter, _ := store.ticket("waiter")
	if waiter.State != board.StateWorking {
		t.Errorf("waiter state = %q, want working once its dependency was accepted", waiter.State)
	}
}

// ---- deleted and removed dependencies -------------------------------------

// Deleting the ticket someone is waiting on must not strand them: an archived
// dependency can never reach done, so it stops counting.
func TestArchivedDependencyStopsBlockingThePull(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)
	store.seedTicket(projA, board.Ticket{ID: idDoomed, Title: titleAbandoned, State: board.StateShaping})
	seedReady(store, "waiter", "Was waiting on it")
	dependsOn(t, svc, "waiter", idDoomed)

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull while the dependency lived: %v", err)
	}
	if w, _ := store.ticket("waiter"); w.State != board.StateReady {
		t.Fatalf("waiter state = %q, want ready while its dependency still existed", w.State)
	}

	if _, err := svc.ArchiveTicket(context.Background(), projA, idDoomed); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull after archiving the dependency: %v", err)
	}
	waiter, _ := store.ticket("waiter")
	if waiter.State != board.StateWorking {
		t.Errorf("waiter state = %q, want working — an archived dependency can never be met, "+
			"so waiting on it forever is the one outcome that cannot be right", waiter.State)
	}
}

// Archiving a ticket others wait on changes the pullable set without any ticket
// changing state, so it owes the pull a nudge or the release waits for an
// unrelated trigger.
func TestArchiveTicket_NudgesThePullWhenItHadDependents(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedTicket(projA, board.Ticket{ID: idDoomed, Title: titleAbandoned, State: board.StateShaping})
	seedReady(store, "waiter", "Waiting")
	dependsOn(t, svc, "waiter", idDoomed)

	before := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate))
	if _, err := svc.ArchiveTicket(context.Background(), projA, idDoomed); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	after := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate))
	if after != before+1 {
		t.Errorf("pull.evaluate emissions = %d, want %d — archiving a ticket with dependents "+
			"frees them and nothing else would notice", after, before+1)
	}
}

// The ordinary delete is unchanged: no dependents, no extra emission.
func TestArchiveTicket_NoPullNudgeWithoutDependents(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedTicket(projA, board.Ticket{ID: "lonely", Title: "Nobody waits", State: board.StateShaping})

	if _, err := svc.ArchiveTicket(context.Background(), projA, "lonely"); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	if got := emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate); len(got) != 0 {
		t.Errorf("pull.evaluate emissions = %d, want 0 for a delete that freed nobody", len(got))
	}
}

// An archived dependency also disappears from the ticket's list — archived
// tickets are gone from every read path (03 §4 amended).
func TestArchivedDependencyDropsOutOfTheList(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedTicket(projA, board.Ticket{ID: idDoomed, Title: titleAbandoned, State: board.StateShaping})
	seedReady(store, "waiter", "Waiting")
	dependsOn(t, svc, "waiter", idDoomed)

	if _, err := svc.ArchiveTicket(context.Background(), projA, idDoomed); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	waiter, err := svc.GetTicket(context.Background(), projA, "waiter")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if len(waiter.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, want empty — the dependency was archived", waiter.DependsOn)
	}
	if waiter.UnmetDependencies != 0 {
		t.Errorf("UnmetDependencies = %d, want 0", waiter.UnmetDependencies)
	}
	if waiter.WaitingOnDependencies() {
		t.Error("a ticket whose only dependency was archived is not waiting on anything")
	}
}

func TestRemoveDependency_UnblocksAndNudgesThePull(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedWorkers(projA, 1)
	seedReady(store, idBlocker, "Not done")
	seedReady(store, "waiter", "Waiting")
	dependsOn(t, svc, "waiter", idBlocker)

	before := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate))
	updated, err := svc.RemoveDependency(context.Background(), projA, "waiter", idBlocker)
	if err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	if len(updated.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, want empty after removal", updated.DependsOn)
	}
	after := len(emissionsWithTopic(store.outboxSnapshot(), board.TopicPullEvaluate))
	if after != before+1 {
		t.Errorf("pull.evaluate emissions = %d, want %d — removing the last dependency can make "+
			"a queued ticket pullable immediately", after, before+1)
	}
}

// Removing an edge that was never there is the caller getting what they asked
// for, not an error — it makes "clear this dependency" safe to call blind.
func TestRemoveDependency_AbsentEdgeSucceeds(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "a", "A")
	seedReady(store, "b", "B")

	if _, err := svc.RemoveDependency(context.Background(), projA, "a", "b"); err != nil {
		t.Errorf("RemoveDependency for an edge that does not exist: %v, want success", err)
	}
}

// ---- what the board refuses -----------------------------------------------

func TestAddDependency_RefusesSelfDependency(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "t1", "Only ticket")

	_, err := svc.AddDependency(context.Background(), projA, "t1", "t1")
	path := requireCircular(t, err, "t1", "t1")
	if len(path) != 0 {
		t.Errorf("Path = %v, want empty for the degenerate self-edge", path)
	}
}

func TestAddDependency_RefusesDirectCycle(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "a", "A")
	seedReady(store, "b", "B")
	dependsOn(t, svc, "a", "b") // a waits for b

	// b waiting for a would close the ring: neither could ever start.
	_, err := svc.AddDependency(context.Background(), projA, "b", "a")
	requireCircular(t, err, "b", "a")

	// And the refusal wrote nothing (03 I7).
	bt, gerr := svc.GetTicket(context.Background(), projA, "b")
	if gerr != nil {
		t.Fatalf("GetTicket: %v", gerr)
	}
	if len(bt.DependsOn) != 0 {
		t.Errorf("DependsOn = %v, want empty — a refused edge must not be recorded", bt.DependsOn)
	}
}

func TestAddDependency_RefusesTransitiveCycleAndNamesThePath(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "a", "A")
	seedReady(store, "b", "B")
	seedReady(store, "c", "C")
	dependsOn(t, svc, "a", "b") // a waits for b
	dependsOn(t, svc, "b", "c") // b waits for c

	// c waiting for a closes the three-ticket ring.
	_, err := svc.AddDependency(context.Background(), projA, "c", "a")
	path := requireCircular(t, err, "c", "a")
	// The path runs from the proposed dependency back to the ticket: a -> b -> c.
	want := []board.TicketID{"a", "b", "c"}
	if !slices.Equal(path, want) {
		t.Fatalf("Path = %v, want %v", path, want)
	}
}

// A ring that only closes through an archived ticket is not a real cycle: the
// dead edge can never be met, so refusing the new one would block a legitimate
// dependency on the strength of a ticket that no longer exists.
func TestAddDependency_ArchivedTicketDoesNotCloseACycle(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "a", "A")
	seedReady(store, "b", "B")
	store.seedTicket(projA, board.Ticket{ID: "gone", Title: titleAbandoned, State: board.StateShaping})
	dependsOn(t, svc, "a", "gone") // a waits for gone
	dependsOn(t, svc, "gone", "b") // gone waits for b  => b -> a would close a ring
	if _, aerr := svc.ArchiveTicket(context.Background(), projA, "gone"); aerr != nil {
		t.Fatalf("ArchiveTicket: %v", aerr)
	}

	if _, err := svc.AddDependency(context.Background(), projA, "b", "a"); err != nil {
		t.Errorf("AddDependency across an archived link: %v, want success — the ring only "+
			"closes through a ticket that no longer exists", err)
	}
}

func TestAddDependency_UnknownDependencyIsNotFound(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "t1", "Real")

	if _, err := svc.AddDependency(context.Background(), projA, "t1", "ghost"); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("AddDependency on a missing dependency: %v, want ErrNotFound", err)
	}
}

func TestAddDependency_UnknownTicketIsNotFound(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "t1", "Real")

	if _, err := svc.AddDependency(context.Background(), projA, "ghost", "t1"); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("AddDependency on a missing ticket: %v, want ErrNotFound", err)
	}
}

// The tenant boundary holds for edges too (11 §3): another project's ticket is
// simply not there.
func TestAddDependency_CannotDependOnAnotherProjectsTicket(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "mine", "Mine")
	store.seedTicket(projB, board.Ticket{ID: "theirs", Title: "Theirs", State: board.StateReady})

	if _, err := svc.AddDependency(context.Background(), projA, "mine", "theirs"); !errors.Is(err, board.ErrNotFound) {
		t.Errorf("AddDependency across projects: %v, want ErrNotFound", err)
	}
}

func TestAddDependency_IsIdempotent(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "a", "A")
	seedReady(store, "b", "B")

	dependsOn(t, svc, "a", "b")
	again := dependsOn(t, svc, "a", "b")
	if len(again.DependsOn) != 1 {
		t.Errorf("DependsOn = %v, want exactly one edge after adding the same one twice", again.DependsOn)
	}
}

// ---- what the board reports -----------------------------------------------

func TestAddDependency_ReturnsTheRefreshedListAndWaitingState(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "waiter", "Waiting")
	seedReady(store, idBlocker, "Blocker")

	updated := dependsOn(t, svc, "waiter", idBlocker)
	if len(updated.DependsOn) != 1 || updated.DependsOn[0] != idBlocker {
		t.Errorf("DependsOn = %v, want [blocker]", updated.DependsOn)
	}
	if updated.UnmetDependencies != 1 {
		t.Errorf("UnmetDependencies = %d, want 1", updated.UnmetDependencies)
	}
	if !updated.WaitingOnDependencies() {
		t.Error("a ready ticket with an unmet dependency is waiting")
	}
}

// Waiting is a property of a *queued* ticket. A shaping ticket is not waiting
// on anything yet, and one already working is past the point its dependencies
// could hold it — so neither renders the waiting affordance.
func TestWaitingOnDependencies_OnlyAppliesToReadyTickets(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	store.seedTicket(projA, board.Ticket{ID: "shaping", Title: "Shaping", State: board.StateShaping})
	store.seedTicket(projA, board.Ticket{ID: idBlocker, Title: "Blocker", State: board.StateShaping})
	dependsOn(t, svc, "shaping", idBlocker)

	tk, err := svc.GetTicket(context.Background(), projA, "shaping")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if tk.UnmetDependencies != 1 {
		t.Errorf("UnmetDependencies = %d, want 1 — the edge is still recorded", tk.UnmetDependencies)
	}
	if tk.WaitingOnDependencies() {
		t.Error("a shaping ticket is not queued, so it is not waiting on its dependencies")
	}
}

// The board snapshot carries dependencies, which is what the client renders.
func TestGetBoard_CarriesDependencies(t *testing.T) {
	store := newFakeStore()
	svc := board.NewService(store)
	seedReady(store, "waiter", "Waiting")
	seedReady(store, idBlocker, "Blocker")
	dependsOn(t, svc, "waiter", idBlocker)

	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	var found bool
	for _, tk := range snap.Ready {
		if tk.ID != "waiter" {
			continue
		}
		found = true
		if len(tk.DependsOn) != 1 || tk.DependsOn[0] != idBlocker {
			t.Errorf("DependsOn = %v, want [blocker]", tk.DependsOn)
		}
		if !tk.WaitingOnDependencies() {
			t.Error("snapshot must carry enough for the client to render the waiting state")
		}
	}
	if !found {
		t.Fatal("waiter missing from the Ready group")
	}
}
