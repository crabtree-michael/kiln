import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

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
// The board+chat client lives at `/debug`; `/` is the spec-08 feed screen
// (main.tsx routes `/` -> PrimaryScreen, `/debug` -> App). This test drives the
// board client, so it navigates to `/debug`.
//
// Backlog is the "Backlog" column (Board.tsx); a freshly created ticket lands in
// state `shaping` (03 §2.1), rendered as a `ticket-card` inside that column
// (BoardColumn.tsx / TicketCard.tsx).
test('saying a build request creates a ticket in Backlog', async ({ page }) => {
  // Mint the dev session in the browser context BEFORE the app boots — the
  // session gate calls GET /api/me on load (page.request shares the page's jar).
  await mintSession(page.request);
  await page.goto('/debug');

  // The board region must render, and the live stream must be connected, before
  // the created ticket can reach us over SSE (07 §8).
  const board = page.getByRole('region', { name: 'Board' });
  await expect(board).toBeVisible();
  await expect(board).toHaveAttribute('data-connection-state', 'connected');

  // This suite runs against a persistent stack, so earlier runs may have left
  // tickets on the board — and a repeated identical request is (correctly) not
  // duplicated by the orchestrator. So we don't count tickets or assume an empty
  // Backlog: we tag THIS request with a unique marker and assert that our own
  // ticket appears. That verifies this send created a ticket regardless of what
  // was already there.
  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Step 1: the user says what they want, through the real chat input.
  await page
    .getByLabel('Message')
    .fill(
      `Create a ticket to build a login form and wire it to the auth endpoint. ` +
        `Include the exact tag ${tag} in the ticket title.`,
    );
  await page.getByRole('button', { name: 'Send' }).click();

  // Step 2: the orchestrator creates the ticket in Backlog; it arrives over SSE
  // and the board re-renders. A real-LLM turn is slow, so give it room (the
  // configured expect timeout ~90s). Assert our tagged ticket is now in the
  // Backlog column — Shaping or Ready both render as a ticket-card there.
  const backlog = page.getByRole('region', { name: 'Backlog' });
  await expect(backlog.locator('[data-role="ticket-card"]', { hasText: tag })).toBeVisible();
});
