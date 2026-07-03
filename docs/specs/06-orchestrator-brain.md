# Kiln — Orchestrator Brain (v1)

**Date:** 2026-07-03
**Status:** Proposed
**Scope:** v1, single project, single user
**Relationship to `01`–`05`:** Realizes `01` §6's `(board state + event) → actions` step and
resolves every `02` §6 open decision. Written together with `07` (the v1 text client):
voice and notifications are **descoped for v1** — the brain replies in text (`say`), which
`09` will later wrap in TTS without changing this spec. Also resolves `03`'s open question
on where the shaping conversation lives (§3).

## 1. Purpose & scope

This document decides:

- **Model**: provider and default model (§2).
- **Input contract**: how board state, conversation, and the event are serialized (§3).
- **The tool set**: exact definitions over the Board API + `say` (§4).
- **The pass**: the bounded tool loop that turns one event into actions (§5).
- **Idempotency**: why a replayed event cannot double-apply (§6).
- **Confirm-before-destructive** (`01` §7) in a text world (§7).
- **Failure handling**: LLM errors, invalid actions (§8).
- **Module topology** (§9).

Out of scope: transport of `human.message` in and `say` out (`04`/`07`); TTS/STT (`09`);
push (`10`).

## 2. Model

**Anthropic Go SDK** (pinned in `02` §3); default model **`claude-sonnet-5`**, overridable
via `KILN_BRAIN_MODEL`. The brain is a tool-calling dispatcher over a small board: strong
tool-following at low latency and cost is the profile, and latency matters doubly when
voice arrives (§10, D1). Temperature and other sampling knobs stay at SDK defaults until
the golden tests (§9) say otherwise.

## 3. Input contract — one pass's context

Each pass is built fresh; the brain holds no in-process state between events (`01` §6).
Three blocks, in one user message after the fixed system prompt:

1. **The board** — the full `GetBoard` snapshot (`03` §4), serialized as compact JSON with
   render-order preserved (Ready in exact pull order). Always full: tens of tickets at
   most, so trimming would save nothing and cost the model context (§10, D2).
2. **The conversation** — the last **20 messages** of the persisted transcript (`07` §3),
   oldest first, each `{role: user|kiln, text, at}`. This is what makes multi-turn shaping
   work ("make it blue" needs the previous message) and resolves `03`'s open question: the
   shaping conversation lives in the transcript; its *outcome* is written into the ticket
   body via `shape_ticket`, and only the body reaches the coding agent.
3. **The event** — one of the two `01` types, tagged with its queue id:
   - `human.message`: the user's text, verbatim.
   - `agent.turn_completed`: the `05` §2.2 payload (ticket, worker, `is_error`, output,
     cost). Long agent output is truncated to a budget (~8k chars head + tail) with an
     elision marker — the brain judges outcomes, it does not re-review diffs.

**The system prompt** states the role (project orchestrator, `01` §1), the board rules it
must respect (it cannot pull — the system does; Blocked means waiting on the human), the
`§7` confirmation rule, the idempotency rule (§6), and the tool-usage contract (act, then
`say` a short status when the user should hear something; end the turn when there is
nothing left to do). It is versioned in the repo as a Go template — prompt changes are
code-reviewed like code.

## 4. The tool set

Seven tools, mapping one-to-one onto Board API operations (`03` §4) plus `say`. JSON
schemas are generated from the same definitions the golden tests use.

| Tool | Maps to | Notes |
| --- | --- | --- |
| `create_ticket` | `CreateTicket(title, body)` | New work lands in Shaping. |
| `shape_ticket` | `ShapeTicket(id, patch)` | Title/body/priority; reprioritize = shape. |
| `mark_ready` | `MarkReady(id)` | Makes it pullable; the pull itself is never a tool (`03` I6). |
| `send_to_agent` | `SendToAgent(id, instruction)` | Resume a blocked agent or new turn for a working one. |
| `mark_blocked` | `MarkBlocked(id, reason)` | Turn ended, human decision genuinely needed. |
| `accept_to_done` | `AcceptToDone(id)` | Accept result; frees + recycles the worker. Destructive — §7. |
| `say` | runtime Say port (`07` §3) | Text to the user: appended to the transcript, pushed over SSE. `09` will speak it. |

Not in the set: anything that pulls (I6), `notify` (descoped with `10`; the mechanical
`notify.send` from `MarkBlocked` still emits and is a log-only executor in v1 — `07` §6),
and any board read — the snapshot is already in context (§10, D3).

## 5. The pass — a bounded tool loop

One event = one **pass** = one Anthropic conversation that loops on tool calls:

1. Build context (§3), call the model.
2. Execute each returned tool call against the Board API / Say port; feed each result —
   including typed errors, verbatim — back as the tool result.
3. Repeat until the model ends its turn, up to **8 tool rounds**; at the cap, append a
   final instruction to wrap up with at most a `say`.

Within a pass the model sees the consequences of its own actions through tool results; it
does **not** get a refreshed board snapshot mid-pass (the serial event worker — `04` §4 —
means nothing else is mutating, except a concurrent `RunPull`, which the model doesn't
act on anyway). Multi-action flows (create → shape → mark ready → say) are one pass.

Cost/latency envelope: typical pass = 1–3 rounds of a small context; worst case 8 rounds.
No streaming — `say` is the user-visible output, not tokens (§10, D4).

## 6. Idempotency — replay must not double-apply

Delivery is at-least-once (`04` §3): a crash mid-pass replays the whole event. Three
layers make that safe, none of them a dedupe table:

1. **Fresh state**: a replayed pass re-reads the board, so it sees whatever the crashed
   pass already committed.
2. **Strict preconditions** (`03` D8): re-issuing an already-applied transition returns
   `ErrInvalidTransition`, which the model receives as a tool result. The system prompt's
   idempotency rule says exactly what to do with it: *treat "invalid transition" as
   already done, verify against the snapshot, and continue — never retry the same call*.
3. **Benign duplicates**: a repeated `say` is accepted (rare — only crash-replay windows),
   and the transcript in context makes literal repetition unlikely since the model sees
   its own earlier reply (§10, D5).

## 7. Confirm-before-destructive

`01` §7's rule came from STT mishearing; text removes mishearing but not ambiguity, so the
rule survives in reduced form (§10, D6). **Destructive** in v1 means exactly:
`accept_to_done` (releases and recycles the worker — the workspace is gone) and
`send_to_agent` when the instruction would discard in-flight work (e.g. "start over").

Enforcement is **prompt-level, not mechanical**: the system prompt requires that a
destructive action taken *in response to an ambiguous or unexpected instruction* be
preceded by a `say` question, ending the pass; the user's answer arrives as the next
`human.message` and the confirmed action executes then. Unambiguous commands ("accept
ticket 3") execute immediately — confirmation theater on every accept would make the tool
unusable. The golden tests (§9) pin both behaviors.

## 8. Failure handling

- **Tool-level** — invalid arguments or Board API typed errors: fed back into the loop
  (§5); the model self-corrects or explains via `say`. Never crashes the pass.
- **Malformed output** — unparseable tool call or unknown tool name: one re-prompt with
  the parse error appended; if it repeats, fail the pass.
- **Pass-level** — Anthropic API error, or a failed pass per above: the handler returns
  the error to the runtime, whose worker retries with backoff (`04` §3, 8 attempts).
- **Dead-letter** — after exhaustion (`04` §3): the event is dead; the runtime emits
  `notify.send` (log-only in v1) **and** appends a `say` — *"I hit a system error handling
  that — please try again."* — so the failure is visible in the chat, not just a log line
  (`07` §6). Board state is untouched throughout: a dead event never mutates anything.

## 9. Module topology & testing

Per `02` §2, all in `/backend/internal/brain`; stateless (`02` §6):

- **Service** — builds context (§3), runs the pass loop (§5), applies tool calls. One
  entry point: `HandleEvent(ctx, event)` — the runtime's Brain port (`04` §2).
- **Ports consumed** — LLM (the Anthropic client behind a port; a **scripted fake** in
  tests), Board API (`03` §4), Say + ConversationReader (`07` §3).
- **No infrastructure of its own** — no tables, no migrations. The transcript belongs to
  the runtime (`07` §4); the brain only reads it through the port.

**Testing** (realizing `02` §14's core promise): the decision step is
`(board state + event) → actions`, so the primary suite is **golden decision tests** —
fixture board + event in, expected tool-call sequence out, run against the scripted LLM
fake for the loop mechanics and against fake board/say ports for application. The
scripted fake also drives loop-edge tests (tool error recovery, round cap, malformed
output). A small **live-model eval set** (~a dozen scenarios: accept vs block judgment,
ambiguity → confirm, multi-turn shaping) runs on demand and in CI-nightly, not in the
commit gate — the gate stays deterministic.

## 10. Decision log

| # | Decision | Alternatives considered | Rationale |
| - | -------- | ----------------------- | --------- |
| D1 | Default `claude-sonnet-5`, config-overridable. | Opus 4.8 (better judgment, ~5× cost, slower); Haiku 4.5 (cheapest, weakest at accept/block judgment). | User decision. Tool-following dispatcher profile; latency compounds once voice wraps `say`. The env var makes stepping up a config change, and the eval set (§9) gives the evidence. |
| D2 | Full board snapshot + last 20 transcript messages every pass. | Event-type-trimmed views; summarized state. | Tens of tickets — trimming saves nothing and invites "the brain didn't know X" bugs. 20 messages bounds the prompt while covering any plausible shaping exchange. |
| D3 | No board-read tool; state is injected. | A `get_board` tool the model calls at will. | One fewer round-trip per pass, and the model can't skip reading state — it always has it. The serial writer (`04` §4) keeps the injected snapshot honest for the pass's duration. |
| D4 | Bounded loop: 8 tool rounds, then forced wrap-up. | Single-shot (one round, all actions at once); unbounded loop. | Single-shot can't react to tool errors — the idempotency story (§6) depends on the model seeing `ErrInvalidTransition`. Unbounded risks runaway passes; 8 covers every legitimate flow with margin. |
| D5 | No action-dedupe table; idempotency = fresh state + strict preconditions + prompt rule. | Recording applied actions per event id. | The board's strictness (`03` D8) already makes double-apply impossible; a dedupe table would duplicate that guarantee and add state that can drift. Duplicate `say` on crash-replay is accepted as benign. |
| D6 | Confirmation is prompt-level, scoped to ambiguous destructive actions. | Mechanical gate (a `confirm` tool + pending-action state); confirming every accept. | A pending-action store re-introduces cross-event brain state that `01` §6 deliberately avoids. Text removes the mishear rationale; ambiguity remains, and the golden tests pin the behavior instead of a mechanism. |
| D7 | Prompt is a versioned Go template in the repo. | Prompt assembled ad hoc in code; stored in the DB. | Prompt changes are behavior changes — they go through the same review + golden-test gate as code (`02` §4). |

**Open questions (owned elsewhere or later):** `say` audio wrapping and voice-specific
prompt rules (`09`); a `notify` tool when push lands (`10`); whether the live eval set
gates releases once the loop is in production (`02` §14 closeout).
