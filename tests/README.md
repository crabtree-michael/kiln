# Kiln end-to-end tests

The top level of the three test levels (02 §4). Unit + component-integration tests
live inside each surface (`backend/internal/**`, `frontend/src/**`) and run offline
against fakes in the commit gate. **These e2e tests are different**: they drive the
**real web client** against a **running stack** and exercise the live loop — no fakes,
so the brain hits the **real LLM** (02 §4a, §1).

## What's here

- `tests/say-creates-ticket.spec.ts` — core-loop steps 1–2 (01 §2): a user says a
  build request in the chat and a ticket appears in **Backlog**. Stops before any
  Amika pull, so it needs no sandbox and leaves nothing to clean up.
- `tests/ready-kicks-off-amika-run.spec.ts` — moving a ticket to **Ready** kicks off a
  **real Amika run**. The board is read-only and has no move affordance (all mutation
  flows through the brain, D5), so this one is **API-driven**: it `POST`s a
  fully-specified request to `/api/message`, the brain creates the ticket and marks it
  ready in one turn, the deterministic pull binds a free worker and emits `agent.send`,
  and the test asserts two signals: (a) the board — it polls `/api/board` until the
  tagged ticket reaches **`working`** (the Developing column), and (b) **the real Amika
  send** — it snapshots each pooled sandbox's session count before the request, then
  polls `GET /sandboxes/{id}/sessions` until one gains a new session, proving
  agent-runtime's `StartTurn` (`POST …/agent-send-jobs`) actually reached Amika. Signal
  (b) deliberately reaches past the agent-runtime abstraction (05 §1) — justified because
  the point is to verify the **default Amika provider** works. **This reaches Developing,
  so it exercises real Amika and bills money.** It hits the backend directly at
  `http://localhost:8080` (override `KILN_E2E_API_URL`) rather than the vite proxy, and
  it needs `AMIKA_API_KEY` (from the repo-root `.env`) plus a **free worker** — if the
  key is missing or a prior run left the pool busy it fails fast with a clear message
  rather than timing out.
- `tests/agent-completion-feeds-brain.spec.ts` — the **return leg** of the loop, a
  separate mechanism from the send leg above: an agent's response feeds back through the
  event queue into the brain. It **seeds** a Developing ticket via the dev-only
  `POST /api/dev/tickets` (no brain, so setup is deterministic), the real pull binds a
  worker, the agent replies, `CheckTurn` emits `agent.turn_completed`, the runtime
  dequeues it into `brain.HandleEvent`, and the brain moves the ticket to **done or
  blocked** — the assertion. Needs `KILN_DEV_ENDPOINTS=1` on the stack (docker-compose
  defaults it on) and a free worker; also reaches Developing (real Amika, bills money).
- `tests/voice-token-mints.spec.ts` — the **voice STT path** (09 §8), **gated**: it only
  runs with `KILN_VOICE_SMOKE=1` (real AssemblyAI; never in `make check`). It mints a token
  via the backend (`POST /api/voice/token`) and opens the **real AssemblyAI streaming
  socket** with it, asserting a `Begin` frame — proving the whole credential path
  (the key never leaves the backend, yet the client's socket authenticates) with **no
  audio asset**. A second assertion streams a canned clip and asserts the utterance lands
  as a `human.message`; it runs only when `KILN_VOICE_SAMPLE=/path/to/clip.pcm` (raw PCM16
  mono 16 kHz) is set. Needs `ASSEMBLYAI_API_KEY` on the **backend** (repo-root `.env`);
  the test itself never sees the key. No Amika, no sandboxes — nothing to clean up.

## Target frontend

Playwright's `baseURL` is the frontend under test:

- Default: `http://localhost:5173` — the client the docker-compose stack serves.
- Override: set `KILN_E2E_BASE_URL` (e.g. a deployed client).

## Running

1. **Bring the stack up** with a real LLM key and the cheap model. Real e2e runs bill
   real money, so use Haiku (repo test-hygiene rule):

   ```sh
   # ANTHROPIC_API_KEY must be set (see .env / .env.example).
   KILN_BRAIN_MODEL=claude-haiku-4-5-20251001 make up
   ```

   Wait until the frontend answers on http://localhost:5173 and the board renders.

2. **Install deps + the browser** (first time only):

   ```sh
   cd tests && pnpm install && pnpm run install-browser
   ```

3. **Run:**

   ```sh
   cd tests && pnpm test
   # or, from the repo root:
   make e2e
   # against a different client:
   KILN_E2E_BASE_URL=https://kiln.example.app make e2e
   ```

   The gated voice smoke (`voice-token-mints`) is skipped unless you opt in. Bring the
   stack up with `ASSEMBLYAI_API_KEY` set on the backend (repo-root `.env`), then:

   ```sh
   # token mint + real socket auth (no audio asset needed):
   KILN_VOICE_SMOKE=1 make e2e
   # also exercise STT -> human.message with a canned clip (raw PCM16 mono 16 kHz):
   KILN_VOICE_SMOKE=1 KILN_VOICE_SAMPLE=/path/to/clip.pcm make e2e
   ```

## Notes

- The suite asserts a live behavior driven by a real model, so give it room: config
  timeouts are generous and each run is single-worker. A failure here is a signal
  about the running system — investigate the stack, don't loosen the assertion.
- `say-creates-ticket` never pulls a ticket into a sandbox, so it needs no Amika
  cleanup. Any e2e that *does* reach Developing must destroy the sandboxes it touches
  (`auto_delete` is off by design — 05 D6).
- **Amika cleanup is automatic** via `global-teardown.ts`: after the suite it lists the
  account's sandboxes, filters to the `kiln-worker-*` pool, and `DELETE`s each. It reads
  `AMIKA_API_KEY` / `AMIKA_BASE_URL` from the repo-root `.env` (loaded by
  `playwright.config.ts`); with no key set it logs and skips (e.g. a mock-provider stack).
  Sandbox names are stable slot uuids (a fixed pool of `KILN_WORKER_COUNT`), so a run's
  own sandbox can't be told apart by name — teardown deletes the whole pool.
- **Caveat:** while the backend is up, its ~60 s reconciler recreates idle slots, so
  teardown is best-effort during a run. A fully clean slate is only guaranteed once the
  stack is down — run `make down` after an e2e session that reached Developing (the
  teardown then has nothing racing it to recreate).
