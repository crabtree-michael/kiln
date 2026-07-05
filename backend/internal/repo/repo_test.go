package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedRemote builds a real local "remote": a bare repo with one seeded commit.
// It returns the bare repo path, usable as a plain-filesystem RepoURL (no token
// injection), keeping the test hermetic (no network).
func seedRemote(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	barePath := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	git := func(dir string, args ...string) {
		t.Helper()
		full := append([]string{
			"-c", "user.name=Test",
			"-c", "user.email=test@example.com",
			"-c", "init.defaultBranch=main",
			"-c", "commit.gpgsign=false",
		}, args...)
		//nolint:gosec // G204: test helper running git with fixed, test-controlled args.
		cmd := exec.CommandContext(context.Background(), "git", full...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(root, "init", "--bare", barePath)
	git(root, "init", work)
	git(work, "-c", "user.name=Test", "commit", "--allow-empty", "-m", "seed: initial commit")
	// Write a file and commit it too, so allowlisted searches have content.
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello kiln\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "README.md")
	git(work, "commit", "-m", "add readme")
	commitSubject := "add readme"
	git(work, "branch", "-M", "main")
	git(work, "remote", "add", "origin", barePath)
	git(work, "push", "-u", "origin", "main")
	return barePath, commitSubject
}

// newEnabledShell constructs a Shell cloned from a local bare remote.
func newEnabledShell(t *testing.T) *Shell {
	t.Helper()
	bare, _ := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled unexpectedly: %s", s.reason)
	}
	return s
}

func TestRun_AllowlistedGitCommand(t *testing.T) {
	bare, subject := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	res := s.Run(context.Background(), "git log --oneline")
	if res.Unavailable {
		t.Fatalf("unexpected unavailable: %s", res.Reason)
	}
	if res.ExitCode != 0 {
		t.Fatalf("git log exit=%d output=%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, subject) {
		t.Fatalf("git log output missing seeded commit %q; got %q", subject, res.Output)
	}
}

func TestRun_NonAllowlistedBinaryUnreachable(t *testing.T) {
	s := newEnabledShell(t)

	// `whoami` exists on the host but is NOT symlinked into allowed-bin, so the
	// restricted PATH must make it unreachable. 127 is the shell's "not found".
	res := s.Run(context.Background(), "whoami")
	if res.ExitCode == 0 {
		t.Fatalf("non-allowlisted binary ran (exit 0); output=%q", res.Output)
	}
	if res.ExitCode != 127 {
		t.Fatalf("expected exit 127 (not found), got %d; output=%q", res.ExitCode, res.Output)
	}
	if !strings.Contains(strings.ToLower(res.Output), "not found") {
		t.Fatalf("expected 'not found' in output; got %q", res.Output)
	}

	// A destructive binary is equally unreachable — it must not have run.
	res = s.Run(context.Background(), "curl --version")
	if res.ExitCode == 0 {
		t.Fatalf("curl was reachable; output=%q", res.Output)
	}
}

func TestRun_Timeout(t *testing.T) {
	s := newEnabledShell(t)
	// Override the per-run timeout via the unexported hook so the test is fast and
	// deterministic. The command is a pure shell-builtin busy loop, so it needs no
	// allowlisted binary and only ends when the deadline kills it.
	s.timeout = 100 * time.Millisecond

	start := time.Now()
	res := s.Run(context.Background(), "while :; do :; done")
	if !res.TimedOut {
		t.Fatalf("expected TimedOut; got %+v", res)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run did not respect timeout: took %s", elapsed)
	}
}

func TestRun_OutputTruncated(t *testing.T) {
	s := newEnabledShell(t)
	// awk is allowlisted; emit well over outputCapBytes (16 KiB).
	res := s.Run(context.Background(), `awk 'BEGIN{for(i=0;i<10000;i++)print "xxxxxxxxxxxxxxxx"}'`)
	if res.ExitCode != 0 {
		t.Fatalf("awk exit=%d output=%q", res.ExitCode, res.Output)
	}
	if !res.Truncated {
		t.Fatalf("expected Truncated for oversized output; got %d bytes", len(res.Output))
	}
	if len(res.Output) > outputCapBytes {
		t.Fatalf("capped output exceeds cap: %d > %d", len(res.Output), outputCapBytes)
	}
	if !strings.Contains(res.Output, "output truncated") {
		t.Fatalf("expected elision marker in output")
	}
}

func TestRun_DisabledShell_EmptyRepoURL(t *testing.T) {
	s := New(context.Background(), Config{RepoURL: "", Dir: filepath.Join(t.TempDir(), "clone")})
	if !s.disabled {
		t.Fatal("expected disabled shell for empty RepoURL")
	}
	res := s.Run(context.Background(), "git log")
	if !res.Unavailable {
		t.Fatalf("expected Unavailable result; got %+v", res)
	}
	if res.Reason == "" {
		t.Fatal("expected a non-empty Reason on a disabled shell")
	}
}

func TestNew_ExistingCloneReused(t *testing.T) {
	bare, subject := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")

	// First New clones.
	s1 := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s1.disabled {
		t.Fatalf("first New disabled: %s", s1.reason)
	}
	// Second New over the same Dir must reuse the existing clone (not re-clone).
	s2 := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s2.disabled {
		t.Fatalf("second New disabled: %s", s2.reason)
	}
	res := s2.Run(context.Background(), "git log --oneline")
	if res.ExitCode != 0 || !strings.Contains(res.Output, subject) {
		t.Fatalf("reused clone log failed: exit=%d output=%q", res.ExitCode, res.Output)
	}
}
