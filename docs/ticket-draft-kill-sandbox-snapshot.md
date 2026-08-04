# Ticket draft — capture the workspace before "Kill sandbox" throws it away

Drafted 2026-08-04 off `docs/root-cause-2026-08-04-followup.md` §5 (DL3) — the one **new**
data-loss risk found by that investigation, introduced by `57d8a1b` (deployed 2026-08-04 02:28).

Paste the title/body below into the board; the rest is working detail.

---

## Title

"Kill sandbox" destroys uncommitted work with no snapshot and no warning of what is lost

## Body

`57d8a1b` added two per-ticket manual controls, `POST /api/tickets/{id}/sandbox/kill` and
`/reassign`. Both are genuinely useful and the kill is, today, the **only** control that can reach
an orphaned `claude` process — the process dies with the VM, and no Kiln-side lock, lease or CAS can
touch it (see the duplicate-instances doc, C2).

The problem is what goes with it. `KillSandbox` (`backend/internal/board/service.go`) appends an
`agent.release` emission and, in its own words, "the workspace behind the slot is thrown away". It
commits nothing, stashes nothing, pushes nothing, and captures no diagnostic snapshot first.

That makes it a one-click, irreversible data-loss path — and the tickets it will be pressed on are
**exactly** the corrupted-tree incidents whose trees are most worth capturing:

- An agent may have hours of uncommitted work in that checkout.
- When two agents have collided in one working directory (the recurring bug this control was added
  to mitigate), that tree is the primary evidence. Killing it destroys the only artefact showing
  what the interleaved writes actually did.
- The user pressing the button cannot see what is uncommitted. Nothing tells them.

`ReassignSandbox` has the same exposure — it recycles the old sandbox after re-briefing on a new
slot.

### The change

1. **Snapshot before destroy.** Before the release emission, commit the working tree to a scratch
   branch (`salvage/<ticket-id>-<timestamp>`) and push it. If the push fails, that should be
   surfaced — but it should not block a kill the user has asked for, since "stop the runaway agent"
   is sometimes urgent. Log the branch name and put it in the response.
2. **Say what is being lost.** The confirmation should report the workspace's dirty state — file
   count, or at minimum "N uncommitted files" — rather than a generic "are you sure". If the
   snapshot in (1) succeeded, name the branch it landed on.
3. **Same treatment for `/reassign`**, which recycles the old sandbox on the same path.

### Interaction with the per-turn worktree proposal

If the per-turn `git worktree` recommendation lands (duplicate-instances doc §6 item 10), the
snapshot becomes nearly free — the turn's work is already on its own branch, so the kill only has to
avoid deleting it. Worth sequencing these together; if worktrees land first, this ticket shrinks to
"don't delete the branch, and say where it is".

### Scope note

This ticket does **not** change what the kill *does*, and should not be read as an objection to it.
The control is the right escape hatch and its own doc comment is honest about being destructive. The
ask is that an irreversible action preserve what it destroys and say so.

### Acceptance criteria

- [ ] Killing a sandbox with uncommitted changes leaves those changes recoverable from a named,
      pushed branch.
- [ ] The API response and the UI both name that branch.
- [ ] The confirmation states how much uncommitted work is at stake before the user commits to it.
- [ ] A snapshot failure is reported, not silently swallowed, and does not prevent the kill.
- [ ] `/reassign` behaves the same way on the sandbox it recycles.

### Tests

- Board-level: kill with a dirty tree ⇒ snapshot branch exists and carries the changes.
- Kill with a clean tree ⇒ no branch churn, no spurious empty commit.
- Snapshot failure (unreachable remote) ⇒ kill still completes, error surfaced.
