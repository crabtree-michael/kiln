package agent_test

// Auto-rotating worker names (05 §4): the permanent fix for the Amika
// sandbox_name_conflict (409) pool wedge. A slot's sandbox name carries a stateless
// generation suffix derived from the live list each sweep, so a squatting failed
// record never wedges the slot — the reconciler rebuilds it at the next generation
// under a fresh name, and the create seam rotates on a 409 as belt-and-suspenders.
// These reuse reconcileProvider (reconcile_test.go), which can pre-seed live workers
// and inject name conflicts.

import (
	"strconv"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/testutil"
)

// genName is the provider-side name of the test slot's sandbox at a generation,
// under the default prefix the single-tenant test service uses: gen 0 is the legacy
// bare name, gen ≥1 gets the "-g<n>" suffix. All rotation tests reconcile the single
// slot reconcileWorkerA, so the id is fixed here.
func genName(gen int) string {
	if gen == 0 {
		return agent.WorkerName(reconcileWorkerA)
	}
	return agent.WorkerName(reconcileWorkerA) + "-g" + strconv.Itoa(gen)
}

// A slot whose only live sandbox is a terminally-failed gen-0 record must be rebuilt
// at gen 1 (a fresh name that routes around the squat), and the failed record swept.
func TestRun_RotatesPastSquattingFailedRecord(t *testing.T) {
	failed0 := agent.ProviderWorker{Name: genName(0), Ref: "failed-0", Status: agent.RunErrored}
	provider := newReconcileProvider(failed0)
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	svc := newService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	// gen 1 is created under a fresh name...
	testutil.Eventually(t, func() bool { return provider.wasCreated(genName(1)) })
	// ...and the squatting failed gen-0 record is destroyed as stale.
	testutil.Eventually(t, func() bool { return provider.wasDestroyed(genName(0)) })
}

// Given both a failed gen-0 record and a live gen-1 for the same slot, adopt the
// highest generation that is not errored (gen 1) and destroy the stale gen-0 — never
// recreate.
func TestRun_AdoptsHighestGenerationAndDestroysStale(t *testing.T) {
	failed0 := agent.ProviderWorker{Name: genName(0), Ref: "failed-0", Status: agent.RunErrored}
	live1 := agent.ProviderWorker{Name: genName(1), Ref: "live-1", Status: agent.RunReady}
	provider := newReconcileProvider(failed0, live1)
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	svc := newService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	testutil.Eventually(t, func() bool { return provider.wasDestroyed(genName(0)) })

	// Let a few more sweeps run, then confirm gen 1 was adopted (never recreated,
	// never swept) — adopt-first over the highest healthy generation (05 §4).
	time.Sleep(150 * time.Millisecond)
	if provider.createdCount() != 0 {
		t.Errorf("adopting the live gen-1 must not create any worker, but %d create(s) happened", provider.createdCount())
	}
	if provider.wasDestroyed(genName(1)) {
		t.Errorf("the adopted highest generation (gen 1) must not be destroyed")
	}
}

// Back-compat: a live legacy-named (gen-0 bare) sandbox is adopted, NEVER recreated
// or destroyed — the deploy must not churn the existing pool.
func TestRun_BackCompatAdoptsLegacyNameWithoutChurn(t *testing.T) {
	legacy := agent.ProviderWorker{Name: genName(0), Ref: "legacy", Status: agent.RunReady}
	provider := newReconcileProvider(legacy)
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	svc := newService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	// Give several sweeps a chance to run, then confirm the legacy worker was left
	// exactly as-is.
	time.Sleep(150 * time.Millisecond)
	if provider.createdCount() != 0 {
		t.Errorf("a live legacy gen-0 worker must be adopted, not recreated; got %d create(s)", provider.createdCount())
	}
	if provider.wasDestroyed(genName(0)) {
		t.Errorf("a live legacy gen-0 worker must not be destroyed on adoption")
	}
}

// The belt-and-suspenders create seam: CreateWorker returning ErrNameConflict makes
// the service retry at the next generation until one succeeds. Two conflicts (gen 0,
// gen 1) then success at gen 2.
func TestRun_CreateNameConflictRotatesToNextGeneration(t *testing.T) {
	provider := newReconcileProvider() // no live sandboxes at all
	provider.conflictsLeft = 2
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	svc := newService(newFakeStore(), provider, &fakeEvents{}, slots, clock, nil)

	stop := runService(t, svc, clock)
	defer stop()

	// gen 0 and gen 1 conflict; gen 2 is created under a name nothing squats.
	testutil.Eventually(t, func() bool { return provider.wasCreated(genName(2)) })
	if provider.wasCreated(genName(0)) || provider.wasCreated(genName(1)) {
		t.Errorf("gen 0 and gen 1 conflicted, so neither may be recorded as created")
	}
}

// Exhaustion: when every CreateWorker conflicts past the bounded number of attempts,
// the create gives up and the slot is recorded errored (reported to board health),
// exactly as a provision failure is today.
func TestRun_CreateNameConflictExhaustsAndRecordsError(t *testing.T) {
	provider := newReconcileProvider()
	provider.alwaysConflict = true
	slots := &fakeSlots{ids: []string{reconcileWorkerA}}
	clock := testutil.NewFakeClock()
	refresher := &fakeRefresher{}
	svc := newService(newFakeStore(), provider, &fakeEvents{}, slots, clock, refresher)

	stop := runService(t, svc, clock)
	defer stop()

	// No generation ever creates, so the slot is health-gated out of the pull.
	testutil.Eventually(t, func() bool {
		ids, _ := refresher.erroredFor(testProject)
		return len(ids) == 1 && ids[0] == reconcileWorkerA
	})
	if provider.createdCount() != 0 {
		t.Errorf("every create conflicted, so no worker may be recorded as created; got %d", provider.createdCount())
	}
}
