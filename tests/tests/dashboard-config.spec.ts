import { test, expect } from '@playwright/test';
import { mintSession } from '../session';

// Phase 1 dashboard flow (spec 11 §8): dev session → onboard → config sticks.
// Needs the compose stack up with KILN_DEV_ENDPOINTS=1 and identity env set
// (GITHUB_OAUTH_CLIENT_ID, KILN_SECRETS_KEY) — both default in local .env.
test('dashboard onboarding stores config and reflects status', async ({ page }) => {
  // page.request shares the browser context's cookie jar, so the minted
  // session cookie authenticates subsequent page navigation. A THROWAWAY login
  // (not the shared e2e user): this test must onboard from scratch.
  await mintSession(page.request, { login: `e2e-dash-${Date.now()}` });

  await page.goto('/dashboard');
  // Fresh user → onboarding.
  await expect(page.getByRole('heading', { name: 'Set up your project' })).toBeVisible();
  await page.getByLabel('Project name').fill('kiln-e2e');
  await page.getByLabel('Repo URL').fill('https://github.com/crabtree-michael/kiln');
  await page.getByRole('button', { name: 'Save project' }).click();

  // Project saved → settings view; credentials auto-save as entered — fill
  // and blur (no submit button exists anymore).
  await page.getByLabel('Anthropic API key').fill('sk-ant-e2e-fake-x4Kd');
  await page.getByLabel('Anthropic API key').press('Tab');
  const status = page.locator('[data-role="secret-status"][data-name="anthropic_api_key"]');
  await expect(status).toHaveAttribute('data-set', 'true');
  await expect(status).toContainText('x4Kd');

  // Write-only: the raw secret never comes back over the wire.
  const me = await page.request.get('/api/me');
  expect(await me.text()).not.toContain('sk-ant-e2e-fake');

  // The blur-triggered save automatically chains a live verify run (no
  // manual "Test connections" step anymore) — the fake key must FAIL against
  // real Anthropic. Generous timeout: this hits the real Anthropic API.
  await expect(
    page.locator('[data-role="credential-status"][data-name="anthropic_api_key"]'),
  ).toHaveAttribute('data-status', 'failed', { timeout: 20_000 });
});
