import { expect, test } from '@playwright/test';
import { mintSession } from '../session';
import { apiBase, seedTicket } from '../keyless';

// KEYLESS E2E — direct text editing of a ticket from the detail sheet, run with
// NO provider keys against the mocked stack (docker-compose.keyless.yml).
//
// This is the one path in the app where the user's own words must reach the
// board untouched: voice-dictated change requests get interpreted by the brain
// and can drift from what was meant, so the sheet's pencil writes the typed text
// straight through (POST /api/tickets/{id}/text → board.ShapeTicket). The brain
// is deliberately NOT in this loop — which is exactly why a keyless stack can
// cover it end to end: the scripted brain never has to be involved.
//
// Driven through the real browser UI (open the proposal → pencil → type → Save)
// and asserted on the server snapshot (GET /api/board), so a client-only change
// that never reached the board fails here.
test('@keyless the ticket sheet edits a proposal’s title and body directly', async ({
  page,
  request,
}) => {
  test.setTimeout(60_000);
  await mintSession(page.request);
  await mintSession(request, { base: apiBase });

  // A shaping ticket is a proposal card in the feed — the state the affordance
  // exists for (wording refined before work starts). Seeded deterministically,
  // with no brain involved.
  const ticketId = await seedTicket(request, {
    title: 'Add teh login redirekt',
    body: 'Send the user somewhere after sign-in.',
    state: 'shaping',
  });

  await page.goto('/app');

  // Open the proposal's full ticket from the feed card.
  await page.getByRole('button', { name: 'Open ticket: Add teh login redirekt' }).click();
  const sheet = page.getByRole('dialog');
  await expect(sheet).toBeVisible();

  // The pencil turns the title and body into fields seeded with the current text.
  await sheet.getByRole('button', { name: 'Edit' }).click();
  const title = page.getByLabel('Title');
  const body = page.getByLabel('Description');
  await expect(title).toHaveValue('Add teh login redirekt');
  await expect(body).toHaveValue('Send the user somewhere after sign-in.');

  await title.fill('Add the login redirect');
  await body.fill('Land the user on /app after sign-in, not /.');
  await page.getByRole('button', { name: 'Save' }).click();

  // The sheet stays open on the corrected ticket — saving an edit is not an exit.
  await expect(page.getByRole('dialog')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Add the login redirect' })).toBeVisible();

  // And the text actually landed on the board, verbatim — the point of the whole
  // affordance. Polled, since it arrives via the board.updated the write emits.
  await expect
    .poll(
      async () => {
        const res = await request.get(`${apiBase}/api/board`);
        if (!res.ok()) return null;
        const board = (await res.json()) as { shaping: { id: string; title: string; body: string }[] };
        const found = board.shaping.find((t) => t.id === ticketId);
        return found ? { title: found.title, body: found.body } : null;
      },
      { message: 'the edited text never reached the board', timeout: 20_000 },
    )
    .toEqual({
      title: 'Add the login redirect',
      body: 'Land the user on /app after sign-in, not /.',
    });
});
