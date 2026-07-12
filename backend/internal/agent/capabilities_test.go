package agent_test

// Phase 0 of the multi-provider design (§5): prove the core can read a provider's
// shape through the neutral CapabilityReporter seam — never by naming a provider.
// These tests pin the escape-hatch contract before any second provider exists.

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/agent"
	"github.com/crabtree-michael/kiln/backend/internal/agent/amika"
	"github.com/crabtree-michael/kiln/backend/internal/agent/mock"
)

func TestCapabilitiesOf_Amika(t *testing.T) {
	got := agent.CapabilitiesOf(amika.New(amika.Config{}, nil))
	want := agent.Capabilities{
		ManagedSandbox: true,
		ReportsCost:    true,
		Snapshots:      true,
		SecretsInject:  true,
	}
	if got != want {
		t.Fatalf("amika capabilities = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesOf_MockDefaultsConservative(t *testing.T) {
	// The mock ships with the conservative zero value — the Devin/virtual-worker
	// shape — until a test sets Caps.
	if got := agent.CapabilitiesOf(mock.New()); got != (agent.Capabilities{}) {
		t.Fatalf("mock default capabilities = %+v, want zero value", got)
	}
	m := mock.New()
	m.Caps = agent.Capabilities{ReportsCost: true, Snapshots: true}
	if got := agent.CapabilitiesOf(m); got != m.Caps {
		t.Fatalf("mock capabilities = %+v, want %+v", got, m.Caps)
	}
}

// bareProvider implements agent.Provider but NOT CapabilityReporter, standing in
// for an adapter that declares nothing. The core must fall back to the
// conservative zero value rather than crash or type-sniff.
type bareProvider struct{ agent.Provider }

func TestCapabilitiesOf_AbsentReporterIsConservative(t *testing.T) {
	if got := agent.CapabilitiesOf(bareProvider{}); got != (agent.Capabilities{}) {
		t.Fatalf("absent-reporter capabilities = %+v, want zero value", got)
	}
}

// TestCapabilitiesOf_ReadsInterfaceNotIdentity is the load-bearing Phase 0 test:
// the core reads capabilities through the neutral interface, so two *different*
// providers reporting the *same* shape are indistinguishable to it — there is no
// provider identity for the core to branch on.
func TestCapabilitiesOf_ReadsInterfaceNotIdentity(t *testing.T) {
	amikaShape := agent.CapabilitiesOf(amika.New(amika.Config{}, nil))

	// A mock configured to Amika's shape is read identically — proving the core
	// keys off declared capabilities, never the concrete type.
	m := mock.New()
	m.Caps = amikaShape
	if got := agent.CapabilitiesOf(m); got != amikaShape {
		t.Fatalf("mock-with-amika-shape read as %+v, want %+v", got, amikaShape)
	}

	// And the read composes with the rest of the port: a capability-declaring
	// provider is still a plain Provider everywhere else.
	var _ agent.Provider = m
	if _, err := m.ListWorkers(context.Background()); err != nil {
		t.Fatalf("ListWorkers on capability-declaring mock: %v", err)
	}
}
