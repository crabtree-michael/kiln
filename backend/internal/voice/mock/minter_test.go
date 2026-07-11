package mock_test

import (
	"context"
	"testing"
	"time"

	voicemock "github.com/crabtree-michael/kiln/backend/internal/voice/mock"
)

func TestMintReturnsCannedToken(t *testing.T) {
	m := voicemock.New()
	tok, exp, err := m.MintStreamingToken(context.Background())
	if err != nil {
		t.Fatalf("MintStreamingToken: unexpected error: %v", err)
	}
	if tok != voicemock.DefaultToken {
		t.Errorf("token = %q, want %q", tok, voicemock.DefaultToken)
	}
	if !exp.After(time.Now()) {
		t.Errorf("expiry %v is not in the future", exp)
	}
}

func TestMintOverrides(t *testing.T) {
	m := &voicemock.Minter{Token: "custom", TTL: time.Minute}
	tok, exp, err := m.MintStreamingToken(context.Background())
	if err != nil {
		t.Fatalf("MintStreamingToken: %v", err)
	}
	if tok != "custom" {
		t.Errorf("token = %q, want custom", tok)
	}
	if d := time.Until(exp); d > time.Minute+time.Second {
		t.Errorf("expiry TTL = %v, want ~1m", d)
	}
}
