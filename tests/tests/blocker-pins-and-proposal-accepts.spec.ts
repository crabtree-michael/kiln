import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mintSession } from '../session';

// E2E (spec 08 §2, §3, §5): the two DERIVED feed cards — the blocker that pins to the top,
// and the proposal you accept. Both derive from board state (08 D1), so they cannot go
// stale, and both are asserted deterministically by seeding the board state they derive
// from (dev endpoints, KILN_DEV_ENDPOINTS=1) rather than coaxing the real LLM into
// mark_blocked / request_approval on demand.
//
// Case 1 — blocker pins on top: a Blocked ticket derives a `blocker` card that sorts ABOVE
// every update ("unresolved blockers pinned on top", 08 §2). We seed Blocked and assert the
// first feed card is the blocker.
// Case 2 — accept a proposal: a Shaping ticket with approval_requested derives a `proposal`
// card with an Accept affordance (08 §5). Per THIS project's decision (overriding 08 D6),
// tap-Accept routes THROUGH THE BRAIN — POST /api/tickets/{id}/accept enqueues a
// human.message the brain answers with mark_ready — so accepting is a real-LLM leg. We
// click Accept and assert the proposal drains from the feed once the ticket leaves Shaping.
//
// AGENT_MODE=mock, cheap model, no sandbox, no teardown.

const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

type FeedCard = { kind: string; label: string; body: string; ticket_id: string | null };
type FeedSnapshot = { cards: FeedCard[] };

async function getFeed(request: APIRequestContext): Promise<FeedSnapshot> {
  const res = await request.get(`${apiBase}/api/feed`);
  expect(res.ok(), `GET /api/feed -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as FeedSnapshot;
}

// A blocked seed binds a free worker (board invariant I3/I4), so it needs pool
// headroom. On a persistent stack a prior run may have consumed every slot —
// surface that as a clear "reset the stack" signal, not a mystery 409 on the
// seed (the existing suite asserts the same precondition, ready-kicks-off §70).
async function requireFreeWorker(request: APIRequestContext): Promise<void> {
  const res = await request.get(`${apiBase}/api/board`);
  expect(res.ok(), `GET /api/board -> ${res.status()}`).toBeTruthy();
  const board = (await res.json()) as { worker_free: number; worker_total: number };
  expect(
    board.worker_free,
    `no free worker (${board.worker_free}/${board.worker_total}) to bind a blocked seed — ` +
      `a prior run left the pool busy; reset the stack (make down && make up) before running`,
  ).toBeGreaterThan(0);
}

async function seedTicket(
  request: APIRequestContext,
  spec: { title: string; body?: string; state?: string; blocked_reason?: string; approval_requested?: boolean },
): Promise<void> {
  const res = await request.post(`${apiBase}/api/dev/tickets`, { data: { body: 'seeded by e2e', ...spec } });
  expect(res.ok(), `POST /api/dev/tickets -> ${res.status()} (needs KILN_DEV_ENDPOINTS=1)`).toBeTruthy();
}

// Both tests drive BOTH transports (request fixture + browser context), and the
// two have separate cookie jars — mint a dev session in each. The browser mint
// runs before openConnectedFeed's page.goto, so the app's session gate passes.
test.beforeEach(async ({ page, request }) => {
  await mintSession(request, { base: apiBase });
  await mintSession(page.request);
});

async function openConnectedFeed(page: Page): Promise<void> {
  await page.goto('/');
  const feed = page.getByRole('region', { name: 'Feed' });
  await expect(feed).toBeVisible();
  await expect(feed).toHaveAttribute('data-connection-state', 'connected');
}

test('a blocked ticket pins a blocker card to the top of the feed', async ({ page, request }) => {
  test.setTimeout(60_000);

  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;
  const reason = `Which auth strategy should the client trust? ${tag}`;

  // Seed a Blocked ticket, then open the feed so the blocker arrives as a derived card.
  await requireFreeWorker(request);
  await seedTicket(request, { title: `Auth ${tag}`, state: 'blocked', blocked_reason: reason });
  await openConnectedFeed(page);

  // The blocker card carrying our reason is present and shows the blocked reason in full
  // (08 §2 "question in full").
  const blocker = page.locator('[data-role="feed-card"][data-kind="blocker"]').filter({ hasText: tag });
  await expect(blocker, `no blocker card carrying ${tag} appeared`).toBeVisible();
  await expect(blocker.locator('[data-role="feed-card-body"]')).toContainText(reason);

  // Pinned on top: the FIRST feed card on the screen is a blocker (blockers precede
  // proposals and updates, 08 §2). Assert on the leading card's kind.
  await expect(
    page.locator('[data-role="feed-card"]').first(),
    'a blocker is on the board but is not the top feed card',
  ).toHaveAttribute('data-kind', 'blocker');
});

test('a proposal can be accepted from the feed and drains once the brain queues it', async ({
  page,
  request,
}) => {
  test.setTimeout(120_000); // includes the real-LLM accept leg.

  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Seed a Shaping ticket the brain has flagged for approval → a proposal card.
  await seedTicket(request, {
    title: `Login redesign ${tag}`,
    body: `Trust the session cookie; use the JWT only for the mobile clients. ${tag}`,
    state: 'shaping',
    approval_requested: true,
  });
  await openConnectedFeed(page);

  const proposal = page.locator('[data-role="feed-card"][data-kind="proposal"]').filter({ hasText: tag });
  await expect(proposal, `no proposal card carrying ${tag} appeared`).toBeVisible();

  // Accept it. The button routes through the brain (POST /api/tickets/{id}/accept →
  // human.message → mark_ready), converging on the same board op as a voice accept (08 §5).
  await proposal.getByRole('button', { name: 'Accept' }).click();

  // Once the brain marks it Ready it is no longer a Shaping+approval ticket, so the derived
  // proposal card drains from the feed (08 §3 — derived cards leave when the fact leaves).
  // Assert on the server snapshot; the accept leg is a real-LLM turn, so poll generously.
  await expect
    .poll(async () => (await getFeed(request)).cards.some((c) => c.kind === 'proposal' && c.body.includes(tag)), {
      message: `proposal ${tag} never drained from the feed after Accept (brain did not mark_ready)`,
      timeout: 90_000,
    })
    .toBe(false);
});
