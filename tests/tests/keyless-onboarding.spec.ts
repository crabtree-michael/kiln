import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// KEYLESS E2E — onboarding a new project (spec 11 §8), run with NO provider keys
// (design docs/keyless-e2e-tests-design.md §Test 3). A brand-new user with no
// project walks the real GUIDED SETUP FLOW — connect GitHub, choose the project,
// choose the provider and its key — then lands on a live board. Exercises
// identity/tenancy, PUT /api/settings + PUT /api/project, the per-project provider
// build and ReconcileWorkers, with no real GitHub/Amika/Anthropic credential
// anywhere: the key entered here is a throwaway string, and this project never
// sends a turn (AMIKA_BASE_URL points nowhere in the keyless overlay, so the
// adapter cannot reach out even if one did).
//
// The flow's ordering is the thing under test as much as its endpoints are: the
// repo picker is only reachable once GitHub is connected, and the provider step
// only asks for the key belonging to the provider just chosen.
test('@keyless a new user is walked through setup and the board comes alive', async ({ page }) => {
  test.setTimeout(60_000);
  // A THROWAWAY login so the user is genuinely new (no project) on every run.
  await mintSession(page.request, { login: `keyless-onboard-${Date.now()}` });

  await page.goto('/dashboard');

  // ---- Step 1: connect GitHub. In production this step sends the user to the
  // GitHub App's authorize screen, and on to the install page — where they choose
  // which repositories Kiln may use — if they have no installation yet. Here
  // KILN_GITHUB_MODE=mock gives this dev-minted session a synthetic
  // INSTALLATION, so the account already reads as connected and the step is a
  // confirmation. That is exactly why this spec is keyless-only: no headless
  // test can complete a real install against github.com.
  await expect(page.getByRole('heading', { name: 'Connect GitHub' })).toBeVisible();
  await expect(page.locator('[data-role="github-connect"]')).toHaveAttribute(
    'data-state',
    'connected',
  );
  await page.getByRole('button', { name: 'Continue' }).click();

  // ---- Step 2: choose the project. The repo comes from the connected account's
  // listing — there is no free-text repo URL field anywhere in the app — and the
  // step asks for nothing else: the project is NAMED after the repo (auto-name
  // from repository), silently — the pick is the whole step.
  await expect(page.getByRole('heading', { name: 'Choose your project' })).toBeVisible();
  await expect(page.getByLabel('Project name')).toHaveCount(0);
  await page.getByLabel('Repository').selectOption('https://example.com/keyless/demo');
  await page.getByRole('button', { name: 'Continue' }).click();

  // ---- Step 3: choose the provider. The picker offers only the real providers
  // (the in-memory mock is registered but never listed — it is what AGENT_MODE=mock
  // resolves to for the seeded projects, not something a user picks), so this new
  // user lands on Amika. Until something is picked the step can't know WHICH key to
  // ask for, so it asks for none.
  await expect(page.getByRole('heading', { name: 'Choose your provider' })).toBeVisible();
  await expect(page.locator('[data-role="provider-option"][data-provider="mock"]')).toHaveCount(0);
  await expect(page.getByLabel('Amika API key')).toHaveCount(0);
  await page.getByRole('radio', { name: 'Amika' }).check();

  // Picking Amika reveals exactly its key, inline on the step — and only its key.
  // Saving chains a live verify; with KILN_VERIFY_MODE=mock the Amika check reports
  // ok offline (no real Amika call), instead of the failed status the key-gated
  // dashboard-config spec asserts.
  await expect(page.locator('[data-role="credential-row"]')).toHaveCount(1);
  await expect(page.getByLabel('Devin API key')).toHaveCount(0);
  await page.getByLabel('Amika API key').fill('mock-amika-key');
  await page.getByRole('button', { name: 'Finish setup' }).click();

  // Setup done → the flow hands over to settings, where credentials live on.
  await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible({ timeout: 20_000 });
  await expect(page.locator('[data-role="onboarding"]')).toHaveCount(0);

  // The key entered during setup is the one the Integrations section now reports:
  // the card reads connected, and its verify landed ok. (The card's own connect
  // modal — the other way in — is covered by dashboard-config.spec.ts.)
  const amikaCard = page.locator('[data-role="integration-card"][data-provider="amika"]');
  await expect(amikaCard).toHaveAttribute('data-connected', 'true');
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
