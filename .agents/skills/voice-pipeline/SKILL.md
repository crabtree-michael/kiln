---
name: voice-pipeline
description: Use when working on the voice I/O layer — speech in front of the message seam (STT → brain → on-screen text). AssemblyAI streaming STT driven from the client, backend-minted temp token. Kiln does NOT speak (no TTS). Spec 02 §9, 09.
---

# Voice pipeline (docs 02 §9, 09)

## What it is (spec 09, Accepted + implemented)

An **input** wrapper in front of the existing message seam: **STT → brain → on-screen text**.
Speech becomes the same `human.message` events the `07` text box produces, through the same
`POST /api/message` (04 §7). The brain's `say` replies stay **text** (rendered in the `08`
reply pill). **Kiln does not speak — there is no TTS anywhere** (09 §10 A1; the old `01` §3
"STT → LLM → TTS" and the `06`/`07` TTS deferrals are closed as won't-do).

Provider: **AssemblyAI Universal-Streaming** (09 D1). Topology: the **client** opens the
AssemblyAI WebSocket **directly**; audio never transits the Kiln backend. The only backend
addition is a token-minting route, so the API key never leaves `/backend` (02 §2, 09 §2/D2).

## Where the code lives

- **Backend token mint** — `backend/internal/voice/assemblyai/client.go`: the only file that
  knows AssemblyAI's HTTP protocol. `MintStreamingToken(ctx) (token, expiresAt, err)` does
  `GET https://streaming.assemblyai.com/v3/token?expires_in_seconds=<ttl>` with header
  `Authorization: <API_KEY>` (raw key, **not** `Bearer`), decodes `{token, expires_in_seconds}`,
  returns `token + now+ttl`. Default TTL 8 min (≤ 10 min per 09 §6).
- **api port + route** — `internal/api/routes.go`: the narrow `VoiceTokenMinter` port +
  `handleVoiceToken` behind `POST /api/voice/token` (200 `wire.VoiceToken{token, expires_at}`;
  mint failure → **502**). Wired at the composition root (`cmd/kiln/wiring.go`), key from
  `ASSEMBLYAI_API_KEY` (main.go `Config`, docker-compose backend env).
- **Frontend `voice/` module** (`frontend/src/voice/`):
  - `commit-machine.ts` — **pure** reducer, the unit-test target. States `listening | paused |
    denied | retry`; owns the settled/tail/commit rules; **no I/O**. Emits a one-tick
    `commit` field the store consumes (it never calls the network itself).
  - `assemblyai-client.ts` — getUserMedia → AudioContext → PCM16 worklet → binary WS frames;
    decodes messages via the **pure exported `decodeAssemblyMessage`** (also unit-tested).
  - `pcm-worklet.ts` — the AudioWorkletProcessor (Float32 → 16 kHz PCM16); loaded via `?url`,
    **never imported into the main thread** (its top-level `registerProcessor` would throw).
  - `voice-store.tsx` / `voice-context.ts` — React glue: token fetch + proactive refresh,
    one-silent-reconnect-then-retry, `visibilitychange` foreground-only, commit → `postMessage`.
  - `useVoice()` → `{ micState, settledText, tailText, pause, resume, cancel }`.
- **Dock** — `components/Dock.tsx`: presentational `useVoice()` consumer. Preserves the `08 §F`
  selector surface (`data-role="dock"/"dock-talk"`, `aria-label="Talk"`, mic-glyph sub-elements);
  `data-dock-state` reflects `micState`. `VoiceProvider` wraps the tree in `PrimaryScreen.tsx`
  (the `07` `App` debug view at `/debug` has no Dock).

## AssemblyAI protocol (verified live 2026-07)

- **Client WebSocket:** `wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le&format_turns=true&speech_model=universal-streaming-english&token=<t>`.
  `speech_model=universal-streaming-english` **pins English** — the v3 default (`universal-3-5-pro`)
  is multilingual and natively code-switches, so ambiguous/accented audio would leak non-English
  transcripts. The English-only model never code-switches.
- Client sends **binary PCM16 mono 16 kHz** frames; closes with `{"type":"Terminate"}`.
- Receives `{"type":"Begin",...}` then `{"type":"Turn", transcript, end_of_turn, turn_is_formatted, words[]}`.
- **Commit trigger = a `Turn` with `end_of_turn && turn_is_formatted`** (the formatted final)
  → `{kind:'final', text}` → settle + POST. Everything else with a transcript → `{kind:'partial'}`
  (ghosted tail). Unformatted end-of-turn is still a partial — wait for the formatted final.

## Testing

- Unit (frontend): `commit-machine.test.ts` (09 §8 cases) + `decodeAssemblyMessage` tests.
  Mock browser I/O — the store/Dock tests mock `@/voice/voice-context`'s `useVoice`; **never**
  exercise a real mic/socket/network in the offline gate.
- Unit (backend): `internal/voice/assemblyai/client_test.go` against an `httptest.Server`;
  `internal/api` token-route tests against a `fakeVoiceTokenMinter` (happy 200, mint → 502).
- Gated real-service smoke: `tests/tests/voice-token-mints.spec.ts` — **only** runs with
  `KILN_VOICE_SMOKE=1` (real AssemblyAI; never in `make check`). It mints via the backend and
  authenticates a real socket (no audio asset needed); the audio→`human.message` assertion runs
  only when `KILN_VOICE_SAMPLE=/path/to/clip.pcm` (raw PCM16 mono 16 kHz) is supplied. Recipe:
  bring the stack up with `ASSEMBLYAI_API_KEY` set, then `KILN_VOICE_SMOKE=1 make e2e`.
- **Full browser E2E** (`tests/tests/voice-mic-to-brain.spec.ts`, Playwright `voice` project):
  Chromium is launched with a **fake microphone** fed by a canned clip — no real mic. Flags:
  `--use-fake-device-for-media-stream`, `--use-fake-ui-for-media-stream`,
  `--use-file-for-fake-audio-capture=<abs .wav>%noloop`, and
  `--autoplay-policy=no-user-gesture-required` (belt-and-suspenders for the AudioContext; the
  spec taps "Talk" to start the mic — that click is itself a user gesture). The clip
  (`tests/fixtures/this-is-a-test.wav`, mono 16 kHz PCM16) is **padded with ~1 s leading + ~1.4 s
  trailing silence**: the lead covers the socket-open startup window (early frames are dropped
  until the socket is OPEN), the trailing silence lets AssemblyAI fire end-of-turn (a seamless
  loop has no pause, so use `%noloop`, not looping). It asserts the utterance lands as a
  `human.message` and the brain runs a turn (a `kiln` reply). Generate a clip on macOS with
  `say -o x.aiff "..."` + `afconvert -f WAVE -d LEI16@16000 -c 1 x.aiff x.wav`.

## Common footguns

- **AssemblyAI rejects frames outside 50–1000 ms** (`error_code 3007` "Input Duration
  Violation", then closes the socket). An AudioWorklet render quantum is 128 samples
  (~2.6 ms), so the worklet MUST batch. `pcm-batch.ts`'s `PcmFramer` decimates to 16 kHz and
  accumulates **1600-sample (~100 ms) frames** before posting — unit-tested so a regression
  is caught in `make check` (the browser E2E is gated, not in the default gate). Symptom of
  regressing this: the socket opens, one tiny frame is sent, `{"type":"Error",...3007}` comes
  back, socket closes, no transcript ever lands.
- **Don't proxy audio through the backend** (09 D2). The backend is SSE+POST only (04 D6);
  the client streams to AssemblyAI directly. Only the temp token crosses our API.
- **Auth header is the raw key**, not `Bearer <key>`, on the `/v3/token` GET.
- **Mic is OFF until an explicit tap** (reverses the old 09 D3 "on by default"). The app opens
  *Paused* ("Tap to talk") and the mic starts ONLY when the user taps the mic control (→ `resume`
  → `startStream`). Nothing else starts it from rest: no start on mount, no foreground resume
  (backgrounding drops a live listen to Paused and returning never reopens it). **Sending KEEPS
  the mic live** so the user can keep speaking without re-tapping: both the send button and an
  end-of-turn auto-commit leave the machine `listening`. They differ only in the socket. The
  auto-commit fires *at* turn end, so the same socket safely stays open (`fireArmedSend` leaves
  `restart` unset). The **send button fires mid-turn interim text**, so leaving that socket open
  would let the just-sent words return in the turn's trailing final and *double-post*; instead
  `fireDisplayedSend` keeps `listening` but flags a one-tick `restart`, and the commit effect
  tears the socket down and **immediately reopens a fresh one** (a clean turn boundary) — a brief
  reconnect the dock shows via `connecting`. So `startStream` callers besides `resume` are: the
  token-refresh timer, the one-shot reconnect, and this post-send restart — all *only inside an
  already-tapped live session*, so none activates the mic from rest. Only an explicit stop (mic
  button / keyboard mode), background, or unmount stops the mic. Commit is still **automatic** on
  end-of-turn (09 D4); the **X cancels** the un-committed utterance client-side (nothing was
  sent). Empty/whitespace finals never POST (and never restart).
- Keep decision logic in `commit-machine` (pure, testable); keep all I/O in `voice-store` /
  `assemblyai-client`. The machine returns a `commit` intent; the store performs the POST.
- Escape-hatch ban (02 §4b): no `any`/`as` — narrow `unknown` with guards. The strict
  `.golangci.yml` (err113/errcheck-check-blank/mnd/nonamedreturns/lll) rejects the "obvious"
  Go — use static wrapped sentinels, a lone named-error return for deferred body-close, named
  timeout consts, and `max(...)` over an `if` (mirror the amika adapter).

## Potential gotchas

- **Token expiry:** the store schedules a proactive refresh ~30 s before `expires_at` and
  reconnects transparently, preserving any on-screen transcript (09 §5).
- **Foreground-only (09 §3, 01 §10):** `visibilitychange` hidden → stop the socket; visible →
  resume **unless the user explicitly paused** (pause is sticky across background).
- **One silent reconnect** on socket/token failure, then **Retry** with the un-committed
  transcript preserved (09 §5). The reconnect budget resets on a healthy `Begin`.
- The gated smoke test needs **no key of its own** — it mints via the backend, mirroring the
  real trust boundary (the worktree has no `.env`; the running backend holds the key).
