# Ticket draft — per-turn git worktree, and reap turns that can never complete

Drafted 2026-08-04 off [`root-cause-2026-08-04-followup.md`](root-cause-2026-08-04-followup.md)
§6 P2 item 10, DL1/DL1a and M6. Paste the title/body below into the board; the rest is working
detail.

This is the **P2 graceful-degradation** ticket. It stops nothing from duplicating — that is
[`ticket-draft-advisory-lock.md`](ticket-draft-advisory-lock.md) (P0) and
[`ticket-draft-turn-claim-cas.md`](ticket-draft-turn-claim-cas.md) (P0). It is what makes a
collision survivable when those have already failed, and it is the only measure in the set that
reaches an orphaned process.

---

## Title

Isolate each agent turn in its own git worktree, and reap turns that can never complete

## Body

Two pieces of graceful degradation, in one ticket because both are about surviving a collision
nothing upstream prevented, and both live at the sandbox boundary.

### (a) Per-turn git worktree

Today every turn in a sandbox writes the same shared checkout at `AMIKA_AGENT_CWD`. When two
`claude` processes land in one sandbox — which has happened **five confirmed times**, most recently
on this investigation's own dispatch — they interleave writes with no file locking and no awareness
of each other.

Make the wrapper create a `git worktree` per turn, so the shared checkout is never written
directly. A collision then degrades from *one corrupted tree* to *two branches, one of which is
discarded*.

**Why the existing rule does not cover this.** The `end-to-end-development` skill already says
"Isolate parallel work via a branch/worktree off the single monorepo" (SKILL.md:40). It does not
help here for two reasons: it is guidance to an agent rather than an enforced control, and both
duplicates are running **byte-identical instructions**, so any branch name either agent derives
would collide too. In the 08-03 `2f9bc4cf` incident only one of the two agents noticed the
collision and took the worktree escape; the other ran unisolated in the shared tree for 26 minutes,
editing `schema/openapi.yaml` and its generated output.

**The data-loss case this closes (DL1a).** `AGENTS.md` instructs every agent to commit to `main`
and **push to `origin/main`**, and ticket completion *requires* a pushed SHA. So the normal,
instructed final act of a turn is `git add` + `commit` + `push`. With two agents in one tree,
whichever commits first sweeps up the other's half-written files and **publishes them to the branch
everything deploys from** — with a green gate, if the mixture happened to compile. The gate checks
that the tree builds, not that it represents one coherent change. Per-turn worktrees make that
interleaving impossible rather than merely unlikely.

Note the loop this closes: the push to `main` triggers the auto-deploy, and the deploy opens the
next 66–83 s window in which a duplicate can occur.

**Cheaper adjacent backstops, worth doing alongside:**

- Have the sandbox wrapper (or `scripts/amika/setup.sh`) **refuse to start a second `claude` in the
  same `AMIKA_AGENT_CWD`**.
- Ask Amika to **reject or serialize a second concurrent `agent-send` on one sandbox**. Amika
  currently spawns a process per call with no per-sandbox exclusion, so Kiln is the only thing
  standing between a duplicate dispatch and two agents in one tree.

These two are the **only** measures that reach an *orphaned* process — one spawned by a backend
instance that then died before recording it, referenced by no turn row. No Kiln-side lock, lease or
CAS can touch that process, because nothing knows it exists.

### (b) Reap turns that can never complete

The set of non-terminal `agent_turns` rows is **growing monotonically**. A shutdown logs one
`agent: persist turn … context canceled` per row it was advancing, which gives an exact census:

| Shutdown | Non-terminal turns |
| --- | --- |
| 2026-08-03T13:08:31 | 12 |
| 2026-08-04T02:31:02 | **18** |

**Ten of the twelve are the same rows 13.4 h later**, including `delivery-8914` and
`delivery-8968` — the known orphan deliveries from the 08-03 incidents. `delivery-8295` was still
being polled a day after it started.

They can never terminate. `CheckTurn` maps a 404 to `Running: true` ("session not visible yet"), so
a turn whose Amika session no longer exists polls forever. `ListNonTerminal`
(`backend/internal/agent/postgres/store.go:63`) has no age bound, turns persist across restarts,
and `recordFailure` is never reached because the path returns no error — the wedge is completely
silent.

Each wedged row is polled every 2 s, indefinitely. The cost is unbounded growth in Amika poll
traffic and a `ListNonTerminal` result that never shrinks. **The duplicate-instruction bug is one
of its feeders**, since every orphaned duplicate contributes a row that can never complete — which
also makes this census the only after-the-fact count of orphans available.

**Fix:** a wall-clock deadline on `PhaseTurnStarted` — a turn that has polled `Running: true` for
N minutes fails and emits its error event rather than polling forever — or bound the consecutive
404 count and treat the conversation as lost. This is the turn-level deadline that has been wanted
since 08-02 A3.1.

**The steward does not cover this.** It pokes a stalled Working ticket after ~5 min, but the poke
starts a *new* turn without terminating the wedged one, and it explicitly never touches a
`building`/`starting` agent. It addresses the ticket-level symptom and makes the row-level leak
slightly worse.

### Acceptance criteria

- [ ] Two concurrent turns on one sandbox write to different worktrees; neither can see or stage
      the other's uncommitted files.
- [ ] A second `claude` in the same `AMIKA_AGENT_CWD` is refused or isolated, never started
      alongside.
- [ ] `git add -A` in one turn cannot stage another turn's files.
- [ ] No `agent_turns` row remains non-terminal past a bounded wall-clock deadline; the wedged row
      transitions to failed and emits its error event.
- [ ] The non-terminal census does not grow monotonically across a week of normal operation.
- [ ] Worktrees are cleaned up on turn completion — this must not become its own disk leak.

### Tests

- E2e in `/tests`: dispatch two instructions to one ticket in quick succession; assert two
  worktrees and an uncorrupted shared checkout.
- Unit: a turn stuck at `Running: true` past the deadline transitions to failed and emits its error
  event.
- Unit: consecutive-404 handling does not treat a permanently missing session as "running".

### Note for whoever picks this up

Sequence this **after** the advisory lock and the turn-machine CAS. Landing it first would mask the
duplicate rate — collisions would stop corrupting trees while still burning two agents, two LLM
passes and two board mutations per event — and the duplicate-turn signal is currently one of the
few ways the problem is visible at all.
