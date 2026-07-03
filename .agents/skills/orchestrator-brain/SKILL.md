---
name: orchestrator-brain
description: Work in the Kiln brain module — the (board state + event) -> actions LLM decision step, its tool schema, prompt assembly, idempotency, and invalid-action handling. Use when editing backend/internal/brain, tool definitions, or the LLM prompt/decision logic.
---

# Orchestrator brain (backend/internal/brain)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §6, realizing
`docs/specs/01-initial.md` §6.

## Responsibility

The `(board state + event) → actions` decision step: wake on **one** event, load
state, reason **once** with the LLM, emit actions from a **fixed** tool set mapped
onto the Board API (§5) plus notify/speak.

## Where the code lives

`backend/internal/brain`, layered (02 §2): the queue-event handler → services
(prompt assembly, decide, action validation/apply) → infra (Anthropic Go SDK
behind an `LLM` port). Tests inject a **scripted fake LLM** — no network.

## Interface

- Input contract: how board state + the event are serialized into the prompt.
- Output contract: the emitted actions and how they are applied to the Board API.
- The tool schema exposed to the LLM (JSON schemas, 02 §3).

## What this area still has to decide (02 §6)

- LLM provider/model (pin it here + in the decision log §16).
- Prompt structure; how much board state to include.
- Exact tool definitions.
- **Idempotency**: replaying the same event must not double-apply actions.
- How destructive/ambiguous actions get the `01` §7 voice confirmation.
- Failure handling when the LLM errors or returns an invalid action.

## Run the gate for this area

```bash
cd backend && go test ./internal/brain/...
```

## Gotchas

- One event → one reasoning pass. Do not loop the LLM inside a single event.
- Validate every emitted action against the tool schema before applying it; an
  invalid action must fail safe, never partially mutate the board.

## Keep this skill current

Record prompt-shape decisions, tool-schema changes, and idempotency keys here.
