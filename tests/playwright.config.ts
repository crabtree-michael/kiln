import { defineConfig, devices } from '@playwright/test';
import dotenv from 'dotenv';

// Load the repo-root .env (where the provider keys already live — ANTHROPIC_API_KEY,
// AMIKA_API_KEY). Playwright's cwd is tests/, so it isn't picked up automatically; the
// global teardown needs AMIKA_API_KEY to destroy the sandboxes a Developing run leaves
// behind. Shell env wins over the file (no `override`), so an explicit export still rules.
dotenv.config({ path: new URL('../.env', import.meta.url).pathname });

// The e2e suite drives the REAL web client against a running stack (02 §4a):
// no fakes at this level — the brain hits the real LLM. Target the frontend the
// docker-compose stack serves by default; KILN_E2E_BASE_URL overrides it (e.g. a
// deployed client). See ./README.md for the run recipe (cheap model, keys).
const baseURL = process.env.KILN_E2E_BASE_URL ?? 'http://localhost:5173';

export default defineConfig({
  testDir: './tests',
  // Any test that reaches Developing spins up a real Amika turn; this destroys every
  // kiln-worker-* sandbox afterwards (auto_delete is off by design — 05 D6).
  globalTeardown: './global-teardown.ts',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  // A real-LLM turn is slow; give the whole test room and each assertion a long
  // poll window (the ticket arrives over SSE once the brain finishes its turn).
  timeout: 120_000,
  expect: { timeout: 90_000 },
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
