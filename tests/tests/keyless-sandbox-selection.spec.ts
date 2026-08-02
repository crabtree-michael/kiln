import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// KEYLESS E2E — sandbox selection on the projects page (feat: sandbox selection &
// dev-box snapshot management), run with NO provider keys against the mocked stack
// (docker-compose.keyless.yml: AGENT_MODE=mock). The mock provider implements the
// SandboxCatalog seam, so GET /api/projects/{id}/snapshots answers 200 — which is
// what flips the project form from a free-text snapshot handle to a real picker.
//
// Exercises the whole wired path the multi-project rebase re-architected: the
// per-project `useSandboxCatalog` hook → GET /api/projects/{id}/snapshots → the
// dual-mounted routes → sandboxCatalogAdapter → the tenant registry's mock
// provider → back through the wire mapping into the picker.
//
// The project form no longer captures a sandbox as a snapshot — saving a sandbox
// is a per-TICKET choice now (the ticket detail sheet's switch) — so this asserts
// the old capture section is GONE from the card. The capture endpoint itself is
// still driven via the API here (the mock accepts any ref), so the picker's
// refresh path stays covered: a freshly captured snapshot shows up as an option
// after a reload.
test('@keyless the projects page picks a snapshot and offers no sandbox capture', async ({
  page,
}) => {
  test.setTimeout(60_000);
  // A THROWAWAY login so the user is genuinely new (no project) on every run.
  await mintSession(page.request, { login: `keyless-sandbox-${Date.now()}` });

  // Onboard a project. Its provider defaults to the deployment mock (AGENT_MODE=
  // mock), which exposes a snapshot catalog — no credentials needed for this path.
  await page.goto('/dashboard');
  await expect(page.getByRole('heading', { name: 'Set up your project' })).toBeVisible();
  await page.getByLabel('Project name').fill('keyless-sandbox');
  // Pick the repo from the connected GitHub account — the repo is no longer
  // typed (settings repo picker). KILN_GITHUB_MODE=mock serves the canned
  // listing and gives this dev-minted session its GitHub credential, so the
  // picker is populated with no real GitHub account involved.
  await page
    .getByLabel('Repository')
    .selectOption('https://example.com/keyless/demo');
  await page.getByRole('button', { name: 'Save project' }).click();

  // The settings view now lists the project as a compact PANEL; its
  // configuration lives in a dialog behind a click (projects-in-a-modal).
  await page.locator('[data-role="project-panel"]').click();
  await expect(page.getByRole('dialog')).toBeVisible();

  // Inside the dialog the sandbox group renders the snapshot PICKER (a select),
  // not the free-text handle, because the project's provider exposes a catalog
  // (the snapshots endpoint answered 200). It offers at least the Default option.
  const picker = page.locator('[data-role="amika-snapshot"]');
  await expect(picker).toBeVisible();
  await expect(picker).toHaveJSProperty('tagName', 'SELECT');
  await expect(picker.locator('option')).toContainText(['Default']);

  // The "save a dev box as a snapshot" section is gone from the project card:
  // saving a sandbox moved to the ticket detail sheet, per ticket.
  await expect(page.locator('[data-role="save-dev-box"]')).toHaveCount(0);
  await expect(page.locator('[data-role="dev-box-select"]')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Save snapshot' })).toHaveCount(0);

  // Capture a snapshot via the API (the mock accepts any ref), so the picker's
  // catalog-refresh path stays covered now that no UI drives the capture.
  const me = (await (await page.request.get('/api/me')).json()) as {
    projects: { id: string }[];
  };
  const projectId = me.projects[0]?.id;
  expect(projectId, 'onboarded project id').toBeTruthy();
  const captured = await page.request.post(`/api/projects/${projectId}/snapshots`, {
    data: { dev_box_ref: 'devbox-e2e', name: 'e2e-warm-base' },
  });
  expect(captured.status(), 'capture accepted (202)').toBe(202);

  // Reload and reopen: the per-project hook re-fetches the catalog when the
  // dialog mounts, and the freshly captured (still-capturing) snapshot now
  // appears as an option in the picker.
  await page.reload();
  await page.locator('[data-role="project-panel"]').click();
  await expect(page.locator('[data-role="amika-snapshot"] option')).toContainText([
    'Default',
    'e2e-warm-base (capturing)',
  ]);
});
