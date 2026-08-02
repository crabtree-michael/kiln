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
  // Pick the repo from the connected GitHub account — the repo is no longer
  // typed (settings repo picker). KILN_GITHUB_MODE=mock serves the canned
  // listing and gives this dev-minted session its GitHub credential, so the
  // picker is populated with no real GitHub account involved.
  await page
    .getByLabel('Repository')
    .selectOption('https://example.com/keyless/demo');
  await page.getByRole('button', { name: 'Save project' }).click();

  // Credentials are entered through the Integrations section: the provider's
  // "Connect" card opens a modal whose single input takes the key. Saving it
  // chains a live verify — with KILN_VERIFY_MODE=mock the Amika check reports
  // ok offline (no real Amika call), instead of the failed status the
  // key-gated dashboard-config spec asserts.
  await page.locator('[data-role="integration-connect"][data-provider="amika"]').click();
  await page.getByLabel('Amika API key').fill('mock-amika-key');
  await page.locator('[data-role="api-key-save"]').click();
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

  // …and the primary screen renders the live feed for this new user.
  await page.goto('/app');
  await expect(page.getByRole('region', { name: 'Feed' })).toBeVisible();
});
