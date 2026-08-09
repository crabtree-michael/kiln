---
name: voice-pipeline
description: Use when working on the voice I/O layer — speech in front of the message seam (STT → brain → on-screen text). AssemblyAI streaming STT driven from the client, backend-minted temp token. Kiln does NOT speak (no TTS). Spec docs/specs/09-voice-pipeline.md.
---

# Voice pipeline (mechanics decided by spec 09)

## What it is (accepted + shipped)

An **input** wrapper in front of the existing message seam: **STT → brain → on-screen text**.
Speech becomes the same `human.message` events the text box produces, through the same
`POST /api/message`. The brain's `say` replies stay **text** (rendered in the 08 reply pill).
**Kiln does not speak — there is no TTS anywhere** (09 §10 A1; `01` §3's "STT → LLM → TTS" and
the `06`/`07` TTS deferrals are closed as won't-do).

Provider: **AssemblyAI Universal-Streaming** (09 D1). Topology: the **client** opens the
AssemblyAI WebSocket **directly**; audio never transits the Kiln backend. The only backend
addition is a token-minting route (`POST /api/voice/token`, mint failure → 502), so the API key
never leaves `/backend` (02 §2, 09 D2). Key from `ASSEMBLYAI_API_KEY`; `KILN_VOICE_MODE=mock`
serves the keyless lane.

Code: `backend/internal/voice/assemblyai` (the only place that knows AssemblyAI's HTTP protocol)
and `frontend/src/voice/` — a **pure** `commit-machine.ts` reducer (settled/tail/commit rules, no
I/O, the unit-test target), `assemblyai-client.ts` (mic → worklet → socket), `pcm-batch.ts` /
`pcm-worklet.ts` (framing), and `voice-store.tsx` (all I/O: token lifecycle, reconnect, timers,
the POST). **Keep decision logic in the machine and every side effect in the store** — the
machine returns a `commit` intent; the store performs the POST.

## AssemblyAI protocol (verified live 2026-07)

- **Client WebSocket:** `wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le&format_turns=true&speech_model=universal-streaming-english&token=<t>`.
  `speech_model=universal-streaming-english` **pins English** — the v3 default is multilingual
  and natively code-switches, so ambiguous or accented audio would leak non-English
  transcripts. The English-only model never code-switches.
- Client sends **binary PCM16 mono 16 kHz** frames; closes with `{"type":"Terminate"}`.
- **Commit trigger = a `Turn` with `end_of_turn && turn_is_formatted`** (the formatted final)
  → settle + POST. Everything else with a transcript is a partial (the ghosted tail).
  Unformatted end-of-turn is still a partial — wait for the formatted final.
- **Token mint is `GET /v3/token` with `Authorization: <API_KEY>` — the raw key, NOT
  `Bearer <key>`.** Default TTL 8 min (≤ 10 min per 09 §6).

## Testing

- Unit (frontend): `commit-machine.test.ts` (09 §8 cases) + the pure `decodeAssemblyMessage` and
  `PcmFramer` tests. Mock browser I/O — the store/Dock tests mock `useVoice`; **never** exercise
  a real mic/socket/network in the offline gate.
- Unit (backend): `httptest` against the mint client; the api token route against a fake minter.
- Gated real-service smoke: `tests/tests/voice-token-mints.spec.ts` and the full browser loop
  `tests/tests/voice-mic-to-brain.spec.ts` — **only** with `KILN_VOICE_SMOKE=1`, never in
  `make check`. The smoke needs **no key of its own**: it mints via the backend, mirroring the
  real trust boundary. Chromium is launched with a **fake microphone** fed by
  `tests/fixtures/this-is-a-test.wav`, which is **padded with ~1 s leading + ~1.4 s trailing
  silence** — the lead covers the socket-open window (early frames are dropped until the socket
  is OPEN) and the trailing silence is what lets AssemblyAI fire end-of-turn, so use `%noloop`
  (a seamless loop has no pause). Recipe and flags: `/tests/README.md`.

## Common footguns

- **AssemblyAI rejects frames outside 50–1000 ms** (`error_code 3007` "Input Duration
  Violation", then closes the socket). An AudioWorklet render quantum is 128 samples (~2.6 ms),
  so the worklet MUST batch: `PcmFramer` decimates to 16 kHz and accumulates **1600-sample
  (~100 ms) frames** before posting. Symptom of regressing this: the socket opens, one tiny
  frame is sent, `{"type":"Error",...3007}` comes back, socket closes, no transcript ever lands.
- **Don't proxy audio through the backend** (09 D2). The backend is SSE+POST only; only the
  temp token crosses our API.
- **`pcm-worklet.ts` is loaded via `?worker&url`** — a plain `?url` import emits raw `.ts` that
  `addModule` rejects — and must **never** be imported into the main thread (its top-level
  `registerProcessor` would throw).
- **Mic is OFF until an explicit tap** (reversing 09 D3's "on by default"). The app opens
  *Paused* ("Tap to talk") and the mic starts ONLY on the mic control. Nothing else starts it
  from rest: no start on mount, and **no foreground resume** — backgrounding drops a live listen
  to Paused and returning never reopens it. The other `startStream` callers (token-refresh
  timer, the one-shot reconnect, the post-send restart below) fire *only inside an already-tapped
  live session*.
- **Sending KEEPS the mic live** so the user can keep speaking without re-tapping — both the
  send button and an end-of-turn auto-commit leave the machine `listening`. They differ only in
  the socket: the auto-commit fires *at* turn end, so the same socket safely stays open, but the
  **send button fires mid-turn interim text**, and leaving that socket open would let the
  just-sent words return in the turn's trailing final and **double-post**. So a displayed send
  flags a one-tick `restart` and the commit effect tears the socket down and immediately reopens
  a fresh one at a clean turn boundary — a brief reconnect the dock shows via `connecting`.
- **The X cancels the un-committed utterance client-side** (nothing was sent); commit stays
  automatic on end-of-turn (09 D4). Empty/whitespace finals never POST and never restart.
- Escape-hatch ban (02 §4b): no `any`/`as` — narrow `unknown` with guards. The strict
  `.golangci.yml` (err113/errcheck-check-blank/mnd/nonamedreturns/lll) rejects the "obvious"
  Go — use static wrapped sentinels, a lone named-error return for deferred body-close, named
  timeout consts, and `max(...)` over an `if` (mirror the amika adapter).

## The grace window and the editable transcript (09 §4a)

An end-of-turn final does not post immediately: it **arms** a send and a countdown runs. This is
the subtlest part of the module and every rule below is load-bearing.

- **Timing lives in the store, not the machine.** The machine arms `state.pending`; the *timer*
  is the store's, and it runs off an **absolute deadline**, not a fixed-duration `setTimeout`, so
  the dock's **"+10" control** can push the deadline out mid-window and reschedule (additive —
  taps stack) without losing elapsed time. The control shows only in the final reveal stretch
  before the send fires, so a "+10" tap that pushes the deadline past that stretch withdraws the
  bubble until the countdown runs back down into it. Extending the window is I/O, so it stays in
  the store and dispatches no reducer action — the machine stays pure.
- **The transcript is editable, and an edit FREEZES the countdown.** Every view of the
  transcript (`Dock`, `TicketDetailTranscript`, `DesktopComposer`) writes straight back to
  `settledText` — there is **one buffer**, no local draft copy. Four things are load-bearing:
  - **The freeze is banked, not a paused deadline.** The remaining time is recorded *inside* the
    edit callback, **before** `editing` flips — the grace effect's cleanup runs on that flip and
    nulls the deadline, so reading it after would see a fresh full window instead of the
    remainder.
  - **The grace effect must keep its `editing` early-return.** Each keystroke re-points `pending`
    at the corrected text and re-runs the effect; without that branch every character would
    restart the countdown under the user's hands.
  - **`pending` survives an edit** (unlike `pause`/`cancel`, which clear it) and follows the
    edited text; editing to empty disarms it. Don't "tidy" `beginEdit` into a `pause`.
  - **`resume`/`cancel`/`sendNow`/`openKeyboard` all clear `editing`.** A stale `editing` freezes
    every later auto-send forever. Backgrounding deliberately does NOT clear it — a
    half-corrected sentence must not post while the app is hidden.
- **Both mobile views render the field on the *same* store flag** while the ticket sheet is
  open, so each keeps a `startedEditRef` and only the surface that was tapped takes the caret.

## Potential gotchas

- **Token expiry:** the store schedules a proactive refresh ~30 s before `expires_at` and
  reconnects transparently, preserving any on-screen transcript (09 §5).
- **One silent reconnect** on socket/token failure, then **Retry** with the un-committed
  transcript preserved (09 §5). The reconnect budget resets on a healthy `Begin`.
