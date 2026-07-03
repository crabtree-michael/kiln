---
name: web-client
description: Use when working in the frontend — the thin, disposable, mobile-first client that renders the board over a live connection, captures mic audio, plays Kiln's voice, and receives notifications. Holds no authoritative state. Anchor /frontend. Spec 02 §11.
---

# Web client (doc 02 §11)

## Functional Requirements

**Responsibility.** A deliberately thin, disposable, mobile-first surface that renders the
board over a live connection, captures mic audio, plays Kiln's voice, and receives
notifications (`01` §4). **Holds no authoritative state.**

**Interface.** Consumes the runtime's live connection (§7) and client endpoints; the voice
pipeline (§9) for audio; the notification transport (§10) for push and deep links. Types are
generated from the wire schema (see `wire-schema`, §3).

**Dependencies.** Runtime (§7); voice pipeline (§9); notifications (§10).

**Open decisions — TBD → §11.**
- [ ] Framework and build.
- [ ] Live-connection transport (shared with §7: WebSocket vs SSE).
- [ ] Board rendering and how live updates are applied.
- [ ] PWA vs. wrapped-native for mic + push on mobile.
- [ ] Reconnection / resync after the connection drops.

## How to work here

**TS escape hatches are banned** (`any`, `as`, `@ts-ignore`, non-null `!`, unused symbols) —
the hard gate enforces it (§4b). Types come from the wire schema; never hand-write the
client↔server types.
_(Accumulate: how to run the frontend locally, build/test commands, the boundary — `/frontend`.)_

## Common footguns

- Reaching for a TS escape hatch to get past the type checker instead of fixing the schema/types.
- Holding authoritative state in the client — it is disposable and holds none.

_(Accumulate more as you work.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
