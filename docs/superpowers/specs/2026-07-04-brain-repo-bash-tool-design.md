# Brain repo-inspection via a sandboxed `bash` tool

**Date:** 2026-07-04
**Status:** proposed
**Module:** `internal/brain` (+ new `internal/repo`, `cmd/kiln` wiring, `backend/Dockerfile`)
**Related:** 06 orchestrator-brain (§4 tool set, §5 pass loop, D7 prompt-as-behavior),
05 agent-runtime (AgentInspector read-seam pattern), 02 §2 (composition-root wiring)

## Problem

The brain marks tickets done (`accept_to_done`) even when the agent's work was
never pushed up from its ephemeral Amika sandbox. `accept_to_done` releases and
recycles the worker — "the workspace is gone" — so any uncommitted/unpushed work
is lost. The brain has **no tool to verify** what is actually on the remote: its
entire world is the injected board snapshot, the transcript, and the triggering
event. A working-directory prompt edit already tells the brain "verify the agent
pushed its work before marking done," but that instruction is currently
unactionable — there is nothing for the brain to check with.

`GITHUB_REPO_URL` and `GITHUB_AUTH_TOKEN` were added to `.env` for this purpose
and are currently unused.

## Goal

Give the brain a **general, read-capable window into the real project repository**
so it can verify pushed work before accepting a ticket, and search the repo for
information when a decision needs it — without breaking the brain's module
boundary (pure decision logic over narrow ports; it never shells out itself).

## Non-goals

- Structured/opinionated verify tools (`check_pushed(ticket)`): rejected — there
  is **no fixed ticket→branch convention** in the codebase (Amika manages branches
  internally), so the model must discover branches (`git branch -r`) and match by
  content. A general tool fits; a structured one would encode a convention that
  does not exist.
- Process/liveness inspection of agents (`pgrep`): dropped. On the orchestrator
  host `pgrep` sees only local processes, never the remote Amika sandboxes where
  agents run, so it cannot answer "is the agent alive?". Agent liveness already
  has its own seam (`AgentInspector` / `list_agents`). The allowlist instead
  carries real repository-search tools.

## Decisions (resolved with the user)

- **[x] Tool shape → a single general `bash` tool.** One tool whose input is a
  shell command string, run in a maintained local clone. Chosen over a structured
  verify tool (no branch convention to encode) and over a strict single-command
  argv tool (the model wants pipes/chaining to search and cross-reference).
- **[x] Clone lifecycle → clone once at startup, fetch on demand.** The
  composition root clones `GITHUB_REPO_URL` once at boot; per call the model runs
  `git fetch` itself to see fresh remote state. One clone reused across all passes.
- **[x] Access width → full git/gh access.** No subcommand filtering. The token
  runs against the user's own repo and the only "adversary" is a confused LLM, so
  the boundary is which *binaries* are reachable, not which subcommands.
- **[x] Enforcement → restricted-PATH shell.** `sh -c "<command>"` with `PATH`
  pointed at an `allowed-bin` directory of symlinks to just the whitelisted tools,
  plus a wall-clock timeout and an output cap. Full shell power (pipes, `&&`), but
  only whitelisted binaries reachable — no `rm`, `curl`, etc.
- **[x] Search tooling.** The allowlist includes `rg` (ripgrep) and other search
  utilities so the model can find information in the repo, not just check git.
- **[x] Clone failure is non-fatal.** If the boot clone fails (or the repo is
  unconfigured), Kiln still starts; the `bash` tool returns a clear
  "repo inspection unavailable: …" result instead of erroring the pass.

## Architecture

Mirrors the existing `AgentInspector` read-seam (05 §2, wired in
`cmd/kiln/adapters.go`). The brain gains one tool backed by one new port; all
infrastructure lives in a new module the brain cannot import.

```
brain.Service ──(bash tool)──> brain.RepoShell (port)
                                     ▲
                     repoShellAdapter │  (cmd/kiln/adapters.go)
                                     │
                              *repo.Shell        (internal/repo)
                                     │
             sh -c in <clone dir>, PATH=<allowed-bin>, GH_TOKEN, timeout, cap
```

### Component 1 — `internal/repo` (new module)

Owns every piece of infrastructure. Constructed at the composition root; unit
tested in isolation against local file-remotes (no network).

- **`Config`**: `RepoURL`, `AuthToken`, `Dir` (clone dir), and the derived
  `allowed-bin` dir path.
- **`New(cfg) (*Shell, error)` / boot**: ensure `Dir`; if not already a git repo,
  `git clone --filter=blob:none <RepoURL> <Dir>` — the clone is **unauthenticated**
  (v1 targets a public repo), so no token is embedded in the URL and none lands in
  the persisted `origin` remote (keeping it out of any `git remote -v` the brain runs
  and its logs); else leave the existing clone. Authenticated/private-repo clone is a
  later addition (`Config.AuthToken` is still carried for `gh`). Build the
  `allowed-bin` directory by
  symlinking the resolved absolute paths of each allowlisted binary. On any
  failure, return a `*Shell` in a **disabled** state (records the reason) rather
  than an error, so wiring stays non-fatal.
- **`Run(ctx, command string) (Result, error)`**:
  - Disabled shell → `Result{ Unavailable: true, Reason: … }`, no error.
  - Otherwise build `exec.CommandContext(ctxWithTimeout, "/bin/sh", "-c", command)`:
    - `Dir = cfg.Dir`
    - `Env = [ "PATH=<allowed-bin>", "HOME=<Dir parent>", "GH_TOKEN=<token>",
      "GIT_TERMINAL_PROMPT=0" ]` (deliberately minimal; nothing else inherited).
    - Capture combined stdout+stderr, cap at `outputCapBytes` (~16 KB, head+tail),
      record exit code and `TimedOut`.
  - `Result`: `Output string`, `ExitCode int`, `TimedOut bool`, `Truncated bool`,
    `Unavailable bool`, `Reason string`. `Run` returns a Go error only for an
    internal failure it cannot express as a `Result` (should be rare); a non-zero
    exit is a normal `Result`, not an error.
- **Constants**: `runTimeout = 30 * time.Second`, `outputCapBytes = 16 * 1024`.
- **Allowlist** (`allowed-bin` symlink targets):
  `git gh rg find ls grep head tail sort uniq wc awk sed cat`.
  (`git grep` is reachable via `git`; `sh` is invoked by absolute path, not via
  PATH.)

### Component 2 — `brain.RepoShell` port + `bash` tool

- **Port** (`ports.go`), analogous to `AgentInspector`, provider-neutral:
  ```go
  type RepoShell interface {
      Run(ctx context.Context, command string) (RepoResult, error)
  }
  type RepoResult struct {
      Output      string
      ExitCode    int
      TimedOut    bool
      Truncated   bool
      Unavailable bool
      Reason      string
  }
  ```
  Best-effort: a `Run` error or an `Unavailable` result becomes a tool result the
  pass loop absorbs (06 §5), never a pass failure. No compile-time assertion (the
  brain cannot import `internal/repo`; satisfied by the adapter).
- **Tool** (`tools.go`): add `ToolBash ToolName = "bash"` as tool #13 in the fixed
  `Tools` set, input schema `{ command: string (required) }`, description naming
  it a read-oriented shell into a clone of the project repo for verifying pushed
  work and searching the code.
- **Service field + constructor**: add `repo RepoShell` to `Service`, threaded
  through `NewService` after `agents AgentInspector`.
- **Dispatch** (`doBash`): `requireField` on `command`; call `s.repo.Run`; render
  the `RepoResult` for the model:
  - `Unavailable` → `"repo inspection unavailable: <reason>"` (IsError).
  - else a compact block: exit code, timed-out/truncated flags, then the output.
  Reuse `truncateHeadTail` if any additional capping is needed at render time.

### Component 3 — composition root (`cmd/kiln`)

- **`Config`** (`main.go`): add `GitHubRepoURL` (`GITHUB_REPO_URL`),
  `GitHubAuthToken` (`GITHUB_AUTH_TOKEN`), `RepoDir` (`KILN_REPO_DIR`, default
  `/var/lib/kiln/repo`).
- **`repoShellAdapter`** (`adapters.go`): bridges `*repo.Shell` → `brain.RepoShell`,
  converting `repo.Result` → `brain.RepoResult` (same shape; a value copy so the
  brain never imports `internal/repo`). `var _ brain.RepoShell = (*repoShellAdapter)(nil)`.
- **`wiring.go` `buildGraph`**: construct `repo.New(...)` (log a warning if it
  comes back disabled), wrap in `repoShellAdapter`, pass it as the new
  `brain.NewService` argument.

### Component 4 — Docker

The current final stage is `gcr.io/distroless/static-debian12:nonroot` — **no
shell, no binaries**, so the `bash` tool cannot run there. Change the final stage
to a base that carries a shell plus the allowlisted tools:

- `debian:bookworm-slim` + `apt-get install --no-install-recommends git gh
  ripgrep ca-certificates` (findutils/coreutils/grep/gawk/sed present in base), or
- `alpine` + `apk add git github-cli ripgrep`.

Keep the build stage and `CGO_ENABLED=0` static binary unchanged (runs on either).
Preserve the non-root run user. Ensure `KILN_REPO_DIR` is writable by that user.

### Component 5 — prompt

The working-dir "Marking As Done" addition is expanded to name the `bash` tool
concretely (still prose, unpinned per 06 D7): before `accept_to_done`, run
`git fetch` and confirm the agent's branch and its commits exist on the remote;
if the work is not there, it is not done — do not accept. Note the tool is
read-oriented and also usable to search the repo. Add `"bash"` to the tool-name
set asserted by `TestSystemPrompt_HasFeedToolGuidance` so the prompt keeps naming
the tool (name only, not prose).

## Data flow (verify-before-done)

1. `agent.turn_completed` event → brain pass.
2. Model calls `bash` → `git fetch origin && git log --oneline origin/<branch>`
   (branch discovered via `git branch -r` / matched to the ticket).
3. Adapter runs it in the clone; `RepoResult` fed back verbatim.
4. Branch + commits present → `accept_to_done`. Absent → the model keeps the
   ticket open / `send_to_agent` to push / `mark_blocked`, per its judgment.

## Testing

- **`internal/repo` (unit, hermetic):** create a bare repo as the "remote" and a
  working clone in `t.TempDir()`; assert (a) an allowlisted git command runs and
  returns expected output, (b) a non-allowlisted binary (`curl`, `rm`) fails
  because it is not on `PATH`, (c) `TimedOut` fires on a sleep past the timeout,
  (d) output over the cap is truncated with `Truncated` set, (e) a disabled shell
  (bad/empty repo URL) returns `Unavailable` without error.
- **brain (golden):** add a fake `RepoShell` to `fakes_test.go`; a golden pass
  where the scripted model calls `bash` then `accept_to_done`; assert the `bash`
  call reaches the port and the result renders. Update the offered-tools golden
  assertion(s) to include `bash`. Update `TestSystemPrompt_HasFeedToolGuidance`
  for `"bash"`.
- **`cmd/kiln`:** wiring compiles; `main_test` smoke unaffected (repo unconfigured
  → disabled shell, non-fatal).

## Risks / open points

- **Unauthenticated clone (v1).** The repo is public, so the clone uses the plain
  `RepoURL` and no token touches git — this keeps the token out of the persisted
  `origin` remote and therefore out of any `git remote -v` the brain runs (which
  would otherwise flow the token to the LLM and the logs). Private-repo support
  (`GIT_ASKPASS`/credential helper reading `GH_TOKEN`, plus a token-less remote) is
  a deliberate later addition; `Config.AuthToken` is already carried for `gh`.
- **`gh` needs network + a valid token**; a bad token surfaces as a normal
  non-zero-exit `Result` the model can read, not a crash.
- **Image size** grows moving off distroless; accepted as the cost of giving the
  brain a shell. Alpine keeps it smaller if size matters.
