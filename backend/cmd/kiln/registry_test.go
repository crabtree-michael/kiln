package main

import (
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// TestProviderRegistryShipSet pins the registered providers (multi-provider design
// §6, D3): the base mock + amika plus the init-registered devin. A new provider
// adds itself here without an if-ladder.
func TestProviderRegistryShipSet(t *testing.T) {
	for _, key := range []string{modeMock, providerAmika, providerDevin} {
		if _, ok := lookupProvider(key); !ok {
			t.Errorf("provider %q is not registered", key)
		}
	}
	if _, ok := lookupProvider("nope"); ok {
		t.Error("lookupProvider(nope) = true, want an unregistered key to miss")
	}
}

// TestValidateConfigRegistryDriven proves the AGENT_MODE check is the registry, not
// a two-value list: a registered key passes, an unregistered one fails fast (D3).
func TestValidateConfigRegistryDriven(t *testing.T) {
	for _, key := range []string{modeMock, providerAmika, providerDevin} {
		if _, err := validateConfig(Config{AgentMode: key}); err != nil {
			t.Errorf("validateConfig(AGENT_MODE=%q) = %v, want nil", key, err)
		}
	}
	const unregistered = "not-a-registered-provider"
	if _, err := validateConfig(Config{AgentMode: unregistered}); err == nil {
		t.Error("validateConfig(AGENT_MODE=unregistered) = nil, want a config error")
	}
}

// TestProviderKeyForDefaulting covers §9's per-project default: a project's own
// AgentProvider wins; an empty one falls back to the deployment default (AGENT_MODE).
func TestProviderKeyForDefaulting(t *testing.T) {
	cfg := Config{AgentMode: providerAmika}
	if got := providerKeyFor(cfg, identity.Project{}); got != providerAmika {
		t.Errorf("empty AgentProvider resolved to %q, want the deployment default %q", got, providerAmika)
	}
	if got := providerKeyFor(cfg, identity.Project{AgentProvider: providerDevin}); got != providerDevin {
		t.Errorf("project AgentProvider resolved to %q, want %q", got, providerDevin)
	}
}

// TestFactoriesBuildProviders exercises each factory to a working agent.Provider and
// confirms the capability shapes the core reads (design §5): Amika is a managed
// sandbox, Devin is a session-shaped virtual-worker provider.
func TestFactoriesBuildProviders(t *testing.T) {
	deps := ProviderDeps{Config: Config{}, Runtime: identity.RuntimeConfig{}}

	amikaP, err := buildAmikaProvider(deps)
	if err != nil {
		t.Fatalf("buildAmikaProvider: %v", err)
	}
	if !agent.CapabilitiesOf(amikaP).ManagedSandbox {
		t.Error("amika provider should declare ManagedSandbox")
	}

	devinP, err := buildDevinProvider(deps)
	if err != nil {
		t.Fatalf("buildDevinProvider: %v", err)
	}
	if caps := agent.CapabilitiesOf(devinP); caps.ManagedSandbox || !caps.ReportsCost {
		t.Errorf("devin capabilities = %+v, want no managed sandbox but cost reporting", caps)
	}

	mockP, err := buildMockProvider(deps)
	if err != nil {
		t.Fatalf("buildMockProvider: %v", err)
	}
	if mockP == nil {
		t.Error("buildMockProvider returned nil")
	}
}

// TestDevinAPIKeySource covers the per-project credential precedence (multi-provider
// design §8): the owner's dashboard-stored key wins, the deployment DEVIN_API_KEY env
// is the fallback when the owner set none.
func TestDevinAPIKeySource(t *testing.T) {
	const envKey = "cog-env"
	cases := []struct {
		name, runtime, env, want string
	}{
		{"owner key preferred over env", "cog-owner", envKey, "cog-owner"},
		{"env fallback when owner unset", "", envKey, envKey},
		{"empty when neither set", "", "", ""},
	}
	for _, c := range cases {
		if got := devinAPIKey(c.runtime, c.env); got != c.want {
			t.Errorf("%s: devinAPIKey(%q, %q) = %q, want %q", c.name, c.runtime, c.env, got, c.want)
		}
	}
}

// TestProviderDescriptors covers the dashboard data source (multi-provider design
// §8, D6): one descriptor per registered provider, labelled, with the capability
// shape the UI gates affordances on — Amika a managed sandbox, Devin not.
func TestProviderDescriptors(t *testing.T) {
	got := providerDescriptors(Config{})
	byKey := map[string]struct {
		label   string
		managed bool
	}{}
	for _, d := range got {
		byKey[d.Key] = struct {
			label   string
			managed bool
		}{d.Label, d.Capabilities.ManagedSandbox}
	}

	amika, ok := byKey[providerAmika]
	if !ok || amika.label != "Amika" || !amika.managed {
		t.Errorf("amika descriptor = %+v, want label Amika + ManagedSandbox", amika)
	}
	devin, ok := byKey[providerDevin]
	if !ok || devin.label != "Devin" || devin.managed {
		t.Errorf("devin descriptor = %+v, want label Devin + no ManagedSandbox", devin)
	}
	if _, ok := byKey[modeMock]; !ok {
		t.Error("mock provider missing from descriptors")
	}
}
