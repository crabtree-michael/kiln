package board_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// GetTicket returns a live ticket by id (backs the brain's get_ticket tool,
// 06 §4 amended).
func TestGetTicket_ReturnsLiveTicket(t *testing.T) {
	store := newFakeStore()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "hello", Body: "the body text", State: board.StateShaping})
	svc := board.NewService(store)

	got, err := svc.GetTicket(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("GetTicket: unexpected error: %v", err)
	}
	if got.ID != "t1" || got.Title != "hello" || got.Body != "the body text" {
		t.Fatalf("GetTicket returned %+v, want id=t1 title=hello body='the body text'", got)
	}
}

// An unknown id is ErrNotFound.
func TestGetTicket_UnknownIsNotFound(t *testing.T) {
	svc := board.NewService(newFakeStore())
	if _, err := svc.GetTicket(context.Background(), projA, "nope"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("GetTicket(unknown) error = %v, want ErrNotFound", err)
	}
}

// ArchiveTicket soft-deletes a shaping ticket: it vanishes from GetTicket and
// the snapshot, and stamps ArchivedAt.
func TestArchiveTicket_ShapingVanishesFromReads(t *testing.T) {
	store := newFakeStore()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "mistake", State: board.StateShaping})
	svc := board.NewService(store)

	got, err := svc.ArchiveTicket(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("ArchiveTicket: unexpected error: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("ArchiveTicket returned ticket with nil ArchivedAt: %+v", got)
	}

	if _, getErr := svc.GetTicket(context.Background(), projA, "t1"); !errors.Is(getErr, board.ErrNotFound) {
		t.Fatalf("after archive, GetTicket error = %v, want ErrNotFound", getErr)
	}
	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(snap.Shaping) != 0 {
		t.Fatalf("after archive, snapshot Shaping = %d tickets, want 0", len(snap.Shaping))
	}
}

// ArchiveTicket emits board.updated and feed.updated (a proposal card may
// disappear).
func TestArchiveTicket_EmitsBoardAndFeedUpdated(t *testing.T) {
	store := newFakeStore()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "mistake", State: board.StateShaping})
	svc := board.NewService(store)

	if _, err := svc.ArchiveTicket(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("ArchiveTicket: %v", err)
	}
	ems := store.outboxSnapshot()
	if got := len(emissionsWithTopic(ems, board.TopicBoardUpdated)); got != 1 {
		t.Errorf("board.updated emissions = %d, want 1", got)
	}
	if got := len(emissionsWithTopic(ems, board.TopicFeedUpdated)); got != 1 {
		t.Errorf("feed.updated emissions = %d, want 1", got)
	}
}

// ArchiveTicket is allowed from ready and done as well as shaping.
func TestArchiveTicket_AllowedFromReadyAndDone(t *testing.T) {
	for _, st := range []board.State{board.StateReady, board.StateDone} {
		store := newFakeStore()
		store.seedTicket(projA, board.Ticket{ID: "t1", Title: "x", State: st})
		svc := board.NewService(store)
		if _, err := svc.ArchiveTicket(context.Background(), projA, "t1"); err != nil {
			t.Errorf("ArchiveTicket from %q: unexpected error: %v", st, err)
		}
	}
}

// Archiving a *working* ticket is refused — it has a live agent mid-turn, so it
// must be resolved before it can be removed rather than have archive kill
// in-flight work. (A blocked ticket, by contrast, is deletable — see below.)
func TestArchiveTicket_WorkingIsRefused(t *testing.T) {
	store := newFakeStore()
	store.seedWorker(projA, "w1")
	wid := board.WorkerID("w1")
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "x", State: board.StateWorking, WorkerID: &wid})
	svc := board.NewService(store)

	_, err := svc.ArchiveTicket(context.Background(), projA, "t1")
	requireInvalidTransition(t, err, board.StateWorking, "ArchiveTicket")
}

// Archiving a *blocked* ticket is allowed and releases the worker it holds: the
// ticket vanishes from reads, its WorkerID is cleared, and it emits agent.release
// (tear the sandbox down) + pull.evaluate (backfill the freed slot) on top of the
// usual board.updated + feed.updated. This is the delete-a-stuck-duplicate path
// (2026-07-11-delete-blocked-ticket-design.md).
func TestArchiveTicket_BlockedReleasesWorker(t *testing.T) {
	store := newFakeStore()
	store.seedWorker(projA, "w1")
	wid := board.WorkerID("w1")
	reason := "duplicate of t0"
	store.seedTicket(projA, board.Ticket{
		ID: "t1", Title: "dupe", State: board.StateBlocked, WorkerID: &wid, BlockedReason: &reason,
	})
	svc := board.NewService(store)

	got, err := svc.ArchiveTicket(context.Background(), projA, "t1")
	if err != nil {
		t.Fatalf("ArchiveTicket(blocked): unexpected error: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("archived ticket has nil ArchivedAt: %+v", got)
	}
	if got.WorkerID != nil {
		t.Fatalf("archived blocked ticket still bound to worker %v, want nil", *got.WorkerID)
	}

	// Vanishes from targeted reads and the snapshot.
	if _, getErr := svc.GetTicket(context.Background(), projA, "t1"); !errors.Is(getErr, board.ErrNotFound) {
		t.Fatalf("after archive, GetTicket error = %v, want ErrNotFound", getErr)
	}
	snap, err := svc.GetBoard(context.Background(), projA)
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(snap.Blocked) != 0 {
		t.Fatalf("after archive, snapshot Blocked = %d tickets, want 0", len(snap.Blocked))
	}

	// Emits the worker release (with the freed worker id) + a pull re-evaluation,
	// alongside the universal board.updated and the card-retracting feed.updated.
	ems := store.outboxSnapshot()
	releases := emissionsWithTopic(ems, board.TopicAgentRelease)
	if len(releases) != 1 {
		t.Fatalf("agent.release emissions = %d, want 1", len(releases))
	}
	payload, ok := releases[0].Payload.(board.ReleasePayload)
	if !ok {
		t.Fatalf("agent.release payload type = %T, want board.ReleasePayload", releases[0].Payload)
	}
	if payload.WorkerID != wid {
		t.Fatalf("agent.release worker = %q, want %q", payload.WorkerID, wid)
	}
	if got := len(emissionsWithTopic(ems, board.TopicPullEvaluate)); got != 1 {
		t.Errorf("pull.evaluate emissions = %d, want 1", got)
	}
	if got := len(emissionsWithTopic(ems, board.TopicBoardUpdated)); got != 1 {
		t.Errorf("board.updated emissions = %d, want 1", got)
	}
	if got := len(emissionsWithTopic(ems, board.TopicFeedUpdated)); got != 1 {
		t.Errorf("feed.updated emissions = %d, want 1", got)
	}
}

// Archiving an unknown or already-archived ticket is ErrNotFound (archived
// tickets are invisible to targeted operations).
func TestArchiveTicket_UnknownOrArchivedIsNotFound(t *testing.T) {
	svc := board.NewService(newFakeStore())
	if _, err := svc.ArchiveTicket(context.Background(), projA, "nope"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("ArchiveTicket(unknown) error = %v, want ErrNotFound", err)
	}

	store := newFakeStore()
	store.seedTicket(projA, board.Ticket{ID: "t1", Title: "x", State: board.StateShaping})
	svc = board.NewService(store)
	if _, err := svc.ArchiveTicket(context.Background(), projA, "t1"); err != nil {
		t.Fatalf("first ArchiveTicket: %v", err)
	}
	if _, err := svc.ArchiveTicket(context.Background(), projA, "t1"); !errors.Is(err, board.ErrNotFound) {
		t.Fatalf("re-archive error = %v, want ErrNotFound", err)
	}
}
