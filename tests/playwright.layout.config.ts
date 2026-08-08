import { defineConfig, devices } from '@playwright/test';

// The layout gate: geometry assertions against the real client in a real browser
// (see layout/harness.ts for why). This is a SEPARATE config from
// playwright.config.ts on purpose — that suite drives a live stack and a real
// LLM and is run deliberately; this one stubs every `/api` call, needs no stack
// and no keys, and runs in `make check` on every commit.
//
//     cd tests && pnpm install && pnpm run install-browser
//     pnpm run test:layout          # or: make test-layout
//
// The client is served by its own Vite dev server, started here. Nothing is
// proxied: `page.route('**/api/**')` intercepts before the request leaves the
// browser, so the backend the dev server would otherwise proxy to is never
// contacted.
declare const process: { env: Record<string, string | undefined> };

const PORT = 5199;

export default defineConfig({
  testDir: './layout',
  fullyParallel: false,
  forbidOnly: process.env.CI !== undefined,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  // Everything here is local and stubbed; a slow assertion means a broken
  // layout, not a slow network.
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: `http://localhost:${String(PORT)}`,
    trace: 'retain-on-failure',
    ...devices['Desktop Chrome'],
    // Each spec sets the viewport it is about (the shell switch is width-driven,
    // at 1024px — use-desktop-layout.ts).
    viewport: { width: 390, height: 720 },
  },
  webServer: {
    command: `pnpm --dir ../frontend exec vite --port ${String(PORT)} --strictPort`,
    url: `http://localhost:${String(PORT)}/app`,
    reuseExistingServer: process.env.CI === undefined,
    timeout: 120_000,
    stdout: 'ignore',
    stderr: 'pipe',
  },
});
