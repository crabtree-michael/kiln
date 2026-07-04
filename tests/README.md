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

## Notes

- The suite asserts a live behavior driven by a real model, so give it room: config
  timeouts are generous and each run is single-worker. A failure here is a signal
  about the running system — investigate the stack, don't loosen the assertion.
- This behavior never pulls a ticket into a sandbox, so there is no Amika cleanup.
  Any e2e that *does* reach Developing must destroy the sandboxes it creates
  (`auto_delete` is off by design — 05 D6).
