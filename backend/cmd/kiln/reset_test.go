package main

// Unit tests for resetCoordinator: it deletes the caller's project state, tears
// down that project's workers, then re-seeds its worker pool — in that order (so
// teardown happens while the wanted set is empty and the fresh session comes
// back up with capacity) — and surfaces any step's error. Every step must carry
// the caller's projectID so a reset never touches another tenant (11 §3). The
// real DB delete and pool re-seed are verified live (reset_integration_test.go);
// this pins ordering, projectID threading, and error propagation against fakes.

import (
	"context"
	"errors"
	"testing"
)

var (
	errFakeDelete      = errors.New("synthetic state delete failure")
	errFakeWorkers     = errors.New("synthetic worker teardown failure")
	errFakeWorkerCount = errors.New("synthetic worker-count lookup failure")
)

// testProjectID is the caller project every resetCoordinator unit test threads;
// each destructive step must carry exactly this id.
const testProjectID = "proj-1"

// Step names the reset/delete coordinator fakes append to their shared order
// slice, so a test can assert the exact cascade order (shared to keep goconst
// happy across the two coordinator test files).
const (
	stepDelete     = "delete"
	stepWorkers    = "workers"
	stepPool       = "pool"
	stepAuthorize  = "authorize"
	stepEvict      = "evict"
	stepSoftDelete = "soft-delete"
)

type fakeStateDeleter struct {
	calls     int
	projectID string
	order     *[]string
	err       error
}

func (f *fakeStateDeleter) DeleteProjectState(_ context.Context, projectID string) error {
	f.calls++
	f.projectID = projectID
	*f.order = append(*f.order, stepDelete)
	return f.err
}

type fakeWorkerResetter struct {
	calls     int
	projectID string
	order     *[]string
	err       error
}

func (f *fakeWorkerResetter) ResetProject(_ context.Context, projectID string) error {
	f.calls++
	f.projectID = projectID
	*f.order = append(*f.order, stepWorkers)
	return f.err
}

type fakePoolReconciler struct {
	calls     int
	n         int
	projectID string
	order     *[]string
	err       error
}

func (f *fakePoolReconciler) ReconcileWorkers(_ context.Context, projectID string, n int) error {
	f.calls++
	f.n = n
	f.projectID = projectID
	*f.order = append(*f.order, stepPool)
	return f.err
}

func TestResetCoordinator_DeletesTearsDownThenReseeds(t *testing.T) {
	var order []string
	sd := &fakeStateDeleter{order: &order}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{state: sd, workers: wr, pool: pool, defaultPoolSize: 3}

	if err := c.Reset(context.Background(), testProjectID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if want := []string{stepDelete, stepWorkers, stepPool}; len(order) != 3 ||
		order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order = %v, want %v", order, want)
	}
	if pool.n != 3 {
		t.Errorf("pool re-seeded to %d, want configured 3", pool.n)
	}
	// Every destructive/reseed step must be scoped to the caller's project.
	if sd.projectID != testProjectID {
		t.Errorf("state deleted for project %q, want the reset's project proj-1", sd.projectID)
	}
	if wr.projectID != testProjectID {
		t.Errorf("workers reset for project %q, want the reset's project proj-1", wr.projectID)
	}
	if pool.projectID != testProjectID {
		t.Errorf("pool re-seeded for project %q, want the reset's project proj-1", pool.projectID)
	}
}

func TestResetCoordinator_ReseedsToConfiguredWorkerCount(t *testing.T) {
	var order []string
	sd := &fakeStateDeleter{order: &order}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	// The project's dashboard setting (7) must win over the deployment default (3).
	c := &resetCoordinator{
		state: sd, workers: wr, pool: pool, defaultPoolSize: 3,
		workerCountFor: func(_ context.Context, projectID string) (int, error) {
			if projectID != testProjectID {
				t.Errorf("worker count resolved for %q, want %q", projectID, testProjectID)
			}
			return 7, nil
		},
	}

	if err := c.Reset(context.Background(), testProjectID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if pool.n != 7 {
		t.Errorf("pool re-seeded to %d, want the configured 7", pool.n)
	}
}

func TestResetCoordinator_ResolverError_FallsBackToDefault(t *testing.T) {
	var order []string
	sd := &fakeStateDeleter{order: &order}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	// A resolver failure (or a non-positive count) must not fail the reset — it
	// re-seeds to the deployment default so the session comes back with capacity.
	c := &resetCoordinator{
		state: sd, workers: wr, pool: pool, defaultPoolSize: 3,
		workerCountFor: func(_ context.Context, _ string) (int, error) {
			return 0, errFakeWorkerCount
		},
	}

	if err := c.Reset(context.Background(), testProjectID); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if pool.n != 3 {
		t.Errorf("pool re-seeded to %d, want the default 3 fallback", pool.n)
	}
}

func TestResetCoordinator_DeleteError_SkipsRest(t *testing.T) {
	var order []string
	sd := &fakeStateDeleter{order: &order, err: errFakeDelete}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{state: sd, workers: wr, pool: pool, defaultPoolSize: 3}

	if err := c.Reset(context.Background(), testProjectID); err == nil {
		t.Fatal("expected error when state delete fails")
	}
	if wr.calls != 0 || pool.calls != 0 {
		t.Errorf("nothing should run after a state-delete failure, got workers=%d pool=%d", wr.calls, pool.calls)
	}
}

func TestResetCoordinator_WorkerError_SkipsReseed(t *testing.T) {
	var order []string
	sd := &fakeStateDeleter{order: &order}
	wr := &fakeWorkerResetter{order: &order, err: errFakeWorkers}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{state: sd, workers: wr, pool: pool, defaultPoolSize: 3}

	if err := c.Reset(context.Background(), testProjectID); err == nil {
		t.Fatal("expected error when worker teardown fails")
	}
	if sd.calls != 1 {
		t.Errorf("state delete should still have run, got %d calls", sd.calls)
	}
	if pool.calls != 0 {
		t.Errorf("pool should not be re-seeded after a teardown failure, got %d calls", pool.calls)
	}
}
