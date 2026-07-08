# Design: mechanical push-gate on ticket Done

**Date:** 2026-07-08
**Status:** proposed
**Scope:** `internal/brain`, `internal/repo` (backend only). No wire-schema change, no DB migration.

## Problem

The brain marks tickets `done` when the coding agent has **not** actually landed the
work on `origin/main`, despite the system prompt telling it to verify. Confirmed from
production runs (Sentry `brain.tool` logs + Render logs, 2026-07-08):

- **Done is an unguarded state transition.** `update_ticket state:"done"` →
  `board.AcceptToDone` (`internal/board/service.go:255`) only checks the state machine
  (`state ∈ {working,blocked} && worker != nil`). No git is ever read. The
  "on origin/main" requirement exists solely as prose in `internal/brain/prompt.go:100-106`
  (typo-laden, buried, advisory).
- **The brain often skips the check.** e.g. ticket `bae18ae5` was marked `done` at
  09:59:54 with **no** preceding `bash` git check; it was re-opened to `blocked`
  ("marked done but no work was pushed to main. No branch exists for this ticket") at
  10:03. Same pattern on `fdc3a027` and `73bc58f6` (marked done immediately after a
  check whose output showed the work was *not* on origin/main).
- **Even when it checks, the check is unreliable.** The brain hallucinates working
  directories (`cd /tmp/repo`, `/tmp/project`, `/repo` → `exit 2`), sometimes reads
  local `main` instead of `origin/main`, and the `repo.Shell` clone is created once at
  boot and **never auto-fetched** (`internal/repo/repo.go:88,117`) — so a bare
  `git log origin/main` reads a stale remote-tracking ref.
- **Render auto-deploys on every push to main, restarting the backend mid-pass.**
  Render logs show `kiln exited with error: http shutdown: context deadline exceeded`
  at 09:44:26 and 09:59:50 with `context canceled` on in-flight turns; the bad done at
  09:59:54 landed 4s after a restart. Each restart interrupts the brain's bounded tool
  loop, so a "verify later" step may never run.

**Root cause:** marking-done depends entirely on the LLM voluntarily running an
unenforced, stale, restart-fragile git check — so it frequently trusts the agent's
self-report and closes tickets whose work never reached main.

## Approach (minimal mechanic)

Make the brain **prove** the push instead of asserting it. When the brain moves a
ticket to `done`, it must supply the commit SHA it claims carries the ticket's work.
The server verifies that SHA is a real commit and is on `origin/main` (against a fresh
fetch) before allowing the transition. The join key between ticket and commit is
minted by the brain at done-time — no agent-side convention, no branch-naming scheme,
no persisted state.

This is deliberately the *smallest* mechanic that closes the reported bug. If it proves
insufficient (e.g. the brain names a real-but-unrelated commit), we harden later —
see [Deferred](#deferred-not-in-this-change).

### Why the brain (not the agent) reports the SHA

The coding agents merge their own work to `origin/main`. The brain already has a
read-oriented repo clone (the `bash` tool / `repo.Shell`) and already inspects git to
find the relevant commit. So the SHA is something the brain can obtain; we simply
require it to hand over what it found and we check it. The agent runtime and its
free-text turn output are untouched.

## Components

### 1. `done_commit` argument on `update_ticket`

`UpdateTicketInput` (`internal/brain/tools.go:107`) gains:

```go
DoneCommit *string `json:"done_commit,omitempty"`
```

The `update_ticket` tool JSON schema + description are updated to document it: *the
`origin/main` commit SHA carrying this ticket's work; required when `state` is `"done"`.*

`done_commit` is a **brain-internal tool argument** (part of the Anthropic tool schema
the model calls), not a field on the `Ticket` that crosses the client wire schema.
Therefore **no `/schema` change and no DB migration** are required.

### 2. Validation (argument shape)

`validateUpdateState` (`internal/brain/tools.go:568`) gains: when `*in.State == stateDone`,

- `done_commit` must be present and non-empty, else malformed feedback:
  `update_ticket: state="done" requires done_commit (the origin/main commit SHA carrying this ticket's work)`.
- `done_commit` must match `^[0-9a-f]{7,40}$` (case-insensitive), else malformed:
  `update_ticket: done_commit must be a git commit SHA`.

Shape failures are `malformed` (the existing `malformedResultMsg` path) — the arguments
are wrong, and the model should re-issue with a SHA.

### 3. Verification port + implementation

Extend the brain's existing `RepoShell` port (`internal/brain/ports.go:135`) with:

```go
// VerifyOnMain fetches origin and reports whether sha is a real commit that is
// an ancestor of origin/main (i.e. merged to main). Best-effort like Run: an
// infra failure is surfaced as (result, nil) with OnMain=false and a Reason,
// never a pass failure.
VerifyOnMain(ctx context.Context, sha string) (RepoVerify, error)
```

`RepoVerify{ OnMain bool; Reason string; Unavailable bool }`.

Implemented on `repo.Shell` (`internal/repo/repo.go`) as a new `VerifyOnMain(sha)`:

1. `git fetch origin` (fresh remote state; defeats the stale-boot-clone problem).
2. `git rev-parse --verify --quiet <sha>^{commit}` — commit exists.
3. `git merge-base --is-ancestor <sha> origin/main` — exit 0 ⇒ on main.

Runs git via **argv** (`exec.CommandContext(gitBin, "merge-base", "--is-ancestor", sha, "origin/main")`),
not `sh -c` — no shell interpolation, so even the (already hex-validated) SHA cannot
inject. Same restricted PATH/env (`runEnv`) as `Run`, same timeout.

The `cmd/kiln` adapter that wraps `*repo.Shell` for `Run` gains the `VerifyOnMain`
passthrough.

### 4. The gate in the done path

In `doUpdateTicket`, when the patch sets `state:"done"`, before calling the board:

```
verify := repo.VerifyOnMain(ctx, *in.DoneCommit)
if verify.Unavailable {
    // fail closed: refuse done, explain
    return <typed tool error: cannot verify — repo shell unavailable>
}
if !verify.OnMain {
    return <typed tool error, NOT malformed>:
      "cannot mark ticket <id> done: commit <sha> is not on origin/main (<reason>).
       Have the agent merge the work to main, then accept it — or set the ticket
       blocked if it needs a decision."
}
// verified → proceed to AcceptToDone
```

The not-on-main result is a **precondition error, fed back verbatim** (like
`updateStepError`), not `malformed` — the arguments were valid, the precondition
failed, and the brain should self-correct (message the agent / block), exactly as the
prompt now instructs.

**Fail-closed on unavailability:** if the repo shell is disabled (no repo configured)
the done is refused. Production has the repo configured; refusing is correct for the
one job this gate exists to do. This is called out so a mis-configured env fails loudly
rather than silently reverting to trust-based done.

Field edits and approval in the same `update_ticket` call are unaffected; only the
`state=done` transition is gated.

### 5. Prompt + bash-tool-description fixes

Rewrite `internal/brain/prompt.go` "What Counts As Done" (lines 100-106) into a hard,
typo-free rule:

> ### What Counts As Done
> A ticket is done only when its change is merged to `origin/main`. To mark a ticket
> done you MUST pass `done_commit`: the `origin/main` commit SHA that carries the
> ticket's work. The system verifies the SHA is on `origin/main` and rejects the done
> if it is not. Use the `bash` tool to find it — run `git fetch origin` first, then
> inspect `git log origin/main`. If no such commit exists, the work is not done: use
> `send_to_agent` to have the agent merge it to main, and set the ticket blocked
> meanwhile. Do not tell the user when you message an agent for this purpose.

Fix the `bash` tool description (`internal/brain/tools.go:357-363`): state that commands
already run **inside** the repo clone (never `cd` into a path), and to run
`git fetch origin` before inspecting `origin/main` (the clone is not auto-updated
between commands). This removes the `cd /tmp/repo` (exit 2) and stale-`main` waste seen
in the logs, and lets the brain reliably find the SHA it must now supply.

### 6. Restart resilience

No new code. The gate makes a false-done structurally impossible regardless of when the
process restarts, because the verification re-runs on every done attempt. Documented
here so we don't build separate restart handling for this bug.

## Data flow

```
brain pass
  → update_ticket{ state:"done", done_commit:<sha> }
      → validateUpdate / validateUpdateState   (shape: present + hex)         [malformed if bad]
      → doUpdateTicket done branch
          → RepoShell.VerifyOnMain(sha)
              → git fetch origin
              → git rev-parse --verify <sha>^{commit}
              → git merge-base --is-ancestor <sha> origin/main
          → OnMain?  yes → board.AcceptToDone(id)
                     no  → typed tool error fed back  (brain: send_to_agent + block)
```

## Testing

- **`repo.Shell.VerifyOnMain`** (`internal/repo`): against a temp local git repo with a
  fake `origin` remote — (a) SHA merged to main → `OnMain=true`; (b) SHA on an
  unmerged branch → `false`; (c) unknown SHA → `false`; (d) fetch/verify surfaces the
  remote state after a new commit is pushed to the fake origin (proves the fetch
  matters); (e) disabled shell → `Unavailable=true`.
- **Brain validation** (`internal/brain`): `state=done` without `done_commit` → malformed;
  non-hex `done_commit` → malformed.
- **Brain gate** (`internal/brain`, fake `RepoShell`): `OnMain=false` → typed (non-malformed)
  tool error, `AcceptToDone` **not** called; `Unavailable=true` → refused; `OnMain=true`
  → `AcceptToDone` called once. Assert the not-on-main message is fed back verbatim.
- **Prompt**: existing prompt render test still passes; add an assertion that the Done
  section names `done_commit` (behavior-as-prompt lives under the same test gate, 06 D7).

## Deferred (not in this change)

If the minimal mechanic proves insufficient, harden with any of:

- **Anti-reuse / correspondence:** persist `done_commit` on the ticket (column +
  migration) and reject a SHA already used to close a *different* ticket, so the brain
  can't stamp every ticket with the latest main commit. (This is the known residual
  gap: the gate proves *a* real on-main commit was named, not that it is *this*
  ticket's work.)
- A ticket-id branch or commit-trailer convention to make the ticket↔commit join
  deterministic rather than brain-judged.

## Non-goals

- No change to the agent runtime, Amika adapter, or turn-output shape.
- No client/frontend change; no `/schema` change; no DB migration.
- Not addressing deploy-restart churn generally — only ensuring it can't produce a
  false-done.
