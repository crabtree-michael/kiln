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
	"sync"

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

// Resolver fetches a project's decrypted runtime config — satisfied by
// (*identity.Service).RuntimeConfig.
type Resolver func(ctx context.Context, projectID string) (identity.RuntimeConfig, error)

// Builder assembles a project's Providers from its resolved config. It is
// supplied by cmd/kiln and is where the per-project brain, Amika client, repo
// shell, and board worker seeding are constructed. A returned error is NOT
// cached — the next For retries the build.
type Builder func(ctx context.Context, rc identity.RuntimeConfig) (*Providers, error)

// Registry is the per-project provider cache. For is single-flight per project:
// concurrent calls for the same, uncached project build exactly once and share
// the result. It is safe for concurrent use.
type Registry struct {
	resolve Resolver
	build   Builder

	mu    sync.Mutex            // guards cache and keyLocks
	cache map[string]*Providers // built providers, keyed by project id
	// keyLocks serializes concurrent builds of the same project so two events
	// don't build twice (single-flight per key).
	keyLocks map[string]*sync.Mutex
}

// New constructs a Registry over a config resolver and a build closure.
func New(resolve Resolver, build Builder) *Registry {
	return &Registry{
		resolve:  resolve,
		build:    build,
		cache:    make(map[string]*Providers),
		keyLocks: make(map[string]*sync.Mutex),
	}
}

// For returns the project's cached Providers, building (and caching) them on the
// first call. Single-flight per project: concurrent callers for the same
// uncached project collapse onto one build. Neither a resolve nor a build error
// is cached — a later For retries.
func (r *Registry) For(ctx context.Context, projectID string) (*Providers, error) {
	// Fast path: already cached.
	r.mu.Lock()
	if p, ok := r.cache[projectID]; ok {
		r.mu.Unlock()
		return p, nil
	}
	// Take (or create) this project's build lock so concurrent For calls for the
	// same project serialize and build once.
	kl, ok := r.keyLocks[projectID]
	if !ok {
		kl = &sync.Mutex{}
		r.keyLocks[projectID] = kl
	}
	r.mu.Unlock()

	kl.Lock()
	defer kl.Unlock()

	// Re-check under the key lock: an earlier holder may have built it while we
	// waited (and an Invalidate between the two locks still forces a rebuild).
	r.mu.Lock()
	if p, ok := r.cache[projectID]; ok {
		r.mu.Unlock()
		return p, nil
	}
	r.mu.Unlock()

	rc, err := r.resolve(ctx, projectID)
	if err != nil {
		return nil, err // not cached — next For retries
	}
	p, err := r.build(ctx, rc)
	if err != nil {
		return nil, err // failed build not cached — next For retries
	}

	r.mu.Lock()
	r.cache[projectID] = p
	r.mu.Unlock()
	return p, nil
}

// Invalidate drops the cached Providers for a project so the next For rebuilds
// from a freshly resolved config. Called by identity's SetInvalidator hook after
// a dashboard credential write. A no-op for an uncached project.
func (r *Registry) Invalidate(projectID string) {
	r.mu.Lock()
	delete(r.cache, projectID)
	r.mu.Unlock()
}
