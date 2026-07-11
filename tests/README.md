# Kiln end-to-end tests

The top level of the three test levels (02 §4). Unit + component-integration tests
live inside each surface (`backend/internal/**`, `frontend/src/**`) and run offline
against fakes in the commit gate. **These e2e tests are different**: they drive the
**real web client** against a **running stack** and exercise the live loop — no fakes,
so the brain hits the **real LLM** (02 §4a, §1).

## Two lanes: real-service and keyless

The specs split into two lanes:

- **Real-service (default, key-gated):** the specs below drive the real LLM / Amika /
  AssemblyAI. Run deliberately, never in the commit gate, and NOT in CI (they need keys).
- **Keyless (`@keyless`-tagged, CI-runnable):** `keyless-*.spec.ts` drive the SAME live
  loop with every paid boundary mocked — `AGENT_MODE=mock`, the scripted brain
  (`KILN_BRAIN_MODE=scripted` + `tests/fixtures/brain/keyless.json`), the mock STT minter
  (`KILN_VOICE_MODE=mock`) + `tests/mock-stt/`, the offline verifier (`KILN_VERIFY_MODE=mock`),
  and a test VAPID pair delivering to `tests/mock-push/`. Design + rationale:
  `docs/keyless-e2e-tests-design.md`. Run them with the keyless overlay (no keys, no onboarding):

  ```sh
  make up-keyless          # docker-compose.yml + docker-compose.keyless.yml (mocks + e2e-user project)
  # wait for http://localhost:5173
  cd tests && pnpm install && pnpm run install-browser
  make e2e-keyless         # cd tests && pnpm test --grep @keyless
  make down-keyless
  ```

  CI runs this lane on every push and PR (`.github/workflows/e2e-keyless.yml`).

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
- `tests/voice-mic-to-brain.spec.ts` — the **full voice loop in a real browser** (09 §8),
  **gated** (`KILN_VOICE_SMOKE=1`) and isolated in the Playwright **`voice` project**, which
  launches Chromium with a **fake microphone** fed by `fixtures/this-is-a-test.wav` (a `say`-
  synthesized clip, padded with silence). Opening the app runs the real frontend pipeline
  (mic-on-by-default → worklet → AssemblyAI socket → commit machine); the spoken "This is a
  test" lands as a `human.message` and the brain runs a turn (a `kiln` reply appears). Needs
  `ASSEMBLYAI_API_KEY` + a brain key on the backend; no Amika. Run it with
  `KILN_VOICE_SMOKE=1 pnpm exec playwright test --project=voice`.

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

   Wait until the frontend answers on http://localhost:5173.

2. **Onboard a project for the test user** (once per fresh DB). Since spec 11 (multi-user)
   every `/api/*` route is project-scoped: these specs mint a dev session for `e2e-user` (or
   `KILN_BOOTSTRAP_GITHUB_USER`), but that user has **no project on a fresh stack**, so the app
   shows the "connect a project" onboarding screen and the `Board` region never renders — the
   specs fail at `expect(board).toBeVisible()`. Seed a project the way the dashboard would,
   against the **same login the specs mint** (default `e2e-user`; needs `KILN_DEV_ENDPOINTS=1`,
   which docker-compose defaults on):

   ```sh
   # Mint a session cookie, then set per-user creds + the project. Read the key
   # values from the repo-root .env into shell vars — do NOT paste secrets inline.
   ANTHROPIC=$(grep -E '^ANTHROPIC_API_KEY=' ../.env | cut -d= -f2-)
   AMIKA=$(grep -E '^AMIKA_API_KEY=' ../.env | cut -d= -f2-)
   CRED=$(grep -E '^AMIKA_CLAUDE_CRED_ID=' ../.env | cut -d= -f2-)
   REPO=$(grep -E '^AMIKA_REPO_URL=' ../.env | cut -d= -f2-)
   jar=$(mktemp)
   curl -sS -c $jar -X POST http://localhost:8080/api/dev/session \
     -H 'Content-Type: application/json' -d '{"github_login":"e2e-user"}' -o /dev/null
   curl -sS -b $jar -X PUT http://localhost:8080/api/settings -H 'Content-Type: application/json' \
     --data-binary "$(printf '{"anthropic_api_key":"%s","amika_api_key":"%s","amika_claude_cred_id":"%s"}' "$ANTHROPIC" "$AMIKA" "$CRED")" -o /dev/null
   curl -sS -b $jar -X PUT http://localhost:8080/api/project -H 'Content-Type: application/json' \
     --data-binary "$(printf '{"name":"kiln","repo_url":"%s","worker_count":3}' "$REPO")" -o /dev/null
   # Confirm: 200 (not 404) means the project exists and the board will render.
   curl -sS -b $jar http://localhost:8080/api/board -o /dev/null -w '%{http_code}\n'
   ```

   `make down` deletes the DB volume (`-v`), so re-run this after a teardown. (Only
   `say-creates-ticket` needs just the brain; the Developing-reaching specs also use the
   `amika_*` creds you set here.)

3. **Install deps + the browser** (first time only):

   ```sh
   cd tests && pnpm install && pnpm run install-browser
   ```

4. **Run:**

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
  account's sandboxes, filters to this stack's own pool (`KILN_WORKER_PREFIX`, default
  `kiln-dev-worker-` — matching the docker-compose backend; never another environment's
  sandboxes), and `DELETE`s each. It reads
  `AMIKA_API_KEY` / `AMIKA_BASE_URL` from the repo-root `.env` (loaded by
  `playwright.config.ts`); with no key set it logs and skips (e.g. a mock-provider stack).
  Sandbox names are stable slot uuids (a fixed pool of `KILN_WORKER_COUNT`), so a run's
  own sandbox can't be told apart by name — teardown deletes the whole pool.
- **Caveat:** while the backend is up, its ~60 s reconciler recreates idle slots, so
  teardown is best-effort during a run. A fully clean slate is only guaranteed once the
  stack is down — run `make down` after an e2e session that reached Developing (the
  teardown then has nothing racing it to recreate).
