---
name: orchestrator-brain
description: Use when working in the brain module — the (board state + event) → actions LLM decision step that wakes on one event, loads state, runs one bounded tool loop, and emits actions from a fixed tool set. Backend anchor internal/brain. Spec docs/specs/06-orchestrator-brain.md.
---

# Orchestrator brain (mechanics decided by spec 06)

## What it is

The `(board state + event) → actions` decision step (`01` §6): wake on one event, load state,
reason once, emit actions from a fixed tool set. `HandleEvent(ctx, event)` is the runtime's
Brain port. It consumes an LLM port (Anthropic client; scripted fake in tests), the Board API,
and Say + ConversationReader. **Stateless — no tables, no migrations.**

**Model and effort are backend-only, never per-project.** Default `claude-sonnet-5`
(`KILN_BRAIN_MODEL`) and `output_config.effort` default `medium` (`KILN_BRAIN_EFFORT`),
resolved at the composition root. Effort is set *explicitly* because leaving it unset defaults
to the API's `high` on every round of every pass — a deliberation budget a dispatcher with
thinking disabled does not need (`docs/brain-optimization-2026-08-05.md` §4).

**Input contract: fresh context per pass** — last 20 transcript messages + the event (agent
output truncated ~8k head+tail). **The board is NOT injected**; the model pulls it via
`list_tickets`/`get_ticket`, so a pass spends no tokens on state it doesn't need. (06 D3's
"state is injected" is superseded.) The system prompt is a single unversioned Go template in
the repo.

**The pass** is a bounded tool loop, `MaxToolRounds = 12` (raised from 8 to absorb the board
reads a pass now makes before acting), tool errors fed back verbatim, no mid-pass snapshot
refresh, no streaming.

**A round that is cut off is repaired, not returned from.** `maxOutputTokens` is 16384 (raised
from 4096, which a real batched-read-plus-instructions round hit), and `max_tokens` maps to its
own `StopMaxTokens` rather than being folded into `StopEndTurn`. The loop dispatches everything
in a truncated round *except its last call* — the only one the cut can land inside — and
re-prompts with a note saying the dropped call never ran. Nothing else drops calls silently:
every path that discards them logs which ones (`logUndispatchedCalls`).

**Idempotency has no dedupe table**: fresh state + the board's strict preconditions (03 D8) +
the prompt rule *"treat `ErrInvalidTransition` as already done, never retry"*. A duplicate
`say` on crash-replay is accepted (06 D5).

**Failure handling:** tool errors → loop; malformed output → one re-prompt then fail; API
errors → runtime backoff; dead-letter → `notify.send` plus a system-error `say` into the chat.

## The tool set — fifteen, clean CRUD over two nouns

Tickets: `create_ticket`, `list_tickets` + `get_ticket` + `search_tickets` (reads),
`update_ticket` (one patch folding the old shape/mark_ready/mark_blocked/accept_to_done/
request_approval verbs), `delete_ticket` (soft archive). Feed: `post_update`, `list_updates`,
`edit_update`, `retract_update`. Plus `send_to_agent`, `say`, `list_agents` +
`get_agent_updates`, and `bash`.

**No pull tool** — Ready→Working is the board's deterministic pull and never a brain action
(03 I6). **No notify tool** — notifications are emitted by the board's `notify.send` outbox
topic on real transitions, not chosen by the model.

**Both reads carry an "allowed now" line** naming what the ticket's current state accepts, in
tool phrasing (`update_ticket state="ready"`, `send_to_agent`, …), rendered from the board's
own `State.AllowedOps()` — once per column on the roster (a column *is* one state) and once
per ticket on `get_ticket`. It exists because 39% of `update_ticket` calls were failing and
most were guesses at an unavailable transition
(`docs/brain-optimization-2026-08-05.md` §2).

**The roster is windowed.** It lists every live ticket but only the **5 most recent Done**
(`doneRosterLimit`), since Done only grows and a pass acts on its tail at most. The header
still counts the whole column and an elision line names the remainder plus the way to reach
it, so a windowed column never reads as a complete one. `search_tickets` is that way:
case-insensitive AND-substring match over id/title/body/blocked_reason of the **GetBoard
snapshot** (filtered in memory, not a second read path), title matches ranked above body-only,
then recency, then id; pages of 5, and a page with more behind it prints the exact next call.
Non-archived only — search never resurrects a deleted ticket.

**Confirm-before-destructive is prompt-level and scoped to *ambiguous* destructive actions** —
`update_ticket` with `state="done"`, `delete_ticket`, and work-discarding `send_to_agent`. Ask
via `say`, ending the pass; unambiguous commands execute immediately. Golden tests pin both.

## How to work here

- Primary suite = **golden decision tests**: fixture board + event → expected tool-call
  sequence, over the scripted LLM fake and fake board/say ports — no real Postgres or LLM. A
  small live-model eval set runs on demand / nightly, **never in the commit gate**.
- **Prompt changes are behaviour changes**: they ride the same review + golden-test gate as
  code.
- Reach the board only through the Board API port, the transcript only through
  `ConversationReader`.
- **Adding a tool is one append per file, not five edits in one.** The action surface is split
  across `tool_schemas.go` (name, input struct, `Tools` entry), `tool_dispatch.go` (the route
  case), `tool_handlers.go` (the handler), `tool_render.go` (how the result reads back to the
  model) and `tool_results.go` (shared constructors). Keep each file in `Tools` order so the
  appends line up. The one handler that lives elsewhere is `update_ticket`'s, at the head of
  the patch facade in `update_ticket.go`.

## Common footguns

- **"Improving" the truncation path into something smarter.** Two rules there look
  over-cautious and are not. (1) The last call of a truncated round is dropped
  *unconditionally*, without first checking whether its JSON parses — a cut-off arguments
  object frequently still parses into a valid, **different** action (slice
  `{"id":"t-9","state":"done","done_commit":"…"}` before the commit and you have a done with no
  gate), so "does it unmarshal" tests nothing. Re-issuing a wrongly dropped call is cheap; the
  memo answers a repeated read from the conversation. (2) A truncated round that leaves nothing
  usable — no text, no complete call — **fails the pass** rather than continuing on a
  synthesized assistant turn, because a truncated `tool_use` block cannot be echoed back
  (`json.RawMessage` holding half an object fails to marshal) and dead-lettering is loud where
  a quiet return is exactly the bug this replaced.

- **Re-introducing prompt versioning.** There is one `systemPrompt` const in `prompt.go`;
  versioning existed and was removed by user decision (git history has v1/v2). Edit it in
  place. `TestSystemPrompt_HasToolGuidance` pins tool-name *presence*, never literal prose.
- **Treating `update_ticket` as a bypass.** It is a *facade*: it routes each patch field to
  the board's own typed op in a fixed order (fields → approval → state), so every board
  precondition still holds. `applyState` returns the board error unwrapped on purpose (fed
  back verbatim) — hence the `//nolint:wrapcheck`.
- **Appending "here's what you *could* do instead" to a refused transition.** The "allowed
  now" line is **preventive only** — it goes on the reads. An `ErrInvalidTransition` comes back
  exactly as the board worded it, because the idempotency rule reads that error as "already in
  that state, treat as done, never retry"; offering alternatives at that moment invites the
  retry the rule forbids. `TestUpdateTicket_RefusalStaysVerbatim` pins it.
- **Trimming the prompt's `## Rounds` section, or the "same round" clause on a read tool's
  description, as redundant prose.** They are the whole of the read-batching fix
  (`docs/brain-optimization-2026-08-08-measured.md` §7): 11–14% of measured rounds were a read
  round foldable into the one before it, and *nothing* had ever told the model it may ask for
  several reads at once — it batches in 22% of rounds unprompted, so this is a nudge, not a new
  capability. The rule is stated once in the prompt and repeated on each read tool because the
  tool description is what the model is reading when it decides what a round contains. Three
  tests pin the two halves and the loop's honouring of the shape.
- **Re-adding "set the ticket blocked meanwhile" to the What Counts As Done gate.** Both gate
  branches used to prescribe that while an agent landed its work — and blocked fires a push
  notification, so the user got woken for coordination the nudge was already handling. The gate
  now says to `send_to_agent` and *leave the ticket where it is*; blocked is for a decision only
  the user can make. The rule lives once, under "A pending merge is not a blocker". None of
  this touches what *done* requires — a verified `origin/main` (or PR) commit still gates it.
- **Treating the per-pass memo (`memo.go`) as a cache and "improving" it** — giving it a TTL,
  sharing it between passes, hanging it off `Service`, or having a reused read replay the
  earlier result instead of pointing at it. None of those are what it is. It is scoped to one
  `runPass` because that is exactly the window in which the board cannot move except by the
  pass's own hand (no mid-pass refresh); across passes it *does* move, and a shared memo would
  serve stale state. It **points rather than replays** because the pass re-sends its whole
  conversation every round, so a second copy of a result grows every remaining round's prefix
  — the cost the whole thing exists to avoid. Two invalidations are load-bearing: any mutating
  call drops every remembered read, and a *successful* one additionally drops every remembered
  refusal (else a refusal the board would now accept is invented from memory). `bash` is
  deliberately not memoizable — the done flow has it run `git fetch origin`.
- **Filing a malformed call in the memo.** Only calls that were *not* malformed are recorded,
  because the one-re-prompt-then-fail rule needs the second identical malformed call to be
  dispatched and counted as malformed again. Suppressing it into a clean result would silently
  switch that rule off; `TestHandleEvent_MalformedRepeat_IsNeverAnsweredFromMemory` pins it.

## Potential gotchas

- **The done gate is configurable per project (merge-gate mode).** `update_ticket` with
  `state="done"` requires a `done_commit` and verifies it before calling `AcceptToDone`: mode
  `main` → the commit landed on `origin/main`, mode `pr` → the work is in a pull request. The
  mode comes from the project's `merge_gate_mode` setting; **a refusal is steered back to the
  agent to actually land the work, not surfaced to the user.**
- **The `main` gate reuses the model's `git fetch`, it does not repeat it.** The prompt has the
  model fetch via `bash` before it looks up the commit, so the verifier used to run a second
  full fetch seconds later inside the same loop (171 of 171 accepted dones —
  `docs/brain-optimization-2026-08-08-measured.md` §6). It now skips its own fetch when
  `.git/FETCH_HEAD` is younger than `fetchFreshness` (60 s), read from the clone because the
  reused fetch went through an opaque `sh -c` string. **The invariant that keeps this honest: a
  negative is never returned on reused refs.** Only a positive may be, since `origin/main` only
  grows; anything else fetches and decides again, so the gate still fails closed. Watch
  `repo.shell.verify`'s `fetched` stay `false` on accepted dones.
- **`post_update` takes its prose under `body` *or* `text`, though the schema advertises only
  `body`.** The model borrows `say`'s `text` key on ~1 in 5 calls and used to burn a round
  self-correcting. Don't "tidy" the alias away, and **don't add `text` to the schema** — one
  advertised name is the point. `edit_update` deliberately has no such alias.
- **Costing a round means reading all three cache-write attrs.** `logRound` emits
  `cache_creation_input_tokens` plus the `_5m_` and `_1h_` split — the aggregate *and* its TTL
  split, because the two TTLs bill differently (5m 1.25×, 1h 2×) and cache writes are 40–60%
  of brain spend. **Sum the split, don't add it to the aggregate** — that double-counts.
  Records written before 2026-08-05 carry the aggregate only, so a window spanning that deploy
  is still a range.
- **The prompt is written to 08's interaction model: the user sees the *feed*, not the board.**
  Routine board actions already emit mechanical toasts (08 §4), so the prompt forbids narrating
  them with `say`/`post_update`. Keep new prompt prose consistent with that surface — one
  ephemeral `say` pill, no chat history, a feed draining toward "All clear".
