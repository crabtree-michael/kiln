package verify_test

// Unit tests for the identity.Verifier live-check adapter (11 §4): each
// method is exercised against an httptest server (Anthropic/Amika) or a
// throwaway local git repo (repo), asserting the outbound request shape and
// the translated CheckResult — never a Go error.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity/verify"
)

// wantOK/wantFailed are the CheckResult.Status values asserted across
// several tests below; testHTTPSRepoURL is the https clone URL AuthedCloneURL
// is exercised against.
const (
	wantOK           = "ok"
	wantFailed       = "failed"
	testHTTPSRepoURL = "https://github.com/x/y"
)

func TestVerifyAnthropicOK(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Api-Key")
		gotVersion = r.Header.Get("Anthropic-Version")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	v := verify.New("", "")
	v.AnthropicBaseURL = srv.URL

	res := v.VerifyAnthropic(context.Background(), "sk-ant-test")

	if gotPath != "/v1/models" {
		t.Fatalf("request path = %q, want /v1/models", gotPath)
	}
	if gotKey != "sk-ant-test" {
		t.Fatalf("x-api-key header = %q, want sk-ant-test", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Fatalf("anthropic-version header = %q, want 2023-06-01", gotVersion)
	}
	if res.Name != "anthropic" || res.Status != wantOK {
		t.Fatalf("res = %+v, want {Name:anthropic Status:ok}", res)
	}
}

func TestVerifyAnthropicBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := verify.New("", "")
	v.AnthropicBaseURL = srv.URL

	res := v.VerifyAnthropic(context.Background(), "bad-key")
	if res.Status != wantFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Message, "401") {
		t.Fatalf("message = %q, want to contain 401", res.Message)
	}
}

func TestVerifyAmikaOK(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	v := verify.New(srv.URL, "")
	res := v.VerifyAmika(context.Background(), "amk-test")

	if gotPath != "/sandboxes" {
		t.Fatalf("request path = %q, want /sandboxes", gotPath)
	}
	if gotAuth != "Bearer amk-test" {
		t.Fatalf("Authorization header = %q, want Bearer amk-test", gotAuth)
	}
	if res.Name != "amika" || res.Status != wantOK {
		t.Fatalf("res = %+v, want {Name:amika Status:ok}", res)
	}
}

func TestVerifyAmikaBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := verify.New(srv.URL, "")
	res := v.VerifyAmika(context.Background(), "bad-key")
	if res.Status != wantFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Message, "401") {
		t.Fatalf("message = %q, want to contain 401", res.Message)
	}
}

func TestVerifyAmikaNoBaseURL(t *testing.T) {
	v := verify.New("", "")
	res := v.VerifyAmika(context.Background(), "amk-test")
	if res.Status != wantFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Message, "amika base url not configured") {
		t.Fatalf("message = %q, want to contain amika base url not configured", res.Message)
	}
}

func TestVerifyDevinOK(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	v := verify.New("", srv.URL)
	res := v.VerifyDevin(context.Background(), "cog-test")

	if gotPath != "/v1/sessions" {
		t.Fatalf("request path = %q, want /v1/sessions", gotPath)
	}
	if gotAuth != "Bearer cog-test" {
		t.Fatalf("Authorization header = %q, want Bearer cog-test", gotAuth)
	}
	if res.Name != "devin" || res.Status != wantOK {
		t.Fatalf("res = %+v, want {Name:devin Status:ok}", res)
	}
}

func TestVerifyDevinBadKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	v := verify.New("", srv.URL)
	res := v.VerifyDevin(context.Background(), "bad-key")
	if res.Status != wantFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Message, "401") {
		t.Fatalf("message = %q, want to contain 401", res.Message)
	}
}

// TestVerifyDevinDefaultBaseURL asserts an empty devinBaseURL falls back to the
// hosted default rather than leaving the probe unconfigured (the Devin key is
// per-user config; its base URL is a deployment default).
func TestVerifyDevinDefaultBaseURL(t *testing.T) {
	v := verify.New("", "")
	if v.DevinBaseURL != verify.DefaultDevinBaseURL {
		t.Fatalf("DevinBaseURL = %q, want %q", v.DevinBaseURL, verify.DefaultDevinBaseURL)
	}
}

// newBareRepo builds a bare git repo with one commit, via a temp clone
// (git init --bare + push), and returns the bare repo's filesystem path.
// Skips the calling test if git isn't on PATH.
func newBareRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.git")
	runGit(t, dir, "init", "--bare", bare)

	work := filepath.Join(dir, "work")
	runGit(t, dir, "clone", bare, work)
	runGit(t, work, "config", "user.email", "verify-test@example.com")
	runGit(t, work, "config", "user.name", "verify-test")
	if err := os.WriteFile(filepath.Join(work, "file.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runGit(t, work, "add", "file.txt")
	runGit(t, work, "commit", "-m", "initial")
	runGit(t, work, "push", "origin", "HEAD:refs/heads/main")

	return bare
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	//nolint:gosec // fixed argv, test-only fixture setup
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestVerifyRepoOK(t *testing.T) {
	bare := newBareRepo(t)

	v := verify.New("", "")
	res := v.VerifyRepo(context.Background(), "file://"+bare, "")
	if res.Status != wantOK {
		t.Fatalf("status = %q message = %q, want ok", res.Status, res.Message)
	}
	if res.Name != "repo" {
		t.Fatalf("name = %q, want repo", res.Name)
	}
}

func TestVerifyRepoMissing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	empty := t.TempDir() // no repo here at all

	v := verify.New("", "")
	res := v.VerifyRepo(context.Background(), "file://"+empty, "")
	if res.Status != wantFailed {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Message, "git ls-remote failed") {
		t.Fatalf("message = %q, want to contain 'git ls-remote failed'", res.Message)
	}
}

func TestAuthedCloneURL(t *testing.T) {
	cases := []struct {
		name, repoURL, token, want string
	}{
		{"https with token", testHTTPSRepoURL, "tok", "https://x-access-token:tok@github.com/x/y"},
		{"empty token unchanged", testHTTPSRepoURL, "", testHTTPSRepoURL},
		{"non-https unchanged", "git@github.com:x/y.git", "tok", "git@github.com:x/y.git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := verify.AuthedCloneURL(c.repoURL, c.token)
			if got != c.want {
				t.Fatalf("AuthedCloneURL(%q, %q) = %q, want %q", c.repoURL, c.token, got, c.want)
			}
		})
	}
}
