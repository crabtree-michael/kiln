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
// loop fires on a status change — and records the per-project errored-worker set
// the loop reports to the board for the health-aware pull.
type fakeRefresher struct {
	mu     sync.Mutex
	n      int
	health map[string][]string // project id -> last-reported errored worker ids
}

func (f *fakeRefresher) RefreshBoard(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return nil
}

func (f *fakeRefresher) SetWorkerHealth(_ context.Context, projectID string, erroredWorkerIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.health == nil {
		f.health = map[string][]string{}
	}
	f.health[projectID] = append([]string(nil), erroredWorkerIDs...)
	return nil
}

func (f *fakeRefresher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

// erroredFor returns the errored-worker set last reported for the project, and
// whether any report has landed yet. Keyed by project to mirror the per-project
// health map, though every test so far is single-tenant on testProject.
//
//nolint:unparam // projectID mirrors the project-keyed health map; kept general.
func (f *fakeRefresher) erroredFor(projectID string) ([]string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids, ok := f.health[projectID]
	return append([]string(nil), ids...), ok
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

// TestRun_ErroredWorkerReportedToBoardHealth pins the health-aware-pull sync:
// the liveness loop reports each project's errored worker ids to the board, so
// the pull can skip failing sandboxes. A healthy pool reports an empty set; an
// errored sandbox is named.
func TestRun_ErroredWorkerReportedToBoardHealth(t *testing.T) {
	store := newFakeStore()
	provider := mock.New()
	clock := testutil.NewFakeClock()
	refresher := &fakeRefresher{}
	svc := newService(store, provider, &fakeEvents{}, &fakeSlots{ids: []string{testWorkerID}}, clock, refresher)

	stop := runService(t, svc, clock)
	defer stop()

	// The reconciler brings the slot's worker up healthy: the first tick reports
	// an empty errored set for the project.
	testutil.Eventually(t, func() bool {
		ids, reported := refresher.erroredFor(testProject)
		return reported && len(ids) == 0
	})

	// The sandbox fails terminally: the next tick names it in the errored set.
	provider.SetWorkerStatus(agent.WorkerName(testWorkerID), agent.RunErrored)
	testutil.Eventually(t, func() bool {
		ids, _ := refresher.erroredFor(testProject)
		return len(ids) == 1 && ids[0] == testWorkerID
	})
}

// TestRun_CreateFailureReportedToBoardHealth pins the create-failure health gate:
// a slot whose sandbox never provisions (CreateWorker keeps failing) leaves no
// live sandbox to observe, yet the health-aware pull must still learn the slot is
// unusable. The reconciler records the provisioning failure and the liveness loop
// reports the slot errored — so the pull skips it and the sandbox_health alert
// fires — instead of the pre-fix behaviour where a never-provisioned slot stayed
// silently 'ok' and Ready tickets bound to a dead sandbox.
func TestRun_CreateFailureReportedToBoardHealth(t *testing.T) {
	store := newFakeStore()
	provider := mock.New()
	provider.FailProvisioning = true // CreateWorker fails: no sandbox ever comes live
	clock := testutil.NewFakeClock()
	refresher := &fakeRefresher{}
	svc := newService(store, provider, &fakeEvents{}, &fakeSlots{ids: []string{testWorkerID}}, clock, refresher)

	stop := runService(t, svc, clock)
	defer stop()

	// No live sandbox exists to report errored, but the failed provision must still
	// mark the slot errored for the pull.
	testutil.Eventually(t, func() bool {
		ids, _ := refresher.erroredFor(testProject)
		return len(ids) == 1 && ids[0] == testWorkerID
	})

	// Provisioning recovers: the reconciler creates the sandbox and the slot flips
	// back to healthy (empty errored set), so the pull can bind it again.
	provider.SetFailProvisioning(false)
	testutil.Eventually(t, func() bool {
		ids, reported := refresher.erroredFor(testProject)
		return reported && len(ids) == 0
	})
}
