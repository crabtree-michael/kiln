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

// fakeStore is an in-memory board.Store. Seed helpers (seedTicket,
// seedWorker) write directly into the maps, bypassing Tx — that's
// deliberate: test fixtures must not depend on the very Service behavior
// under test (which, in the red phase, is all errNotImplemented stubs).
type fakeStore struct {
	mu      sync.Mutex
	tickets map[board.TicketID]board.Ticket
	workers map[board.WorkerID]board.Worker
	outbox  []board.Emission

	seq      int
	nextTime time.Time // monotonically-advancing fake clock for CreatedAt/UpdatedAt
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tickets:  map[board.TicketID]board.Ticket{},
		workers:  map[board.WorkerID]board.Worker{},
		nextTime: time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC),
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
	workersBefore := make(map[board.WorkerID]board.Worker, len(s.workers))
	maps.Copy(workersBefore, s.workers)
	outboxBefore := make([]board.Emission, len(s.outbox))
	copy(outboxBefore, s.outbox)

	txn := &fakeTx{s: s}
	err := fn(txn)
	if err != nil {
		s.tickets = ticketsBefore
		s.workers = workersBefore
		s.outbox = outboxBefore
		return err
	}
	return nil
}

// Snapshot groups tickets by their State field alone (03 D1: column/zone are
// derived render groupings, never stored) and orders each group per 03 §4 /
// entities.go's Snapshot doc. This grouping logic is test-fixture code
// standing in for the real adapter's SQL ORDER BY (postgres/store.go); the
// literal SQL ordering is proven separately in the integration suite against
// real Postgres.
func (s *fakeStore) Snapshot(ctx context.Context) (board.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var snap board.Snapshot
	var ready []board.Ticket
	for _, t := range s.tickets {
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

	snap.WorkerTotal = len(s.workers)
	busy := busyWorkers(s.tickets)
	free := 0
	for id := range s.workers {
		if !busy[id] {
			free++
		}
	}
	snap.WorkerFree = free
	return snap, nil
}

// GetTicket reads one non-archived ticket by id (03 §4 amended). Archived or
// missing ids are ErrNotFound, matching the store contract.
func (s *fakeStore) GetTicket(_ context.Context, id board.TicketID) (board.Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	if !ok || t.ArchivedAt != nil {
		return board.Ticket{}, board.ErrNotFound
	}
	return t, nil
}

// now returns a strictly-increasing timestamp, so CreatedAt/tie-break
// ordering tests never depend on real wall-clock resolution.
func (s *fakeStore) now() time.Time {
	t := s.nextTime
	s.nextTime = s.nextTime.Add(time.Millisecond)
	return t
}

// seedTicket inserts a ticket exactly as given (fixture setup, not a Store
// operation).
func (s *fakeStore) seedTicket(t board.Ticket) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = s.now()
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	s.tickets[t.ID] = t
}

// seedWorker inserts a worker row (fixture setup — no Board API operation
// creates workers; that's postgres.Store.ReconcileWorkers, composition-root
// only per 03 §8).
func (s *fakeStore) seedWorker(id board.WorkerID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[id] = board.Worker{ID: id, CreatedAt: s.now()}
}

// seedWorkers seeds n workers named w1..wn and returns their ids in order.
func (s *fakeStore) seedWorkers(n int) []board.WorkerID {
	ids := make([]board.WorkerID, 0, n)
	for i := 1; i <= n; i++ {
		id := board.WorkerID(fmt.Sprintf("w%d", i))
		s.seedWorker(id)
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

func (s *fakeStore) outboxSnapshot() []board.Emission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]board.Emission, len(s.outbox))
	copy(out, s.outbox)
	return out
}

func busyWorkers(tickets map[board.TicketID]board.Ticket) map[board.WorkerID]bool {
	busy := map[board.WorkerID]bool{}
	for _, t := range tickets {
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

func (t *fakeTx) LockTicket(_ context.Context, id board.TicketID) (board.Ticket, error) {
	tk, ok := t.s.tickets[id]
	if !ok || tk.ArchivedAt != nil { // archived tickets are invisible to targeted ops (03 §4 amended)
		return board.Ticket{}, board.ErrNotFound
	}
	return tk, nil
}

func (t *fakeTx) InsertTicket(_ context.Context, tk board.Ticket) (board.Ticket, error) {
	t.s.seq++
	if tk.ID == "" {
		tk.ID = board.TicketID(fmt.Sprintf("ticket-%d", t.s.seq))
	}
	now := t.s.now()
	tk.CreatedAt = now
	tk.UpdatedAt = now
	t.s.tickets[tk.ID] = tk
	return tk, nil
}

func (t *fakeTx) UpdateTicket(_ context.Context, tk board.Ticket) (board.Ticket, error) {
	if _, ok := t.s.tickets[tk.ID]; !ok {
		return board.Ticket{}, board.ErrNotFound
	}
	tk.UpdatedAt = t.s.now()
	t.s.tickets[tk.ID] = tk
	return tk, nil
}

func (t *fakeTx) NextReadyTicket(_ context.Context) (board.Ticket, bool, error) {
	var best *board.Ticket
	for _, tk := range t.s.tickets {
		if tk.State != board.StateReady {
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

func (t *fakeTx) FreeWorker(_ context.Context) (board.Worker, bool, error) {
	busy := busyWorkers(t.s.tickets)
	ids := make([]string, 0, len(t.s.workers))
	for id := range t.s.workers {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		wid := board.WorkerID(id)
		if !busy[wid] {
			return t.s.workers[wid], true, nil
		}
	}
	return board.Worker{}, false, nil
}

func (t *fakeTx) AppendOutbox(_ context.Context, e board.Emission) error {
	t.s.outbox = append(t.s.outbox, e)
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
