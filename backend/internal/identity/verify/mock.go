package verify

import (
	"context"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// Mock is the keyless-e2e identity.Verifier selected by KILN_VERIFY_MODE=mock
// (design docs/keyless-e2e-tests-design.md §Test 3). A keyless stack has no real
// Anthropic/Amika/repo credentials to probe — the coding agent is AGENT_MODE=mock
// and the brain is scripted — so the dashboard's live checks would all fail. This
// verifier reports every configured group "ok" without touching the network, so
// onboarding's POST /api/settings/verify comes back green offline. The identity
// service still reports an unconfigured group as "skipped" (it only calls the
// verifier for configured groups), so Mock never fabricates an ok for a
// credential the user never set.
type Mock struct{}

var _ identity.Verifier = Mock{}

// NewMock returns the offline mock verifier.
func NewMock() Mock { return Mock{} }

func (Mock) VerifyAnthropic(_ context.Context, _ string) identity.CheckResult {
	return ok(nameAnthropic)
}

func (Mock) VerifyAmika(_ context.Context, _ string) identity.CheckResult {
	return ok(nameAmika)
}

func (Mock) VerifyDevin(_ context.Context, _ string) identity.CheckResult {
	return ok(nameDevin)
}

func (Mock) VerifyRepo(_ context.Context, _, _ string) identity.CheckResult {
	return ok(nameRepo)
}

// ok is a passing CheckResult for a mock-verified group.
func ok(name string) identity.CheckResult {
	return identity.CheckResult{Name: name, Status: statusOK, Message: "mock: not checked"}
}
