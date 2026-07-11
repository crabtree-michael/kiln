import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// KEYLESS E2E — onboarding a new project (spec 11 §8), run with NO provider keys
// (design docs/keyless-e2e-tests-design.md §Test 3). A brand-new user with no
// project lands on the connect-a-project screen, fills the real dashboard form,
// and — because KILN_VERIFY_MODE=mock makes the live checks pass offline — the
// credential comes back "ok" and the board renders with a seeded worker pool.
// Exercises identity/tenancy, PUT /api/settings + PUT /api/project, the per-project
// provider build (mock agent + scripted brain), and ReconcileWorkers, with no real
// GitHub/Amika/Anthropic credential anywhere.
test('@keyless a new user connects a project and the board comes alive', async ({ page }) => {
  test.setTimeout(60_000);
  // A THROWAWAY login so the user is genuinely new (no project) on every run.
  await mintSession(page.request, { login: `keyless-onboard-${Date.now()}` });

  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: 'Set up your project' })).toBeVisible();
  await page.getByLabel('Project name').fill('keyless-e2e');
  await page.getByLabel('Repo URL').fill('https://example.com/keyless/repo.git');
  await page.getByRole('button', { name: 'Save project' }).click();

  // Credentials auto-save on blur, then a live verify runs. With KILN_VERIFY_MODE=mock
  // the Amika check reports ok offline (no real Amika call), instead of the failed
  // status the key-gated dashboard-config spec asserts.
  await page.getByLabel('Amika API key').fill('mock-amika-key');
  await page.getByLabel('Amika API key').press('Tab');
  const secret = page.locator('[data-role="secret-status"][data-name="amika_api_key"]');
  await expect(secret).toHaveAttribute('data-set', 'true');
  await expect(
    page.locator('[data-role="credential-status"][data-name="amika_api_key"]'),
    'mock verify should report ok offline',
  ).toHaveAttribute('data-status', 'ok', { timeout: 20_000 });

  // The project now exists: GET /api/board is 200 (not the 404 a projectless user gets)…
  await expect
    .poll(async () => (await page.request.get('/api/board')).status(), {
      message: 'GET /api/board never returned 200 — project was not created',
      timeout: 20_000,
    })
    .toBe(200);

  // …and the board client renders the live board for this new user.
  await page.goto('/debug');
  await expect(page.getByRole('region', { name: 'Board' })).toBeVisible();
});
