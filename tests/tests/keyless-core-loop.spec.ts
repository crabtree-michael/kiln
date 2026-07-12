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
// outbox, the pull, the agent state machine — runs for real.
//
// Driven at the message seam (POST /api/message) and asserted on the server
// snapshots (GET /api/board, GET /api/feed): the board+chat web client that once
// drove this loop through its UI lived at /debug and has been removed, so the
// loop is now exercised directly at the same HTTP seam that client used.
//
// It stops at Developing, not Done: accept_to_done verifies a real origin/main
// commit via the repo shell, which a keyless stack disables — so "→ Done" stays
// the key-gated lane's job. Reaching Developing + a completion reaction exercises
// the whole loop keyless.
test('@keyless core loop: a build request creates a ticket, runs a worker, and Kiln reacts', async ({
  request,
}) => {
  test.setTimeout(60_000);
  await mintSession(request, { base: apiBase });

  // Step 1: the user asks for work at the message seam. "login form" matches the
  // core-loop rule in tests/fixtures/brain/keyless.json.
  const send = await request.post(`${apiBase}/api/message`, {
    data: { text: 'Create a ticket to build a login form and wire it to the auth endpoint.' },
  });
  expect(send.ok(), `POST /api/message -> ${send.status()}`).toBeTruthy();

  // Step 2: the scripted brain creates the ticket → it lands in Backlog
  // (shaping/ready).
  await expect
    .poll(
      async () => {
        const res = await request.get(`${apiBase}/api/board`);
        if (!res.ok()) return false;
        const board = (await res.json()) as {
          shaping: { title: string }[];
          ready: { title: string }[];
        };
        return [...board.shaping, ...board.ready].some((t) => t.title.includes('Build a login form'));
      },
      { message: 'the scripted brain never created the ticket in Backlog', timeout: 30_000 },
    )
    .toBe(true);

  // Step 3: mark_ready + the deterministic pull bind a mock worker → the ticket
  // moves into the Developing column (state=working).
  await expect
    .poll(
      async () => {
        const res = await request.get(`${apiBase}/api/board`);
        if (!res.ok()) return false;
        const board = (await res.json()) as { working: { title: string }[] };
        return board.working.some((t) => t.title.includes('Build a login form'));
      },
      {
        message: 'ticket never reached Developing — pull/agent-runtime did not bind a worker',
        timeout: 30_000,
      },
    )
    .toBe(true);

  // Step 4: the mock turn completes → agent.turn_completed re-enters the brain,
  // which posts a feed update.
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
