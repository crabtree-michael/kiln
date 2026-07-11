# Design: four end-to-end tests that run without API keys

**Status:** design / proposal · **Date:** 2026-07-11 · **Scope:** `/tests` (Playwright e2e), with two small composition-root additions in `backend/cmd/kiln`.

## 1. Why this document exists

Kiln has a real end-to-end tier — the Playwright suite in `/tests` that drives the **real web client** against a **running Docker Compose stack** and exercises the live loop (spec 02 §4a). By design that tier "hits real services": the brain calls the real Anthropic LLM, and the Developing-reaching specs bill real Amika turns. That is deliberate — but it is exactly why the e2e suite is **excluded from CI** (`.github/workflows/check.yml`: "E2e … needs a live stack and real provider keys … and is run deliberately, not on every push"). It also means a contributor cannot run the flagship user journeys locally without provisioning four paid credentials.

This document designs **four e2e tests that need no API keys**, so the highest-value user workflows can run in CI and on a laptop with nothing but Docker. They are *not* a replacement for the existing real-service e2e specs (which still verify the actual Anthropic/Amika/AssemblyAI wiring). They are a second, deterministic e2e lane that exercises the same code paths — routing, the durable event queue, the board state machine, the deterministic pull, the transactional outbox, SSE, the React client — with every **paid boundary swapped for an in-process fake at the composition root**.

The guiding principle matches the one the `mock` agent provider already states (`internal/agent/mock/provider.go`): *fake only the provider; the generic machinery, the tables, and the event path all run for real.*

## 2. The four external boundaries and how each is neutralized

Every external dependency in Kiln already sits behind a single-purpose Go interface with an existing test double. The table maps each to its keyless strategy.

| Boundary | Interface (port) | Real impl | Keyless strategy | Status |
|---|---|---|---|---|
| **Anthropic** (brain LLM) | `brain.LLM` (`internal/brain/llm.go:125`) | `brain.Adapter` | Scripted, fixture-driven `LLM` selected by a new `KILN_BRAIN_MODE=scripted` switch | **new wiring** (§3.1) |
| **Amika** (coding agent) | `agent.Provider` (`internal/agent/provider.go`) | `amika.Client` | In-memory `mock.Provider`, `AGENT_MODE=mock` | **exists** |
| **AssemblyAI** (STT) | `api.VoiceTokenMinter` (`internal/api/routes.go:171`) | `assemblyai.Client` | Canned token + local mock STT WebSocket, `KILN_VOICE_MODE=mock` | **new wiring** (§3.2) |
| **Web Push** (notify) | `runtime.Notifier` + `push.Store` | `push.Sender` (VAPID) | Locally-generated VAPID pair; subscription endpoint points at a mock push server | **exists** |
| **GitHub OAuth** (auth) | `api.DevSessionMinter` | OAuth callback | `POST /api/dev/session` dev mint | **exists** |
| **Git repo clone** (brain repo shell) | `brain.RepoShell` | `repo.New` | Non-fatal: an unconfigured clone yields a disabled shell — no key needed | **exists** |

Two of the six need new composition-root wiring. Both mirror the existing `AGENT_MODE=mock` pattern exactly — a single env switch that selects an in-process fake in `wiring.go`, changing nothing in the modules themselves.

### Key insight: only the brain blocks keyless e2e today

The coding agent is *already* keyless — `AGENT_MODE=mock` is the docker-compose default (spec 05 §8). Push works with a locally generated VAPID pair (no paid service). Auth has `POST /api/dev/session`. The repo shell degrades gracefully. **The one hard blocker is the brain**: `newBrainLLM` (`cmd/kiln/wiring.go:379`) unconditionally builds a real Anthropic `Adapter`, and there is no env switch to swap it. Every event-driven flow — a typed message, a completed agent turn, an accepted proposal — wakes `brain.HandleEvent`, which calls `LLM.Do`. Without an offline brain, no full loop can run keyless. Closing that one gap (§3.1) unlocks all four tests below.

## 3. Shared harness additions

### 3.1 A scripted brain (`KILN_BRAIN_MODE=scripted`)

The brain already has a battle-tested fake: `scriptedLLM` in `internal/brain/fakes_test.go` plays back a fixed `[]brain.LLMResponse` and records every `LLMRequest`. It powers the brain's golden-decision unit suite. The proposal is to **promote that pattern into production code** as a new package `internal/brain/scripted` (parallel to `internal/agent/mock`), fixture-driven so tests can script decisions without recompiling:

- **Selection.** `newBrainLLM` gains a branch: when `KILN_BRAIN_MODE=scripted`, return a `scripted.LLM` loaded from `KILN_BRAIN_SCRIPT` (a JSON fixture path) instead of `brain.NewAdapterWithClient`. Default/unset preserves today's real-Anthropic behavior byte-for-byte. `buildGraph` validates the mode once, exactly as it validates `AGENT_MODE`.
- **The fixture format.** A list of rules, each matching an inbound turn and emitting a canned tool-call sequence. The brain runs its *real* bounded tool loop over these responses — real `create_ticket` / `mark_ready` / `accept_to_done` / `mark_blocked` / `say` / `request_approval` tool calls hit the real board service, real outbox, real SSE. Only the "which tool to call" judgment is canned.

  ```jsonc
  // tests/fixtures/brain/core-loop.json
  {
    "rules": [
      {
        "match": { "event": "human.message", "textContains": "login form" },
        "responses": [
          { "toolCalls": [
              { "name": "create_ticket",
                "input": { "title": "Build a login form (${TAG})", "body": "..." } },
              { "name": "mark_ready", "input": { "title_contains": "${TAG}" } } ] },
          { "endTurn": "On it — I opened a ticket and marked it ready." }
        ]
      },
      {
        "match": { "event": "agent.turn_completed" },
        "responses": [
          { "toolCalls": [ { "name": "accept_to_done", "input": { "ticket_ref": "..." } } ] },
          { "endTurn": "Done — the login form work is merged." }
        ]
      }
    ],
    "default": { "responses": [ { "endTurn": "" } ] }   // StopEndTurn — matches scriptedLLM's under-scripted fallback
  }
  ```

  A rule's `match` is evaluated against the event kind and (for `human.message`) a substring of the message text, so a fixture stays valid regardless of the unique per-run tag the test injects. The `default` mirrors `scriptedLLM`'s existing behavior: an unmatched turn returns `StopEndTurn` and does nothing, so an under-scripted fixture fails loudly (a ticket that never moves) rather than silently passing.

- **Why fixtures, not hardcoded Go.** The scripts live under `tests/fixtures/brain/` next to the specs that use them, so a spec author edits one file. The same stack image serves every keyless e2e; only `KILN_BRAIN_SCRIPT` changes per run (or the fixture holds rules for all specs).

This is ~150 lines of production code that reuses types the brain already exports (`LLMResponse`, `ToolCall`, `StopReason`), and it is the linchpin: tests 1, 2, and 4 all depend on it.

### 3.2 A mock STT path (`KILN_VOICE_MODE=mock`)

Voice needs two swaps because the audio never transits the backend — the browser opens the AssemblyAI WebSocket directly:

1. **Token mint.** `api.VoiceTokenMinter` is swapped for a canned minter (the shape already exists as `fakeVoiceTokenMinter` in `internal/api/fakes_test.go`): it returns a static token + expiry and never calls AssemblyAI. Selected by `KILN_VOICE_MODE=mock` in `buildGraph`.
2. **The streaming socket.** `frontend/src/voice/assemblyai-client.ts` is the only file that knows the WS URL; it already reads an override. A tiny **mock AssemblyAI WS server** ships under `tests/mock-stt/` — a ~40-line Node `ws` server that, on connect, emits a `Begin` frame and then a scripted final `Turn` transcript, ignoring the audio bytes. The Playwright config points the client at it via `KILN_VOICE_WS_URL` (or the config's existing frontend env plumbing). Real worklet, real commit machine, real "commit → `human.message`" path — only the STT vendor socket is faked.

The existing `voice` Playwright project (Chromium launched with `--use-file-for-fake-audio-capture`) is reused unchanged; the fake mic still supplies the user-gesture'd audio stream, but the transcript now comes from the mock socket instead of billing AssemblyAI.

### 3.3 Isolation, auth, and cleanup (all existing seams)

- **Auth.** Every spec mints a dev session with `mintSession()` (`tests/session.ts` → `POST /api/dev/session`), already used by the current suite. Requires `KILN_DEV_ENDPOINTS=1` (docker-compose default).
- **Per-test isolation.** The suite runs against a persistent stack, so specs must not assume an empty board. Two tactics, both already in the repo: (a) tag each run's data with a unique marker and assert on *your own* rows (the `say-creates-ticket` pattern), and (b) call `POST /api/dev/reset` (mounted under `KILN_DEV_ENDPOINTS`/`EnableReset`) in a `beforeEach` to wipe project state deterministically.
- **No teardown of paid resources.** The current `global-teardown.ts` deletes Amika sandboxes; with `AGENT_MODE=mock` there are none (it already "logs and skips … a mock-provider stack"), and the mock provider is in-memory — a stack restart resets it. Nothing to clean up.
- **A CI-friendly compose profile.** Add a `keyless` profile (or a `docker-compose.keyless.yml` override) that sets `AGENT_MODE=mock`, `KILN_BRAIN_MODE=scripted`, `KILN_VOICE_MODE=mock`, `KILN_DEV_ENDPOINTS=1`, a generated `VAPID_*` pair, and a bootstrap-seeded `e2e-user` project (mock creds). No `ANTHROPIC_API_KEY`, `AMIKA_API_KEY`, or `ASSEMBLYAI_API_KEY` set. A new `make e2e-keyless` brings this up and runs the four specs below (tagged `@keyless`), and CI runs *that* on every PR — the real-service `make e2e` stays a deliberate, manual lane.

## 4. The four tests

Each is a complete user journey driven through the real web client against the real stack, with every paid boundary faked per §2–§3. Target frontend routes: `/debug` is the board+chat client (`App`), `/` is the spec-08 feed screen (`PrimaryScreen`) — as documented in `say-creates-ticket.spec.ts`.

---

### Test 1 — Core loop: "say a build request → the work ships to Done"

**File:** `tests/tests/keyless-core-loop.spec.ts`

**What it covers.** The entire spec-01 §2 core loop, end to end, keyless — the single most valuable journey in the product. A user asks for work in plain language; a ticket is created and readied; the deterministic pull binds a worker; the (mock) agent runs a turn; its completion feeds back through the queue; the brain accepts the result; the board shows Done. This is the union of what the real-service specs `say-creates-ticket`, `ready-kicks-off-amika-run`, and `agent-completion-feeds-brain` cover — but in one deterministic pass with no keys and no billing.

**Path through the system.**
`type in chat` → `POST /api/message` → `runtime.PostMessage` (transcript append + `EnqueueEvent human.message`) → events worker → scripted brain: `create_ticket` + `mark_ready` → board write + transactional outbox (`pull.evaluate`, `activity.toast`) → `board` SSE → **deterministic pull** binds a free mock worker, emits `agent.send` → outbox executor → `agent.Service.Send` → `mock.Provider.StartTurn` → poller `CheckTurn` returns canned success after `DefaultTurnDelay` → `agent.turn_completed` enqueued → events worker → scripted brain: `accept_to_done` → board write → `board` SSE → client re-render.

**Mocking strategy.** Scripted brain (§3.1, fixture `core-loop.json`); mock agent provider (`AGENT_MODE=mock`, canned success — the default). Nothing else faked: the queue, board, pull, outbox, agent state machine, `agent_turns` table, and SSE all run for real.

**Fixtures / test data.** `tests/fixtures/brain/core-loop.json` (§3.1). A unique per-run tag (`E2E-${Date.now().toString(36)}`) injected into the request text and matched into the ticket title, so the assertions target this run's ticket regardless of prior state.

**Steps.**
1. `mintSession(page.request)`, then `page.goto('/debug')`.
2. Assert the `Board` region is visible and `data-connection-state="connected"` (SSE up).
3. Fill the `Message` input with a request containing the tag; click `Send`.
4. Assert the tagged `ticket-card` appears in **Backlog** (created + readied).
5. Assert it moves to the **Developing** column (`state=working` — the pull bound a worker and the mock turn started).
6. Assert it reaches **Done** (the completion fed the brain, which accepted).

**Expected outcome.** A single tagged ticket observed traversing Backlog → Developing → Done, entirely over SSE, in a few seconds (mock turn delay ~100 ms + queue latency), with zero external calls. A failure localizes cleanly: stuck in Backlog ⇒ routing/brain-fixture; stuck in Developing ⇒ pull/agent-runtime/outbox; never-Done ⇒ the return-leg event path.

---

### Test 2 — Blocked ticket surfaces in the feed, notifies the user, and is resolved

**File:** `tests/tests/keyless-blocker-and-accept.spec.ts`

**What it covers.** The human-in-the-loop escape hatch and the notification transport (the `notifications` skill's core scenario, spec 02 §10): the orchestrator gets stuck, **pins a blocker in the feed**, **emits a Web Push notification** to reach a backgrounded user, and the user **accepts a proposal** to move the work forward. Exercises the feed snapshot/SSE, the transactional-outbox `notify.send` topic, the real `push.Sender` (RFC 8291 encryption + VAPID signing), and the `POST /api/tickets/{id}/accept` return path.

**Path through the system.**
Seed a proposal/blocker deterministically, then assert two side effects and the resolution:
- **Feed + push:** the blocked/approval-requested transition emits `feed.updated` (→ `feed` SSE, the client pins the card) and `notify.send` (→ `runtime.Notifier.Send` → `push.Sender.sendOne` → HTTP POST to the subscription endpoint). The subscription endpoint was registered via `POST /api/push/subscribe` pointing at a **mock push server** (a local `httptest`-style receiver bundled in `tests/mock-push/`), so the encrypted push is captured and asserted — no real browser push service, no FCM/Mozilla.
- **Accept:** the user clicks **Accept** on the feed card → `POST /api/tickets/{id}/accept` → scripted brain resolves the proposal (`accept_to_done` or `mark_ready`) → `board`/`feed` SSE → the card clears.

**Mocking strategy.** Scripted brain (§3.1) for the accept turn. Real `push.Sender` with a **locally generated VAPID pair** (`VAPID_PUBLIC_KEY`/`PRIVATE_KEY`/`SUBJECT` in the keyless profile — VAPID is self-issued, not a paid credential). The push *service* is the mock server; the subscription is registered against its URL (the API accepts any endpoint URL — see `push_test.go` using `https://push.example/a`). The blocker itself is seeded via `POST /api/dev/tickets {"state":"blocked","blocked_reason":"…"}` or `{"approval_requested":true}` (deterministic, no brain), so setup doesn't depend on LLM judgment.

**Fixtures / test data.** `tests/fixtures/brain/accept.json` (one rule: on the accept event, resolve). The mock push server (records received pushes). A generated VAPID pair baked into the keyless compose profile. Unique tag per run.

**Steps.**
1. `mintSession`; register a push subscription whose `endpoint` is the mock push server URL (`POST /api/push/subscribe`).
2. `page.goto('/')` (the feed screen); assert the feed region is connected.
3. Seed a blocked/approval-requested ticket for this project via `POST /api/dev/tickets` with the tag.
4. Assert the client **pins the blocker/proposal card** in the feed (over SSE).
5. Assert the **mock push server received one push** addressed to the registered endpoint (proves `notify.send` → real Sender → RFC 8291 payload). Assert the payload references the blocked ticket.
6. Click **Accept** on the card; assert it clears and the ticket advances (scripted brain resolved it).

**Expected outcome.** The blocker appears in the feed *and* a real (locally-signed, locally-delivered) push is captured, then the user's Accept unblocks the work — the full stuck→notify→resolve loop, keyless. Verifies the push encryption/signing code (not just that a row was written) because a real `push.Sender` produced the captured body.

---

### Test 3 — Onboarding: a new user connects a project and the board comes alive

**File:** `tests/tests/keyless-onboarding.spec.ts`

**What it covers.** The spec-11 multi-user front door: a brand-new user with **no project** lands on the connect-a-project onboarding screen, fills in project settings through the real dashboard UI, and the board renders with a seeded worker pool. Exercises identity/tenancy (`withProject` gating, `PUT /api/settings`, `PUT /api/project`, `POST /api/settings/verify`), the per-project provider registry (`buildTenantProviders`), and `ReconcileWorkers` seeding the slot pool — the path today's real suite only reaches via a manual curl recipe in `tests/README.md`.

**Path through the system.**
Fresh dev session for a never-seen login → app boots, `GET /api/me` reports no project → onboarding screen (the `Board` region does not render, matching the README's note). User submits the dashboard form → `PUT /api/settings` (mock creds) + `PUT /api/project {name, repo_url, worker_count}` → tenant registry builds this project's providers (mock agent + scripted brain) and `ReconcileWorkers` seeds N idle slots → `GET /api/board` now 200 → the client renders the board with the worker pool.

**Mocking strategy.** `AGENT_MODE=mock` + `KILN_BRAIN_MODE=scripted` mean the providers built for the new project need no real creds. `POST /api/settings/verify` runs the account's live checks — in the keyless profile these must resolve **without external calls**. The design requires the mock providers to satisfy `Verify` locally (a mock-mode check returns `ok` rather than pinging Amika/Anthropic); this is the one behavior to confirm/extend when wiring the keyless profile (`identity` account service `Verify`). Auth uses `POST /api/dev/session` with a fresh login so the user genuinely has no project.

**Fixtures / test data.** No fixtures beyond a unique login (`onboard-${tag}`) so the user is new on every run — avoids the "already onboarded" state. Project form values are literals (name, a dummy repo URL, `worker_count: 3`). No `dev/reset` needed because the user is fresh.

**Steps.**
1. `mintSession(page.request, { login: uniqueLogin })`; `page.goto('/')`.
2. Assert the **onboarding / connect-a-project** screen is shown and the `Board` region is **not** present (the tenancy gate).
3. Drive the dashboard form: enter project name, repo URL, worker count; submit.
4. (Optional) click **Verify** and assert the checks report ok (mock-mode local verification).
5. Assert the app transitions to the board: the `Board` region becomes visible, and `worker_count` worker slots are rendered/idle.
6. Assert `GET /api/board` returns 200 for this session (project exists).

**Expected outcome.** A new user goes from "no project" to a live, empty board with a seeded worker pool through the real UI — with mock providers standing in for every paid credential. Verifies the tenancy gate, per-project provider construction, and worker seeding end-to-end.

---

### Test 4 — Voice: a spoken request becomes on-screen text and wakes the brain

**File:** `tests/tests/keyless-voice.spec.ts` (Playwright `voice` project)

**What it covers.** The voice I/O layer (spec 02 §9, spec 09): the full in-browser pipeline — mic → audio worklet → STT socket → commit machine → committed `human.message` → brain turn → on-screen reply — with a **mock STT socket** instead of AssemblyAI. This is the keyless twin of `voice-mic-to-brain`, minus the real-vendor billing and the `KILN_VOICE_SMOKE` gate. Kiln does not speak (TTS was cancelled), so the assertion is on-screen text, not audio out.

**Path through the system.**
Browser boots with a fake microphone (existing `--use-fake-device-for-media-stream` flag) → the client fetches a voice token (`POST /api/voice/token` → canned minter, §3.2) → `assemblyai-client.ts` opens the WS at the override URL → **mock STT server** emits `Begin` then a scripted final `Turn` transcript → the commit machine commits the utterance → `POST /api/message` (`human.message`) → scripted brain runs a turn and emits `say`/`create_ticket` → SSE → the transcript and a `kiln` reply appear in the Dock/feed.

**Mocking strategy.** Canned `VoiceTokenMinter` (`KILN_VOICE_MODE=mock`) — the token never came from AssemblyAI. Mock AssemblyAI WS server (`tests/mock-stt/`, §3.2) supplies the transcript. Scripted brain (§3.1, fixture `voice.json`) turns the utterance into a `say` reply and/or a ticket. The frontend voice pipeline (worklet, socket client, commit machine, Dock) runs entirely for real.

**Fixtures / test data.** `tests/fixtures/this-is-a-test.wav` (the existing fake-mic clip — still needed to satisfy `getUserMedia` and supply the user gesture; its *content* no longer matters since the transcript is scripted). The mock STT server's scripted transcript, e.g. "Create a ticket to build a login form." `tests/fixtures/brain/voice.json` (one rule: on that `human.message`, `say` a confirmation and `create_ticket`).

**Steps.**
1. In the `voice` Playwright project: `mintSession(page.request)`; `page.goto('/')`.
2. Click **Talk** (supplies the AudioContext user gesture; the fake mic + granted permission auto-accept getUserMedia).
3. Assert the token fetch succeeded and the client connected to the mock STT socket (a `Begin`-driven "listening" state, or the transcript rendering).
4. Assert the scripted transcript ("Create a ticket to build a login form") appears **on screen** as the committed utterance.
5. Assert a `kiln` reply appears (the brain ran a turn) and — if the fixture creates one — the ticket surfaces on the board/feed.

**Expected outcome.** A spoken phrase (scripted, keyless) travels the real browser voice pipeline to on-screen text and a brain response, proving the mic→worklet→socket→commit→message→brain path without touching AssemblyAI or Anthropic. Verifies the whole voice seam short of the vendor socket itself (which the gated real-service `voice-token-mints` spec still covers).

---

## 5. Coverage matrix

| Module / concern | Test 1 | Test 2 | Test 3 | Test 4 |
|---|:--:|:--:|:--:|:--:|
| Client-facing API + routing (`internal/api`) | ● | ● | ● | ● |
| SSE stream + hub | ● | ● | ● | ● |
| Durable event queue + workers (`internal/runtime`) | ● | ● | | ● |
| Brain tool loop (`internal/brain`, scripted LLM) | ● | ● | | ● |
| Board state machine + transactional outbox (`internal/board`) | ● | ● | ● | |
| Deterministic pull + worker binding | ● | | ● | |
| Agent runtime + mock provider (`internal/agent`) | ● | | (seed) | |
| Feed snapshot + proposals/accept | | ● | | |
| Web Push transport (`internal/push`, real Sender) | | ● | | |
| Identity / tenancy / onboarding (`internal/identity`, `tenant`) | (session) | (session) | ● | (session) |
| Voice pipeline (`internal/voice` + frontend worklet/commit) | | | | ● |
| React web client (`/frontend`) | ● | ● | ● | ● |

## 6. What ships to make this real

**New production code (small, mirrors existing patterns):**
1. `internal/brain/scripted` — a fixture-driven `brain.LLM` (promotes the existing `scriptedLLM`), plus a `KILN_BRAIN_MODE` branch in `cmd/kiln/wiring.go:newBrainLLM` and validation in `buildGraph` (parallel to `AGENT_MODE`).
2. A `KILN_VOICE_MODE=mock` branch selecting a canned `VoiceTokenMinter` (the `fakeVoiceTokenMinter` shape) in `buildGraph`.
3. Mock-mode `Verify` for the account service so onboarding's verify checks pass offline (§Test 3).

**New test harness (under `/tests`, no product changes):**
4. `tests/mock-stt/` — a ~40-line mock AssemblyAI WS server.
5. `tests/mock-push/` — a mock push-service receiver.
6. `tests/fixtures/brain/*.json` — the scripted-brain fixtures per spec.
7. Four `keyless-*.spec.ts` specs, tagged `@keyless`.

**Ops / CI:**
8. A `keyless` docker-compose profile (or override file) that sets the mock env switches, a generated VAPID pair, `KILN_DEV_ENDPOINTS=1`, and a bootstrap-seeded `e2e-user` project — with **no** `ANTHROPIC_API_KEY` / `AMIKA_API_KEY` / `ASSEMBLYAI_API_KEY`.
9. `make e2e-keyless` and a CI job that brings up the keyless profile and runs the `@keyless` specs on every PR — turning e2e from a manual, key-gated lane into part of the hard gate.

**Non-goals.** These tests do not verify the real Anthropic/Amika/AssemblyAI wire formats or that a real push service accepts the payload — the existing key-gated specs (`ready-kicks-off-amika-run`, `agent-completion-feeds-brain`, `voice-token-mints`, `voice-mic-to-brain`) remain the authority on the live vendor seams and are still run deliberately. The keyless lane verifies **everything Kiln itself owns** between those seams, deterministically, for free.

## 7. Implementation status (branch `keyless-e2e-tests`)

Built and gated (backend `make check` green — lint + unit): `internal/brain/scripted` (fixture-driven `brain.LLM`) + `KILN_BRAIN_MODE`/`KILN_BRAIN_SCRIPT` wiring; `internal/voice/mock` minter + `KILN_VOICE_MODE=mock`; `internal/identity/verify.Mock` + `KILN_VERIFY_MODE=mock`; unit tests for all three plus a `cmd/kiln` wiring test. Frontend: `VITE_VOICE_WS_URL` override (typecheck + lint green). Harness (e2e lane, runs against the live stack, not the commit gate): `tests/fixtures/brain/keyless.json`, zero-dependency `tests/mock-stt/` and `tests/mock-push/` servers (both smoke-tested with Node), the four `keyless-*.spec.ts` specs tagged `@keyless`, the `voice`-project config update, `docker-compose.keyless.yml` (config-validated), `make {up,down,e2e}-keyless`, and the `e2e-keyless` CI workflow.

Two deltas from the design above, both forced by real external dependencies the keyless stack can't fake without weakening the check:

- **Test 1 stops at Developing, not Done.** `accept_to_done` gates on a real `origin/main` commit verified through the repo shell (06 §7); a keyless stack's repo shell is disabled and fails that gate closed. So the core-loop fixture reacts to `agent.turn_completed` with a `say` + `post_update` (asserted), and the spec asserts the ticket reaches **Developing** plus that completion reaction — not Done. Done-via-gate stays the key-gated lane's job.
- **Test 2 uses the start-transition push, not a blocker+accept.** A Web Push (`notify.send`) fires on exactly three transitions — start, blocked, done (03 §7.1). The deterministic, brain-free one is **start** (the pull moving a seeded-ready ticket to working), so `keyless-notification.spec.ts` registers a subscription against the mock push service, seeds a Ready ticket, and asserts a real encrypted+VAPID-signed push is delivered. The proposal-accept human-in-the-loop leg is covered by the fixture's `tapped Accept` rule and the existing (mock-agent) `blocker-pins-and-proposal-accepts` spec.

The env switch for offline verify is `KILN_VERIFY_MODE=mock` (the design referred to it loosely as "mock-mode Verify").
