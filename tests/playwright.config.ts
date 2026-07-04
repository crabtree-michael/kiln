import { defineConfig, devices } from '@playwright/test';

// The e2e suite drives the REAL web client against a running stack (02 §4a):
// no fakes at this level — the brain hits the real LLM. Target the frontend the
// docker-compose stack serves by default; KILN_E2E_BASE_URL overrides it (e.g. a
// deployed client). See ./README.md for the run recipe (cheap model, keys).
const baseURL = process.env.KILN_E2E_BASE_URL ?? 'http://localhost:5173';

export default defineConfig({
  testDir: './tests',
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
