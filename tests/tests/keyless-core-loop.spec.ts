import { expect, test } from '@playwright/test';
import { mintSession } from '../session';
import { apiBase } from '../keyless';

// KEYLESS E2E — the flagship core loop (spec 01 §2), run with NO provider keys
// (design docs/keyless-e2e-tests-design.md §Test 1). Against the mocked stack
// (docker-compose.keyless.yml): the scripted brain turns a plain-language request
// into create_ticket + mark_ready, the deterministic pull binds an AGENT_MODE=mock
// worker, the mock turn completes, and the completion feeds back into the brain,
// which reacts with a feed update + say. Every layer between the mocked boundaries
// — routing, the durable queue, the board state machine, the transactional
// outbox, the pull, the agent state machine, SSE — runs for real.
//
// It stops at Developing, not Done: accept_to_done verifies a real origin/main
// commit via the repo shell, which a keyless stack disables — so "→ Done" stays
// the key-gated lane's job. Reaching Developing + a completion reaction exercises
// the whole loop keyless.
test('@keyless core loop: a build request creates a ticket, runs a worker, and Kiln reacts', async ({
  page,
  request,
}) => {
  test.setTimeout(60_000);
  await mintSession(page.request);
  await mintSession(request, { base: apiBase });

  await page.goto('/debug');
  const board = page.getByRole('region', { name: 'Board' });
  await expect(board).toBeVisible();
  await expect(board).toHaveAttribute('data-connection-state', 'connected');

  // Step 1: the user asks for work through the real chat. "login form" matches the
  // core-loop rule in tests/fixtures/brain/keyless.json.
  await page
    .getByLabel('Message')
    .fill('Create a ticket to build a login form and wire it to the auth endpoint.');
  await page.getByRole('button', { name: 'Send' }).click();

  // Step 2: the scripted brain creates the ticket → it lands in Backlog.
  const backlog = page.getByRole('region', { name: 'Backlog' });
  await expect(
    backlog.locator('[data-role="ticket-card"]', { hasText: 'Build a login form' }),
  ).toBeVisible();

  // Step 3: mark_ready + the deterministic pull bind a mock worker → the ticket
  // moves into the Developing column (state=working).
  const developing = page.getByRole('region', { name: 'Developing' });
  await expect(
    developing.locator('[data-role="ticket-card"]', { hasText: 'Build a login form' }),
    'ticket never reached Developing — pull/agent-runtime did not bind a worker',
  ).toBeVisible();

  // Step 4: the mock turn completes → agent.turn_completed re-enters the brain,
  // which posts a feed update. Assert it on the server snapshot (robust to where
  // the client renders it).
  await expect
    .poll(
      async () => {
        const res = await request.get(`${apiBase}/api/feed`);
        if (!res.ok()) return false;
        const feed = (await res.json()) as { cards: { body: string }[] };
        return feed.cards.some((c) => c.body.includes('first turn complete'));
      },
      {
        message: 'the brain never reacted to agent.turn_completed (no completion update in the feed)',
        timeout: 30_000,
      },
    )
    .toBe(true);
});
