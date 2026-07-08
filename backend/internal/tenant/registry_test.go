package tenant

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// Static sentinels for the error-path tests (err113: no dynamic errors).
var (
	errResolveBoom = errors.New("synthetic resolve failure")
	errBuildBoom   = errors.New("synthetic build failure")
)

// closeSpy is a trivial io.Closer used to observe Providers/registry teardown.
// Placed on Providers.Brain (typed any) so tests don't need a full agent.Provider.
type closeSpy struct{ closed atomic.Int32 }

func (c *closeSpy) Close() error {
	c.closed.Add(1)
	return nil
}

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

func TestFor_ResolveErrorBacksOffThenRetries(t *testing.T) {
	var attempts int32
	resolve := func(_ context.Context, _ string) (identity.RuntimeConfig, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return identity.RuntimeConfig{}, errResolveBoom
		}
		return identity.RuntimeConfig{Project: identity.Project{ID: "P1"}}, nil
	}
	var builds int32
	r := New(resolve, countingBuilder(&builds))
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errResolveBoom) {
		t.Fatalf("want sentinel resolve error, got %v", err)
	}
	if builds != 0 {
		t.Fatalf("build must not run when resolve fails; got %d", builds)
	}
	// A second call inside the backoff window is served the cached error without
	// re-running the (expensive) resolve.
	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errResolveBoom) {
		t.Fatalf("want backed-off resolve error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("resolve re-ran inside the backoff window: attempts=%d, want 1", attempts)
	}
	// Past the window it re-attempts and now succeeds.
	now = now.Add(failureBackoff + time.Second)
	if _, err := r.For(context.Background(), "P1"); err != nil {
		t.Fatalf("retry after backoff window: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("want 2 resolve attempts (retry after window), got %d", attempts)
	}
}

func TestFor_BuildErrorBacksOffThenRetries(t *testing.T) {
	var builds int32
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		if atomic.AddInt32(&builds, 1) == 1 {
			return nil, errBuildBoom
		}
		return &Providers{ProjectID: rc.Project.ID}, nil
	}
	r := New(fakeResolver(new(int32)), build)
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now }

	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errBuildBoom) {
		t.Fatalf("want sentinel build error, got %v", err)
	}
	// Inside the window: the failed build is not re-attempted.
	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errBuildBoom) {
		t.Fatalf("want backed-off build error, got %v", err)
	}
	if builds != 1 {
		t.Fatalf("build re-ran inside the backoff window: builds=%d, want 1", builds)
	}
	// Past the window it re-attempts and succeeds.
	now = now.Add(failureBackoff + time.Second)
	p, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("retry after backoff window: %v", err)
	}
	if p.ProjectID != "P1" {
		t.Fatalf("unexpected providers after retry: %+v", p)
	}
	if builds != 2 {
		t.Fatalf("want 2 build attempts (retry after window), got %d", builds)
	}
}

// TestInvalidate_ClearsFailureBackoff pins that a corrected credential (which
// fires Invalidate) rebuilds on the very next event rather than waiting out the
// failureBackoff window.
func TestInvalidate_ClearsFailureBackoff(t *testing.T) {
	var attempts int32
	resolve := func(_ context.Context, _ string) (identity.RuntimeConfig, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			return identity.RuntimeConfig{}, errResolveBoom
		}
		return identity.RuntimeConfig{Project: identity.Project{ID: "P1"}}, nil
	}
	r := New(resolve, countingBuilder(new(int32)))
	now := time.Unix(0, 0)
	r.now = func() time.Time { return now } // clock never advances

	if _, err := r.For(context.Background(), "P1"); !errors.Is(err, errResolveBoom) {
		t.Fatalf("want resolve error, got %v", err)
	}
	r.Invalidate("P1") // credential fix clears the backoff
	if _, err := r.For(context.Background(), "P1"); err != nil {
		t.Fatalf("For after Invalidate should skip the backoff: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("want an immediate retry after Invalidate: attempts=%d, want 2", attempts)
	}
}

// TestInvalidate_DuringBuildDropsStaleBuild pins the TOCTOU fix: an Invalidate
// that lands while a build is in flight must not be lost — the stale build is
// not cached and the next For rebuilds from the corrected config.
func TestInvalidate_DuringBuildDropsStaleBuild(t *testing.T) {
	var builds int32
	entered := make(chan struct{})
	release := make(chan struct{})
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		if atomic.AddInt32(&builds, 1) == 1 {
			close(entered)
			<-release // park the first build so Invalidate can race it
		}
		return &Providers{ProjectID: rc.Project.ID}, nil
	}
	r := New(fakeResolver(new(int32)), build)

	type result struct {
		p   *Providers
		err error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := r.For(context.Background(), "P1")
		ch <- result{p, err}
	}()
	<-entered

	r.Invalidate("P1") // lands mid-build: bumps the generation

	close(release)
	first := <-ch
	if first.err != nil {
		t.Fatalf("first For: %v", first.err)
	}

	second, err := r.For(context.Background(), "P1")
	if err != nil {
		t.Fatalf("second For: %v", err)
	}
	if second == first.p {
		t.Fatalf("stale build was cached despite an Invalidate racing it")
	}
	if builds != 2 {
		t.Fatalf("want 2 builds (superseded + rebuild), got %d", builds)
	}
}

// TestInvalidate_ClosesEvictedBundle pins that evicting a cached bundle tears its
// per-tenant resources down (the Providers.Close teardown seam).
func TestInvalidate_ClosesEvictedBundle(t *testing.T) {
	spy := &closeSpy{}
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		return &Providers{ProjectID: rc.Project.ID, Brain: spy}, nil
	}
	r := New(fakeResolver(new(int32)), build)
	if _, err := r.For(context.Background(), "P1"); err != nil {
		t.Fatalf("For: %v", err)
	}
	r.Invalidate("P1")
	if got := spy.closed.Load(); got != 1 {
		t.Fatalf("Invalidate did not Close the evicted bundle: closed=%d, want 1", got)
	}
}

// TestClose_ClosesAllCachedBundles pins the shutdown teardown path.
func TestClose_ClosesAllCachedBundles(t *testing.T) {
	spies := map[string]*closeSpy{"A": {}, "B": {}}
	build := func(_ context.Context, rc identity.RuntimeConfig) (*Providers, error) {
		return &Providers{ProjectID: rc.Project.ID, Brain: spies[rc.Project.ID]}, nil
	}
	r := New(fakeResolver(new(int32)), build)
	for _, id := range []string{"A", "B"} {
		if _, err := r.For(context.Background(), id); err != nil {
			t.Fatalf("For %s: %v", id, err)
		}
	}
	r.Close()
	for id, s := range spies {
		if got := s.closed.Load(); got != 1 {
			t.Fatalf("Close did not close bundle %s: closed=%d, want 1", id, got)
		}
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
