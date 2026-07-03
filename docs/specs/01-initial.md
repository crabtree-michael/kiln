# Kiln — Design (v1)

**Date:** 2026-07-03
**Status:** Approved for planning
**Scope:** v1, single project, single user

## 1. What Kiln is

Kiln is a **cloud orchestrator for autonomous coding agents, driven by voice.** A user
runs several autonomous coding agents in the cloud and manages them by talking to a
single orchestrator through a mobile-first web app.

The intended user is an on-the-go "hyper-builder": they have agents doing real work
(editing, running, committing) and they want to stay hands-off until an agent genuinely
needs a decision. When that happens, Kiln notifies them; they open the app, hear what's
going on, answer by voice, and the agent continues.

The board and the agents are the machinery. **Talking to Kiln is the product.** Voice is
the interface layer on top of an event-driven orchestrator.

## 2. The core loop (definition of "v1 done")

v1 is complete when this loop works end-to-end:

1. The user opens the app and says, e.g., *"Build a login form and wire it to the auth
   endpoint."*
2. The orchestrator creates a ticket on the board.
3. By voice, the user moves the ticket into **Developing**, binding it to a sandbox.
4. Entering Developing **deterministically dispatches** a coding agent (Claude or Codex)
   into that sandbox to work the ticket.
5. The agent works and finishes its turn.
6. The orchestrator interprets the result and judges that a human decision is needed. The
   ticket moves into the **Blocked** zone and Kiln sends a notification.
7. The user taps the notification; the app opens and Kiln explains the blocker by voice.
8. The user answers by voice.
9. The orchestrator relays the answer to the agent, which resumes.

A required variant of the loop: **the user can come online at any point and give input**,
not only in response to a blocker. Unsolicited voice input (redirect an in-flight agent,
add or reprioritize tickets, ask for status) is a first-class event the orchestrator
handles in any board state.

## 3. Key decisions

- **Event-driven orchestrator (not a persistent autonomous loop).** The orchestrator acts
  in response to events, not on a background timer. A long-running "always thinking"
  orchestrator that initiates its own check-ins is a deliberate future step; v1 builds the
  event-driven substrate it depends on.
- **The orchestrator's heartbeat is agent turn-completion.** When an agent finishes a
  turn, control returns to the orchestrator, and that boundary is where it decides what
  happens next. There is no separate proactive machinery in v1.
- **Board position has deterministic, mechanical meaning.** Moving a ticket is not just a
  visualization; it triggers real side effects (see §5).
- **Voice is a stitched pipeline: speech-to-text → orchestrator LLM → text-to-speech.**
  Chosen over realtime speech-to-speech so the orchestrator stays a clean, independent
  service we fully control and can test and reuse when the autonomous-loop step arrives.
- **Single project, single orchestrator, single user for v1.** Multi-project (one
  orchestrator per project) is future work.

## 4. Components

1. **Web client (mobile-first).** Renders the board over a live connection, captures
   microphone audio, plays Kiln's voice, and receives notifications. Deliberately thin and
   disposable — it holds no authoritative state.
2. **Orchestrator service (cloud).** Holds the API and the board state. Wakes on two event
   types — an **agent finishing a turn** and **human voice input** — runs the orchestrator
   LLM once per event, mutates the board, executes side effects, and sends updates and
   notifications back to the client.
3. **Board state store.** The single source of truth for tickets, columns, zones, and
   sandbox bindings. Every orchestrator event reads and writes it.
4. **Agent-platform integration (Amika).** Dispatches a coding agent into a sandbox,
   sends follow-up instructions to a running or blocked agent, and receives the agent's
   output when a turn ends. Designed against Amika's actual API; treated as an interface
   until those docs are in hand.
5. **Voice pipeline.** Speech-to-text on the way in, text-to-speech on the way out,
   bridging the user and the orchestrator LLM.

## 5. The board

One project's board has three columns:

- **Backlog** — a created ticket with no sandbox.
- **Developing** — a ticket bound to one of the N available sandboxes. This column is
  split into two stacked zones:
  - **Blocked** (top) — the agent's turn ended and the ticket is waiting on a human
    decision. It keeps its sandbox binding while blocked.
  - **Working** (bottom) — the agent is actively running.
  A ticket moves between Blocked and Working **without leaving the column or releasing its
  sandbox**. Blocked is a sub-state of Developing, not a separate destination.
- **Done** — the orchestrator accepted the result; the sandbox is released.

**Concurrency.** Work-in-progress in Developing is hard-capped at the number of available
Amika sandboxes.

**Side-effect transitions.** Position changes drive real actions:

| Transition | Side effect |
| --- | --- |
| Backlog → Developing (Working) | Dispatch the agent into the bound sandbox |
| Working → Blocked | (no dispatch) turn ended; ticket now awaits the user |
| Blocked → Working | Resume the agent with the user's answer |
| Working → Working (new turn) | Orchestrator sends the agent another turn itself, no human |
| Developing → Done | Accept the result and release the sandbox |

## 6. The orchestrator brain

The orchestrator is an LLM service that wakes on an event, loads current board state,
reasons, and emits actions from a fixed tool set:

- create a ticket
- move a ticket (which fires the side effects in §5)
- bind a ticket to a sandbox
- send an instruction to a running or blocked agent
- notify the user
- speak to the user

**Two event types:**

- **Agent turn completed** — the heartbeat. The orchestrator decides: accept and mark
  Done, send another turn and keep it in Working, or mark Blocked and pull in the user.
  Most turn boundaries are handled silently; the user is only involved when a human
  decision is genuinely required.
- **Human voice input** — the interrupt. Handled in any board state: answering a blocker,
  redirecting an in-flight agent, creating or reprioritizing tickets, or reporting status.

The board state store is the orchestrator's memory between events; it always reads and
writes authoritative state there rather than relying on in-process state.

## 7. Voice & notifications

- **App foregrounded:** the microphone is open; speech-to-text feeds the orchestrator and
  replies return as text-to-speech. Any utterance is a human-input event.
- **App backgrounded or closed:** a notification fires when the orchestrator needs the
  user. Tapping it opens the app to an already-updated board and attaches the voice
  channel.
- **Confirmation before destructive actions:** because speech-to-text can mishear, the
  orchestrator confirms by voice before taking actions that are hard to undo.

## 8. Error handling

- **Agent dispatch or result-delivery failure:** retry; if it persists, surface the
  failure on the ticket rather than silently dropping it.
- **Agent crash or timeout:** move the ticket to Blocked with a reason, so the user is
  brought in rather than the ticket stalling invisibly.
- **Speech-to-text errors:** the orchestrator can confirm intent by voice before acting,
  especially for destructive or ambiguous requests.

## 9. Testing approach

The orchestrator's decision step is expressible as *(board state + event) → actions* and
is testable against a mocked agent platform, with no real voice required. The full loop
(create → dispatch → turn ends → decide → block → resume) is verifiable end-to-end using
mocked agents. Voice is an I/O layer on top and can be exercised separately.

## 10. Out of scope (v1)

- A persistent, always-thinking orchestrator that initiates its own check-ins on a timer.
- Multi-project and multi-orchestrator operation.
- Wake-word or always-on background listening (v1 listens only while the app is
  foregrounded).
- Rich agent-to-agent collaboration/choreography.
- Anything beyond single-user auth.

## 11. Open items

- **Amika API.** The dispatch / instruct / receive-result interface is designed against
  Amika's real API. Its docs/SDK need to be reviewed before the integration is built; the
  integration is an interface with a placeholder implementation until then.
- **Stack (proposed, not yet locked):** TypeScript mobile-first PWA client, Node/TypeScript
  orchestrator service, Postgres for board state, Web Push for notifications, and a
  speech-to-text and text-to-speech provider for the voice pipeline.
