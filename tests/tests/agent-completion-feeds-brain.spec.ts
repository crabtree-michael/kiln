import { expect, test, type APIRequestContext } from '@playwright/test';
import { mintSession } from '../session';

// E2E: an agent's response feeds back through the event queue into the brain
// (docs/specs 04 §2, 05 §2.2, 06). This is the RETURN leg of the loop — a separate
// mechanism from ready-kicks-off-amika-run (the outbound leg):
//
//   seed a Developing ticket (POST /api/dev/tickets — no brain) → the real pull
//   binds a free worker → the agent runs and replies → CheckTurn emits
//   agent.turn_completed → the runtime dequeues it → brain.HandleEvent → the brain
//   moves the ticket to done (or blocked).
//
// The ticket reaching done|blocked is the proof the agent's response was dequeued
// into the brain and acted on. The ticket is seeded deterministically (NOT
// brain-created, via the dev-only endpoint) so setup can't flake on the LLM; the
// one LLM-decided step under test is the brain's reaction to the completed turn.
//
// This uses the real agent, so it reaches Developing and bills money — run against
// the real stack on the cheap model (KILN_BRAIN_MODEL=claude-haiku-4-5-...); the
// global teardown destroys the kiln-worker-* sandboxes. Needs KILN_DEV_ENDPOINTS=1
// on the stack (docker-compose defaults it on) and a free worker.

// API-driven: hit the backend directly. Override with KILN_E2E_API_URL.
const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

type Ticket = { title: string; state: string };
type Board = {
  shaping: Ticket[];
  ready: Ticket[];
  working: Ticket[];
  blocked: Ticket[];
  done: Ticket[];
  worker_free: number;
  worker_total: number;
};

async function getBoard(request: APIRequestContext): Promise<Board> {
  const res = await request.get(`${apiBase}/api/board`);
  expect(res.ok(), `GET /api/board -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as Board;
}

test('an agent response feeds back through the queue into the brain', async ({ request }) => {
  test.setTimeout(200_000); // real agent turn + real brain turn, polled

  // All /api/* calls need a session cookie; mint one in this request context.
  await mintSession(request, { base: apiBase });

  // Precondition: a free worker, or the pull can't bind — surface that as a clear
  // stack signal rather than a mystery timeout below. Don't loosen; reset the stack.
  const initial = await getBoard(request);
  expect(
    initial.worker_free,
    `no free worker (${initial.worker_free}/${initial.worker_total}) — a prior run left the ` +
      `pool busy; reset the stack before running`,
  ).toBeGreaterThan(0);

  const tag = `E2E-FEEDBACK-${Date.now().toString(36).toUpperCase()}`;

  // Seed a Developing ticket deterministically (no brain). The body is the work
  // order the agent receives — instruct a trivial, fast completion with no real
  // changes, so the return leg fires quickly.
  const seed = await request.post(`${apiBase}/api/dev/tickets`, {
    data: {
      title: `${tag}: completion feedback`,
      // state=ready drives the real mark_ready → pull → Developing path (the dev
      // seed default is now shaping, which is not pull-eligible).
      state: 'ready',
      body:
        `This is an automated test. Do NOT make any file or code changes. Immediately ` +
        `reply with exactly this sentence and nothing else: ` +
        `"I have completed the work for this ticket."`,
    },
  });
  expect(
    seed.status(),
    `POST /api/dev/tickets -> ${seed.status()} (is KILN_DEV_ENDPOINTS=1 set on the stack?)`,
  ).toBe(201);

  // Assert the return leg closed: the brain received agent.turn_completed and moved
  // the ticket out of Developing — to done (accepted) or blocked. Both prove the
  // response was dequeued into the brain and acted on. A real agent + brain turn is
  // slow, so poll generously.
  await expect
    .poll(
      async () => {
        const b = await getBoard(request);
        const mine = (t: Ticket) => t.title.includes(tag);
        if (b.done.some(mine)) return 'done';
        if (b.blocked.some(mine)) return 'blocked';
        return 'pending';
      },
      {
        message:
          `ticket ${tag} never reached done/blocked — the agent's response did not feed back ` +
          `through the queue into the brain`,
        timeout: 180_000,
      },
    )
    .not.toBe('pending');
});
