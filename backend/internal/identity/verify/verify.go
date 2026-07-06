// Package verify is the live-check adapter behind identity.Verifier (11 §4):
// it probes the user's Anthropic key, Amika key, and repo access over the
// network and translates every outcome — including transport failures — into
// an identity.CheckResult. Verify* methods never return a Go error; nothing
// here ever surfaces a secret (API key, GitHub token, or the authed clone URL
// a token is embedded in) in a message or log line.
package verify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// DefaultAnthropicBaseURL is api.anthropic.com; Verifier.AnthropicBaseURL
// overrides it for tests.
const DefaultAnthropicBaseURL = "https://api.anthropic.com"

const (
	// anthropicVersion is the API version header Anthropic requires.
	anthropicVersion = "2023-06-01"
	// httpTimeout bounds the Anthropic/Amika reachability requests.
	httpTimeout = 10 * time.Second
	// repoTimeout bounds the git ls-remote reachability probe.
	repoTimeout = 15 * time.Second

	statusOK     = "ok"
	statusFailed = "failed"

	nameAnthropic = "anthropic"
	nameAmika     = "amika"
	nameRepo      = "repo"
)

// Verifier is the live connection-check adapter satisfying identity.Verifier.
type Verifier struct {
	hc *http.Client
	// AnthropicBaseURL overrides DefaultAnthropicBaseURL; exported so tests
	// can point it at an httptest server.
	AnthropicBaseURL string
	// AmikaBaseURL is the platform-global Amika base URL (AMIKA_BASE_URL,
	// 11 §3 amended 2026-07-06) — no longer per-user config. Empty means the
	// platform itself is misconfigured, not a user error, so VerifyAmika
	// reports that distinctly.
	AmikaBaseURL string
}

var _ identity.Verifier = (*Verifier)(nil)

// New builds a Verifier with a 10s HTTP client timeout, the default
// Anthropic base URL, and the platform's Amika base URL.
func New(amikaBaseURL string) *Verifier {
	return &Verifier{
		hc:               &http.Client{Timeout: httpTimeout},
		AnthropicBaseURL: DefaultAnthropicBaseURL,
		AmikaBaseURL:     amikaBaseURL,
	}
}

// VerifyAnthropic hits GET {AnthropicBaseURL}/v1/models — free, no tokens
// billed — as a key-validity probe.
func (v *Verifier) VerifyAnthropic(ctx context.Context, apiKey string) identity.CheckResult {
	base := v.AnthropicBaseURL
	if base == "" {
		base = DefaultAnthropicBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return identity.CheckResult{Name: nameAnthropic, Status: statusFailed, Message: "build request failed"}
	}
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Anthropic-Version", anthropicVersion)
	return v.request(nameAnthropic, req)
}

// VerifyAmika hits GET {AmikaBaseURL}/sandboxes — the same call
// tests/global-teardown.ts uses to enumerate worker sandboxes. AmikaBaseURL is
// platform config (AMIKA_BASE_URL); an empty value here is a platform
// misconfiguration, not a user error.
func (v *Verifier) VerifyAmika(ctx context.Context, apiKey string) identity.CheckResult {
	if v.AmikaBaseURL == "" {
		return identity.CheckResult{Name: nameAmika, Status: statusFailed, Message: "amika base url not configured"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.AmikaBaseURL+"/sandboxes", nil)
	if err != nil {
		return identity.CheckResult{Name: nameAmika, Status: statusFailed, Message: "build request failed"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return v.request(nameAmika, req)
}

// VerifyRepo runs `git ls-remote` against repoURL (optionally authenticated
// via AuthedCloneURL) as a network+auth reachability probe. Its failure
// message is always the static string below plus the process exit code —
// never the command's stdout/stderr or the URL itself, since token is
// embedded in the URL and git error output can echo it back.
func (v *Verifier) VerifyRepo(ctx context.Context, repoURL, token string) identity.CheckResult {
	ctx, cancel := context.WithTimeout(ctx, repoTimeout)
	defer cancel()

	authed := AuthedCloneURL(repoURL, token)
	//nolint:gosec // repoURL/token come from the caller's own stored identity config, not attacker input
	cmd := exec.CommandContext(ctx, "git", "ls-remote", authed, "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// Discard stdout/stderr deliberately: git's output could include the
	// authed URL (with the embedded token) in an error message.
	if err := cmd.Run(); err != nil {
		return identity.CheckResult{
			Name:    nameRepo,
			Status:  statusFailed,
			Message: fmt.Sprintf("git ls-remote failed: exit %d", exitCode(err)),
		}
	}
	return identity.CheckResult{Name: nameRepo, Status: statusOK, Message: "repo reachable"}
}

// request issues req and translates the outcome into a CheckResult. It never
// returns a Go error and never echoes a response body (which could carry a
// secret reflected back by a misbehaving provider). The body is closed
// eagerly (its content is never needed) rather than deferred, so the
// close error can be checked without a named return.
func (v *Verifier) request(name string, req *http.Request) identity.CheckResult {
	resp, err := v.hc.Do(req)
	if err != nil {
		return identity.CheckResult{Name: name, Status: statusFailed, Message: "request failed"}
	}
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return identity.CheckResult{Name: name, Status: statusFailed, Message: fmt.Sprintf("http %d", resp.StatusCode)}
	}
	if closeErr != nil {
		return identity.CheckResult{Name: name, Status: statusFailed, Message: "close response body failed"}
	}
	return identity.CheckResult{Name: name, Status: statusOK}
}

// exitCode extracts a process exit code from an exec error, or -1 if err
// isn't an *exec.ExitError (e.g. the binary itself couldn't start).
func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// AuthedCloneURL embeds an x-access-token credential into an https clone URL
// for git ls-remote (GitHub's fine-grained/PAT convention). Non-https URLs
// (e.g. ssh, file://) or an empty token pass through unchanged. The result
// carries a live secret in-band — callers must never log or return it.
func AuthedCloneURL(repoURL, token string) string {
	const httpsPrefix = "https://"
	if token == "" || !strings.HasPrefix(repoURL, httpsPrefix) {
		return repoURL
	}
	return httpsPrefix + "x-access-token:" + token + "@" + strings.TrimPrefix(repoURL, httpsPrefix)
}
