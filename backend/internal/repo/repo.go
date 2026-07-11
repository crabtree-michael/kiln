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
	// dirPerm is the permission for directories this shell creates (the clone
	// parent and allowed-bin): owner rwx, group rx, no world access.
	dirPerm = 0o750
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

// Verify is the outcome of a merge-gate check (VerifyOnMain or VerifyInPR).
// OnMain is true only when the commit exists and is an ancestor of origin/main;
// InPR is true only when the commit is associated with a pull request. Exactly
// one of the two is meaningful per call — the gate mode picks which check runs.
// Unavailable marks a disabled shell (no repo / clone failed); Reason explains a
// negative result or Unavailable.
//
// On a positive result URL and Ref name the completed work on GitHub so the feed
// can link to it (the "done" card's second line): for VerifyOnMain URL is the
// commit page and Ref the abbreviated SHA; for VerifyInPR URL is the pull
// request page and Ref its "#<number>". Both are empty on a negative or
// unavailable result.
type Verify struct {
	OnMain      bool
	InPR        bool
	URL         string
	Ref         string
	Unavailable bool
	Reason      string
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
		if err := os.MkdirAll(filepath.Dir(cfg.Dir), dirPerm); err != nil {
			return s.disable(ctx, fmt.Sprintf("prepare clone dir: %v", err))
		}
		if out, err := s.clone(ctx); err != nil {
			return s.disable(ctx, fmt.Sprintf("git clone failed: %v: %s", err, strings.TrimSpace(out)))
		}
	}

	slog.InfoContext(ctx, "repo.shell.ready", "repo_url", cfg.RepoURL, "dir", cfg.Dir)
	return s
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

	// Running an arbitrary command string is the entire purpose of this tool; it
	// is contained by a restricted PATH (only the allowlisted symlinks are
	// reachable), a hermetic env, a timeout, and an output cap — see runEnv.
	//nolint:gosec // G204: intentional shell runner, contained by restricted PATH/env (allowlist).
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = s.cfg.Dir
	cmd.Env = s.runEnv()

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()

	res := Result{}
	res.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		// Could not start the process (e.g. /bin/sh missing) — surface as a
		// non-zero exit with the error text rather than a Go error.
		res.ExitCode = -1
		buf.WriteString("\n" + err.Error())
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

// VerifyOnMain fetches origin and reports whether sha is a real commit that is
// an ancestor of origin/main (i.e. merged to main). Best-effort like Run: an
// infra failure yields OnMain=false with a Reason, never an error.
//
// git is invoked via argv (not sh -c), so sha is never shell-interpreted — a
// belt-and-suspenders pairing with the caller's hex validation. Each step runs
// with the same restricted PATH/env and per-run timeout as Run.
func (s *Shell) VerifyOnMain(ctx context.Context, sha string) Verify {
	if s.disabled {
		return Verify{Unavailable: true, Reason: s.reason}
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	v := s.verifyOnMain(sha, s.argvRunner(ctx, "git"))
	slog.InfoContext(ctx, "repo.shell.verify", "sha", sha, "on_main", v.OnMain, "reason", v.Reason)
	return v
}

// VerifyInPR reports whether sha is associated with a pull request (open or
// merged) on the project's GitHub remote. It gates update_ticket state="done"
// under the "pr" merge-gate mode (06 §7). Best-effort like VerifyOnMain: an
// infra/auth failure yields InPR=false with a Reason, never an error, and a
// disabled shell yields Unavailable — the gate fails closed on both.
//
// Unlike VerifyOnMain this asks the GitHub API (via gh), not local git, so it
// recognizes work on an unmerged branch that was never fetched into the clone.
// gh resolves the {owner}/{repo} from the clone's origin remote and reads the
// token from GH_TOKEN (runEnv); sha reaches gh via argv, never shell-interpreted.
func (s *Shell) VerifyInPR(ctx context.Context, sha string) Verify {
	if s.disabled {
		return Verify{Unavailable: true, Reason: s.reason}
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	v := s.verifyInPR(sha, s.argvRunner(ctx, "gh"))
	slog.InfoContext(ctx, "repo.shell.verify_pr", "sha", sha, "in_pr", v.InPR, "reason", v.Reason)
	return v
}

// argvRunner returns a runner that execs the named allowlisted binary by argv
// (no shell, so arguments are never interpreted) in the clone, with the same
// restricted PATH/env and shared ctx timeout as Run. Shared by the verify calls.
func (s *Shell) argvRunner(ctx context.Context, bin string) func(args ...string) (int, string) {
	binPath := filepath.Join(s.allowedBinDir, bin)
	return func(args ...string) (int, string) {
		//nolint:gosec // G204: fixed subcommand of an allowlisted bin; argv (no shell), restricted PATH/env.
		cmd := exec.CommandContext(ctx, binPath, args...)
		cmd.Dir = s.cfg.Dir
		cmd.Env = s.runEnv()
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0, strings.TrimSpace(string(out))
		case errors.As(err, &exitErr):
			return exitErr.ExitCode(), strings.TrimSpace(string(out))
		default:
			return -1, strings.TrimSpace(string(out) + "\n" + err.Error())
		}
	}
}

// verifyOnMain is VerifyOnMain's pure decision over an injected git runner:
// fetch origin, require the commit to exist, then require it to be an ancestor
// of origin/main. Split out so the sequencing is testable and VerifyOnMain owns
// only the exec/log wiring.
func (s *Shell) verifyOnMain(sha string, run func(args ...string) (int, string)) Verify {
	if code, out := run("fetch", "origin"); code != 0 {
		return Verify{Reason: fmt.Sprintf("git fetch origin failed (exit %d): %s", code, out)}
	}
	if code, out := run("rev-parse", "--verify", "--quiet", sha+"^{commit}"); code != 0 {
		return Verify{Reason: fmt.Sprintf("commit %s not found in repo: %s", sha, out)}
	}
	switch code, out := run("merge-base", "--is-ancestor", sha, "origin/main"); code {
	case 0:
		return Verify{OnMain: true, URL: commitURL(s.cfg.RepoURL, sha), Ref: shortSHA(sha)}
	case 1:
		return Verify{Reason: fmt.Sprintf("commit %s is not an ancestor of origin/main", sha)}
	default:
		return Verify{Reason: fmt.Sprintf("git merge-base failed (exit %d): %s", code, out)}
	}
}

// verifyInPR is VerifyInPR's pure decision over an injected gh runner: ask the
// GitHub API for the pull requests associated with the commit and read the
// first one's number and web URL. A non-zero exit (unknown commit, auth failure,
// no network) fails closed with a Reason; an empty array means the commit is in
// no PR. Split out so the decision is testable and VerifyInPR owns only the
// exec/log wiring.
func (s *Shell) verifyInPR(sha string, run func(args ...string) (int, string)) Verify {
	// {owner}/{repo} are resolved by gh from the clone's origin remote; the --jq
	// program (gh has jq built in) emits "<number>\t<html_url>" for the first
	// associated PR, or nothing when the array is empty. A tab separates the two
	// so a URL containing spaces (it won't, but be safe) still parses cleanly.
	const jq = `if length == 0 then "" else "\(.[0].number)\t\(.[0].html_url)" end`
	code, out := run("api", "repos/{owner}/{repo}/commits/"+sha+"/pulls", "--jq", jq)
	if code != 0 {
		return Verify{Reason: fmt.Sprintf("gh could not list pull requests for %s (exit %d): %s", sha, code, out)}
	}
	if out == "" {
		return Verify{Reason: fmt.Sprintf("commit %s is not associated with any pull request", sha)}
	}
	number, url, _ := strings.Cut(out, "\t")
	return Verify{InPR: true, URL: url, Ref: "#" + number}
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
	// The command is fixed; only the configured repo URL/dir vary, and git runs
	// with the restricted PATH/env of runEnv.
	//nolint:gosec // G204: configured clone of a trusted repo URL, restricted PATH/env.
	cmd := exec.CommandContext(ctx, gitBin, "clone", "--filter=blob:none", s.cfg.RepoURL, s.cfg.Dir)
	cmd.Env = s.runEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildAllowedBin (re)creates the allowed-bin directory with symlinks to the
// resolved absolute path of each allowlisted binary found on the host. It is
// idempotent. Returns ok=false (with a reason) only when git is unavailable.
func (s *Shell) buildAllowedBin() (string, bool) {
	if err := os.MkdirAll(s.allowedBinDir, dirPerm); err != nil {
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
		//nolint:errcheck,gosec // idempotent rebuild: a missing link is expected and fine.
		os.Remove(link)
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

// commitURL builds the GitHub web URL for a commit from the configured repo URL
// (e.g. https://github.com/owner/name -> .../commit/<sha>). Trailing slashes and
// a ".git" suffix are stripped so an https clone URL in either form resolves. An
// empty repo URL yields "" (no link), which the feed renders as a link-less card.
func commitURL(repoURL, sha string) string {
	base := strings.TrimSuffix(strings.TrimRight(repoURL, "/"), ".git")
	if base == "" {
		return ""
	}
	return base + "/commit/" + sha
}

// shortSHA abbreviates a commit SHA to the conventional 7 characters for display
// as a link label, leaving an already-short (or non-hex) value untouched.
func shortSHA(sha string) string {
	const abbrev = 7
	if len(sha) <= abbrev {
		return sha
	}
	return sha[:abbrev]
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
	// halves splits the kept budget between a head slice and a tail slice.
	const halves = 2
	keep := outputCapBytes - len(marker)
	if keep < halves {
		return string(b[:outputCapBytes]), true
	}
	head := keep / halves
	tail := keep - head
	var sb strings.Builder
	sb.Grow(outputCapBytes)
	sb.Write(b[:head])
	sb.WriteString(marker)
	sb.Write(b[len(b)-tail:])
	return sb.String(), true
}
