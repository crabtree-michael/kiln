package main

// Unit tests for resetCoordinator: it truncates state, tears down workers, then
// re-seeds the worker pool — in that order (so teardown happens while the
// wanted set is empty and the fresh session comes back up with capacity) — and
// surfaces any step's error. The real DB truncate and pool re-seed are verified
// live; this pins ordering and error propagation against fakes.

import (
	"context"
	"errors"
	"testing"
)

var (
	errFakeTruncate = errors.New("synthetic truncate failure")
	errFakeWorkers  = errors.New("synthetic worker teardown failure")
)

type fakeTruncator struct {
	calls int
	order *[]string
	err   error
}

func (f *fakeTruncator) TruncateState(context.Context) error {
	f.calls++
	*f.order = append(*f.order, "truncate")
	return f.err
}

type fakeWorkerResetter struct {
	calls int
	order *[]string
	err   error
}

func (f *fakeWorkerResetter) Reset(context.Context) error {
	f.calls++
	*f.order = append(*f.order, "workers")
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
	*f.order = append(*f.order, "pool")
	return f.err
}

func TestResetCoordinator_TruncatesTearsDownThenReseeds(t *testing.T) {
	var order []string
	tr := &fakeTruncator{order: &order}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{tables: tr, workers: wr, pool: pool, poolSize: 3}

	if err := c.Reset(context.Background(), "proj-1"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if want := []string{"truncate", "workers", "pool"}; len(order) != 3 ||
		order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("order = %v, want %v", order, want)
	}
	if pool.n != 3 {
		t.Errorf("pool re-seeded to %d, want configured 3", pool.n)
	}
	if pool.projectID != "proj-1" {
		t.Errorf("pool re-seeded for project %q, want the reset's project proj-1", pool.projectID)
	}
}

func TestResetCoordinator_TruncateError_SkipsRest(t *testing.T) {
	var order []string
	tr := &fakeTruncator{order: &order, err: errFakeTruncate}
	wr := &fakeWorkerResetter{order: &order}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{tables: tr, workers: wr, pool: pool, poolSize: 3}

	if err := c.Reset(context.Background(), "proj-1"); err == nil {
		t.Fatal("expected error when truncate fails")
	}
	if wr.calls != 0 || pool.calls != 0 {
		t.Errorf("nothing should run after a truncate failure, got workers=%d pool=%d", wr.calls, pool.calls)
	}
}

func TestResetCoordinator_WorkerError_SkipsReseed(t *testing.T) {
	var order []string
	tr := &fakeTruncator{order: &order}
	wr := &fakeWorkerResetter{order: &order, err: errFakeWorkers}
	pool := &fakePoolReconciler{order: &order}
	c := &resetCoordinator{tables: tr, workers: wr, pool: pool, poolSize: 3}

	if err := c.Reset(context.Background(), "proj-1"); err == nil {
		t.Fatal("expected error when worker teardown fails")
	}
	if tr.calls != 1 {
		t.Errorf("truncate should still have run, got %d calls", tr.calls)
	}
	if pool.calls != 0 {
		t.Errorf("pool should not be re-seeded after a teardown failure, got %d calls", pool.calls)
	}
}
