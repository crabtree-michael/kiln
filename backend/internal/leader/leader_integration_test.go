//go:build integration

// Package leader_test exercises the Elector against a real Postgres. There is
// no meaningful fake here: pg_try_advisory_lock's semantics — one session at a
// time, released with the session however it ends — ARE the thing under test,
// and a stub of them would only assert that the stub is a stub.
//
// The Elector needs no tables at all (advisory locks are session state, not
// schema), so unlike the other integration suites this one creates nothing,
// truncates nothing, and shares the database harmlessly. Every test takes its
// own key from a reserved 0x7E57 ("test") block so it can never collide with
// leader.LockKey or with a sibling.
//
// Run with:
//
//	TEST_DATABASE_URL=postgres://kiln:kiln@localhost:5432/kiln_test?sslmode=disable \
//	    go test -tags=integration ./internal/leader/...
package leader_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/crabtree-michael/kiln/backend/internal/leader"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// Per-test advisory-lock keys. The high half (0x7E570000) stays below 2^31 for
// the same reason leader.LockKey does — see TestLockKeyFitsPgLocksReassembly.
const (
	keySingleOwner int64 = 0x7E57000000000001
	keyTakeover    int64 = 0x7E57000000000002
	keyConnDeath   int64 = 0x7E57000000000003
	keyRelease     int64 = 0x7E57000000000004
)

// Cadences for the tests: fast enough that a takeover is observable inside
// testutil.EventuallyTimeout, slow enough not to hammer the database.
const (
	testRetry = 50 * time.Millisecond
	testCheck = 50 * time.Millisecond
)

// settleWindow is how long a negative assertion ("the follower never ran")
// waits — many multiples of testRetry, so a follower that was going to start
// would have done so several times over.
const settleWindow = 500 * time.Millisecond

// pidOfHolderSQL finds the backend holding one advisory key, mirroring the
// reassembly the Elector's own verification does.
const pidOfHolderSQL = `
	SELECT pid FROM pg_locks
	WHERE locktype = 'advisory' AND granted AND objsubid = 1
	  AND ((classid::bigint << 32) | objid::bigint) = $1`

// testDB opens a fresh pool per call, so two Electors in one test are backed
// by separate pools — the production shape (two processes), not two
// connections that happen to share a pool.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run leader integration tests")
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

// tracker stands in for the background loops: it records that it is running
// for as long as its context is live, and counts how many times it has been
// entered (so a re-acquisition after a lost lock is observable).
type tracker struct {
	mu      sync.Mutex
	running bool
	starts  int
}

// loop is the func handed to Elector.Run — it holds "running" true for exactly
// the span the Elector says this process owns the work.
func (tr *tracker) loop(ctx context.Context) {
	tr.mu.Lock()
	tr.running = true
	tr.starts++
	tr.mu.Unlock()

	<-ctx.Done()

	tr.mu.Lock()
	tr.running = false
	tr.mu.Unlock()
}

func (tr *tracker) isRunning() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.running
}

func (tr *tracker) startCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.starts
}

// instance is one simulated backend process: its own pool, its own Elector.
type instance struct {
	tracker *tracker
	stop    context.CancelFunc
	done    chan struct{}
}

// start launches one instance campaigning for key. The returned instance's
// done channel closes once Run has returned — i.e. once the loops have stopped
// AND the lock has been released.
func start(t *testing.T, key int64) *instance {
	t.Helper()
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	inst := &instance{tracker: &tracker{}, stop: cancel, done: make(chan struct{})}
	e := leader.New(db, leader.Config{
		Key: key, Retry: testRetry, Check: testCheck, Log: slog.New(slog.DiscardHandler),
	})
	go func() {
		defer close(inst.done)
		e.Run(ctx, inst.tracker.loop)
	}()
	t.Cleanup(func() {
		cancel()
		<-inst.done
	})
	return inst
}

// stopAndWait cancels the instance and waits for Run to return.
func (i *instance) stopAndWait(t *testing.T) {
	t.Helper()
	i.stop()
	select {
	case <-i.done:
	case <-time.After(testutil.EventuallyTimeout):
		t.Fatal("Elector.Run did not return after context cancellation")
	}
}

// TestOnlyOneInstanceRunsTheLoops is the acceptance criterion in one test: two
// instances alive at once — the state every Render deploy is in for 67–83 s —
// and exactly one of them runs the work.
func TestOnlyOneInstanceRunsTheLoops(t *testing.T) {
	a := start(t, keySingleOwner)
	testutil.Eventually(t, a.tracker.isRunning)

	b := start(t, keySingleOwner)
	// Several retry intervals of both being alive.
	time.Sleep(settleWindow)

	if !a.tracker.isRunning() {
		t.Error("the leader stopped running the loops while it still held the lock")
	}
	if b.tracker.isRunning() || b.tracker.startCount() != 0 {
		t.Errorf("the follower ran the loops %d time(s) while another instance held the lock",
			b.tracker.startCount())
	}
}

// TestFollowerTakesOverOnCleanShutdown is the deploy handoff: the old instance
// drains, and the new one starts working within seconds — without user-visible
// stalled work.
func TestFollowerTakesOverOnCleanShutdown(t *testing.T) {
	a := start(t, keyTakeover)
	testutil.Eventually(t, a.tracker.isRunning)

	b := start(t, keyTakeover)
	time.Sleep(settleWindow)
	if b.tracker.isRunning() {
		t.Fatal("the follower led while the leader was still alive")
	}

	a.stopAndWait(t)

	testutil.Eventually(t, b.tracker.isRunning)
	if a.tracker.isRunning() {
		t.Error("the outgoing leader is still running the loops after shutdown")
	}
}

// TestLeaderStopsLoopsWhenConnectionDies covers the hard-exit and dead-pooler
// halves at once. Killing the leader's backend is what a SIGKILL/OOM/container
// stop looks like from Postgres's side: the lock goes with the session. Two
// things must follow — the leader must STOP the loops rather than carry on
// un-locked, and the lock must become available to whoever asks next (here,
// the same instance re-campaigning).
func TestLeaderStopsLoopsWhenConnectionDies(t *testing.T) {
	admin := testDB(t)
	a := start(t, keyConnDeath)
	testutil.Eventually(t, a.tracker.isRunning)

	pid := holderPID(t, admin, keyConnDeath)
	terminateBackend(t, admin, pid)

	// The loops stop: the Elector re-verifies its hold every testCheck and
	// cancels the moment it can no longer confirm it.
	testutil.Eventually(t, func() bool { return !a.tracker.isRunning() })

	// And the lock is free again, so work resumes rather than stalling.
	testutil.Eventually(t, func() bool { return a.tracker.startCount() >= 2 })
	testutil.Eventually(t, a.tracker.isRunning)
}

// TestLockIsReleasedOnShutdown pins the pinned-connection rule: the Elector
// must unlock BEFORE returning its conn to the pool. A conn handed back while
// still holding a session-scoped lock keeps the lock alive for as long as the
// pool does — leadership would never transfer, and the failure would look like
// "the new instance just never starts working".
func TestLockIsReleasedOnShutdown(t *testing.T) {
	observer := testDB(t)
	a := start(t, keyRelease)
	testutil.Eventually(t, a.tracker.isRunning)

	a.stopAndWait(t)

	// A wholly unrelated pool must be able to take the key immediately.
	conn, err := observer.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin observer conn: %v", err)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			t.Logf("close observer conn: %v", cerr)
		}
	}()
	var acquired bool
	if err := conn.QueryRowContext(context.Background(),
		`SELECT pg_try_advisory_lock($1)`, keyRelease).Scan(&acquired); err != nil {
		t.Fatalf("try advisory lock: %v", err)
	}
	if !acquired {
		t.Fatal("the lock is still held after a clean shutdown; it leaked onto a pooled connection")
	}
	if _, err := conn.ExecContext(context.Background(),
		`SELECT pg_advisory_unlock($1)`, keyRelease); err != nil {
		t.Logf("unlock: %v", err)
	}
}

// holderPID returns the backend pid holding key, failing if nobody does.
func holderPID(t *testing.T, db *sql.DB, key int64) int {
	t.Helper()
	var pid int
	if err := db.QueryRowContext(context.Background(), pidOfHolderSQL, key).Scan(&pid); err != nil {
		t.Fatalf("find advisory lock holder for %#x: %v", key, err)
	}
	return pid
}

// terminateBackend kills one backend, standing in for a hard process exit.
func terminateBackend(t *testing.T, db *sql.DB, pid int) {
	t.Helper()
	var terminated bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil {
		t.Fatalf("terminate backend %d: %v", pid, err)
	}
	if !terminated {
		t.Fatalf("backend %d was not terminated", pid)
	}
}
