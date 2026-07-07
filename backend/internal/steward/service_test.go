package steward

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// errBoardDown is the static board-read failure the error-path tests inject.
var errBoardDown = errors.New("board down")

// testProject is the single project most tests sweep; multi-project behavior
// gets its own test.
const testProject = "p1"

// fakeProjects serves a fixed project id list.
type fakeProjects struct {
	ids []string
	err error
}

func (p *fakeProjects) ProjectIDs(context.Context) ([]string, error) { return p.ids, p.err }

// fakeBoard records the sweep's board actions and serves per-project fixed
// Working sets.
type fakeBoard struct {
	working    map[string][]WorkingTicket // project_id -> Working set
	workingErr map[string]error           // project_id -> WorkingTickets error
	poked      map[string][]string        // project_id -> poked ticket ids, in order
	blocked    map[string]string          // ticket_id -> reason
}

func (b *fakeBoard) WorkingTickets(_ context.Context, projectID string) ([]WorkingTicket, error) {
	if err := b.workingErr[projectID]; err != nil {
		return nil, err
	}
	return b.working[projectID], nil
}

func (b *fakeBoard) Poke(_ context.Context, projectID, ticketID string) error {
	if b.poked == nil {
		b.poked = map[string][]string{}
	}
	b.poked[projectID] = append(b.poked[projectID], ticketID)
	return nil
}

func (b *fakeBoard) Block(_ context.Context, _, ticketID, reason string) error {
	if b.blocked == nil {
		b.blocked = map[string]string{}
	}
	b.blocked[ticketID] = reason
	return nil
}

// fakeAgents serves per-project fixed status maps.
type fakeAgents struct {
	states map[string]map[string]AgentState // project_id -> worker_id -> state
}

func (a *fakeAgents) States(_ context.Context, projectID string) (map[string]AgentState, error) {
	return a.states[projectID], nil
}

// fakeFeed records posted poke cards per project.
type fakeFeed struct {
	posted map[string][]string // project_id -> ticket ids
}

func (f *fakeFeed) PostPoke(_ context.Context, projectID, ticketID string) error {
	if f.posted == nil {
		f.posted = map[string][]string{}
	}
	f.posted[projectID] = append(f.posted[projectID], ticketID)
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

func (s *fakeStore) Upsert(_ context.Context, projectID, ticketID, workerID string, pokedAt time.Time) error {
	s.records[ticketID] = PokeRecord{ProjectID: projectID, TicketID: ticketID, WorkerID: workerID, PokedAt: pokedAt}
	return nil
}

func (s *fakeStore) Delete(_ context.Context, ticketID string) error {
	delete(s.records, ticketID)
	s.deleted = append(s.deleted, ticketID)
	return nil
}

const stall = 5 * time.Minute

// harness wires a Service over the fakes at a fixed "now", with everything
// living in testProject.
type harness struct {
	svc   *Service
	board *fakeBoard
	feed  *fakeFeed
	store *fakeStore
	now   time.Time
}

func newHarness(working []WorkingTicket, states map[string]AgentState, recs ...PokeRecord) *harness {
	clock := testutil.NewFakeClock()
	board := &fakeBoard{working: map[string][]WorkingTicket{testProject: working}}
	agents := &fakeAgents{states: map[string]map[string]AgentState{testProject: states}}
	feed := &fakeFeed{}
	store := newFakeStore(recs...)
	svc := NewService(&fakeProjects{ids: []string{testProject}}, board, agents, feed, store, clock,
		Config{Stall: stall, Interval: time.Minute})
	return &harness{svc: svc, board: board, feed: feed, store: store, now: clock.Now()}
}

func TestSweep_PokesIdleAgentPastThreshold(t *testing.T) {
	h := newHarness(
		[]WorkingTicket{{ID: "t1", WorkerID: "w1"}},
		map[string]AgentState{"w1": {Status: statusIdle, UpdatedAt: fixedNow().Add(-stall - time.Second)}},
	)
	h.svc.sweep(context.Background())

	if got := h.board.poked[testProject]; len(got) != 1 || got[0] != "t1" {
		t.Fatalf("expected t1 poked in %s, got %v", testProject, h.board.poked)
	}
	if got := h.feed.posted[testProject]; len(got) != 1 || got[0] != "t1" {
		t.Fatalf("expected t1 feed poke in %s, got %v", testProject, h.feed.posted)
	}
	rec, ok := h.store.records["t1"]
	if !ok {
		t.Fatalf("expected poke recorded for t1")
	}
	if rec.ProjectID != testProject {
		t.Fatalf("expected poke record to carry project %s, got %q", testProject, rec.ProjectID)
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
		PokeRecord{ProjectID: testProject, TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-stall + time.Minute)},
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
		PokeRecord{ProjectID: testProject, TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-stall - time.Second)},
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
		PokeRecord{ProjectID: testProject, TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-time.Minute)},
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
		PokeRecord{ProjectID: testProject, TicketID: "t1", WorkerID: "w1", PokedAt: fixedNow().Add(-time.Hour)},
	)
	h.svc.sweep(context.Background())
	if _, ok := h.store.records["t1"]; ok {
		t.Fatalf("expected stale record t1 pruned")
	}
	if len(h.store.deleted) != 1 || h.store.deleted[0] != "t1" {
		t.Fatalf("expected exactly t1 deleted, got %v", h.store.deleted)
	}
}

func TestSweep_PrunesLegacyRecordOnlyOnCleanPass(t *testing.T) {
	// A pre-tenancy record (empty ProjectID) whose ticket is gone: pruned when
	// every project swept cleanly, kept when any project errored.
	legacy := PokeRecord{TicketID: "t-old", WorkerID: "w-old", PokedAt: fixedNow().Add(-time.Hour)}

	h := newHarness(nil, nil, legacy)
	h.svc.sweep(context.Background())
	if _, ok := h.store.records["t-old"]; ok {
		t.Fatalf("expected legacy record pruned on clean pass")
	}

	h = newHarness(nil, nil, legacy)
	h.board.workingErr = map[string]error{testProject: errBoardDown}
	h.svc.sweep(context.Background())
	if _, ok := h.store.records["t-old"]; !ok {
		t.Fatalf("expected legacy record kept when a project failed to sweep")
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

func TestSweep_TwoProjects_EachPokedInOwnScope_ErrorInOneSkipsOnlyIt(t *testing.T) {
	// Project A's board errors; project B's stalled ticket must still be poked
	// through B's scope, and A's existing record must survive the prune.
	stalled := func(w string) map[string]AgentState {
		return map[string]AgentState{w: {Status: statusIdle, UpdatedAt: fixedNow().Add(-stall - time.Second)}}
	}
	clock := testutil.NewFakeClock()
	board := &fakeBoard{
		working: map[string][]WorkingTicket{
			"pA": {{ID: "tA", WorkerID: "wA"}},
			"pB": {{ID: "tB", WorkerID: "wB"}},
		},
		workingErr: map[string]error{"pA": errBoardDown},
	}
	agents := &fakeAgents{states: map[string]map[string]AgentState{
		"pA": stalled("wA"),
		"pB": stalled("wB"),
	}}
	feed := &fakeFeed{}
	// tA was poked long ago; because pA fails to sweep, the record must be kept
	// even though tA is not observed live this pass.
	store := newFakeStore(PokeRecord{ProjectID: "pA", TicketID: "tA", WorkerID: "wA", PokedAt: fixedNow().Add(-time.Hour)})
	svc := NewService(&fakeProjects{ids: []string{"pA", "pB"}}, board, agents, feed, store, clock,
		Config{Stall: stall, Interval: time.Minute})

	svc.sweep(context.Background())

	if got := board.poked["pB"]; len(got) != 1 || got[0] != "tB" {
		t.Fatalf("expected tB poked in pB despite pA failing, got %v", board.poked)
	}
	if got := board.poked["pA"]; len(got) != 0 {
		t.Fatalf("expected no pokes in failed project pA, got %v", got)
	}
	if got := feed.posted["pB"]; len(got) != 1 || got[0] != "tB" {
		t.Fatalf("expected tB feed poke in pB, got %v", feed.posted)
	}
	if rec := store.records["tB"]; rec.ProjectID != "pB" {
		t.Fatalf("expected tB's record to carry pB, got %+v", rec)
	}
	if _, ok := store.records["tA"]; !ok {
		t.Fatalf("expected tA's record kept while pA is unsweepable, deleted=%v", store.deleted)
	}
	if len(board.blocked) != 0 {
		t.Fatalf("expected no blocks, got %v", board.blocked)
	}
}

func TestSweep_TwoProjects_BothSwept(t *testing.T) {
	// No errors: each project's stalled ticket is poked via its own scope, and
	// a stale record from a swept project is pruned.
	stalled := func(w string) map[string]AgentState {
		return map[string]AgentState{w: {Status: statusStopped, UpdatedAt: fixedNow().Add(-stall - time.Second)}}
	}
	clock := testutil.NewFakeClock()
	board := &fakeBoard{
		working: map[string][]WorkingTicket{
			"pA": {{ID: "tA", WorkerID: "wA"}},
			"pB": {{ID: "tB", WorkerID: "wB"}},
		},
	}
	agents := &fakeAgents{states: map[string]map[string]AgentState{
		"pA": stalled("wA"),
		"pB": stalled("wB"),
	}}
	feed := &fakeFeed{}
	// A stale record in pB for a ticket that left Working — prunable.
	store := newFakeStore(PokeRecord{
		ProjectID: "pB", TicketID: "t-gone", WorkerID: "w-gone", PokedAt: fixedNow().Add(-time.Hour),
	})
	svc := NewService(&fakeProjects{ids: []string{"pA", "pB"}}, board, agents, feed, store, clock,
		Config{Stall: stall, Interval: time.Minute})

	svc.sweep(context.Background())

	for pid, tid := range map[string]string{"pA": "tA", "pB": "tB"} {
		if got := board.poked[pid]; len(got) != 1 || got[0] != tid {
			t.Fatalf("expected %s poked in %s, got %v", tid, pid, board.poked)
		}
		if rec := store.records[tid]; rec.ProjectID != pid {
			t.Fatalf("expected %s's record to carry %s, got %+v", tid, pid, rec)
		}
	}
	if _, ok := store.records["t-gone"]; ok {
		t.Fatalf("expected stale record t-gone pruned")
	}
}

// fixedNow mirrors testutil.NewFakeClock's seed instant so tests can position
// agent activity / poke times relative to the sweep's "now".
func fixedNow() time.Time { return time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC) }
