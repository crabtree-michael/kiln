package tenant

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// Static sentinels for the error-path tests (err113: no dynamic errors).
var (
	errResolveBoom = errors.New("synthetic resolve failure")
	errBuildBoom   = errors.New("synthetic build failure")
)

// fakeResolver returns a fixed RuntimeConfig for any project id, counting calls.
func fakeResolver(calls *int32) func(context.Context, string) (identity.RuntimeConfig, error) {
	return func(_ context.Context, projectID string) (identity.RuntimeConfig, error) {
		atomic.AddInt32(calls, 1)
		return identity.RuntimeConfig{
			Project:     identity.Project{ID: projectID, WorkerCount: 3},
			OwnerUserID: "owner-" + projectID,
		}, nil
	}
}

// countingBuilder builds a Providers echoing the resolved config, counting calls.
func countingBuilder(calls *int32) Builder {
	return func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		atomic.AddInt32(calls, 1)
		return &Providers{
			ProjectID:   rc.Project.ID,
			OwnerUserID: rc.OwnerUserID,
			WorkerCount: rc.Project.WorkerCount,
		}, nil
	}
}

func TestFor_BuildsOnceAndCaches(t *testing.T) {
	var resolves, builds int32
	r := New(fakeResolver(&resolves), countingBuilder(&builds))

	first, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("first For: %v", err)
	}
	if first.ProjectID != "P1" || first.OwnerUserID != "owner-P1" || first.WorkerCount != 3 {
		t.Fatalf("unexpected providers: %+v", first)
	}

	second, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("second For: %v", err)
	}
	if first != second {
		t.Fatalf("cache miss: want same *Providers pointer, got %p and %p", first, second)
	}
	if builds != 1 {
		t.Fatalf("want 1 build, got %d", builds)
	}
	if resolves != 1 {
		t.Fatalf("want 1 resolve, got %d", resolves)
	}
}

func TestFor_DistinctProjectsBuildIndependently(t *testing.T) {
	var builds int32
	r := New(fakeResolver(new(int32)), countingBuilder(&builds))

	a, err := r.For(context.Background(), "A")
	if err != nil {
		t.Fatalf("For A: %v", err)
	}
	b, err := r.For(context.Background(), "B")
	if err != nil {
		t.Fatalf("For B: %v", err)
	}
	if a.ProjectID != "A" || b.ProjectID != "B" {
		t.Fatalf("wrong ids: %s %s", a.ProjectID, b.ProjectID)
	}
	if builds != 2 {
		t.Fatalf("want 2 builds for 2 projects, got %d", builds)
	}
}

func TestInvalidate_ForcesRebuild(t *testing.T) {
	var builds int32
	r := New(fakeResolver(new(int32)), countingBuilder(&builds))

	first, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("first For: %v", err)
	}
	r.Invalidate("P1")
	second, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("second For: %v", err)
	}

	if builds != 2 {
		t.Fatalf("want 2 builds after invalidate, got %d", builds)
	}
	if first == second {
		t.Fatalf("want a freshly built *Providers after invalidate, got the cached one")
	}
}

func TestInvalidate_UncachedIsNoOp(t *testing.T) {
	var builds int32
	r := New(fakeResolver(new(int32)), countingBuilder(&builds))
	r.Invalidate("never-cached") // must not panic
	if builds != 0 {
		t.Fatalf("Invalidate must not build; got %d builds", builds)
	}
}

func TestFor_ResolveErrorNotCached(t *testing.T) {
	var attempts int32
	resolve := func(_ context.Context, _ string) (identity.RuntimeConfig, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return identity.RuntimeConfig{}, errResolveBoom
		}
		return identity.RuntimeConfig{Project: identity.Project{ID: "P1"}}, nil
	}
	var builds int32
	r := New(resolve, countingBuilder(&builds))

	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errResolveBoom) {
		t.Fatalf("want sentinel resolve error, got %v", err)
	}
	if builds != 0 {
		t.Fatalf("build must not run when resolve fails; got %d", builds)
	}
	// Second call retries (not cached) and now succeeds.
	if _, err := r.For(context.Background(), "P1"); err != nil {
		t.Fatalf("retry after resolve error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("want 2 resolve attempts (error not cached), got %d", attempts)
	}
}

func TestFor_BuildErrorNotCached(t *testing.T) {
	var builds int32
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		if atomic.AddInt32(&builds, 1) == 1 {
			return nil, errBuildBoom
		}
		return &Providers{ProjectID: rc.Project.ID}, nil
	}
	r := New(fakeResolver(new(int32)), build)

	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errBuildBoom) {
		t.Fatalf("want sentinel build error, got %v", err)
	}
	// A failed build is not cached: the next For retries and succeeds.
	p, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("retry after build error: %v", err)
	}
	if p.ProjectID != "P1" {
		t.Fatalf("unexpected providers after retry: %+v", p)
	}
	if builds != 2 {
		t.Fatalf("want 2 build attempts (error not cached), got %d", builds)
	}
}

func TestFor_ConcurrentBuildsOnce(t *testing.T) {
	var builds int32
	// Gate the build so all goroutines pile up on the single-flight lock before
	// the first build completes — the strongest test that they collapse to one.
	release := make(chan struct{})
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		atomic.AddInt32(&builds, 1)
		<-release
		return &Providers{ProjectID: rc.Project.ID}, nil
	}
	r := New(fakeResolver(new(int32)), build)

	const n = 10
	var wg sync.WaitGroup
	results := make([]*Providers, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			p, err := r.For(context.Background(), "P1")
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			results[i] = p
		}(i)
	}
	close(release)
	wg.Wait()

	if builds != 1 {
		t.Fatalf("want exactly 1 build under concurrency, got %d", builds)
	}
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("goroutine %d saw a different *Providers than goroutine 0", i)
		}
	}
}
