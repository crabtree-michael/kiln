//go:build integration

package agent_test

// The concurrency test the gate never had (docs/root-cause-2026-08-03-duplicate-instances.md
// §6 item 8, open since 08-02 rec #7): TWO agent.Services over ONE store is
// what production actually runs for the 67–83 s of every Render deploy, and
// nothing in this suite covered it.
//
// It is integration-tagged and lives here rather than in internal/leader
// because both halves have to be real to mean anything: the real turn machine
// (pollOnce → stepStartTurn has no durable claim — see the note at the bottom)
// and a real Postgres advisory lock. The only fakes are the ones the module's
// unit tests already use: an in-memory store, the mock provider, a fake clock.
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -race -tags=integration ./internal/agent/...

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/leader"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// concurrencyLockKey is this test's own advisory key, from the same reserved
// 0x7E57 ("test") block internal/leader's suite uses — never leader.LockKey.
const concurrencyLockKey int64 = 0x7E57000000000101

// Cadences: fast election so a test does not wait on production intervals.
const (
	electorRetry = 50 * time.Millisecond
	electorCheck = 50 * time.Millisecond
)

// duplicateWindow is how long both instances are left alive and campaigning
// with the turn parked at worker_ready. It is many multiples of both the
// elector's retry and the agent poll interval the fake clock is racing
// through, so an instance that polled at all would have started the turn.
const duplicateWindow = 500 * time.Millisecond

// gatedProvider counts StartTurn calls and holds every one of them open until
// the test releases it.
//
// Holding the call open is what gives this test teeth. Kiln's turn machine
// moves the row to turn_started only AFTER StartTurn returns, so while the
// first call is parked the row sits at worker_ready for the whole window —
// exactly the state pollOnce acts on. A second instance that polled even once
// would necessarily call StartTurn too, and the assertion below would see 2.
// Without the leader lock this test fails deterministically rather than
// flakily, which is the difference between a regression test and a coin flip.
type gatedProvider struct {
	agent.Provider

	mu      sync.Mutex
	calls   int
	arrived chan struct{} // closed on the first StartTurn
	once    sync.Once
	release chan struct{} // closed by the test to let the parked calls finish
}

func newGatedProvider() *gatedProvider {
	return &gatedProvider{
		Provider: mock.New(),
		arrived:  make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (p *gatedProvider) StartTurn(
	ctx context.Context, w agent.ProviderWorker, conversation, message string, fresh bool,
) (agent.TurnRef, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.once.Do(func() { close(p.arrived) })

	select {
	case <-p.release:
	case <-ctx.Done():
		return agent.TurnRef{}, fmt.Errorf("gatedProvider: %w", ctx.Err())
	}
	ref, err := p.Provider.StartTurn(ctx, w, conversation, message, fresh)
	if err != nil {
		return ref, fmt.Errorf("gatedProvider: %w", err)
	}
	return ref, nil
}

func (p *gatedProvider) startTurns() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// leaderTestDB opens one pool per simulated instance, so the two Electors hold
// their advisory locks on genuinely separate sessions — the production shape.
func leaderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run the agent leader-concurrency test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dsn, err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Logf("close db: %v", cerr)
		}
	})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// runInstance starts one simulated backend: an agent.Service whose Run loop is
// gated on the shared advisory lock, with its clock pumped so poll/reconcile
// cadences cost no wall time. Cleanup cancels it and waits for the loop to
// stop and the lock to be released.
func runInstance(t *testing.T, svc *agent.Service, clock *testutil.FakeClock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopPump := make(chan struct{})
	go clock.Pump(stopPump, time.Second)

	e := leader.New(leaderTestDB(t), leader.Config{
		Key:   concurrencyLockKey,
		Retry: electorRetry,
		Check: electorCheck,
		Log:   slog.New(slog.DiscardHandler),
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Run(ctx, func(loopCtx context.Context) {
			if err := svc.Run(loopCtx); err != nil {
				t.Errorf("Service.Run: %v", err)
			}
		})
	}()
	t.Cleanup(func() {
		close(stopPump)
		cancel()
		select {
		case <-done:
		case <-time.After(testutil.EventuallyTimeout):
			t.Error("gated Service.Run did not stop after cancellation")
		}
	})
}

// TestTwoInstancesStartOneTurn is the acceptance criterion at the level the
// collision actually bites: two agent.Services over one store, both alive, and
// exactly one instruction reaching the provider.
//
// Every confirmed incident is this shape — two instances polling one
// agent_turns row and both calling StartTurn, minting two Amika sessions and
// two `claude -p` processes in one working tree.
func TestTwoInstancesStartOneTurn(t *testing.T) {
	store := newFakeStore()
	provider := newGatedProvider()
	defer close(provider.release)

	clockA, clockB := testutil.NewFakeClock(), testutil.NewFakeClock()
	instA := newService(store, provider, &fakeEvents{}, &fakeSlots{ids: []string{testWorkerID}}, clockA, nil)
	instB := newService(store, provider, &fakeEvents{}, &fakeSlots{ids: []string{testWorkerID}}, clockB, nil)

	// One send, recorded once in the shared store — as the outbox would.
	msg := sendPayload(t, testTicketID, testWorkerID, "implement the feature")
	if err := instA.Send(context.Background(), 1, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	runInstance(t, instA, clockA)
	runInstance(t, instB, clockB)

	// The leader reaches StartTurn and parks there, leaving the row at
	// worker_ready for the rest of the window.
	select {
	case <-provider.arrived:
	case <-time.After(testutil.EventuallyTimeout):
		t.Fatal("no instance started the turn; the leader lock stalled the work entirely")
	}

	// Both instances stay alive and campaigning across this window.
	time.Sleep(duplicateWindow)

	if got := provider.startTurns(); got != 1 {
		t.Fatalf("StartTurn called %d times for one turn; want exactly 1 "+
			"(two instances acted on the same pending work)", got)
	}
}

// NOTE for whoever picks up the turn-machine CAS ticket
// (docs/ticket-draft-turn-claim-cas.md): the single StartTurn asserted above is
// the leader lock's doing, not the turn machine's. stepStartTurn still has no
// durable claim — drop the Electors from runInstance and this test fails — so
// the CAS remains required for the crash-mid-StartTurn shape a lock cannot
// prevent. Do not read this passing test as evidence that the machine is safe.
