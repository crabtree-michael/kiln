// Package mock is the keyless-e2e voice token minter selected by
// KILN_VOICE_MODE=mock — the STT counterpart to AGENT_MODE=mock and the scripted
// brain (design docs/keyless-e2e-tests-design.md §3.2). It satisfies
// api.VoiceTokenMinter without calling AssemblyAI: MintStreamingToken returns a
// static token, so POST /api/voice/token succeeds with no ASSEMBLYAI_API_KEY.
// The browser then opens its STT socket against the test's mock streaming server
// (frontend WS-URL override), so the real voice pipeline runs end to end with no
// paid credential anywhere.
package mock

import (
	"context"
	"time"
)

// DefaultToken is the canned streaming token handed to the client. It is never
// redeemed against AssemblyAI — the mock STT server ignores it — so its value
// only needs to be non-empty (the api handler rejects an empty mint).
const DefaultToken = "mock-voice-token"

// DefaultTTL is the canned redemption window reported to the client, matching
// the real adapter's short-lived-token contract (09 §6).
const DefaultTTL = 10 * time.Minute

// Minter is the in-memory api.VoiceTokenMinter. Zero value is usable and yields
// DefaultToken / DefaultTTL.
type Minter struct {
	Token string        // overrides DefaultToken when set
	TTL   time.Duration // overrides DefaultTTL when > 0
}

// New returns a mock minter with the default token and TTL.
func New() *Minter { return &Minter{} }

// MintStreamingToken returns the canned token and its expiry; it never fails and
// never touches the network.
func (m *Minter) MintStreamingToken(_ context.Context) (string, time.Time, error) {
	token := m.Token
	if token == "" {
		token = DefaultToken
	}
	ttl := m.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return token, time.Now().Add(ttl), nil
}
