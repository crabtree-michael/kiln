---
name: voice-pipeline
description: Work in the Kiln voice pipeline — STT that turns speech into human-voice-input events and TTS that turns brain speak actions into audio, as a separable I/O layer. Use when editing STT/TTS integration, audio transport/format, mic capture, or the confirm-before-destructive mishear path.
---

# Voice pipeline (STT → brain → TTS)

**Spec:** `docs/specs/02-initial-technical-architecture.md` §9, realizing
`docs/specs/01-initial.md` §7. Providers are **real in v1** (02 §1).

## Responsibility

The I/O layer that turns speech into `human-voice-input` events and the brain's
replies into audio. It is **independent of the orchestrator** so it can be tested
separately.

## Interface

- Inbound: audio → text → a `human-voice-input` event to the runtime (§7).
- Outbound: brain `speak` actions → synthesized audio to the client (§11).

## Where it lives

Backend bridges STT/TTS providers behind ports (mic capture + playback live in the
web client, §11). Provider clients are infra adapters injected at the composition
root; the pipeline logic depends only on the ports.

## What this area still has to decide (02 §9)

- STT and TTS providers (pin in the decision log §16).
- Audio transport (streaming vs batched) and format.
- Latency budget for the round trip.
- Foreground-mic handling (`01` §7: mic open only while foregrounded).
- How mishears surface for the `01` §7 confirm-before-destructive rule.

## Test it separately

Voice is a separable I/O layer (02 §14): unit-test STT-text → event and
speak-action → TTS without the real orchestrator loop.

## Gotchas

- Confirm-before-destructive lives at the seam between a mishear-prone STT result
  and a destructive brain action — never auto-apply a destructive action from raw
  transcribed text (coordinate with brain §6).

## Keep this skill current

Record provider choices, audio formats, and the latency budget you measure.
