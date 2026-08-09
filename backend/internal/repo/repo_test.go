package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// errTokenUnavailable stands in for a credential that cannot be resolved — a
// dead GitHub App installation (static, per the err113 rule against dynamic
// errors).
var errTokenUnavailable = errors.New("repo_test: token unavailable")

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

// gitOut runs git in dir with the hermetic test identity and returns trimmed
// combined output, failing the test on error. Unlike seedRemote's closure it
// returns the output, so callers can capture SHAs.
func gitOut(t *testing.T, dir string, args ...string) string {
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// seedRemoteWithBranch builds a bare remote whose main has one commit and whose
// unmerged "feature" branch has another. Returns (barePath, onMainSHA,
// offMainSHA). The work tree is left on main.
func seedRemoteWithBranch(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	barePath := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")

	gitOut(t, root, "init", "--bare", barePath)
	gitOut(t, root, "init", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello kiln\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "README.md")
	gitOut(t, work, "commit", "-m", "seed: initial commit")
	gitOut(t, work, "branch", "-M", "main")
	gitOut(t, work, "remote", "add", "origin", barePath)
	gitOut(t, work, "push", "-u", "origin", "main")
	onMain := gitOut(t, work, "rev-parse", "HEAD")

	gitOut(t, work, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(work, "feat.txt"), []byte("wip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "feat.txt")
	gitOut(t, work, "commit", "-m", "wip on feature")
	offMain := gitOut(t, work, "rev-parse", "HEAD")
	gitOut(t, work, "push", "-u", "origin", "feature")
	gitOut(t, work, "checkout", "main")

	return barePath, onMain, offMain
}

func TestVerifyOnMain(t *testing.T) {
	bare, onMain, offMain := seedRemoteWithBranch(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	if v := s.VerifyOnMain(context.Background(), onMain); !v.OnMain || v.Unavailable {
		t.Fatalf("expected OnMain for merged commit %s; got %+v", onMain, v)
	} else if v.Ref != onMain[:7] || !strings.HasSuffix(v.URL, "/commit/"+onMain) {
		t.Fatalf("expected commit link (ref %s, .../commit/%s); got ref %q url %q", onMain[:7], onMain, v.Ref, v.URL)
	} else if v.Summary != "seed: initial commit" {
		t.Fatalf("expected commit message as Summary; got %q", v.Summary)
	}
	if v := s.VerifyOnMain(context.Background(), offMain); v.OnMain {
		t.Fatalf("expected NOT OnMain for unmerged branch commit %s; got %+v", offMain, v)
	}
	if v := s.VerifyOnMain(context.Background(), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); v.OnMain {
		t.Fatalf("expected NOT OnMain for unknown sha; got %+v", v)
	}
}

// TestVerifyOnMain_FullCommitMessage proves the Summary carries the entire
// commit message (subject + body via %B), not just the subject line — the
// expandable done-card body (08 §7).
func TestVerifyOnMain_FullCommitMessage(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	gitOut(t, root, "init", "--bare", bare)
	gitOut(t, root, "init", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "README.md")
	// Subject, blank line, then a body paragraph — the shape a real commit has.
	const message = "feat(web): show a 404 page\n\nAdds a catch-all route so unmatched paths render the 404 view."
	gitOut(t, work, "commit", "-m", message)
	gitOut(t, work, "branch", "-M", "main")
	gitOut(t, work, "remote", "add", "origin", bare)
	gitOut(t, work, "push", "-u", "origin", "main")
	sha := gitOut(t, work, "rev-parse", "HEAD")

	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	v := s.VerifyOnMain(context.Background(), sha)
	if !v.OnMain {
		t.Fatalf("expected OnMain for %s; got %+v", sha, v)
	}
	if v.Summary != message {
		t.Fatalf("expected full commit message as Summary; got %q, want %q", v.Summary, message)
	}
}

// TestVerifyOnMain_FetchesFreshCommits proves the verify fetches: a commit
// pushed to origin/main AFTER the clone must still be recognized as on main.
func TestVerifyOnMain_FetchesFreshCommits(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	gitOut(t, root, "init", "--bare", bare)
	gitOut(t, root, "init", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "README.md")
	gitOut(t, work, "commit", "-m", "seed")
	gitOut(t, work, "branch", "-M", "main")
	gitOut(t, work, "remote", "add", "origin", bare)
	gitOut(t, work, "push", "-u", "origin", "main")

	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	// New commit on origin/main, pushed after the clone exists.
	if err := os.WriteFile(filepath.Join(work, "later.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "later.txt")
	gitOut(t, work, "commit", "-m", "landed after clone")
	later := gitOut(t, work, "rev-parse", "HEAD")
	gitOut(t, work, "push", "origin", "main")

	if v := s.VerifyOnMain(context.Background(), later); !v.OnMain {
		t.Fatalf("expected VerifyOnMain to fetch and recognize %s on origin/main; got %+v", later, v)
	}
}

// scriptedGit is a fake git runner for the fetch-reuse tests. It answers from a
// tiny model of the clone: `landed` is whether the refs it currently holds
// contain the commit, and a `fetch` sets it (i.e. the commit is on the remote
// and arrives with the fetch). Every subcommand it is asked for is recorded, so
// a test can assert how many fetches the gate actually ran.
func scriptedGit(calls *[]string, landed bool) func(args ...string) (int, string) {
	return func(args ...string) (int, string) {
		*calls = append(*calls, args[0])
		switch args[0] {
		case "fetch":
			landed = true
			return 0, ""
		case "rev-parse", "merge-base":
			if !landed {
				return 1, ""
			}
			return 0, ""
		case "show":
			return 0, "feat: land the work"
		default:
			return 1, "unexpected git " + args[0]
		}
	}
}

// count is how many of calls equal name.
func count(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

// TestVerifyOnMain_ReusesAFreshFetch pins the whole point of the freshness
// window: when origin was fetched moments ago (normally by the model's own
// `git fetch origin` earlier in the same pass), an accepted done costs ZERO
// further fetches — one per done, not two. It also pins what freshness may not
// do: a negative is never returned on reused refs, and a stale window always
// fetches first, so the gate still fails closed against fetched refs.
func TestVerifyOnMain_ReusesAFreshFetch(t *testing.T) {
	const sha = "abc1234"
	s := Shell{cfg: Config{RepoURL: "https://github.com/o/r"}}

	t.Run("fresh refs already prove it: no fetch", func(t *testing.T) {
		var calls []string
		v, fetched := s.verifyOnMain(sha, true, scriptedGit(&calls, true))
		if !v.OnMain {
			t.Fatalf("expected OnMain on refs that already contain the commit; got %+v", v)
		}
		if n := count(calls, "fetch"); n != 0 {
			t.Fatalf("a fresh fetch must be reused, not repeated; git fetch ran %d times (%v)", n, calls)
		}
		if fetched {
			t.Fatal("expected fetched=false when the recent fetch was reused")
		}
		if v.Ref != shortSHA(sha) || v.Summary != "feat: land the work" {
			t.Fatalf("reused-fetch positive must carry the same link and summary; got %+v", v)
		}
	})

	t.Run("stale refs: fetches first", func(t *testing.T) {
		var calls []string
		v, fetched := s.verifyOnMain(sha, false, scriptedGit(&calls, false))
		if !v.OnMain || !fetched {
			t.Fatalf("expected a fetch to land the commit and yield OnMain; got %+v (fetched=%v)", v, fetched)
		}
		if len(calls) == 0 || calls[0] != "fetch" {
			t.Fatalf("a stale window must fetch before deciding anything; calls = %v", calls)
		}
	})

	t.Run("fresh refs say no: re-fetches and re-decides", func(t *testing.T) {
		// The commit landed on origin AFTER the fetch we would have reused — the one
		// case reused refs can get wrong. It must not be reported as not-on-main.
		var calls []string
		v, fetched := s.verifyOnMain(sha, true, scriptedGit(&calls, false))
		if !v.OnMain || !fetched {
			t.Fatalf("a negative on reused refs must be re-decided after a real fetch; got %+v (fetched=%v)", v, fetched)
		}
		if n := count(calls, "fetch"); n != 1 {
			t.Fatalf("the fallback must fetch exactly once; git fetch ran %d times (%v)", n, calls)
		}
	})
}

// TestVerifyOnMain_ReusesAFreshFetch_RealGit is the reuse proved against real
// git rather than a scripted runner: the model fetches through Run, then the
// remote is moved out from under the clone so that ANY fetch would fail. A
// verify that still says OnMain can only have reused the model's fetch — the
// one fetch per accepted done this whole change is about.
func TestVerifyOnMain_ReusesAFreshFetch_RealGit(t *testing.T) {
	bare, onMain, _ := seedRemoteWithBranch(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	// The model's own `git fetch origin`, as the prompt tells it to run before it
	// looks up the ticket's commit.
	if res := s.Run(context.Background(), "git fetch origin"); res.ExitCode != 0 {
		t.Fatalf("model fetch failed (exit %d): %s", res.ExitCode, res.Output)
	}
	// Break the remote: from here a fetch cannot succeed.
	if err := os.Rename(bare, bare+".moved"); err != nil {
		t.Fatal(err)
	}

	v := s.VerifyOnMain(context.Background(), onMain)
	if !v.OnMain {
		t.Fatalf("expected the gate to reuse the model's fetch instead of running its own; got %+v", v)
	}
	if v.Summary != "seed: initial commit" {
		t.Fatalf("reused-fetch positive must still carry the commit message; got %q", v.Summary)
	}
}

// TestVerifyOnMain_FreshWindowStillSeesALaterPush is the previous test's
// fallback against real git: FETCH_HEAD is fresh, but the commit reached
// origin/main after that fetch. The gate must fetch again rather than reject
// work that is genuinely on main.
func TestVerifyOnMain_FreshWindowStillSeesALaterPush(t *testing.T) {
	bare, onMain, _ := seedRemoteWithBranch(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	// A first verify leaves a FETCH_HEAD seconds old — the state the model's own
	// `git fetch origin` leaves behind mid-pass.
	if v := s.VerifyOnMain(context.Background(), onMain); !v.OnMain {
		t.Fatalf("expected OnMain for the seeded commit; got %+v", v)
	}
	if !s.fetchIsFresh() {
		t.Fatal("expected the clone to read as freshly fetched after a verify")
	}

	// Now land a commit on origin/main that those fresh refs cannot know about.
	work := filepath.Join(t.TempDir(), "work2")
	gitOut(t, filepath.Dir(work), "clone", bare, work)
	if err := os.WriteFile(filepath.Join(work, "later.txt"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, work, "add", "later.txt")
	gitOut(t, work, "commit", "-m", "landed after the last fetch")
	later := gitOut(t, work, "rev-parse", "HEAD")
	gitOut(t, work, "push", "origin", "main")

	if v := s.VerifyOnMain(context.Background(), later); !v.OnMain {
		t.Fatalf("expected the gate to re-fetch and recognize %s on origin/main; got %+v", later, v)
	}
}

// TestFetchIsFresh proves the freshness signal is read from the clone itself, so
// it sees the fetch the MODEL ran through Run (an opaque `sh -c` string this
// package never parses) — the fetch the gate is there to reuse.
func TestFetchIsFresh(t *testing.T) {
	bare, _ := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")
	s := New(context.Background(), Config{RepoURL: bare, Dir: dir})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	// A clone writes no FETCH_HEAD, so a never-fetched clone reads as stale.
	if s.fetchIsFresh() {
		t.Fatal("a freshly cloned, never-fetched repo must read as stale")
	}

	if res := s.Run(context.Background(), "git fetch origin"); res.ExitCode != 0 {
		t.Fatalf("model fetch failed (exit %d): %s", res.ExitCode, res.Output)
	}
	if !s.fetchIsFresh() {
		t.Fatal("expected the model's own git fetch to make the clone read as fresh")
	}

	// Age FETCH_HEAD past the window: an old fetch is not reusable.
	old := time.Now().Add(-fetchFreshness - time.Minute)
	if err := os.Chtimes(filepath.Join(dir, ".git", "FETCH_HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	if s.fetchIsFresh() {
		t.Fatalf("a fetch older than %s must read as stale", fetchFreshness)
	}
}

// TestVerifyInPR drives the pure decision over a fake gh runner: the outcome
// hinges only on gh's exit code and the "<number>\t<html_url>\t<title>\n\n<body>"
// text the --jq program prints, so a stubbed runner exercises every branch
// (including the URL, Ref and Summary it lifts for the feed link and expandable
// card body) without a real GitHub API.
func TestVerifyInPR(t *testing.T) {
	var s Shell
	const sha = "abc1234"
	const (
		prURL = "https://github.com/o/r/pull/42"
		prRef = "#42"
		// A title, a blank line, then a body — the "title\n\nbody" shape the jq
		// joins and the whole thing rides Summary as the expandable card body.
		titleAndBody = "fix(web): show a 404 page\n\n" +
			"Adds a catch-all route so unmatched paths render the 404 view."
	)

	cases := []struct {
		name        string
		code        int
		out         string
		wantInPR    bool
		wantURL     string
		wantRef     string
		wantSummary string
	}{
		{
			"associated with a PR", 0,
			"42\t" + prURL + "\tfix(web): show a 404 page",
			true, prURL, prRef, "fix(web): show a 404 page",
		},
		{
			// Title + body: the title is the preview, the body follows on expand.
			"associated with a PR, title and body", 0,
			"42\t" + prURL + "\t" + titleAndBody,
			true, prURL, prRef, titleAndBody,
		},
		{
			// GitHub PR bodies arrive CRLF; cleanMessage strips the carriage
			// returns so the client renders clean line breaks.
			"associated with a PR, CRLF body normalized", 0,
			"42\t" + prURL + "\ttitle\r\n\r\nbody line\r\n",
			true, prURL, prRef, "title\n\nbody line",
		},
		{
			"associated with a PR, empty title", 0,
			"42\t" + prURL + "\t",
			true, prURL, prRef, "",
		},
		{"no associated PR (empty output)", 0, "", false, "", "", ""},
		{"gh error (unknown commit / auth)", 1, "HTTP 422: No commit found for SHA", false, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			run := func(args ...string) (int, string) {
				gotArgs = args
				return tc.code, tc.out
			}
			v := s.verifyInPR(sha, run)
			if v.InPR != tc.wantInPR {
				t.Fatalf("InPR = %v, want %v (%+v)", v.InPR, tc.wantInPR, v)
			}
			if v.URL != tc.wantURL || v.Ref != tc.wantRef {
				t.Fatalf("URL/Ref = %q/%q, want %q/%q", v.URL, v.Ref, tc.wantURL, tc.wantRef)
			}
			if v.Summary != tc.wantSummary {
				t.Fatalf("Summary = %q, want %q", v.Summary, tc.wantSummary)
			}
			if !tc.wantInPR && v.Reason == "" {
				t.Fatal("expected a Reason on a negative result")
			}
			const jq = `if length == 0 then "" else "\(.[0].number)\t\(.[0].html_url)\t\(.[0].title)\n\n\(.[0].body // "")" end`
			want := []string{"api", "repos/{owner}/{repo}/commits/" + sha + "/pulls", "--jq", jq}
			if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
				t.Fatalf("gh args = %v, want %v", gotArgs, want)
			}
		})
	}
}

func TestVerifyInPR_DisabledShell(t *testing.T) {
	s := New(context.Background(), Config{RepoURL: "", Dir: filepath.Join(t.TempDir(), "clone")})
	if !s.disabled {
		t.Fatal("expected disabled shell")
	}
	v := s.VerifyInPR(context.Background(), "abc1234")
	if !v.Unavailable {
		t.Fatalf("expected Unavailable on disabled shell; got %+v", v)
	}
	if v.InPR {
		t.Fatal("disabled shell must not report InPR")
	}
}

func TestVerifyOnMain_DisabledShell(t *testing.T) {
	s := New(context.Background(), Config{RepoURL: "", Dir: filepath.Join(t.TempDir(), "clone")})
	if !s.disabled {
		t.Fatal("expected disabled shell")
	}
	v := s.VerifyOnMain(context.Background(), "abc1234")
	if !v.Unavailable {
		t.Fatalf("expected Unavailable on disabled shell; got %+v", v)
	}
	if v.OnMain {
		t.Fatal("disabled shell must not report OnMain")
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

// The credential is resolved PER INVOCATION, not once at boot. That is the
// whole reason Config.Token is a function: a GitHub App installation token
// lives about an hour, and a long-running brain outlives many of them — a value
// captured when the Shell was built would be dead by the time it was used.
func TestRunResolvesTheTokenOnEveryInvocation(t *testing.T) {
	bare, _ := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")

	var calls int
	s := New(context.Background(), Config{
		RepoURL: bare,
		Dir:     dir,
		Token: func(context.Context) (string, error) {
			calls++
			return fmt.Sprintf("minted-%d", calls), nil
		},
	})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}
	// The clone itself resolved once; count from there so the assertion is about
	// the Runs, not about how many times boot happened to ask.
	afterClone := calls

	res := s.Run(context.Background(), "echo $GH_TOKEN")
	if res.ExitCode != 0 {
		t.Fatalf("echo exit=%d output=%q", res.ExitCode, res.Output)
	}
	first := strings.TrimSpace(res.Output)

	res = s.Run(context.Background(), "echo $GH_TOKEN")
	if res.ExitCode != 0 {
		t.Fatalf("second echo exit=%d output=%q", res.ExitCode, res.Output)
	}
	second := strings.TrimSpace(res.Output)

	if first == second {
		t.Errorf("GH_TOKEN was %q both times; the source must be re-resolved per run", first)
	}
	if got := calls - afterClone; got != 2 {
		t.Errorf("token resolved %d times across 2 runs, want 2", got)
	}
}

// A credential that cannot be resolved must not take the whole repo tool down:
// `gh` reports its own auth error (which the brain reads as a failed command),
// and every git command — the majority of what this shell runs — keeps working.
func TestRunSurvivesAnUnresolvableToken(t *testing.T) {
	bare, subject := seedRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")

	s := New(context.Background(), Config{
		RepoURL: bare,
		Dir:     dir,
		Token: func(context.Context) (string, error) {
			return "", errTokenUnavailable
		},
	})
	if s.disabled {
		t.Fatalf("shell disabled: %s", s.reason)
	}

	res := s.Run(context.Background(), "git log --oneline")
	if res.ExitCode != 0 {
		t.Fatalf("git log exit=%d output=%q — git needs no credential here", res.ExitCode, res.Output)
	}
	if !strings.Contains(res.Output, subject) {
		t.Fatalf("git log output missing seeded commit %q; got %q", subject, res.Output)
	}

	res = s.Run(context.Background(), "echo \"[$GH_TOKEN]\"")
	if got := strings.TrimSpace(res.Output); got != "[]" {
		t.Errorf("GH_TOKEN = %q, want empty when the source fails", got)
	}
}

// A nil source is the unconfigured deployment, and behaves the same way: no
// credential, no crash.
func TestRunWithNoTokenSource(t *testing.T) {
	s := newEnabledShell(t) // built with no Token at all
	res := s.Run(context.Background(), "echo \"[$GH_TOKEN]\"")
	if got := strings.TrimSpace(res.Output); got != "[]" {
		t.Errorf("GH_TOKEN = %q, want empty with no source configured", got)
	}
}
