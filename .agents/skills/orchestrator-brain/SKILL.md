---
name: orchestrator-brain
description: Use when working in the brain module — the (board state + event) → actions LLM decision step that wakes on one event, loads state, reasons once, and emits actions from a fixed tool set. Backend anchor internal/brain. Spec 02 §6.
---

# Orchestrator brain (doc 02 §6)

## Functional Requirements

**Responsibility.** The `(board state + event) → actions` decision step (`01` §6): wake on
one event, load state, reason once, emit actions from the fixed tool set.

**Interface.** The tool schema exposed to the LLM, mapped onto the Board API (§5) plus
notify / speak. Input contract: how board state and the event are serialized into the
prompt. Output contract: the emitted actions and how they are applied.

**Dependencies.** Board API (§5) for state and mutations; runtime (§7) to be invoked and to
deliver notify / speak; LLM provider — **Anthropic SDK (Go)**, model TBD (§3/§6).

**Open decisions — TBD → §6.**
- [ ] LLM provider and model.
- [ ] Prompt structure and how much board state to include.
- [ ] The exact tool definitions.
- [ ] Idempotency — replaying the same event must not double-apply actions.
- [ ] How destructive / ambiguous actions get the `01` §7 voice confirmation.
- [ ] Failure handling when the LLM errors or returns an invalid action.

## How to work here

_(Accumulate: how to run the decision step against a scripted/fake LLM and a fake Board API
— no real Postgres or LLM in the loop; the module boundary — `backend/internal/brain`.)_

## Common footguns

_(Accumulate: mistakes agents predictably make in this module.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
