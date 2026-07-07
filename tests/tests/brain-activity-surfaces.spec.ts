import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mintSession } from '../session';

// E2E (spec 08 §4): the ephemeral ACTIVITY LAYER above the dock surfaces the brain's
// live work — the `thinking` spinner while a pass runs, the brain's `say` reply in the
// persistent pill, and an orchestrator ACTION toast for a side-effect board transition.
//
// All real except the agent provider (AGENT_MODE=mock). The activity layer is SSE-only and
// never stored (08 D3): the runtime brackets each brain pass with `activity {thinking}`,
// board ops append `activity.toast` beside board.updated, and `say` rides its existing SSE.
//
// The two behaviours are SEPARATE test() cases on purpose: 08 §4's pill-contention rule
// makes a persistent `say` hold back queued toasts, so a single page cannot reliably show
// both a `say` pill and a `toast` pill at once. A fresh page per case avoids that.
//
// Determinism: the brain is a real LLM. The thinking+say case steers it with a plain
// question and polls; the toast case is driven MECHANICALLY (a dev-seeded ready ticket →
// mark_ready/pull emit the toast, no brain say to contend with) so it is fully
// deterministic. Cheap model, no sandbox, no teardown.

const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

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

// Dev-only deterministic seed (KILN_DEV_ENDPOINTS=1), mirroring POST /api/dev/tickets. A
// ready-state seed drives the real mark_ready/pull path, so it emits the action toast(s)
// without any brain turn.
async function seedTicket(
  request: APIRequestContext,
  spec: { title: string; body?: string; state?: string; blocked_reason?: string; approval_requested?: boolean },
): Promise<void> {
  const res = await request.post(`${apiBase}/api/dev/tickets`, { data: { body: 'seeded by e2e', ...spec } });
  expect(res.ok(), `POST /api/dev/tickets -> ${res.status()} (needs KILN_DEV_ENDPOINTS=1)`).toBeTruthy();
}

test('the brain thinking spinner and its say reply appear on the primary screen', async ({
  page,
  request,
}) => {
  test.setTimeout(120_000); // one real-LLM turn.

  await openConnectedFeed(page);

  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Act: a plain question the brain answers with a `say` (no board mutation needed). Ask it
  // to echo the tag so we can assert on OUR reply on the persistent-say pill.
  const post = await request.post(`${apiBase}/api/message`, {
    data: { text: `In one short sentence, reply and include the exact text ${tag}. Do not create or change any tickets.` },
  });
  expect(post.status(), `POST /api/message -> ${post.status()}`).toBe(202);

  // Signal 1 (thinking): while the pass runs, the activity row shows the spinner. It is
  // visible for the whole pass (a real-LLM turn is seconds), so the poll catches it. The
  // spinner coexists with any pill on the one activity layer (08 §4), so it stays up
  // whether or not the say has landed yet.
  await expect(
    page.locator('[data-role="thinking-indicator"]'),
    'the "Kiln is thinking" spinner never appeared during the brain pass',
  ).toBeVisible();

  // Signal 2 (say): the brain's reply lands in the persistent say pill carrying the tag.
  await expect(
    page.locator('[data-role="say-pill"]').filter({ hasText: tag }),
    `the brain's say reply carrying ${tag} never appeared in the pill`,
  ).toBeVisible();
});

test('an orchestrator action surfaces as a toast on the primary screen', async ({ page, request }) => {
  test.setTimeout(60_000);

  await openConnectedFeed(page);

  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Act MECHANICALLY: seed a ready ticket. The dev seed drives the real mark_ready (→
  // `queued` toast) and the deterministic pull (→ `started` toast if a worker is free),
  // both appended as activity.toast outbox rows → `activity` SSE. No brain turn, so the
  // pill is empty and the toast renders immediately (no say contention).
  await seedTicket(request, { title: `Toast probe ${tag}`, state: 'ready' });

  // The auto-dismissing toast pill appears carrying one of the §4 verbs. Poll within its
  // on-screen window (it slides away after ~4s; Playwright polls fast enough to catch it).
  // A single mechanical transition can surface MORE than one pill at once — mark_ready
  // emits `queued` and, when a worker is free, the pull immediately emits `started` — so
  // scope to the first pill (any valid orchestrator toast satisfies "an action surfaced")
  // rather than tripping strict mode on a legitimately multi-toast state.
  const toast = page.locator('[data-role="toast-pill"]').first();
  await expect(toast, 'no orchestrator action toast appeared after the mechanical transition').toBeVisible();
  await expect(toast).toHaveAttribute('data-verb', /queued|started|nudged|finished/);
});
