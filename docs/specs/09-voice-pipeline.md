# Kiln — Voice Pipeline (v1)

**Date:** 2026-07-04
**Status:** Accepted
**Scope:** v1, single project, single user
**Relationship to** `01`**–**`08`**:** Fills the voice slot `07` deferred — but narrower than
promised. **Kiln does not speak.** This spec amends `01` §3: the pipeline is
**STT → brain → on-screen text**, with no TTS stage anywhere (§10, A1). Voice is an
*input* wrapper: speech becomes the same `human.message` events that `07`'s text box
produces, through the same `POST /api/message` seam (`04` §7). The brain's `say` replies
stay text and render in the primary screen's reply pill (`08` §4). The STT provider is
**AssemblyAI** (user decision — §10, D1).

## 1. Purpose & scope

This document decides:

- The **provider and topology**: AssemblyAI streaming STT, driven from the client, with
backend-minted temporary tokens (§2).
- The **client state machine**: mic lifecycle, live transcript, utterance commit (§3–§4).
- **Failure surfaces** (§5).
- The **backend surface**: one token-minting endpoint; nothing else changes (§6).
- The frontend **module shape** (§7) and **testing** (§8).
- Assembly AI API docs live here: [https://assembly-preview.mintlify.app/docs/api-reference/overview](https://assembly-preview.mintlify.app/docs/api-reference/overview)

Out of scope: the screen the mic lives in — backlog, activity pill, reply rendering
(`08`); push notifications (`10`); wake-word / background listening (`01` §10); TTS
(removed, not deferred — §10, A1).

## 2. Provider & topology

**AssemblyAI Universal-Streaming over WebSocket, opened directly from the client.**
Audio never transits the Kiln backend; only final text crosses our API.

The trust boundary (`02` §2) holds because the client never sees the real API key:

- The AssemblyAI API key lives only in the backend environment, like every other
provider credential.
- The client calls `POST /api/voice/token`; the backend mints a **short-lived
temporary streaming token** from AssemblyAI and returns `{token, expires_at}`.
- The client opens `wss://streaming.assemblyai.com/…` authenticated with that token and
streams microphone PCM; AssemblyAI streams back partial and formatted-final
transcripts with end-of-turn markers.

The alternative — proxying audio through the backend — was rejected (§10, D2): it would
add a second realtime transport to a backend that is deliberately SSE + POST only
(`04` D6), put an audio relay on the hot path for zero product gain, and turn a managed
provider's scaling problem into ours.

## 3. Mic lifecycle

**The microphone is on by default.** Opening the app starts listening immediately: the
client requests mic permission, fetches a token, opens the socket, and enters
**Listening** with no tap required (user decision — §10, D3). "Tap to talk" is not the
resting state; it appears only when listening has stopped for a reason:


| State         | Entered when                                                 | Mic button reads                                                                      |
| ------------- | ------------------------------------------------------------ | ------------------------------------------------------------------------------------- |
| **Listening** | App opens (auto-start), or user un-pauses                    | amber glow, "Listening…"                                                              |
| **Paused**    | User taps the mic while listening                            | grey, "Tap to talk"                                                                   |
| **Denied**    | Mic permission denied                                        | grey, explains how to re-enable — the button is the error surface, no modal (`07` §8) |
| **Retry**     | Socket/token failure after one silent reconnect attempt (§5) | grey, "Tap to retry"                                                                  |


Listening ends automatically when the app is backgrounded or the tab hidden
(`visibilitychange`) — v1 listens only while foregrounded (`01` §10) — and resumes
automatically on return to the foreground (it re-enters the default state). A paused
mic stays paused across background/foreground; pausing is the user's explicit choice.

### 3a. Background audio coexistence

Capturing the mic must **not** silence other apps' audio (music, a podcast). Instead the
device should let it keep playing — **ducked** (lowered) while the mic is live, matching
Siri / Voice Memos — which is also the cleaner input for STT (§10, D5).

Kiln is a PWA (`07`; native packaging deferred to `10`), so the audio session is only
controllable through what the browser exposes:

- **iOS Safari (16.4+)** is the platform that hard-stops other audio: WebKit's default
recording session is exclusive. The client declares a `play-and-record`
[Audio Session](https://www.w3.org/TR/audio-session/) (`navigator.audioSession.type`)
**before** `getUserMedia` so the platform coexists with — and ducks — other audio rather
than interrupting it. This is the only web knob for it; the exact duck-vs-mix outcome is
the platform's to decide, not ours.
- **Android Chrome / everything else** does not implement the Audio Session API and does
not hard-stop other audio on capture — `navigator.audioSession` is absent and the call
no-ops (feature-detected).

`echoCancellation` stays on for cleaner STT; if on-device testing shows it re-forces an
exclusive session and still stops music, the fallback is to drop it to `false` (trading
some echo when the user is on speaker).

**Resuming other audio after a report.** Ducking only lowers the other app's volume while
the mic is live; on some devices WebKit interrupts (pauses) it outright instead, and an
interrupted third-party app does not reliably auto-resume. So a **send releases the mic**:
committing an utterance — an auto-send (end-of-turn) or the send button — tears the stream
down, ending the `play-and-record` session, which is the only thing iOS turns into a
"resume other apps" signal. Teardown also resets `navigator.audioSession.type` off the
exclusive `play-and-record` to nudge a clean deactivation. This ends the earlier
"hands-free keeps listening across turns" behavior (D4): a send now returns to **Paused**
("tap to talk"), the unavoidable cost of letting music resume — a live capture session is
exactly what holds the other app's audio. Resuming a *specific* app stays **best effort**
(Apple's Music/Podcasts resume; some apps don't), because the web exposes no cross-app
"play" — only our own session's release.

Because the runtime behavior is platform-decided and cannot be asserted from code or an
automated browser test, the acceptance gate is a **real-device checklist** (both must
pass before this ships):

- [ ] **iPhone (Safari 16.4+):** start Spotify/Apple Music playing → open Kiln (mic
auto-starts) → music **keeps playing, ducked** (not stopped/muted).
- [ ] **iPhone:** speak while music plays → the utterance transcribes and commits
correctly (STT is not degraded by the background audio).
- [ ] **iPhone:** pause the mic / background the app → music returns to full volume; the
mic does not remain holding the audio session.
- [ ] **iPhone:** music playing → open Kiln, speak a report, send it → the mic returns to
"tap to talk" and music resumes (best effort; Apple Music/Podcasts should resume).
- [ ] **iPhone (headphones/BT):** output routing is not yanked to the built-in speaker
when the mic engages.
- [ ] **Android (Chrome):** music keeps playing while the mic is active and STT works
(confirms the no-op path did not regress the already-milder default).
- [ ] **Regression:** the default gate (`pnpm run check`) stays green; the gated browser
E2E (fake mic) still lands a `human.message`.

## 4. Transcript & utterance commit

While Listening, the dock renders the live transcript per the `Kiln Voice Screen`
design (5a): **settled words in ink** (formatted finals), the **still-forming tail
ghosted** (partials), a caret at the leading edge.

- **Commit is automatic.** AssemblyAI's end-of-turn detection closes the utterance
(user decision — §10, D4). On end-of-turn, the client POSTs the final transcript to
`/api/message` — the unchanged `07` §4 contract — and the utterance becomes a
committed `human.message` event like any typed message.
- **The X cancels, not deletes.** The X beside the mic discards the current
*un-committed* utterance client-side. Nothing was sent; nothing to undo server-side.
- **Empty utterances never post.** Finals that are empty or whitespace-only are
discarded silently.
- **After commit, listening continues.** The committed text stays visible in the dock
until the brain's activity supersedes it (thinking → reply, `08` §4); the next speech
starts a fresh transcript.

Mis-commits are cheap by construction: the brain confirms destructive actions in
conversation (`01` §7), and the user can simply speak again. That is why auto-endpoint
wins over tap-to-send — the always-listening feel of the design costs at worst one
clarifying exchange.

## 5. Failure surfaces

- **Socket drop / token-mint failure:** one silent reconnect attempt (fresh token if
needed). If that fails, enter **Retry** with any un-committed transcript preserved on
screen — X to discard, tap to retry.
- **Token expiry mid-session:** refresh and reconnect transparently; same preservation
rule. `expires_at` lets the client refresh proactively rather than on error.
- **Commit POST fails:** the utterance is already final text; reuse `07` §8's rule —
inline retry affordance on the dock, never a modal.
- **Permission denied:** the **Denied** state (§3). No audio is captured, ever, until
the user re-enables.



## 6. Backend surface

One new route in the api module; everything else is untouched:


| Endpoint           | Method | Contract                                                                                                                                                                                                                      |
| ------------------ | ------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/voice/token` | POST   | Mint a temporary AssemblyAI streaming token (expiry ≤ 10 min). Returns `{token, expires_at}`. Thin handler → STT-provider port; the concrete AssemblyAI client lives in infra and is wired at the composition root (`02` §2). |


The port is one method (`MintStreamingToken(ttl) → {token, expiresAt}`), so tests fake
it trivially and a future provider swap touches one adapter. Request/response shapes
live in `/schema` like every wire type (`02` §3).

`POST /api/message`, the `human.message` event, the transcript table, and the brain are
all unchanged — this spec adds speech in front of an existing seam, exactly as `04` §6
and `07` §2 planned.

## 7. Frontend module shape

One new `voice/` module beside `07` §5's transport → stores → components layering,
which stays untouched:

- **provider client** — the AssemblyAI socket + mic audio plumbing (getUserMedia,
audio worklet downsampling to PCM16, send loop, message decode). The only file that
knows AssemblyAI's protocol.
- **commit state machine** — owns §3's states and §4's commit rules; consumes provider
events, exposes `{micState, settledText, tailText}` plus pause/resume/cancel to the
dock components. Pure logic, no I/O — the unit-test target.
- **dock components** — render mic button, transcript, X (`08` §2's dock region).

The `07` client (board + chat) survives whole as the debug view (`08` §6); it keeps the
text box, which remains the debug path for exercising the message seam without a mic.

## 8. Testing

- **Unit (frontend):** the commit state machine against a scripted fake provider —
partials then formatted final then end-of-turn → exactly one commit; drop
mid-utterance → Retry with transcript preserved; X during tail → no POST; empty final
→ no POST; background/foreground transitions; pause survives foregrounding.
- **Unit (backend):** token route against a fake STT-provider port — happy path, mint
failure → 502, response shape from `/schema`.
- **Image snapshots (**`02` **§4a):** dock in Listening (with live transcript), Paused,
Blocked, Retry.
- **Real-service smoke (gated):** one test with the real AssemblyAI API and a canned
audio clip → asserts a `human.message` lands with non-empty text. Runs only when
explicitly invoked, per the repo's real-service test hygiene; never in the default
gate.



## 9. Decision log


| #   | Decision / Amendment                                                                                                                                                                               | Alternatives considered                               | Rationale                                                                                                                                         |
| --- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| A1  | **Remove TTS entirely** — amend `01` §3's "STT → LLM → TTS" to STT → brain → on-screen text; `07`'s "TTS on top of `say`" deferral and `06` §4's "`09` will speak it" note are closed as won't-do. | Keep TTS deferred; TTS for blockers only; chime-only. | User decision. The orchestrator does not talk; replies render as text in the `08` pill and backlog. Fully silent beat audio cues.                 |
| D1  | AssemblyAI as the STT provider.                                                                                                                                                                    | Deepgram; Whisper streaming; Web Speech API.          | User decision. Managed streaming API with native end-of-turn detection and temporary-token auth — fits §2's topology exactly.                     |
| D2  | Client streams to AssemblyAI directly with a backend-minted temp token.                                                                                                                            | Proxy audio through the backend over WS.              | Keeps the backend SSE+POST only (`04` D6) and off the audio hot path; the temp token preserves the credential boundary (`02` §2).                 |
| D3  | Mic on by default at app open; "Tap to talk" only after explicit pause (or Blocked/Retry).                                                                                                         | Tap-to-start each session.                            | User decision. The product moment is "open the app and talk" (`01` §2 step 9–10); a mandatory tap taxes every session.                            |
| D4  | Utterance commit via AssemblyAI auto end-of-turn; X cancels pre-commit.                                                                                                                            | Tap-to-stop-and-send; hybrid grace-window coalescing. | User decision. Hands-free matches the design; mis-fires are cheap (`01` §7 confirmation) and hybrid coalescing is client state we don't need yet. |
| D5  | Mic capture must not silence other apps' audio — duck (not full-stop, not full-volume) via a `play-and-record` Audio Session on iOS; no-op elsewhere (§3a).                                          | Full-stop (status quo); full-volume mix; native wrapper with `AVAudioSession` control. | Ducking matches Siri/Voice Memos and is the cleanest STT input. Duck-vs-mix is ultimately platform-decided; a native wrapper (real `.duckOthers` control) waits on `10` packaging. |
| D6  | A sent report **releases the mic** (returns to Paused, ending hands-free continuation — amends D4/§3a) so the audio session frees other apps' audio and iOS can resume the paused/ducked music — best effort. | Keep hands-free and resume music mid-session (impossible — a live capture session is what holds the other app's audio); native `AVAudioSession` control (waits on `10` packaging). | User decision. The only web lever to resume another app is our own session going inactive; keeping the mic live precludes it, so a send must end the turn. |


**Open questions (owned elsewhere or later):** PWA-vs-wrapped-native packaging — gated
on mobile mic + push together, so it stays with `10` (`07` D2); audio-format/codec
tuning beyond PCM16 if mobile battery or bandwidth demands it; multilingual STT
(`02` §15-class future work).