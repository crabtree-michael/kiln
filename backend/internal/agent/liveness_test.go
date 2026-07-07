package agent_test

// Liveness-poll tests for the status loop (amended 2026-07-05): a silent
// sandbox auto-stop fires no event, so the agent runtime re-reads worker
// liveness on a timer and nudges the board to re-push when it changes — that is
// what makes a dead session visible in Streams without a manual nudge. These
// drive the real Run loop through the shared fake clock (runService pumps it),
// so they exercise the actual goroutine, not the unexported step in isolation.

import (
	"context"
	"sync"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// fakeRefresher counts RefreshBoard calls — the board-push nudge the liveness
// loop fires on a status change.
type fakeRefresher struct {
	mu sync.Mutex
	n  int
}

func (f *fakeRefresher) RefreshBoard(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return nil
}

func (f *fakeRefresher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func TestRun_LivenessChangeNudgesBoard(t *testing.T) {
	store := newFakeStore()
	provider := mock.New()
	clock := testutil.NewFakeClock()
	refresher := &fakeRefresher{}
	svc := newService(store, provider, &fakeEvents{}, &fakeSlots{ids: []string{testWorkerID}}, clock, refresher)

	stop := runService(t, svc, clock)
	defer stop()

	// The reconciler brings the slot's worker up; the next liveness tick sees the
	// pool go from empty to one live worker — a change — and nudges the board.
	testutil.Eventually(t, func() bool { return refresher.count() >= 1 })

	// A silent stop: flip the live sandbox to stopped. The loop must detect the
	// status change and push again.
	before := refresher.count()
	provider.SetWorkerStatus(agent.WorkerName(testWorkerID), agent.RunStopped)
	testutil.Eventually(t, func() bool { return refresher.count() > before })
}
