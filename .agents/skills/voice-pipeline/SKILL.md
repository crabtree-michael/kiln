---
name: voice-pipeline
description: Use when working on the voice I/O layer — turning speech into human-input events and the brain's replies into audio (STT → brain → TTS). Spans the frontend mic/playback and the runtime bridge. Spec 02 §9.
---

# Voice pipeline (doc 02 §9)

## Functional Requirements

> **Deferred in v1** (07 D1): the text client talks to the brain through the same seams —
> this spec later puts STT in front of `POST /api/message` and TTS on top of the `say`
> SSE event. Nothing in 03–07 changes when it lands.

**Responsibility.** The I/O layer that turns speech into human-input events and the brain's
replies into audio: STT → brain → TTS (`01` §7). Independent of the orchestrator so it can
be tested separately.

**Interface.** Inbound: audio → text → a `human-voice-input` event to the runtime (§7).
Outbound: brain `speak` actions → synthesized audio to the client (§11).

**Dependencies.** STT and TTS providers (managed APIs, real in v1); runtime (§7); client
(§11) for mic capture and playback.

**Open decisions — TBD → §9.**
- [ ] STT and TTS providers.
- [ ] Audio transport (streaming vs batched) and format.
- [ ] Latency budget for the round trip.
- [ ] Foreground-mic handling (`01` §7: mic open only while foregrounded).
- [ ] How mishears are surfaced for the `01` §7 confirm-before-destructive rule.

## How to work here

_(Accumulate: how to exercise voice as a separable I/O layer, provider config, where the
frontend capture/playback code lives vs. the runtime bridge.)_

## Common footguns

_(Accumulate: mistakes agents predictably make here.)_

## Potential gotchas

_(Accumulate: non-obvious traps and edge cases.)_
