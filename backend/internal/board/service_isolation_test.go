package board_test

// Project isolation (11 §3): every Service operation is scoped to the
// projectID it is called with, and a valid id belonging to another project
// behaves exactly like a missing one. One assertion per query family against
// the project-scoped fake: snapshot reads, targeted reads (GetTicket),
// targeted mutations (the LockTicket path), the pull's candidate queries
// (NextReadyTicket, FreeWorker), inserts, and outbox appends. The same
// families are proven against real SQL predicates in
// postgres/store_integration_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// seedBothProjects gives each project one shaping ticket and one worker, so
// any cross-tenant leak has something to leak.
func seedBothProjects(store *fakeStore) {
	store.seedWorker(projA, "wa")
	store.seedWorker(projB, "wb")
	store.seedTicket(projA, board.Ticket{ID: "ta", Title: "A's ticket", State: board.StateShaping})
	store.seedTicket(projB, board.Ticket{ID: "tb", Title: "B's ticket", State: board.StateShaping})
}

// Snapshot family: GetBoard(A) must see only A's tickets and count only A's
// workers.
func TestGetBoard_ScopedToProject(t *testing.T) {
	svc, store := newTestService()
	seedBothProjects(store)
	store.seedWorkers(projB, 2) // extra capacity in B must not inflate A's counts

	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard(projA): unexpected error: %v", err)
	}
	assertIDs(t, "Shaping", snap.Shaping, "ta")
	if snap.WorkerTotal != 1 || snap.WorkerFree != 1 {
		t.Errorf("WorkerTotal/Free = %d/%d, want 1/1 — B's workers must be invisible to A", snap.WorkerTotal, snap.WorkerFree)
	}
}

// Targeted-read family: a valid ticket id from another project is ErrNotFound.
func TestGetTicket_OtherProjectIsNotFound(t *testing.T) {
	svc, store := newTestService()
	seedBothProjects(store)

	if _, err := svc.GetTicket(context.Background(), projA, "tb"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("GetTicket(projA, B's id) error = %v, want ErrNotFound", err)
	}
	// Sanity: the same id resolves under its own project.
	if _, err := svc.GetTicket(context.Background(), projB, "tb"); err != nil {
		t.Fatalf("GetTicket(projB, tb): unexpected error: %v", err)
	}
}

// Targeted-mutation family (the LockTicket path): mutating B's ticket under
// A's scope is ErrNotFound and leaves B's ticket untouched.
func TestMutate_OtherProjectIsNotFound(t *testing.T) {
	svc, store := newTestService()
	seedBothProjects(store)

	if _, err := svc.MarkReady(context.Background(), projA, "tb"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("MarkReady(projA, B's id) error = %v, want ErrNotFound", err)
	}
	tb, _ := store.ticket("tb")
	if tb.State != board.StateShaping {
		t.Errorf("B's ticket state = %q, want untouched shaping", tb.State)
	}
	if len(store.outboxSnapshot()) != 0 {
		t.Error("a cross-project mutation attempt must not emit anything (03 I7)")
	}
}

// Pull family, ticket side: A's pull must never take B's ready ticket, even
// when A has free capacity and no ready work of its own.
func TestRunPull_NeverPullsOtherProjectsReadyTicket(t *testing.T) {
	svc, store := newTestService()
	store.seedWorker(projA, "wa")
	rt := store.now()
	store.seedTicket(projB, board.Ticket{ID: "tb", Title: "T", State: board.StateReady, ReadyAt: &rt})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull(projA): unexpected error: %v", err)
	}
	tb, _ := store.ticket("tb")
	if tb.State != board.StateReady {
		t.Errorf("B's ready ticket state = %q, want still ready — A's pull crossed the tenant boundary", tb.State)
	}
	if len(store.outboxSnapshot()) != 0 {
		t.Error("RunPull(projA) with no A-side work must emit nothing")
	}
}

// Pull family, worker side: A's ready ticket must not bind B's free worker.
func TestRunPull_NeverBindsOtherProjectsWorker(t *testing.T) {
	svc, store := newTestService()
	store.seedWorker(projB, "wb")
	rt := store.now()
	store.seedTicket(projA, board.Ticket{ID: "ta", Title: "T", State: board.StateReady, ReadyAt: &rt})

	if err := svc.RunPull(context.Background(), projA); err != nil {
		t.Fatalf("RunPull(projA): unexpected error: %v", err)
	}
	ta, _ := store.ticket("ta")
	if ta.State != board.StateReady {
		t.Errorf("A's ticket state = %q, want still ready — only B has a free worker and it must be invisible to A", ta.State)
	}
}

// FreeWorker family via SeedTicket: an active seed in A must not bind B's
// only free worker.
func TestSeedTicket_ActiveSeedIgnoresOtherProjectsWorkers(t *testing.T) {
	svc, store := newTestService()
	store.seedWorker(projB, "wb")

	_, err := svc.SeedTicket(context.Background(), projA, board.SeedSpec{
		Title: "B has capacity, A does not", State: board.StateBlocked,
	})
	if !errors.Is(err, board.ErrNoFreeWorker) {
		t.Fatalf("SeedTicket(projA, blocked) error = %v, want ErrNoFreeWorker — B's worker must be invisible", err)
	}
}

// Insert + outbox families: a ticket created under A is invisible to B's
// board, and every emission the operation appended is recorded under A.
func TestCreateTicket_InvisibleToOtherProjectAndOutboxScoped(t *testing.T) {
	svc, store := newTestService()

	created, err := svc.CreateTicket(context.Background(), projA, "A only", "")
	if err != nil {
		t.Fatalf("CreateTicket(projA): unexpected error: %v", err)
	}
	if _, getErr := svc.GetTicket(context.Background(), projB, created.ID); !errors.Is(getErr, board.ErrNotFound) {
		t.Fatalf("GetTicket(projB, A's id) error = %v, want ErrNotFound", getErr)
	}
	snapB, err := svc.GetBoard(context.Background(), projB)
	if err != nil {
		t.Fatalf("GetBoard(projB): unexpected error: %v", err)
	}
	if len(snapB.Shaping) != 0 {
		t.Errorf("B's Shaping = %v, want empty — A's ticket leaked", snapB.Shaping)
	}

	entries := store.outboxEntries()
	if len(entries) == 0 {
		t.Fatal("CreateTicket must append outbox emissions")
	}
	for _, e := range entries {
		if e.Project != projA {
			t.Errorf("emission %q recorded under project %q, want %q", e.E.Topic, e.Project, projA)
		}
	}
}
