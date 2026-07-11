package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/brain"
	voicemock "github.com/crabtree-michael/kiln/backend/internal/voice/mock"
)

// TestValidateConfigBrainMode covers the keyless-e2e brain switch wiring
// (design §3.1): KILN_BRAIN_MODE selects a scripted brain.LLM, and every bad
// combination fails fast at startup rather than surfacing as a dead brain.
func TestValidateConfigBrainMode(t *testing.T) {
	t.Run("default is the real adapter (nil scripted brain)", func(t *testing.T) {
		llm, err := validateConfig(Config{AgentMode: "amika"})
		if err != nil {
			t.Fatalf("validateConfig: %v", err)
		}
		if llm != nil {
			t.Errorf("scripted brain = %v, want nil in the default (Anthropic) case", llm)
		}
	})

	t.Run("scripted loads the fixture", func(t *testing.T) {
		path := writeScript(t, `{"rules":[{"when":{"contains":["hi"]},"rounds":[{"text":"ok"}]}]}`)
		llm, err := validateConfig(Config{AgentMode: modeMock, BrainMode: modeScripted, BrainScript: path})
		if err != nil {
			t.Fatalf("validateConfig: %v", err)
		}
		if llm == nil {
			t.Fatal("scripted brain is nil, want a loaded LLM")
		}
		// A matched pass plays the scripted end-turn text.
		resp, err := llm.Do(context.Background(), brain.LLMRequest{
			Messages: []brain.LLMMessage{{Role: brain.LLMRoleUser, Text: "say hi"}},
		})
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if resp.Text != "ok" {
			t.Errorf("scripted response = %q, want ok", resp.Text)
		}
	})

	t.Run("scripted without a script path is a config error", func(t *testing.T) {
		if _, err := validateConfig(Config{AgentMode: modeMock, BrainMode: modeScripted}); err == nil {
			t.Error("want an error for KILN_BRAIN_MODE=scripted with no KILN_BRAIN_SCRIPT")
		}
	})

	t.Run("unknown brain mode is a config error", func(t *testing.T) {
		if _, err := validateConfig(Config{AgentMode: modeMock, BrainMode: "bogus"}); err == nil {
			t.Error("want an error for an unknown KILN_BRAIN_MODE")
		}
	})

	t.Run("unknown agent mode is a config error", func(t *testing.T) {
		if _, err := validateConfig(Config{AgentMode: "bogus"}); err == nil {
			t.Error("want an error for an unknown AGENT_MODE")
		}
	})
}

// TestNewVoiceMinterMode covers KILN_VOICE_MODE (design §3.2): mock yields the
// canned minter, which mints a non-empty token with no AssemblyAI key.
func TestNewVoiceMinterMode(t *testing.T) {
	tok, _, err := newVoiceMinter(Config{VoiceMode: modeMock}).MintStreamingToken(context.Background())
	if err != nil {
		t.Fatalf("mock minter: %v", err)
	}
	if tok != voicemock.DefaultToken {
		t.Errorf("mock token = %q, want %q", tok, voicemock.DefaultToken)
	}
	// Default mode builds the real adapter (no network call here — just a non-nil
	// minter of the other concrete type).
	if newVoiceMinter(Config{}) == nil {
		t.Error("default voice minter is nil")
	}
}

// TestNewVerifierMode covers KILN_VERIFY_MODE (design §Test 3): mock reports ok
// offline; default builds the live-check adapter.
func TestNewVerifierMode(t *testing.T) {
	if got := newVerifier(Config{VerifyMode: modeMock}).VerifyAnthropic(context.Background(), "k").Status; got != "ok" {
		t.Errorf("mock verify status = %q, want ok", got)
	}
	if newVerifier(Config{}) == nil {
		t.Error("default verifier is nil")
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
