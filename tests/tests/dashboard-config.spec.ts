import { test, expect } from '@playwright/test';

// Phase 1 dashboard flow (spec 11 §8): dev session → onboard → config sticks.
// Needs the compose stack up with KILN_DEV_ENDPOINTS=1 and identity env set
// (GITHUB_OAUTH_CLIENT_ID, KILN_SECRETS_KEY) — both default in local .env.
test('dashboard onboarding stores config and reflects status', async ({ page }) => {
  // page.request shares the browser context's cookie jar, so the minted
  // session cookie authenticates subsequent page navigation.
  const mint = await page.request.post('/api/dev/session', {
    data: { github_login: `e2e-dash-${Date.now()}` },
  });
  expect(mint.ok()).toBe(true);

  await page.goto('/dashboard');
  // Fresh user → onboarding.
  await expect(page.getByRole('heading', { name: 'Set up your project' })).toBeVisible();
  await page.getByLabel('Project name').fill('kiln-e2e');
  await page.getByLabel('Repo URL').fill('https://github.com/crabtree-michael/kiln');
  await page.getByRole('button', { name: 'Save project' }).click();

  // Project saved → settings view; store credentials.
  await page.getByLabel('Anthropic API key').fill('sk-ant-e2e-fake-x4Kd');
  await page.getByRole('button', { name: 'Save credentials' }).click();
  const status = page.locator('[data-role="secret-status"][data-name="anthropic_api_key"]');
  await expect(status).toHaveAttribute('data-set', 'true');
  await expect(status).toContainText('x4Kd');

  // Write-only: the raw secret never comes back over the wire.
  const me = await page.request.get('/api/me');
  expect(await me.text()).not.toContain('sk-ant-e2e-fake');

  // Verify endpoint runs live: the fake key must FAIL against real Anthropic;
  // amika reports skipped (never configured). The repo check RUNS (repo_url is
  // set) and passes — public repo, ls-remote needs no token.
  await page.getByRole('button', { name: 'Test connections' }).click();
  await expect(page.locator('[data-role="verify-check"][data-name="anthropic"]')).toHaveAttribute(
    'data-status', 'failed');
});
