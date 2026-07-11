package board_test

// Shared in-memory unit-test fakes for the board module (03 §9's "unit tests
// exercise BoardService transition rules and error paths against an
// in-memory store fake — asserting emitted outbox rows *is* asserting side
// effects, no agent-runtime fake needed"). Everything here is offline and
// single-threaded-serial: fakeStore.Tx holds one mutex for its whole
// duration, which is enough to model "one operation = one short
// transaction" (03 §6) for unit-level tests. Concurrent-access races (SKIP
// LOCKED, the one_active_ticket_per_worker backstop) are proven only against
// real Postgres — see postgres/store_integration_test.go.
//
// Tenancy (11 §3): the fake mirrors the project-scoped port — every ticket,
// worker, and emission is tagged with a project id, and every read/write
// filters on it, so cross-project isolation is assertable at unit level.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/board"
)

// Fixed tenant ids for every unit test: operations run under projA; projB
// exists to prove nothing leaks across the project boundary.
const (
	projA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	projB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// fakeEmission is one appended outbox row plus the project it was scoped to,
// so tests can assert emissions land under the right tenant.
type fakeEmission struct {
	Project string
	E       board.Emission
}

// fakeStore is an in-memory board.Store. Seed helpers (seedTicket,
// seedWorker) write directly into the maps, bypassing Tx — that's
// deliberate: test fixtures must not depend on the very Service behavior
// under test (which, in the red phase, is all errNotImplemented stubs).
type fakeStore struct {
	mu          sync.Mutex
	tickets     map[board.TicketID]board.Ticket
	ticketProj  map[board.TicketID]string // ticket id -> owning project
	workers     map[board.WorkerID]board.Worker
	workerProj  map[board.WorkerID]string // worker id -> owning project
	workerError map[board.WorkerID]bool   // worker id -> errored (unhealthy); absent = healthy
	outbox      []fakeEmission

	seq      int
	nextTime time.Time // monotonically-advancing fake clock for CreatedAt/UpdatedAt
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tickets:     map[board.TicketID]board.Ticket{},
		ticketProj:  map[board.TicketID]string{},
		workers:     map[board.WorkerID]board.Worker{},
		workerProj:  map[board.WorkerID]string{},
		workerError: map[board.WorkerID]bool{},
		nextTime:    time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
	}
}

// Tx models "one operation = one short transaction" (03 §6, I7): fn runs
// against the live maps; on error, every change fn made — ticket writes and
// outbox appends alike — is rolled back, proving no partial writes ever
// surface from a failed precondition.
func (s *fakeStore) Tx(ctx context.Context, fn func(board.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ticketsBefore := make(map[board.TicketID]board.Ticket, len(s.tickets))
	maps.Copy(ticketsBefore, s.tickets)
	ticketProjBefore := make(map[board.TicketID]string, len(s.ticketProj))
	maps.Copy(ticketProjBefore, s.ticketProj)
	workersBefore := make(map[board.WorkerID]board.Worker, len(s.workers))
	maps.Copy(workersBefore, s.workers)
	workerProjBefore := make(map[board.WorkerID]string, len(s.workerProj))
	maps.Copy(workerProjBefore, s.workerProj)
	outboxBefore := make([]fakeEmission, len(s.outbox))
	copy(outboxBefore, s.outbox)

	txn := &fakeTx{s: s}
	err := fn(txn)
	if err != nil {
		s.tickets = ticketsBefore
		s.ticketProj = ticketProjBefore
		s.workers = workersBefore
		s.workerProj = workerProjBefore
		s.outbox = outboxBefore
		return err
	}
	return nil
}

// Snapshot groups the project's tickets by their State field alone (03 D1:
// column/zone are derived render groupings, never stored) and orders each
// group per 03 §4 / entities.go's Snapshot doc. This grouping logic is
// test-fixture code standing in for the real adapter's SQL (postgres/
// store.go); the literal SQL ordering and project predicates are proven
// separately in the integration suite against real Postgres.
func (s *fakeStore) Snapshot(ctx context.Context, projectID string) (board.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var snap board.Snapshot
	var ready []board.Ticket
	for id, t := range s.tickets {
		if s.ticketProj[id] != projectID { // tenant boundary (11 §3)
			continue
		}
		if t.ArchivedAt != nil { // archived tickets are gone from every read (03 §4 amended)
			continue
		}
		switch t.State {
		case board.StateShaping:
			snap.Shaping = append(snap.Shaping, t)
		case board.StateReady:
			ready = append(ready, t)
		case board.StateBlocked:
			snap.Blocked = append(snap.Blocked, t)
		case board.StateWorking:
			snap.Working = append(snap.Working, t)
		case board.StateDone:
			snap.Done = append(snap.Done, t)
		}
	}
	sort.Slice(snap.Shaping, func(i, j int) bool {
		if snap.Shaping[i].Priority != snap.Shaping[j].Priority {
			return snap.Shaping[i].Priority > snap.Shaping[j].Priority
		}
		return snap.Shaping[i].CreatedAt.Before(snap.Shaping[j].CreatedAt)
	})
	sort.Slice(ready, func(i, j int) bool { return readyLess(ready[i], ready[j]) })
	snap.Ready = ready

	busy := s.busyWorkers(projectID)
	total, free := 0, 0
	for id := range s.workers {
		if s.workerProj[id] != projectID {
			continue
		}
		total++
		if !busy[id] && !s.workerError[id] { // free = unbound AND healthy (03 §5 amended)
			free++
		}
	}
	snap.WorkerTotal = total
	snap.WorkerFree = free
	return snap, nil
}

// GetTicket reads one of the project's non-archived tickets by id (03 §4
// amended). Archived, missing, or other-project ids are ErrNotFound, matching
// the store contract.
func (s *fakeStore) GetTicket(_ context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok || s.ticketProj[id] != projectID || t.ArchivedAt != nil {
		return board.Ticket{}, board.ErrNotFound
	}
	return t, nil
}

// SetWorkerHealth reconciles the project's worker health exactly like the
// adapter's full-reconcile UPDATE: ids in erroredWorkerIDs become unhealthy,
// every other of the project's workers becomes healthy.
func (s *fakeStore) SetWorkerHealth(_ context.Context, projectID string, erroredWorkerIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	errset := make(map[board.WorkerID]bool, len(erroredWorkerIDs))
	for _, id := range erroredWorkerIDs {
		errset[board.WorkerID(id)] = true
	}
	for id := range s.workers {
		if s.workerProj[id] != projectID {
			continue
		}
		if errset[id] {
			s.workerError[id] = true
		} else {
			delete(s.workerError, id)
		}
	}
	return nil
}

// now returns a strictly-increasing timestamp, so CreatedAt/tie-break
// ordering tests never depend on real wall-clock resolution.
func (s *fakeStore) now() time.Time {
	t := s.nextTime
	s.nextTime = s.nextTime.Add(time.Millisecond)
	return t
}

// seedTicket inserts a ticket under the project exactly as given (fixture
// setup, not a Store operation).
func (s *fakeStore) seedTicket(projectID string, t board.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = s.now()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	if t.StateChangedAt.IsZero() {
		t.StateChangedAt = t.UpdatedAt
	}
	s.tickets[t.ID] = t
	s.ticketProj[t.ID] = projectID
}

// seedWorker inserts a worker row under the project (fixture setup — no Board
// API operation creates workers; that's postgres.Store.ReconcileWorkers,
// composition-root only per 03 §8). Worker ids stay globally unique (03 I2).
func (s *fakeStore) seedWorker(projectID string, id board.WorkerID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[id] = board.Worker{ID: id, CreatedAt: s.now()}
	s.workerProj[id] = projectID
}

// seedWorkers seeds n workers named w1..wn under the project and returns
// their ids in order.
func (s *fakeStore) seedWorkers(projectID string, n int) []board.WorkerID {
	ids := make([]board.WorkerID, 0, n)
	for i := 1; i <= n; i++ {
		id := board.WorkerID(fmt.Sprintf("w%d", i))
		s.seedWorker(projectID, id)
		ids = append(ids, id)
	}
	return ids
}

func (s *fakeStore) ticket(id board.TicketID) (board.Ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	return t, ok
}

// outboxSnapshot returns every emission across all projects, in emission
// order; use outboxEntries when the owning project matters.
func (s *fakeStore) outboxSnapshot() []board.Emission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]board.Emission, 0, len(s.outbox))
	for _, e := range s.outbox {
		out = append(out, e.E)
	}
	return out
}

// outboxEntries returns every emission with the project it was appended
// under, for tenancy assertions.
func (s *fakeStore) outboxEntries() []fakeEmission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]fakeEmission, len(s.outbox))
	copy(out, s.outbox)
	return out
}

// busyWorkers derives the project's busy set (03 D2): a worker is busy iff
// one of the project's active tickets references it. Caller holds s.mu.
func (s *fakeStore) busyWorkers(projectID string) map[board.WorkerID]bool {
	busy := map[board.WorkerID]bool{}
	for id, t := range s.tickets {
		if s.ticketProj[id] != projectID {
			continue
		}
		if t.State.Active() && t.WorkerID != nil {
			busy[*t.WorkerID] = true
		}
	}
	return busy
}

// readyLess implements the pull order (03 D9): priority DESC, ready_at ASC,
// id ASC.
func readyLess(a, b board.Ticket) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	var at, bt time.Time
	if a.ReadyAt != nil {
		at = *a.ReadyAt
	}
	if b.ReadyAt != nil {
		bt = *b.ReadyAt
	}
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.ID < b.ID
}

// fakeTx is the transaction-scoped view fakeStore.Tx hands to fn. It has no
// mutex of its own — fakeStore.Tx already holds s.mu for the transaction's
// whole duration, matching "one operation = one short transaction".
type fakeTx struct{ s *fakeStore }

var _ board.Tx = (*fakeTx)(nil)

func (t *fakeTx) LockTicket(_ context.Context, projectID string, id board.TicketID) (board.Ticket, error) {
	tk, ok := t.s.tickets[id]
	// Archived tickets are invisible to targeted ops (03 §4 amended); an id
	// owned by another project must be indistinguishable from a missing one.
	if !ok || t.s.ticketProj[id] != projectID || tk.ArchivedAt != nil {
		return board.Ticket{}, board.ErrNotFound
	}
	return tk, nil
}

func (t *fakeTx) InsertTicket(_ context.Context, projectID string, tk board.Ticket) (board.Ticket, error) {
	t.s.seq++
	if tk.ID == "" {
		tk.ID = board.TicketID(fmt.Sprintf("ticket-%d", t.s.seq))
	}
	now := t.s.now()
	tk.CreatedAt = now
	tk.UpdatedAt = now
	tk.StateChangedAt = now
	t.s.tickets[tk.ID] = tk
	t.s.ticketProj[tk.ID] = projectID
	return tk, nil
}

func (t *fakeTx) UpdateTicket(_ context.Context, projectID string, tk board.Ticket) (board.Ticket, error) {
	prev, ok := t.s.tickets[tk.ID]
	if !ok || t.s.ticketProj[tk.ID] != projectID {
		return board.Ticket{}, board.ErrNotFound
	}
	tk.UpdatedAt = t.s.now()
	// state_changed_at advances only on a real transition — mirrors the CASE in
	// postgres.UpdateTicket so a same-state mutation (e.g. a Working→Working
	// nudge) leaves the time-in-status clock untouched.
	if tk.State != prev.State {
		tk.StateChangedAt = t.s.now()
	} else {
		tk.StateChangedAt = prev.StateChangedAt
	}
	t.s.tickets[tk.ID] = tk
	return tk, nil
}

func (t *fakeTx) TicketIDByDoneCommit(_ context.Context, projectID, sha string) (board.TicketID, bool, error) {
	for id, tk := range t.s.tickets {
		if t.s.ticketProj[id] != projectID || tk.DoneCommit == nil {
			continue
		}
		if *tk.DoneCommit == sha {
			return id, true, nil
		}
	}
	return "", false, nil
}

func (t *fakeTx) NextReadyTicket(_ context.Context, projectID string) (board.Ticket, bool, error) {
	var best *board.Ticket
	for id, tk := range t.s.tickets {
		if t.s.ticketProj[id] != projectID || tk.State != board.StateReady {
			continue
		}
		cand := tk
		if best == nil || readyLess(cand, *best) {
			c := cand
			best = &c
		}
	}
	if best == nil {
		return board.Ticket{}, false, nil
	}
	return *best, true, nil
}

func (t *fakeTx) FreeWorker(_ context.Context, projectID string) (board.Worker, bool, error) {
	busy := t.s.busyWorkers(projectID)
	ids := make([]string, 0, len(t.s.workers))
	for id := range t.s.workers {
		if t.s.workerProj[id] != projectID {
			continue
		}
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		wid := board.WorkerID(id)
		if !busy[wid] && !t.s.workerError[wid] { // pull binds only healthy free workers (03 §5 amended)
			return t.s.workers[wid], true, nil
		}
	}
	return board.Worker{}, false, nil
}

func (t *fakeTx) AppendOutbox(_ context.Context, projectID string, e board.Emission) error {
	t.s.outbox = append(t.s.outbox, fakeEmission{Project: projectID, E: e})
	return nil
}

// ---- shared assertion helpers ----------------------------------------

// emissionsWithTopic returns every emitted Emission on the given topic, in
// emission order.
func emissionsWithTopic(ems []board.Emission, topic board.Topic) []board.Emission {
	var out []board.Emission
	for _, e := range ems {
		if e.Topic == topic {
			out = append(out, e)
		}
	}
	return out
}

func requireInvalidTransition(t *testing.T, err error, wantFrom board.State, wantAttempted string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected ErrInvalidTransition{From: %q, Attempted: %q}, got nil error", wantFrom, wantAttempted)
	}
	var it *board.ErrInvalidTransition
	if !errors.As(err, &it) {
		t.Fatalf("expected *board.ErrInvalidTransition, got %T: %v", err, err)
	}
	if it.From != wantFrom {
		t.Errorf("ErrInvalidTransition.From = %q, want %q", it.From, wantFrom)
	}
	if it.Attempted != wantAttempted {
		t.Errorf("ErrInvalidTransition.Attempted = %q, want %q", it.Attempted, wantAttempted)
	}
}
