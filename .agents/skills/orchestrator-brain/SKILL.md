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
- [x] Model → 06 §2: Anthropic Go SDK, default `claude-sonnet-5`, `KILN_BRAIN_MODEL`
      override.
- [x] Input contract → 06 §3: fresh context per pass — full board snapshot + last 20
      transcript messages + the event (agent output truncated ~8k head+tail). System
      prompt is a versioned Go template (D7).
- [x] Tools → 06 §4: seven — create_ticket, shape_ticket, mark_ready, send_to_agent,
      mark_blocked, accept_to_done, say. No pull (03 I6), no notify (deferred to 10),
      no board read (state is injected, D3).
- [x] The pass → 06 §5: bounded tool loop, max 8 rounds, tool errors fed back verbatim;
      no mid-pass snapshot refresh; no streaming.
- [x] Idempotency → 06 §6: no dedupe table — fresh state + 03 D8 strict preconditions +
      the prompt rule "treat ErrInvalidTransition as already done, never retry".
      Duplicate say on crash-replay accepted (D5).
- [x] Confirm-before-destructive → 06 §7: prompt-level, scoped to *ambiguous* destructive
      actions (accept_to_done; work-discarding send_to_agent); ask via say, ending the
      pass; unambiguous commands execute immediately. Golden tests pin both.
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

- Editing a shipped `systemPromptV*` const in place. Prompt versions are append-only:
  add `systemPromptV(N+1)`, register it in `promptTemplates`, bump
  `CurrentPromptVersion`, and update `TestCurrentPromptVersion_IsV*` in
  `dispatch_test.go` (it pins the shipped version number + tool-name presence, never
  literal prose — 06 D7).

## Potential gotchas

- The prompt is written to 08's interaction model (v3): the user sees the *feed*, not
  the board — routine board actions already emit mechanical toasts (08 §4), so the
  prompt forbids narrating them with `say`/`post_update`. Keep new prompt prose
  consistent with that surface (one ephemeral `say` pill, no chat history, feed drains
  toward "All clear") rather than the `/debug` board view.
