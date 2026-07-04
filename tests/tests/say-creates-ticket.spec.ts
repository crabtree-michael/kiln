import { expect, test } from '@playwright/test';

// E2E: the first two steps of the core loop (docs/specs/01 §2).
//
//   1. The user opens the app and says "Build a login form ...".
//   2. The orchestrator creates a ticket in Backlog.
//
// Driven end-to-end through the real web client (07): typing into the chat
// fires POST /api/message -> durable queue -> brain -> real LLM -> board write
// -> `board` SSE event -> re-render. We stop before any Amika pull, so this
// needs no sandbox (nothing to clean up) and never leaves Backlog.
//
// Backlog is the "Backlog" column (Board.tsx); a freshly created ticket lands in
// state `shaping` (03 §2.1), rendered as a `ticket-card` inside that column
// (BoardColumn.tsx / TicketCard.tsx).
test('saying a build request creates a ticket in Backlog', async ({ page }) => {
  await page.goto('/');

  // The board region must render before we can reason about its contents.
  const board = page.getByRole('region', { name: 'Board' });
  await expect(board).toBeVisible();

  // Every ticket in Backlog — Shaping or Ready — renders as a ticket-card
  // inside the Backlog column. Record the starting count rather than assuming
  // an empty board: this suite runs against a persistent stack, so earlier runs
  // may have left tickets. We assert this send adds one, not that Backlog is
  // empty. Let the initial snapshot settle before counting (the board arrives
  // over SSE just after connect).
  const backlog = page.getByRole('region', { name: 'Backlog' });
  const backlogTickets = backlog.locator('[data-role="ticket-card"]');
  await expect(board).toHaveAttribute('data-connection-state', 'connected');
  const before = await backlogTickets.count();

  // Step 1: the user says what they want, through the real chat input.
  await page
    .getByLabel('Message')
    .fill('Create a ticket to build a login form and wire it to the auth endpoint.');
  await page.getByRole('button', { name: 'Send' }).click();

  // Step 2: the orchestrator creates the ticket; it arrives over SSE and the
  // board re-renders. A real-LLM turn takes a while — poll until Backlog holds
  // at least one more card than before (expect.timeout gives it ~90s). Relative
  // to `before`, so pre-existing tickets don't matter.
  await expect
    .poll(() => backlogTickets.count(), {
      message: 'expected a new ticket to appear in Backlog after the build request',
    })
    .toBeGreaterThan(before);
});
