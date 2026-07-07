// Regenerate the marketing landing-page screenshots in frontend/public/shots.
//
// These are REAL captures of the running app, driven headlessly against a
// locally seeded stack (no fakes beyond the seed) — the same dev endpoints the
// e2e suite uses (KILN_DEV_ENDPOINTS=1). Shots that appear in both themes are
// captured twice (light + dark) and the page swaps them by prefers-color-scheme;
// the board is the developer view, which the app renders dark-only, so it is a
// single dark capture.
//
// Prerequisites (see docs/specs/02 §1, §4 and tests/README.md):
//   1. Postgres:  docker compose up -d db
//   2. Backend (mock agents, dev endpoints, small worker pool so the seeded
//      Ready/Working zones stay put instead of being drained by the pull):
//        cd backend && DATABASE_URL='postgres://kiln:kiln@localhost:5432/kiln?sslmode=disable' \
//          AGENT_MODE=mock KILN_DEV_ENDPOINTS=1 KILN_WORKER_COUNT=3 go run ./cmd/kiln
//   3. Frontend:  cd frontend && pnpm dev
//   4. node tests/capture-landing-shots.mjs
import { chromium } from '@playwright/test';
import { mkdir } from 'node:fs/promises';

const API = process.env.KILN_E2E_API_URL ?? 'http://localhost:8080';
const BASE = process.env.KILN_E2E_BASE_URL ?? 'http://localhost:5173';
const OUT = new URL('../frontend/public/shots/', import.meta.url).pathname;
await mkdir(OUT, { recursive: true });

// ── seed a coherent, brain-free board + feed ──────────────────────────────────
const previewSvg = `
<svg xmlns="http://www.w3.org/2000/svg" width="640" height="380" viewBox="0 0 640 380">
  <rect width="640" height="380" fill="#faf6ef"/>
  <rect x="0" y="0" width="640" height="52" fill="#fffcf5"/>
  <circle cx="26" cy="26" r="6" fill="#e4442e"/>
  <rect x="44" y="20" width="120" height="12" rx="6" fill="#e4d8c1"/>
  <rect x="200" y="92" width="240" height="26" rx="6" fill="#221c15"/>
  <rect x="248" y="138" width="144" height="12" rx="6" fill="#a2977f"/>
  <rect x="200" y="182" width="240" height="44" rx="10" fill="#fffcf5" stroke="#e6ddca" stroke-width="2"/>
  <rect x="216" y="198" width="120" height="12" rx="6" fill="#a2977f"/>
  <rect x="200" y="238" width="240" height="44" rx="10" fill="#fffcf5" stroke="#e6ddca" stroke-width="2"/>
  <rect x="216" y="254" width="90" height="12" rx="6" fill="#a2977f"/>
  <rect x="200" y="298" width="240" height="46" rx="23" fill="#e4442e"/>
  <rect x="286" y="315" width="68" height="12" rx="6" fill="#fff9f0"/>
</svg>`;
const previewImageUrl = `data:image/svg+xml,${encodeURIComponent(previewSvg.trim())}`;

async function post(path, body) {
  const res = await fetch(`${API}${path}`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`POST ${path} -> ${res.status}: ${await res.text()}`);
}
const seedTicket = (spec) => post('/api/dev/tickets', { body: 'seeded', ...spec });
const postNote = (note) => post('/api/dev/notifications', note);

// Reset so a rerun is deterministic (best-effort — falls back to assuming a
// fresh stack), then bind the worker pool FIRST (blocked + working) so the
// deterministic pull can't drain the Ready zone we seed after.
try {
  await post('/api/dev/reset', {});
} catch (err) {
  console.warn('reset skipped (run against a fresh stack for a clean board):', err.message);
}
await seedTicket({
  title: 'checkout-refactor',
  body: 'Move checkout onto the new payments SDK.',
  state: 'blocked',
  blocked_reason:
    'The Stripe test key is rejected in CI. Use the sandbox key from the vault, or should I skip the live-charge test?',
});
await seedTicket({ title: 'login-form', body: 'New login form wired to /api/auth.', state: 'working' });
await seedTicket({ title: 'db-migrations', body: 'Backfill the 0007 migration on staging.', state: 'working' });
await seedTicket({ title: 'csv-export', body: 'Export the board to CSV from the header menu.', state: 'ready' });
await seedTicket({ title: 'search-index', body: 'Background-index tickets for instant search.', state: 'ready' });
await seedTicket({ title: 'weekly-digest', body: 'Weekly summary email of shipped work.', state: 'shaping' });
await seedTicket({
  title: 'saved-filters',
  body: 'Add saved filters to the tickets list: a pinned row of chips above results, persisted per user. Tap a chip to apply; long-press to rename.',
  state: 'shaping',
  approval_requested: true,
});
await seedTicket({ title: 'flaky-test-fix', body: 'Stabilise the retry-timeout test.', state: 'done' });
await seedTicket({ title: 'dark-mode-audit', body: 'Audit contrast across the night theme.', state: 'done' });
await postNote({
  kind: 'update',
  body: 'Ran the 0007 migration on the staging replica. 2.1M rows backfilled, no lock contention. Moving on to the read-path changes.',
});
await postNote({ kind: 'preview', body: 'Wired the new login form to /api/auth. Here is the happy path.', image_url: previewImageUrl });

// give the board a moment to settle after the initial pull churn
await new Promise((r) => setTimeout(r, 1500));

// ── capture ───────────────────────────────────────────────────────────────────
const browser = await chromium.launch({ headless: true });

async function primary(theme) {
  const ctx = await browser.newContext({
    viewport: { width: 402, height: 860 },
    deviceScaleFactor: 2,
    colorScheme: theme,
    permissions: ['microphone'],
    baseURL: BASE,
  });
  const page = await ctx.newPage();
  await page.goto('/');
  await page.getByRole('region', { name: 'Feed' }).waitFor({ state: 'visible' });
  await page.locator('[data-role="feed-card"][data-kind="blocker"]').first().waitFor();
  await page.waitForTimeout(1200); // fonts + preview image

  await page.screenshot({ path: `${OUT}feed-${theme}.png` });

  const proposal = page
    .locator('[data-role="feed-card"][data-kind="proposal"]')
    .filter({ hasText: 'saved-filters' })
    .first();
  await proposal.screenshot({ path: `${OUT}proposal-${theme}.png` });
  await page.locator('[data-role="dock"]').screenshot({ path: `${OUT}dock-${theme}.png` });
  console.log(`captured primary (${theme})`);
  await ctx.close();
}

async function board() {
  // /debug is dark-only; tall viewport so the board fits its region without an
  // internal scroll (which would stitch a sibling panel into the element shot).
  const ctx = await browser.newContext({
    viewport: { width: 1200, height: 1500 },
    deviceScaleFactor: 2,
    colorScheme: 'dark',
    baseURL: BASE,
  });
  const page = await ctx.newPage();
  await page.goto('/debug');
  await page.locator('[data-role="ticket-card"]').first().waitFor();
  await page.waitForTimeout(700);
  await page.locator('[data-role="board"]').screenshot({ path: `${OUT}board-dark.png` });
  console.log('captured board (dark)');
  await ctx.close();
}

await primary('light');
await primary('dark');
await board();
await browser.close();
console.log('done ->', OUT);
