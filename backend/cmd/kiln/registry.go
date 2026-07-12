package main

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/amika"
	"github.com/crabtree-michael/kiln/backend/internal/agent/devin"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// providerLabels are the human-facing names the dashboard shows for each provider
// key (multi-provider design §8). A key with no entry falls back to the key itself.
var providerLabels = map[string]string{
	modeMock:      "Mock",
	providerAmika: "Amika",
	providerDevin: "Devin",
}

// Provider-registry keys (design §6): the value AGENT_MODE and a project's
// AgentProvider carry, and the identity of a dashboard descriptor (§8). modeMock
// is shared with the other keyless-e2e switches (wiring.go).
const (
	providerAmika = "amika"
	providerDevin = "devin"
)

// ProviderDeps is everything a provider factory needs to build one project's
// agent.Provider (design §6): the deployment-global Config, the project's
// decrypted RuntimeConfig (owner credentials + project settings, plaintext,
// in-process only), the per-project worker-name prefix, and a per-tenant HTTP
// client so each project's connection pool is isolated and tenant.Providers.Close
// can release it on a credential rebuild without touching http.DefaultClient
// (11 §3).
type ProviderDeps struct {
	Config       Config
	Runtime      identity.RuntimeConfig
	WorkerPrefix string
	HTTPClient   *http.Client
}

// ProviderFactory builds one project's agent.Provider from its deps (design §6).
// A registry of these — keyed by provider key (AGENT_MODE / Project.AgentProvider)
// — replaces the hard-coded mock/amika branch, so adding a provider is one entry
// in providerRegistry plus its adapter package, with no edit to
// buildTenantProviders' body (D3).
type ProviderFactory func(ProviderDeps) (agent.Provider, error)

// providerRegistry maps a provider key to its constructor (design §6, D3). The
// key is the value AGENT_MODE and a project's AgentProvider carry. The ship set is
// the in-memory mock, the Amika sandbox adapter, and the Devin session adapter;
// adding a provider is one entry here plus its adapter package — never an edit to
// buildTenantProviders' body. Composition-root validation (validateConfig,
// providerKeyFor) is "is the key registered", never an if-ladder.
var providerRegistry = map[string]ProviderFactory{
	modeMock:      buildMockProvider,
	providerAmika: buildAmikaProvider,
	providerDevin: buildDevinProvider,
}

// buildMockProvider is the AGENT_MODE=mock factory: the in-memory Provider (05 §8),
// which needs none of the deps.
func buildMockProvider(ProviderDeps) (agent.Provider, error) { return mock.New(), nil }

// buildAmikaProvider is the amika factory (05 §6): the sandbox HTTP client built
// from the project's owner credentials + settings, over its per-tenant HTTP client.
// BaseURL is the deployment-global AMIKA_BASE_URL; everything else is per-project.
func buildAmikaProvider(d ProviderDeps) (agent.Provider, error) {
	return amika.New(amika.Config{
		BaseURL:      d.Config.AmikaBaseURL,
		APIKey:       d.Runtime.AmikaAPIKey,
		RepoURL:      d.Runtime.Project.RepoURL,
		Snapshot:     d.Runtime.Project.AmikaSnapshot,
		ClaudeCredID: d.Runtime.AmikaClaudeCredID,
		WorkerPrefix: d.WorkerPrefix,
		Secrets:      amikaSecretRefs(d.Runtime.AmikaSecrets),
	}, d.HTTPClient), nil
}

// buildDevinProvider is the AGENT_MODE=devin factory (design §Phase 2): the
// session-based virtual-worker adapter over its per-tenant HTTP client, so each
// project's connection pool is isolated and tenant.Providers.Close can release it
// on a credential rebuild (11 §3). Its config is sourced from the deployment-global
// composition-root Config — the opt-in path; per-project Devin config via the
// dashboard descriptor is a later phase (§3). The adapter sends the idempotent
// create flag itself, so a replayed StartTurn(fresh) reuses the same session
// rather than opening a duplicate (design §4).
func buildDevinProvider(d ProviderDeps) (agent.Provider, error) {
	return devin.New(devin.Config{
		BaseURL:     d.Config.DevinBaseURL,
		APIKey:      d.Config.DevinAPIKey,
		Snapshot:    d.Config.DevinSnapshot,
		MaxACULimit: d.Config.DevinMaxACULimit,
	}, d.HTTPClient), nil
}

// resolveTenantProvider builds one project's coding-agent Provider through the
// registry (multi-provider design §6, §9): the project's own AgentProvider default
// when set, else the deployment default (AGENT_MODE). An unregistered key fails
// LOUD and CONTAINED with agent.ErrProviderUnavailable (D7) — the reconciler/poller
// isolate the resolve failure per project (spec §6), never silently falling back. A
// per-tenant HTTP client so each project's connection pool is isolated and
// tenant.Providers.Close can release it on a credential rebuild (11 §3) without
// touching the shared http.DefaultClient.
func resolveTenantProvider(cfg Config, rc identity.RuntimeConfig, prefix string) (agent.Provider, error) {
	key := providerKeyFor(cfg, rc.Project)
	factory, ok := lookupProvider(key)
	if !ok {
		return nil, fmt.Errorf("%w: project %s provider %q (registered: %v)",
			agent.ErrProviderUnavailable, rc.Project.ID, key, providerKeys())
	}
	provider, err := factory(ProviderDeps{
		Config:       cfg,
		Runtime:      rc,
		WorkerPrefix: prefix,
		HTTPClient:   &http.Client{},
	})
	if err != nil {
		return nil, fmt.Errorf("kiln: build provider %q for project %s: %w", key, rc.Project.ID, err)
	}
	return provider, nil
}

// lookupProvider returns the factory registered under key, or false when the key
// names no registered provider (design §6): the resolver then fails loud with
// agent.ErrProviderUnavailable rather than silently falling back to the deployment
// default (D7).
func lookupProvider(key string) (ProviderFactory, bool) {
	f, ok := providerRegistry[key]
	return f, ok
}

// providerDescriptors builds the dashboard's provider descriptors from the registry
// (multi-provider design §8, D6): one per registered key, in sorted order, carrying
// its label and its declared Capabilities. Each provider is instantiated with empty
// deps purely to read its static Capabilities — New performs no I/O — so this is
// safe at startup. This is the single place the composition root turns "which
// providers are registered" into the neutral data the generic dashboard renders,
// so adding a provider surfaces it in the UI with no dashboard edit.
func providerDescriptors(cfg Config) []wire.ProviderDescriptor {
	out := make([]wire.ProviderDescriptor, 0, len(providerRegistry))
	for _, key := range providerKeys() {
		p, err := providerRegistry[key](ProviderDeps{Config: cfg})
		if err != nil {
			// A descriptor probe must never fail startup: skip a provider whose probe
			// build errors (it simply isn't offered). The real per-project build path
			// surfaces its error loudly (ErrProviderUnavailable) if it is ever selected.
			continue
		}
		caps := agent.CapabilitiesOf(p)
		label := providerLabels[key]
		if label == "" {
			label = key
		}
		out = append(out, wire.ProviderDescriptor{
			Key:   key,
			Label: label,
			Capabilities: wire.ProviderCapabilities{
				ManagedSandbox: caps.ManagedSandbox,
				ReportsCost:    caps.ReportsCost,
				Snapshots:      caps.Snapshots,
				SecretsInject:  caps.SecretsInject,
			},
		})
	}
	return out
}

// providerKeys returns the registered provider keys in sorted order — for the
// config-error message that tells the operator which AGENT_MODE values are valid.
func providerKeys() []string {
	keys := make([]string, 0, len(providerRegistry))
	for k := range providerRegistry {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// providerKeyFor resolves which provider serves a project (design §9): the
// project's own AgentProvider default when it set one, else the deployment default
// (AGENT_MODE). Resolved per build so it always reflects the current stored value —
// a mid-flight owner change takes effect on the next provider build (11 §3).
func providerKeyFor(cfg Config, p identity.Project) string {
	if p.AgentProvider != "" {
		return p.AgentProvider
	}
	return cfg.AgentMode
}
