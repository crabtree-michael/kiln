package verify_test

import (
	"context"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity/verify"
)

func TestMockVerifierReportsOK(t *testing.T) {
	m := verify.NewMock()
	ctx := context.Background()
	for _, got := range []struct {
		name   string
		result string
	}{
		{"anthropic", m.VerifyAnthropic(ctx, "any").Status},
		{"amika", m.VerifyAmika(ctx, "any").Status},
		{"devin", m.VerifyDevin(ctx, "any").Status},
		{"repo", m.VerifyRepo(ctx, "repo", "token").Status},
	} {
		if got.result != "ok" {
			t.Errorf("%s status = %q, want ok", got.name, got.result)
		}
	}
}
