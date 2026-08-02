import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// KEYLESS E2E — onboarding a new project (spec 11 §8), run with NO provider keys
// (design docs/keyless-e2e-tests-design.md §Test 3). A brand-new user with no
// project walks the real GUIDED SETUP FLOW — connect GitHub, choose the project,
// choose the provider — then enters a credential and lands on a live board.
// Exercises identity/tenancy, PUT /api/settings + PUT /api/project, the per-project
// provider build (mock agent + scripted brain), and ReconcileWorkers, with no real
// GitHub/Amika/Anthropic credential anywhere.
//
// The flow's ordering is the thing under test as much as its endpoints are: the
// repo picker is only reachable once GitHub is connected, and the provider step
// only asks for the key belonging to the provider just chosen.
test('@keyless a new user is walked through setup and the board comes alive', async ({ page }) => {
  test.setTimeout(60_000);
  // A THROWAWAY login so the user is genuinely new (no project) on every run.
  await mintSession(page.request, { login: `keyless-onboard-${Date.now()}` });

  await page.goto('/dashboard');

  // ---- Step 1: connect GitHub. Signing in grants no scopes, so this step is a
  // real repo-scoped grant — but KILN_GITHUB_MODE=mock gives this dev-minted
  // session a synthetic credential that the mock reports as repo-scoped, so the
  // account already reads as connected and the step is a confirmation. That is
  // exactly why this spec is keyless-only: no headless test can complete the
  // real grant against github.com.
  await expect(page.getByRole('heading', { name: 'Connect GitHub' })).toBeVisible();
  await expect(page.locator('[data-role="github-connect"]')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await page.getByRole('button', { name: 'Continue' }).click();

  // ---- Step 2: choose the project. The repo comes from the connected account's
  // listing — there is no free-text repo URL field anywhere in the app — and the
  // project name defaults to the repo's own name.
  await expect(page.getByRole('heading', { name: 'Choose your project' })).toBeVisible();
  await page.getByLabel('Repository').selectOption('https://example.com/keyless/demo');
  await expect(page.getByLabel('Project name')).toHaveValue('demo');
  await page.getByLabel('Project name').fill('keyless-e2e');
  await page.getByRole('button', { name: 'Continue' }).click();

  // ---- Step 3: choose the provider. Mock is the keyless lane's provider (it is
  // what AGENT_MODE=mock resolves to anyway) and it authenticates with no key at
  // all, so the step correctly asks for none.
  await expect(page.getByRole('heading', { name: 'Choose your provider' })).toBeVisible();
  await expect(page.getByLabel('Amika API key')).toHaveCount(0);
  await page.getByRole('radio', { name: 'Mock' }).check();
  await expect(page.locator('[data-role="credential-row"]')).toHaveCount(0);
  await page.getByRole('button', { name: 'Finish setup' }).click();

  // Setup done → the flow hands over to settings, where credentials live.
  await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible({ timeout: 20_000 });
  await expect(page.locator('[data-role="onboarding"]')).toHaveCount(0);

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
