---
name: orchestrator-brain
description: Use when working in the brain module — the (board state + event) → actions LLM decision step that wakes on one event, loads state, runs one bounded tool loop, and emits actions from a fixed tool set. Backend anchor internal/brain. Specs 02 §6, 06.
---

# Orchestrator brain (02 §6, mechanics decided by 06)

## Functional Requirements

**Responsibility.** The `(board state + event) → actions` decision step (`01` §6): wake on
one event, load state, reason once, emit actions from the fixed tool set.

**Interface.** `HandleEvent(ctx, event)` — the runtime's Brain port (04 §2). Ports
consumed: LLM (Anthropic client behind a port; scripted fake in tests), Board API (03 §4),
Say + ConversationReader (07 §3). Stateless; no tables, no migrations.

**Open decisions — resolved in `docs/specs/06-orchestrator-brain.md` (status: proposed).**
- [x] Model → 06 §2: Anthropic Go SDK, default `claude-haiku-4-5-20251001` (`DefaultModel`
      in `llm.go` — switched from Sonnet to Haiku to cut cost/latency), `KILN_BRAIN_MODEL`
      override (`ModelEnvVar`). Per-project override via the `brain_model` project setting.
- [x] Input contract → 06 §3 (amended by the CRUD consolidation): fresh context per pass —
      last 20 transcript messages + the event (agent output truncated ~8k head+tail). The
      board is NO LONGER injected — the model pulls it via list_tickets/get_ticket, so a
      pass spends no tokens on state it doesn't need. System prompt is a Go template in the
      repo (D7; a single unversioned prompt — versioning was removed by user decision).
- [x] Tools → 06 §4 (amended, CRUD consolidation): **fourteen** — clean CRUD over the two
      nouns. Tickets: create_ticket, list_tickets + get_ticket (reads), update_ticket (one
      patch folding the old shape/mark_ready/mark_blocked/accept_to_done/request_approval
      verbs — routes each field to the board's typed op; see applyState/applyUpdate in
      tools.go), delete_ticket (soft archive). Feed: post_update, list_updates, edit_update,
      retract_update. Plus send_to_agent, say, list_agents+get_agent_updates, bash. No pull
      (03 I6), no notify (deferred to 10). Board read IS now a tool (list_tickets/get_ticket)
      — D3's "state is injected" is superseded.
- [x] The pass → 06 §5: bounded tool loop, **max 12 rounds** (raised from 8 to absorb the
      board reads a pass now makes before acting), tool errors fed back verbatim; no
      mid-pass snapshot refresh; no streaming.
- [x] Idempotency → 06 §6: no dedupe table — fresh state + 03 D8 strict preconditions +
      the prompt rule "treat ErrInvalidTransition as already done, never retry".
      Duplicate say on crash-replay accepted (D5).
- [x] Confirm-before-destructive → 06 §7: prompt-level, scoped to *ambiguous* destructive
      actions — now `update_ticket` with state="done", `delete_ticket`, and work-discarding
      send_to_agent; ask via say, ending the pass; unambiguous commands execute immediately.
      Golden tests pin both.
- [x] Failure handling → 06 §8: tool errors → loop; malformed output → one re-prompt then
      fail; API errors → runtime backoff; dead-letter → notify.send (log-only v1) + a
      system-error say into the chat.

## How to work here

- Primary suite = **golden decision tests**: fixture board + event → expected tool-call
  sequence, over the scripted LLM fake and fake board/say ports — no real Postgres or LLM
  (06 §9). A small live-model eval set runs on demand / nightly, never in the commit gate.
- Prompt changes are behavior changes: they ride the same review + golden-test gate as
  code (06 D7).
- Module boundary: `backend/internal/brain`; reach the board only through the Board API
  port, the transcript only through ConversationReader.

## Common footguns

- Re-introducing prompt versioning. There is one `systemPrompt` const in `prompt.go`
  (versioning existed and was removed by user decision — git history has v1/v2). Edit
  it in place; `TestSystemPrompt_HasToolGuidance` in `dispatch_test.go` pins
  tool-name presence, never literal prose (06 D7).
- update_ticket is a *facade*, not a bypass: it routes each patch field to the board's
  own typed op (ShapeTicket/RequestApproval/MarkReady/MarkBlocked/AcceptToDone) in a fixed
  order (fields → approval → state), so every board precondition + ErrInvalidTransition
  still holds. applyState returns the board error unwrapped on purpose (fed back verbatim,
  06 §6) — hence the //nolint:wrapcheck there.

## Potential gotchas

- **The done gate is configurable per project (merge-gate mode).** `update_ticket` with
  `state="done"` requires a `done_commit` and verifies it before calling `AcceptToDone`: mode
  `main` → `verifyDoneOnMain` (commit landed on `origin/main`), mode `pr` → `verifyDoneInPR`
  (work is in a pull request). The mode comes from the project's `merge_gate_mode` setting
  (`GatePR` etc. in `tools.go`); a refusal is steered back to the agent to actually land the
  work, not surfaced to the user.
- The prompt is written to 08's interaction model: the user sees the *feed*, not
  the board — routine board actions already emit mechanical toasts (08 §4), so the
  prompt forbids narrating them with `say`/`post_update`. Keep new prompt prose
  consistent with that surface (one ephemeral `say` pill, no chat history, feed drains
  toward "All clear") rather than the `/debug` board view.
