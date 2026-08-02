import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// KEYLESS E2E — sandbox selection on the projects page (feat: sandbox selection &
// dev-box snapshot management), run with NO provider keys against the mocked stack
// (docker-compose.keyless.yml: AGENT_MODE=mock). The mock provider implements the
// SandboxCatalog seam, so GET /api/projects/{id}/snapshots answers 200 — which is
// what flips the project form from a free-text snapshot handle to a real picker.
//
// Exercises the whole wired path the multi-project rebase re-architected: the
// per-project `useSandboxCatalog` hook → GET /api/projects/{id}/snapshots +
// /dev-boxes → the dual-mounted routes → sandboxCatalogAdapter → the tenant
// registry's mock provider → back through the wire mapping into the picker.
//
// The mock seeds no dev boxes, so a UI-driven capture can't pick a source (the
// select is empty, Save stays disabled); the capture is driven via the API
// instead (the mock accepts any ref), and the assertion is that the freshly
// captured snapshot then shows up as an option in the picker after a reload.
test('@keyless the projects page picks a snapshot and shows a captured dev box', async ({ page }) => {
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

  // The "save a dev box as a snapshot" section is present, with an empty dev-box
  // select and a Save button that stays disabled until a source + name are chosen
  // (the mock seeds no dev boxes, so it can't be enabled through the UI here).
  await expect(page.locator('[data-role="save-dev-box"]')).toBeVisible();
  await expect(page.locator('[data-role="dev-box-select"]')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save snapshot' })).toBeDisabled();

  // Capture a dev box as a snapshot via the API (the mock accepts any ref). This
  // is the same POST the UI issues; driving it directly lets the test assert the
  // catalog re-renders even though the mock offers no dev box to pick in the UI.
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
