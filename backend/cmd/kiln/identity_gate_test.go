package main

// The identity boot gate (11 §2, GitHub App migration). The gate exists because
// the identity surface is all-or-nothing: a deployment with some of the App
// settings and not the rest would serve a flawless-looking /auth/github/connect
// whose callback fails every token exchange. That is precisely the state a
// half-finished env-var rename leaves behind, which is what these pin down.

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"log/slog"
	"strings"
	"testing"
)

// testAppSlug is the App's public-link slug in these fixtures.
const testAppSlug = "kiln"

// fullIdentityConfig is a Config with every identity setting present — the
// baseline each case below removes exactly one thing from.
func fullIdentityConfig(t *testing.T) Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return Config{
		GitHubAppID:           "123456",
		GitHubAppSlug:         testAppSlug,
		GitHubAppPrivateKey:   base64.StdEncoding.EncodeToString(pemBytes),
		GitHubAppClientID:     "Iv1.client",
		GitHubAppClientSecret: "client-secret",
		// 64 hex chars — the cipher's own gate, unchanged by this migration.
		SecretsKey: strings.Repeat("ab", 32),
	}
}

// captureLogger returns a logger writing into buf, so a test can assert on the
// warning the gate emits — the only signal an operator gets that identity is
// silently absent.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestBuildIdentityMountsOnAFullConfig(t *testing.T) {
	var logs bytes.Buffer
	svc, err := buildIdentity(fullIdentityConfig(t), nil, captureLogger(&logs))
	if err != nil {
		t.Fatalf("buildIdentity: %v", err)
	}
	if svc == nil {
		t.Fatal("identity not mounted despite a complete configuration")
	}
}

// Nothing configured is TODAY'S BOOT for a deployment that never turned
// identity on: no service, no error, and — importantly — no warning, because
// there is nothing wrong to warn about.
func TestBuildIdentityStaysDarkWhenNothingIsSet(t *testing.T) {
	var logs bytes.Buffer
	svc, err := buildIdentity(Config{}, nil, captureLogger(&logs))
	if err != nil {
		t.Fatalf("buildIdentity: %v", err)
	}
	if svc != nil {
		t.Error("identity mounted with no configuration at all")
	}
	if logs.Len() != 0 {
		t.Errorf("unconfigured boot warned: %q — silence is the contract here", logs.String())
	}
}

// One missing setting must not mount a half-working identity surface, and the
// warning must NAME what is missing: "identity disabled" alone sends an
// operator hunting through six env vars.
func TestBuildIdentityRefusesAPartialConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		blank func(*Config)
		want  string
	}{
		{"app id", func(c *Config) { c.GitHubAppID = "" }, "KILN_GITHUB_APP_ID"},
		{"slug", func(c *Config) { c.GitHubAppSlug = "" }, "KILN_GITHUB_APP_SLUG"},
		{"private key", func(c *Config) { c.GitHubAppPrivateKey = "" }, "KILN_GITHUB_APP_PRIVATE_KEY"},
		{"client id", func(c *Config) { c.GitHubAppClientID = "" }, "KILN_GITHUB_APP_CLIENT_ID"},
		{"client secret", func(c *Config) { c.GitHubAppClientSecret = "" }, "KILN_GITHUB_APP_CLIENT_SECRET"},
		{"secrets key", func(c *Config) { c.SecretsKey = "" }, "KILN_SECRETS_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fullIdentityConfig(t)
			tc.blank(&cfg)

			var logs bytes.Buffer
			svc, err := buildIdentity(cfg, nil, captureLogger(&logs))
			if err != nil {
				t.Fatalf("buildIdentity: %v", err)
			}
			if svc != nil {
				t.Errorf("identity mounted with %s missing", tc.want)
			}
			if !strings.Contains(logs.String(), tc.want) {
				t.Errorf("warning %q does not name the missing %s", logs.String(), tc.want)
			}
		})
	}
}

// A key that cannot sign fails the BOOT. Deferring it to the first sign-in
// would mean a deploy that looks healthy — health check green, connect route
// redirecting — right up until a user tries to use it.
func TestBuildIdentityFailsHardOnAnUnusablePrivateKey(t *testing.T) {
	cfg := fullIdentityConfig(t)
	cfg.GitHubAppPrivateKey = base64.StdEncoding.EncodeToString([]byte("not a pem"))

	var logs bytes.Buffer
	if _, err := buildIdentity(cfg, nil, captureLogger(&logs)); err == nil {
		t.Fatal("expected a boot failure on an unparseable App private key")
	}
}

// Likewise a malformed secrets key: a half-working cipher must never silently
// store plaintext (11 §3).
func TestBuildIdentityFailsHardOnAMalformedSecretsKey(t *testing.T) {
	cfg := fullIdentityConfig(t)
	cfg.SecretsKey = "not-hex"

	var logs bytes.Buffer
	if _, err := buildIdentity(cfg, nil, captureLogger(&logs)); err == nil {
		t.Fatal("expected a boot failure on a malformed KILN_SECRETS_KEY")
	}
}
