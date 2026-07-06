package steward

import (
	"context"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// fakeBoard records the sweep's board actions and serves a fixed Working set.
type fakeBoard struct {
	working []WorkingTicket
	poked   []string
	blocked map[string]string // ticket_id -> reason
}

func (b *fakeBoard) WorkingTickets(context.Context) ([]WorkingTicket, error) { return b.working, nil }
func (b *fakeBoard) Poke(_ context.Context, ticketID string) error {
	b.poked = append(b.poked, ticketID)
	return nil
}

func (b *fakeBoard) Block(_ context.Context, ticketID, reason string) error {
	if b.blocked == nil {
		b.blocked = map[string]string{}
	}
	b.blocked[ticketID] = reason
	return nil
}

// fakeAgents serves a fixed status map.
type fakeAgents struct{ states map[string]AgentState }

func (a *fakeAgents) States(context.Context) (map[string]AgentState, error) { return a.states, nil }

// fakeFeed records posted poke cards.
type fakeFeed struct{ posted []string }

func (f *fakeFeed) PostPoke(_ context.Context, ticketID string) error {
	f.posted = append(f.posted, ticketID)
	return nil
}

// fakeStore is an in-memory steward.Store recording upserts and deletes.
type fakeStore struct {
	records map[string]PokeRecord
	deleted []string
}

func newFakeStore(recs ...PokeRecord) *fakeStore {
	m := map[string]PokeRecord{}
	for _, r := range recs {
		m[r.TicketID] = r
	}
	return &fakeStore{records: m}
}

func (s *fakeStore) List(context.Context) ([]PokeRecord, error) {
	out := make([]PokeRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeStore) Upsert(_ context.Context, ticketID, workerID string, pokedAt time.Time) error {
	s.records[ticketID] = PokeRecord{TicketID: ticketID, WorkerID: workerID, PokedAt: pokedAt}
	return nil
}

func (s *fakeStore) Delete(_ context.Context, ticketID string) error {
	delete(s.records, ticketID)
	s.deleted = append(s.deleted, ticketID)
	return nil
}

const stall = 5 * time.Minute

// harness wires a Service over the fakes at a fixed "now".
type harness struct {
	svc   *Service
	board *fakeBoard
	feed  *fakeFeed
	store *fakeStore
	now   time.Time
}

func newHarness(working []WorkingTicket, states map[string]AgentState, recs ...PokeRecord) *harness {
	clock := testutil.NewFakeClock()
	board := &fakeBoard{working: working}
	feed := &fakeFeed{}
	store := newFakeStore(recs...)
	svc := NewService(board, &fakeAgents{states: states}, feed, store, clock, Config{Stall: stall, Interval: time.Minute})
	return &harness{svc: svc, board: board, feed: feed, store: store, now: clock.Now()}
}

func TestSweep_PokesIdleAgentPastThreshold(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusIdle, UpdatedAt: fixedNow().Add(-stall - time.Second)}},
	)
	h.svc.sweep(context.Background())

	if got := h.board.poked; len(got) != 1 || got[0] != "t1" {
		t.Fatalf("expected t1 poked, got %v", got)
	}
	if got := h.feed.posted; len(got) != 1 || got[0] != "t1" {
		t.Fatalf("expected t1 feed poke, got %v", got)
	}
	if _, ok := h.store.records["t1"]; !ok {
		t.Fatalf("expected poke recorded for t1")
	}
	if len(h.board.blocked) != 0 {
		t.Fatalf("did not expect any block, got %v", h.board.blocked)
	}
}

func TestSweep_LeavesFreshIdleAlone(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusIdle, UpdatedAt: fixedNow().Add(-stall + time.Minute)}},
	)
	h.svc.sweep(context.Background())
	if len(h.board.poked) != 0 {
		t.Fatalf("did not expect a poke before threshold, got %v", h.board.poked)
	}
}

func TestSweep_NeverPokesBuildingOrStarting(t *testing.T) {
	for _, status := range []string{statusBuilding, statusStarting} {
		h := newHarness(
			[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
			// UpdatedAt long in the past — status must dominate.
			map[string]AgentState{"w1": {Status: status, UpdatedAt: fixedNow().Add(-time.Hour)}},
		)
		h.svc.sweep(context.Background())
		if len(h.board.poked) != 0 || len(h.board.blocked) != 0 {
			t.Fatalf("status %q: expected no action, poked=%v blocked=%v", status, h.board.poked, h.board.blocked)
		}
	}
}

func TestSweep_SkipsZeroUpdatedAtWhenNeverPoked(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusIdle}}, // zero UpdatedAt
	)
	h.svc.sweep(context.Background())
	if len(h.board.poked) != 0 {
		t.Fatalf("expected no poke for zero-baseline agent, got %v", h.board.poked)
	}
}

func TestSweep_OnlyOnePokePerEpisode(t *testing.T) {
	// Already poked recently; still idle but within the post-poke grace.
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusIdle, UpdatedAt: fixedNow().Add(-time.Hour)}},
		PokeRecord{TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-stall + time.Minute)},
	)
	h.svc.sweep(context.Background())
	if len(h.board.poked) != 0 {
		t.Fatalf("expected no second poke, got %v", h.board.poked)
	}
	if len(h.board.blocked) != 0 {
		t.Fatalf("expected no block within grace, got %v", h.board.blocked)
	}
}

func TestSweep_BlocksOnStallAgainAfterPoke(t *testing.T) {
	// Poked a full threshold ago and still idle — escalate.
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusStopped, UpdatedAt: fixedNow().Add(-time.Hour)}},
		PokeRecord{TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-stall - time.Second)},
	)
	h.svc.sweep(context.Background())
	if got := h.board.blocked["t1"]; got != reasonStalledTwice {
		t.Fatalf("expected stalled-twice block, got %q", got)
	}
	if _, ok := h.store.records["t1"]; ok {
		t.Fatalf("expected poke record cleared after block")
	}
}

func TestSweep_BlocksOnErrorAfterPoke(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusErrored, UpdatedAt: fixedNow()}},
		PokeRecord{TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-time.Minute)},
	)
	h.svc.sweep(context.Background())
	if got := h.board.blocked["t1"]; got != reasonErroredAfterPoke {
		t.Fatalf("expected errored-after-poke block, got %q", got)
	}
}

func TestSweep_LeavesFreshErrorToBrain(t *testing.T) {
	// Errored but never poked — the brain owns a fresh error via turn_completed.
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusErrored, UpdatedAt: fixedNow()}},
	)
	h.svc.sweep(context.Background())
	if len(h.board.blocked) != 0 || len(h.board.poked) != 0 {
		t.Fatalf("expected no action on fresh error, poked=%v blocked=%v", h.board.poked, h.board.blocked)
	}
}

func TestSweep_PrunesRecordsForTicketsThatLeftWorking(t *testing.T) {
	// t1 is gone from the Working set; its stale record must be pruned.
	h := newHarness(
		[]WorkingTicket{{ID: "t2", WorkerID: "w2"}},
		map[string]AgentState{"w2": {Status: statusBuilding, UpdatedAt: fixedNow()}},
		PokeRecord{TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-time.Hour)},
	)
	h.svc.sweep(context.Background())
	if _, ok := h.store.records["t1"]; ok {
		t.Fatalf("expected stale record t1 pruned")
	}
	if len(h.store.deleted) != 1 || h.store.deleted[0] != "t1" {
		t.Fatalf("expected exactly t1 deleted, got %v", h.store.deleted)
	}
}

func TestSweep_SkipsTicketWithNoAgentInfo(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{}, // no info for w1
	)
	h.svc.sweep(context.Background())
	if len(h.board.poked) != 0 || len(h.board.blocked) != 0 {
		t.Fatalf("expected no action without agent info, poked=%v blocked=%v", h.board.poked, h.board.blocked)
	}
}

// fixedNow mirrors testutil.NewFakeClock's seed instant so tests can position
// agent activity / poke times relative to the sweep's "now".
func fixedNow() time.Time { return time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC) }
