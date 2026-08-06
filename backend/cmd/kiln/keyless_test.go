package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
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

// TestNewGitHubMode covers KILN_GITHUB_MODE (settings repo picker): mock lists
// the canned repos offline so the keyless lane can onboard through the real
// dashboard form, and mints a canned installation token so it exercises the App
// credential path too.
func TestNewGitHubMode(t *testing.T) {
	gh, err := newGitHub(Config{GitHubMode: modeMock})
	if err != nil {
		t.Fatalf("newGitHub(mock): %v", err)
	}
	repos, err := gh.ListRepos(context.Background(), "any")
	if err != nil {
		t.Fatalf("mock ListRepos: %v", err)
	}
	if len(repos) == 0 {
		t.Error("mock ListRepos returned no repos, want the canned listing")
	}
	// The mock must satisfy the mint half too, or a keyless stack's brain has no
	// credential at all once the dev session stores an installation.
	if _, err := gh.MintInstallationToken(context.Background(), 1); err != nil {
		t.Errorf("mock MintInstallationToken: %v", err)
	}
	// The mock stands in for GitHub entirely, so it needs no private key —
	// exactly what lets a keyless stack boot with no credentials anywhere.
	if _, err := newGitHub(Config{GitHubMode: modeMock}); err != nil {
		t.Errorf("mock github adapter needs no App key, got %v", err)
	}
}

// A live adapter needs a usable App private key, and the key is carried
// base64-encoded because a multi-line PEM does not survive a hosting provider's
// environment intact. Both forms must load, and a broken value must fail the
// BOOT rather than the first user's sign-in.
func TestNewGitHubParsesTheAppPrivateKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "base64 (how it is stored)", value: base64.StdEncoding.EncodeToString(pemBytes)},
		{name: "raw PEM", value: string(pemBytes)},
		{name: "neither", value: "!!! not base64 and not pem !!!", wantErr: true},
		{name: "base64 of garbage", value: base64.StdEncoding.EncodeToString([]byte("nope")), wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newGitHub(Config{GitHubAppPrivateKey: tc.value})
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil — a bad key must fail the boot")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("newGitHub: %v", err)
			}
		})
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
