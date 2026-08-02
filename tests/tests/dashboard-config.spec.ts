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
  // The project is seeded over the API rather than through the form, because the
  // repo now comes from the caller's connected GitHub account (settings repo
  // picker) and a dev-minted session has no GitHub credential — its picker shows
  // the "Connect GitHub account" prompt, which no headless test can complete
  // against real GitHub. The keyless lane covers the form path instead, via
  // KILN_GITHUB_MODE=mock. This spec's subject is the credential store + live
  // verify below, which is unaffected.
  const created = await page.request.put('/api/project', {
    data: {
      name: 'kiln-e2e',
      repo_url: 'https://github.com/crabtree-michael/kiln',
      worker_count: 1,
    },
  });
  expect(created.ok(), 'seeding the project over PUT /api/project failed').toBe(true);
  await page.reload();

  // Project saved → settings view. Credentials now live in the Integrations
  // section as one connect card per provider: "Connect" opens a small modal
  // whose single input takes the key (there is no free-text credential form
  // anymore, and GitHub connects through OAuth rather than a pasted token).
  // The Anthropic key is a global env setting and its card is hidden, so this
  // exercises the credential path through the Amika card instead.
  await page.locator('[data-role="integration-connect"][data-provider="amika"]').click();
  await page.getByLabel('Amika API key').fill('sk-amika-e2e-fake-x4Kd');
  await page.locator('[data-role="api-key-save"]').click();
  const status = page.locator('[data-role="secret-status"][data-name="amika_api_key"]');
  await expect(status).toHaveAttribute('data-set', 'true');
  await expect(status).toContainText('x4Kd');

  // Write-only: the raw secret never comes back over the wire.
  const me = await page.request.get('/api/me');
  expect(await me.text()).not.toContain('sk-amika-e2e-fake');

  // The blur-triggered save automatically chains a live verify run (no
  // manual "Test connections" step anymore) — the fake key must FAIL against
  // real Amika. Generous timeout: this hits the real Amika API.
  await expect(
    page.locator('[data-role="credential-status"][data-name="amika_api_key"]'),
  ).toHaveAttribute('data-status', 'failed', { timeout: 20_000 });
});
