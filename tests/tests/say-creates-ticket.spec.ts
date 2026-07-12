import { expect, test } from '@playwright/test';
import { mintSession } from '../session';

// E2E: the first two steps of the core loop (docs/specs/01 §2).
//
//   1. The user says "Build a login form ...".
//   2. The orchestrator creates a ticket in Backlog.
//
// Driven at the message seam: POST /api/message -> durable queue -> brain ->
// real LLM -> board write, asserted on GET /api/board. We stop before any Amika
// pull, so this needs no sandbox (nothing to clean up) and never leaves Backlog.
//
// The board+chat web client that once drove this through its UI lived at /debug
// and has been removed; the loop is now exercised directly at the same HTTP seam
// that client used.
//
// Backlog is Shaping + Ready (03 §2.1): a freshly created ticket lands in state
// `shaping`, and either state counts as "in Backlog" here.
test('saying a build request creates a ticket in Backlog', async ({ page }) => {
  // Mint the dev session on the page's request context (its own cookie jar), so
  // the /api/* calls below are authorized (session gate, spec 11 phase 2).
  await mintSession(page.request);

  // This suite runs against a persistent stack, so earlier runs may have left
  // tickets on the board — and a repeated identical request is (correctly) not
  // duplicated by the orchestrator. So we don't count tickets or assume an empty
  // Backlog: we tag THIS request with a unique marker and assert that our own
  // ticket appears. That verifies this send created a ticket regardless of what
  // was already there.
  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Step 1: the user says what they want, at the real message endpoint.
  const send = await page.request.post('/api/message', {
    data: {
      text:
        `Create a ticket to build a login form and wire it to the auth endpoint. ` +
        `Include the exact tag ${tag} in the ticket title.`,
    },
  });
  expect(send.ok(), `POST /api/message -> ${send.status()}`).toBeTruthy();

  // Step 2: the orchestrator creates the tagged ticket in Backlog; it lands there
  // over the durable queue + a real-LLM turn (slow, so poll with room). Shaping or
  // Ready both count as Backlog.
  await expect
    .poll(
      async () => {
        const res = await page.request.get('/api/board');
        if (!res.ok()) return false;
        const board = (await res.json()) as {
          shaping: { title: string }[];
          ready: { title: string }[];
        };
        return [...board.shaping, ...board.ready].some((t) => t.title.includes(tag));
      },
      { message: 'the tagged ticket never appeared in Backlog', timeout: 90_000 },
    )
    .toBe(true);
});
