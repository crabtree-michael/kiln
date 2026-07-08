// Package tenant is the per-project provider cache at the heart of the
// multi-tenant flip (spec §6). For(ctx, projectID) lazily builds one project's
// runtime providers — brain, agent provider, worker seeding — from its owner's
// decrypted identity.RuntimeConfig, caches the result, and hands the same
// bundle to every subsequent event for that project. Invalidate(projectID)
// drops the cached entry so a corrected dashboard credential takes effect on the
// next event with no restart.
//
// The concrete build closure (per-project brain, Amika client, repo shell, and
// board ReconcileWorkers seeding) lives in cmd/kiln; the registry only knows how
// to resolve a config, run that closure once per project, and cache it.
package tenant

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// Providers is one project's assembled runtime dependencies. Brain is typed
// `any` to avoid a dependency on the runtime/brain packages (they depend on the
// wiring closure, not the other way around); cmd/kiln binds it to the concrete
// runtime.Brain when it constructs the Builder.
type Providers struct {
	ProjectID    string
	OwnerUserID  string
	WorkerPrefix string
	WorkerCount  int
	Brain        any // concretely runtime.Brain, bound by the cmd/kiln closure
	Agent        agent.Provider
}

// Close tears down a bundle's per-project resources when it is evicted or
// superseded (11 §3): whichever of Agent/Brain implements io.Closer is closed
// (the Amika client releases its per-tenant HTTP connection pool; the mock
// provider is not a Closer and is skipped). It is the teardown seam a credential
// rebuild uses so a replaced bundle does not abandon live per-tenant resources.
// Idempotent and nil-safe: the per-tenant HTTP pool's CloseIdleConnections only
// reaps idle keep-alives, so Close is safe even while an in-flight event still
// holds the bundle. The on-disk repo clone is deliberately NOT removed — it lives
// at RepoDir/<projectID> and is reused across rebuilds (removing it would force a
// fresh clone on every credential edit).
func (p *Providers) Close() {
	if p == nil {
		return
	}
	for _, dep := range []any{p.Agent, p.Brain} {
		c, ok := dep.(io.Closer)
		if !ok {
			continue
		}
		if err := c.Close(); err != nil {
			// Best-effort teardown: a failed idle-pool close is logged, not
			// propagated — the bundle is being discarded regardless.
			slog.Warn("tenant: close provider bundle resource", "project_id", p.ProjectID, "err", err)
		}
	}
}

// Resolver fetches a project's decrypted runtime config — satisfied by
// (*identity.Service).RuntimeConfig.
type Resolver func(ctx context.Context, projectID string) (identity.RuntimeConfig, error)

// Builder assembles a project's Providers from its resolved config. It is
// supplied by cmd/kiln and is where the per-project brain, Amika client, repo
// shell, and board worker seeding are constructed. A returned error is NOT
// cached — the next For retries the build.
type Builder func(ctx context.Context, rc identity.RuntimeConfig) (*Providers, error)

// failureBackoff is how long a project whose resolve/build failed is served its
// cached error before For re-attempts the (expensive) resolve + repo clone
// (11 §3). It bounds the "persistently-broken project re-runs the full resolve on
// every incoming event" cost without stalling recovery: a corrected credential
// goes through identity → Invalidate, which clears the backoff so the very next
// event rebuilds immediately (see Invalidate).
const failureBackoff = 30 * time.Second

// failure records a project's last resolve/build error and when it happened, so
// For can short-circuit repeated attempts within failureBackoff.
type failure struct {
	at  time.Time
	err error
}

// Registry is the per-project provider cache. For is single-flight per project:
// concurrent calls for the same, uncached project build exactly once and share
// the result. It is safe for concurrent use.
type Registry struct {
	resolve Resolver
	build   Builder
	now     func() time.Time // injectable clock; time.Now in production

	mu    sync.Mutex            // guards cache, keyLocks, gen, and failures
	cache map[string]*Providers // built providers, keyed by project id
	// keyLocks serializes concurrent builds of the same project so two events
	// don't build twice (single-flight per key).
	keyLocks map[string]*sync.Mutex
	// gen is a per-project generation counter bumped by Invalidate. For captures
	// it before an (unlocked) build and compare-and-swaps on store, so an
	// Invalidate that races an in-flight build is never lost (the stale build is
	// not cached) — the TOCTOU the bare delete(cache) had before.
	gen map[string]uint64
	// failures records the last resolve/build error per project for the
	// failureBackoff short-circuit; cleared on success or Invalidate.
	failures map[string]failure
}

// New constructs a Registry over a config resolver and a build closure.
func New(resolve Resolver, build Builder) *Registry {
	return &Registry{
		resolve:  resolve,
		build:    build,
		now:      time.Now,
		cache:    make(map[string]*Providers),
		keyLocks: make(map[string]*sync.Mutex),
		gen:      make(map[string]uint64),
		failures: make(map[string]failure),
	}
}

// For returns the project's cached Providers, building (and caching) them on the
// first call. Single-flight per project: concurrent callers for the same
// uncached project collapse onto one build. Neither a resolve nor a build error
// is cached — a later For retries.
func (r *Registry) For(ctx context.Context, projectID string) (*Providers, error) {
	// Fast path: already cached, or within a recent-failure backoff window.
	r.mu.Lock()
	if p, ok := r.cache[projectID]; ok {
		r.mu.Unlock()
		return p, nil
	}
	if err := r.backoffErrLocked(projectID); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	// Take (or create) this project's build lock so concurrent For calls for the
	// same project serialize and build once.
	kl := r.keyLockLocked(projectID)
	r.mu.Unlock()

	kl.Lock()
	defer kl.Unlock()

	// Re-check under the key lock: an earlier holder may have built it while we
	// waited (or recorded a fresh failure). Capture the project's build
	// generation here, under the key lock, so a concurrent Invalidate during the
	// resolve+build below is detected at store time (compare-and-swap).
	r.mu.Lock()
	if p, ok := r.cache[projectID]; ok {
		r.mu.Unlock()
		return p, nil
	}
	if err := r.backoffErrLocked(projectID); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	startGen := r.gen[projectID]
	r.mu.Unlock()

	rc, err := r.resolve(ctx, projectID)
	if err != nil {
		r.recordFailure(projectID, startGen, err) // backed off, next For within the window short-circuits
		return nil, err
	}
	p, err := r.build(ctx, rc)
	if err != nil {
		r.recordFailure(projectID, startGen, err)
		return nil, err
	}

	r.mu.Lock()
	r.clearFailureLocked(projectID)
	if r.gen[projectID] != startGen {
		// Superseded: an Invalidate landed during resolve+build. Do NOT cache the
		// now-stale bundle — the next For rebuilds from the corrected config. The
		// bundle is still returned for this event's use; Close only reaps its idle
		// connections, so it stays usable for the in-flight call.
		r.mu.Unlock()
		p.Close()
		return p, nil
	}
	// Defensive: nothing should be cached under the held key lock, but if it is,
	// tear it down before replacing so we never abandon a live bundle.
	if old, ok := r.cache[projectID]; ok && old != p {
		old.Close()
	}
	r.cache[projectID] = p
	r.mu.Unlock()
	return p, nil
}

// Invalidate drops the cached Providers for a project so the next For rebuilds
// from a freshly resolved config, and tears the evicted bundle's per-tenant
// resources down. Called by identity's SetInvalidator hook after a dashboard
// credential write. Bumping the generation makes an Invalidate that races an
// in-flight build win: that build's store is dropped (see For). Clearing any
// recorded failure lets a corrected credential rebuild on the very next event
// instead of waiting out failureBackoff. A no-op (beyond the generation bump)
// for an uncached project.
func (r *Registry) Invalidate(projectID string) {
	r.mu.Lock()
	r.gen[projectID]++
	old := r.cache[projectID]
	delete(r.cache, projectID)
	delete(r.failures, projectID)
	r.mu.Unlock()
	old.Close() // nil-safe
}

// Close tears down every cached bundle — the shutdown counterpart to Invalidate,
// releasing all per-tenant resources when the process drains.
func (r *Registry) Close() {
	r.mu.Lock()
	bundles := make([]*Providers, 0, len(r.cache))
	for id, p := range r.cache {
		bundles = append(bundles, p)
		delete(r.cache, id)
	}
	r.mu.Unlock()
	for _, p := range bundles {
		p.Close()
	}
}

// keyLockLocked returns (creating if needed) a project's single-flight build
// lock. Caller holds r.mu.
func (r *Registry) keyLockLocked(projectID string) *sync.Mutex {
	kl, ok := r.keyLocks[projectID]
	if !ok {
		kl = &sync.Mutex{}
		r.keyLocks[projectID] = kl
	}
	return kl
}

// backoffErrLocked returns a project's cached failure error while it is still
// within the failureBackoff window, else nil. Caller holds r.mu.
func (r *Registry) backoffErrLocked(projectID string) error {
	f, ok := r.failures[projectID]
	if !ok {
		return nil
	}
	if r.now().Sub(f.at) < failureBackoff {
		return f.err
	}
	return nil
}

// recordFailure remembers a resolve/build error so For short-circuits repeated
// attempts within failureBackoff — unless an Invalidate superseded this attempt
// (generation moved), in which case a fresh config is already pending and must
// not be shadowed by a stale backoff.
func (r *Registry) recordFailure(projectID string, startGen uint64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen[projectID] != startGen {
		return
	}
	r.failures[projectID] = failure{at: r.now(), err: err}
}

// clearFailureLocked drops a project's recorded failure. Caller holds r.mu.
func (r *Registry) clearFailureLocked(projectID string) {
	delete(r.failures, projectID)
}
