import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

// E2E (spec 08 §3, §7): a brain-authored UPDATE reaches the primary-screen feed, and an
// update that has been SEEN drains out of the feed (the "inbox that drains", 08 D2).
//
// This exercises the whole authored-notification pipeline end to end, all real except the
// agent provider (AGENT_MODE=mock — no Amika sandbox is touched):
//   POST /api/message → real brain turn → post_update tool → notifications row +
//   feed.updated outbox (08 §7) → `feed` SSE event → feed store → the primary screen `/`
//   renders an `update` card. Then, because the card renders on a foregrounded, visible
//   screen, the client acks POST /api/feed/seen; the server stamps seen_at and the update
//   drops out of the next feed snapshot (08 §3 "seen means gone"). Blockers/proposals are
//   NOT drained by being seen — only updates are (covered in the sibling specs).
//
// Determinism: the brain is a real LLM (there is no mock brain), so we steer it with an
// explicit FYI-not-a-task prompt and poll for the outcome — never a fixed sleep. Run on
// the cheap model (KILN_BRAIN_MODEL=claude-haiku-4-5-...). No sandbox, no teardown.

const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

// The slice of GET /api/feed (wire.FeedSnapshot) this test reads.
type FeedCard = {
  kind: string;
  label: string;
  body: string;
  notification_id: number | null;
};
type FeedSnapshot = { cards: FeedCard[] };

async function getFeed(request: APIRequestContext): Promise<FeedSnapshot> {
  const res = await request.get(`${apiBase}/api/feed`);
  expect(res.ok(), `GET /api/feed -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as FeedSnapshot;
}

// Gate: the feed region must be rendered AND the live SSE stream connected before an
// SSE-delivered card can reach us (mirrors the board's data-connection-state gate, 07 §8).
async function openConnectedFeed(page: Page): Promise<void> {
  await page.goto('/');
  const feed = page.getByRole('region', { name: 'Feed' });
  await expect(feed).toBeVisible();
  await expect(feed).toHaveAttribute('data-connection-state', 'connected');
}

test('a brain-authored update appears in the feed, then drains once seen', async ({
  page,
  request,
}) => {
  test.setTimeout(120_000); // one real-LLM turn, then the seen-drain poll window.

  await openConnectedFeed(page);

  // Tag THIS request so we assert on our own update regardless of what is already on the
  // persistent feed (08 tests run against a persistent stack; isolate by tag).
  const tag = `E2E-${Date.now().toString(36).toUpperCase()}`;

  // Act: tell the brain this is a status FYI, not a task, so it uses post_update and does
  // NOT create a ticket or ask a question (which would surface a different card kind).
  const post = await request.post(`${apiBase}/api/message`, {
    data: {
      text:
        `This is a status FYI, not a task. Do not create a ticket and do not ask me ` +
        `anything — just post a short update to my feed that contains the exact text ` +
        `${tag}.`,
    },
  });
  expect(post.status(), `POST /api/message -> ${post.status()}`).toBe(202);

  // Signal 1 (feed render): an `update` card carrying the tag appears on the primary
  // screen, delivered over the `feed` SSE event. A real-LLM turn is slow — poll within the
  // config's generous expect window.
  const updateCard = page
    .locator('[data-role="feed-card"][data-kind="update"]')
    .filter({ hasText: tag });
  await expect(updateCard, `no update card carrying ${tag} reached the feed`).toBeVisible();

  // Signal 1b (body renders): the tag must render in the card's BODY element, not
  // merely somewhere in the card. This pins the post_update body → feed-card-body
  // path so a regression that drops the body text (empty/hidden `<p>`) is caught
  // even though the card shell still appears.
  const updateBody = updateCard.locator('[data-role="feed-card-body"]');
  await expect(updateBody, `update card body did not render the ${tag} text`).toBeVisible();
  await expect(updateBody).toContainText(tag);

  // Signal 2 (drain): the card rendered on a visible screen, so the client acked
  // /api/feed/seen; the server must now exclude it from the assembled snapshot (08 D2).
  // We assert on the SERVER snapshot (GET /api/feed) — that directly verifies seen high-
  // water stamping + assembly filtering, independent of the client's session-hold (which
  // deliberately keeps the seen card on THIS page for the rest of the session).
  await expect
    .poll(
      async () => (await getFeed(request)).cards.some((c) => c.kind === 'update' && c.body.includes(tag)),
      {
        message: `update ${tag} never drained from GET /api/feed after being seen`,
        timeout: 30_000,
      },
    )
    .toBe(false);
});
