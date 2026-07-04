package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// runTimeout bounds one Run's wall-clock; commands past it are killed and the
	// Result carries TimedOut.
	runTimeout = 30 * time.Second
	// outputCapBytes caps the combined stdout+stderr kept in a Result; larger
	// output is elided head+tail and Truncated is set.
	outputCapBytes = 16 * 1024
)

// allowlist is the set of binaries symlinked into allowed-bin; PATH points there
// so nothing else on the host is reachable. Order is stable for deterministic
// rebuilds. `git grep` is reached via `git`; `sh` is invoked by absolute path.
var allowlist = []string{
	"git", "gh", "rg", "find", "ls", "grep",
	"head", "tail", "sort", "uniq", "wc", "awk", "sed", "cat",
}

// Config is the boot input for a Shell.
type Config struct {
	RepoURL   string // e.g. https://github.com/owner/name (may be empty)
	AuthToken string // GitHub token, embedded into an https clone URL
	Dir       string // absolute path to the clone directory
}

// Result is the outcome of one Run. A non-zero ExitCode is a normal Result, not
// an error. Unavailable marks a disabled shell (no repo / clone failed).
type Result struct {
	Output      string // combined stdout+stderr, capped
	ExitCode    int
	TimedOut    bool
	Truncated   bool
	Unavailable bool   // true when the shell is disabled (no repo / clone failed)
	Reason      string // why Unavailable, "" otherwise
}

// Shell is a maintained clone plus a restricted-PATH command runner over it.
// The zero value is not usable; construct with New.
type Shell struct {
	cfg           Config
	allowedBinDir string
	home          string
	disabled      bool
	reason        string

	// timeout is the per-run wall-clock bound. Defaults to runTimeout; overridable
	// by tests via the unexported hook so a timeout can be exercised deterministically.
	timeout time.Duration
}

// New clones at boot (non-fatal). On any failure it returns a *Shell in a
// DISABLED state whose Run always yields Result{Unavailable:true, Reason:...}.
// It never returns an error — wiring must stay non-fatal.
func New(ctx context.Context, cfg Config) *Shell {
	s := &Shell{cfg: cfg, timeout: runTimeout}

	if cfg.RepoURL == "" || cfg.Dir == "" {
		return s.disable(ctx, "repository not configured (GITHUB_REPO_URL / KILN_REPO_DIR unset)")
	}

	s.home = filepath.Dir(cfg.Dir)
	s.allowedBinDir = filepath.Join(s.home, "allowed-bin")

	// Ensure the allowed-bin directory first: git must be reachable to clone, and a
	// missing git disables the shell.
	if reason, ok := s.buildAllowedBin(); !ok {
		return s.disable(ctx, reason)
	}

	// Ensure the clone. If Dir is already a git repo, leave it; else clone.
	if !isGitRepo(cfg.Dir) {
		if err := os.MkdirAll(filepath.Dir(cfg.Dir), 0o755); err != nil {
			return s.disable(ctx, fmt.Sprintf("prepare clone dir: %v", err))
		}
		if out, err := s.clone(ctx); err != nil {
			return s.disable(ctx, fmt.Sprintf("git clone failed: %v: %s", err, strings.TrimSpace(out)))
		}
	}

	slog.InfoContext(ctx, "repo.shell.ready", "repo_url", cfg.RepoURL, "dir", cfg.Dir)
	return s
}

// disable records the reason and returns the shell in a disabled state.
func (s *Shell) disable(ctx context.Context, reason string) *Shell {
	s.disabled = true
	s.reason = reason
	slog.WarnContext(ctx, "repo.shell.disabled", "reason", reason, "repo_url", s.cfg.RepoURL)
	return s
}

// clone runs `git clone --filter=blob:none <RepoURL> <Dir>`. The clone is
// UNAUTHENTICATED — v1 targets a public repo, so no token goes into the URL.
// Keeping the token out of the clone URL keeps it out of the persisted origin
// remote (and thus out of any `git remote -v` the brain runs and its logs).
// Private-repo auth is a later addition (Config.AuthToken is still carried for
// gh; see runEnv). The returned string is combined git output.
func (s *Shell) clone(ctx context.Context) (string, error) {
	gitBin := filepath.Join(s.allowedBinDir, "git")
	cmd := exec.CommandContext(ctx, gitBin, "clone", "--filter=blob:none", s.cfg.RepoURL, s.cfg.Dir)
	cmd.Env = s.runEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildAllowedBin (re)creates the allowed-bin directory with symlinks to the
// resolved absolute path of each allowlisted binary found on the host. It is
// idempotent. Returns ok=false (with a reason) only when git is unavailable.
func (s *Shell) buildAllowedBin() (reason string, ok bool) {
	if err := os.MkdirAll(s.allowedBinDir, 0o755); err != nil {
		return fmt.Sprintf("create allowed-bin: %v", err), false
	}
	gitFound := false
	for _, name := range allowlist {
		abs, err := exec.LookPath(name)
		if err != nil {
			continue // not on the host — skip, per the design
		}
		abs, err = filepath.Abs(abs)
		if err != nil {
			continue
		}
		link := filepath.Join(s.allowedBinDir, name)
		_ = os.Remove(link) // idempotent rebuild
		if err := os.Symlink(abs, link); err != nil {
			continue
		}
		if name == "git" {
			gitFound = true
		}
	}
	if !gitFound {
		return "git not found on host", false
	}
	return "", true
}

// Run executes `sh -c command` in the clone with a restricted PATH, a timeout,
// and an output cap. Never returns an error: a non-zero exit is a normal
// Result; a disabled shell yields Result{Unavailable:true}.
func (s *Shell) Run(ctx context.Context, command string) Result {
	if s.disabled {
		return Result{Unavailable: true, Reason: s.reason}
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = s.cfg.Dir
	cmd.Env = s.runEnv()

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()

	res := Result{}
	res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)

	switch {
	case err == nil:
		res.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			// Could not start the process (e.g. /bin/sh missing) — surface as a
			// non-zero exit with the error text rather than a Go error.
			res.ExitCode = -1
			buf.WriteString("\n" + err.Error())
		}
	}

	res.Output, res.Truncated = capOutput(buf.Bytes())

	slog.InfoContext(ctx, "repo.shell.run",
		"exit_code", res.ExitCode,
		"timed_out", res.TimedOut,
		"truncated", res.Truncated,
		"output_bytes", len(res.Output),
	)
	return res
}

// runEnv is the deliberately minimal environment; nothing else is inherited.
//
// The git clone is unauthenticated (public repo, v1), so git needs no
// credentials at all. GH_TOKEN is passed only for `gh`, which requires a token
// even against a public repo; it is never used by git and so never lands in the
// remote URL.
//
// GIT_CONFIG_GLOBAL/SYSTEM=/dev/null cut git off from ALL host configuration.
// This is a hard requirement, not hygiene: a developer host's git config
// commonly sets `credential.helper = osxkeychain` (or libsecret on Linux), and
// without this isolation a clone/fetch could invoke that helper — which can pop
// a blocking OS keychain prompt. GIT_TERMINAL_PROMPT=0 turns any auth failure
// into a fast error instead of a hang. The result is a hermetic clone that never
// touches the host keychain and behaves identically on a dev laptop and in the
// distro-clean container.
func (s *Shell) runEnv() []string {
	return []string{
		"PATH=" + s.allowedBinDir,
		"HOME=" + s.home,
		"GH_TOKEN=" + s.cfg.AuthToken,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		// Force the credential-helper list empty for every git invocation,
		// regardless of any config file — belt and suspenders with the /dev/null
		// configs above. An empty value resets the list, so no helper (osxkeychain,
		// libsecret, …) is ever spawned, and the host keychain is never touched.
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=",
	}
}

// isGitRepo reports whether dir already holds a git repository (has a .git
// entry). A missing dir or a non-repo dir returns false.
func isGitRepo(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

// capOutput caps b to outputCapBytes, keeping head+tail with an elision marker
// when it exceeds the cap. The second return is whether truncation occurred.
func capOutput(b []byte) (string, bool) {
	if len(b) <= outputCapBytes {
		return string(b), false
	}
	const marker = "\n...[output truncated]...\n"
	keep := outputCapBytes - len(marker)
	if keep < 2 {
		return string(b[:outputCapBytes]), true
	}
	head := keep / 2
	tail := keep - head
	var sb strings.Builder
	sb.Grow(outputCapBytes)
	sb.Write(b[:head])
	sb.WriteString(marker)
	sb.Write(b[len(b)-tail:])
	return sb.String(), true
}
