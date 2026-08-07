// Layout check for the desktop shell (spec 13). Hand-run, NOT part of the
// Playwright suite and needing no stack: it serves the built frontend, stubs
// every `/api` call the screen makes, and then measures + screenshots the shell
// at a desk-sized viewport and a phone-sized one.
//
// It exists because jsdom performs no layout. The gate's `DesktopScreen.layout`
// test asserts the CSS *text*, which catches a deleted rule but not a rule that
// is present and wrong — regions that render fine in the DOM and lay out on top
// of each other in a browser. This is the only thing in the repo that can see
// that, so run it after any change to `DesktopScreen.css`:
//
//     cd frontend && pnpm build
//     cd ../tests && node desktop-shell-smoke.mjs
//
// Screenshots land in /tmp; the measurements print to stdout. Same hand-run
// stance as `capture-landing-shots.mjs`.
import { chromium } from '@playwright/test';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';

const DIST = new URL('../frontend/dist/', import.meta.url).pathname;
const TYPES = {
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.svg': 'image/svg+xml',
  '.woff2': 'font/woff2',
  '.json': 'application/json',
  '.webmanifest': 'application/manifest+json',
};

// A minimal SPA static server: anything that isn't a real file falls through to
// index.html so client-side routes (`/app`) resolve.
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  let file = normalize(join(DIST, url.pathname));
  if (!file.startsWith(DIST)) file = join(DIST, 'index.html');
  let body;
  try {
    body = await readFile(file);
  } catch {
    body = await readFile(join(DIST, 'index.html'));
    file = 'index.html';
  }
  res.writeHead(200, { 'content-type': TYPES[extname(file)] ?? 'application/octet-stream' });
  res.end(body);
});
await new Promise((resolve) => server.listen(4173, resolve));

const project = (id, name) => ({
  id,
  name,
  repo_url: `https://github.com/acme/${name}`,
  agent_provider: 'mock',
  amika_snapshot: '',
  worker_count: 3,
  merge_gate_mode: 'main',
  amika_secrets: [],
});

const ticket = (id, title, state) => ({
  id,
  title,
  body: 'body',
  state,
  priority: 1,
  approval_requested: false,
  keep_sandbox: false,
  created_at: '2026-08-04T09:00:00Z',
  updated_at: '2026-08-04T11:00:00Z',
  state_changed_at: '2026-08-04T11:00:00Z',
});

// One board per project, chosen so the rail shows all three states at once —
// that is the thing worth looking at (13 §5: only `needs-you` gets the accent).
const boards = {
  // kiln: a blocker and a proposal → needs-you.
  p1: {
    shaping: [ticket('t9', 'Rate-limit the webhook retry', 'shaping')],
    // Two ready tickets, in the pull order the server sends them in — the panel
    // must not re-sort them, and with every worker busy (worker_free: 0) this is
    // exactly the state the backlog section exists for: work that is queued and
    // would otherwise be invisible at a desk.
    ready: [
      ticket('t10', 'Backfill the search index', 'ready'),
      ticket('t11', 'Drop the legacy webhook route', 'ready'),
    ],
    blocked: [ticket('t1', 'auth refresh', 'blocked')],
    // Two tickets being worked, and one of them with a dead session behind it:
    // the working strip must name both and say plainly that the second is not
    // actually running (13 §8.2).
    working: [ticket('t2', 'poller', 'working'), ticket('t4', 'index migration', 'working')],
    done: [],
    worker_total: 3,
    worker_free: 0,
    agents: [
      { worker_id: 'w1', ticket_id: 't2', status: 'building' },
      { worker_id: 'w2', ticket_id: 't4', status: 'errored' },
    ],
    alerts: [],
  },
  // atlas: nothing wanted, but building → working.
  p2: {
    shaping: [],
    ready: [],
    blocked: [],
    working: [ticket('t3', 'index migration', 'working')],
    done: [],
    worker_total: 2,
    worker_free: 1,
    agents: [],
    alerts: [],
  },
  // ledger: genuinely quiet — no mark at all.
  p3: {
    shaping: [],
    ready: [],
    blocked: [],
    working: [],
    done: [],
    worker_total: 2,
    worker_free: 2,
    agents: [],
    alerts: [],
  },
};

const feed = {
  summary: {
    blocker_count: 1,
    update_count: 2,
    stream_count: 2,
    building: 1,
    idle: 1,
    last_word_at: '2026-08-04T11:52:00Z',
    last_seen_notification_id: 40,
  },
  has_more_history: true,
  cards: [
    {
      kind: 'blocker',
      id: 'c1',
      label: 'auth refresh',
      // Long on purpose: the blocker question must read as a paragraph in full,
      // not as a clipped line (13 §6, §10).
      body: 'The refresh endpoint returns 401 for tokens that were rotated while a session was open. I can either retry once with a freshly minted token and carry on, or surface the failure to the user and let them re-authenticate. Which do you want?',
      created_at: '2026-08-04T11:40:00Z',
      ticket_id: 't1',
    },
    {
      kind: 'proposal',
      id: 'c2',
      label: 'Rate-limit the webhook retry',
      body: 'Back off exponentially on 5xx from the delivery endpoint, capped at five attempts over roughly ten minutes, and drop the delivery with a logged reason after that.',
      created_at: '2026-08-04T11:20:00Z',
      ticket_id: 't9',
    },
    {
      kind: 'update',
      id: 'c3',
      label: 'poller',
      // Long on purpose too: an update must still CLAMP, so the column stays
      // scannable instead of becoming a wall.
      body: 'Landed the retry and backed it with a table test covering the rotated-token case. '.repeat(
        8,
      ),
      created_at: '2026-08-04T11:05:00Z',
      notification_id: 44,
    },
    {
      kind: 'done',
      id: 'c4',
      label: 'session cookie flags',
      body: '',
      created_at: '2026-08-04T09:30:00Z',
      notification_id: 41,
      ticket_id: 't7',
      github_url: 'https://github.com/acme/kiln/commit/abc1234',
      github_label: 'abc1234',
      work_summary: 'Set SameSite=Lax and Secure on the session cookie.',
    },
    {
      kind: 'update',
      id: 'c5',
      label: 'schema',
      body: 'Regenerated the Go and TS types after the feed card change.',
      created_at: '2026-08-03T18:00:00Z',
      notification_id: 38,
    },
  ],
};

// The same project with nothing to say — the resting state (13 §1, §10), which
// is what a window left open all day actually shows. `has_more_history` is true
// so the one "Show earlier" control is on screen with it: the empty state has to
// hold that control at its foot, and there is no way to see that without cards
// to be absent. Swapped in for the empty-feed pass near the end.
const emptyFeed = {
  summary: {
    blocker_count: 0,
    update_count: 0,
    stream_count: 0,
    building: 0,
    idle: 3,
    last_word_at: '2026-08-04T11:52:00Z',
    last_seen_notification_id: 44,
  },
  has_more_history: true,
  cards: [],
};

// Which of the two the `/feed` stub answers with. A `let` rather than a second
// route so the swap needs nothing but a reload.
let feedBody = feed;

// What the SSE stub replays. Empty for every pass but one: an empty body closes
// the connection at once, which settles the shell into `reconnecting` and gets
// the disconnected indication (13 §10) into the screenshots for free. The
// "Show earlier" toast pass below swaps in a `say` and a board `toast` so the
// activity band is actually up — the one state that used to cover the control.
let streamBody = '';
const bandStream = [
  `event: say\ndata: ${JSON.stringify({ message_id: 1, text: 'Rate-limiting the webhook retry now — backing off on 5xx, capped at five attempts.', at: '2026-08-04T11:55:00Z' })}\n\n`,
  `event: activity\ndata: ${JSON.stringify({ kind: 'toast', verb: 'started', ticket_title: 'auth refresh', ticket_id: 't1' })}\n\n`,
].join('');

const browser = await chromium.launch();
// Opens in dark because the OS says dark — not because the shell forces it
// (13 D6a). The light pass below flips exactly this and nothing else.
const page = await browser.newPage({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
});

// How long the board/feed reads are held before answering. Zero for every pass
// except the project-switch one at the end, which needs the wait to be long
// enough to look at (12 §4.1's loading indication).
let apiDelayMs = 0;

await page.route('**/api/**', async (route) => {
  const url = new URL(route.request().url());
  const json = async (body) => {
    if (apiDelayMs > 0) await new Promise((resolve) => setTimeout(resolve, apiDelayMs));
    return route.fulfill({ json: body });
  };
  if (url.pathname.endsWith('/me')) {
    return json({
      user: { github_login: 'amika', display_name: 'Amika', avatar_url: '' },
      projects: [project('p1', 'kiln'), project('p2', 'atlas'), project('p3', 'ledger')],
      settings: {
        anthropic_api_key: { set: true, tail: 'abcd' },
        amika_api_key: { set: true, tail: 'wxyz' },
        devin_api_key: { set: false, tail: '' },
        github_auth_token: { set: true, tail: '1234' },
        github_connection: {
          status: 'connected',
          login: 'amika',
          installation_id: 4242,
          configure_url: 'https://github.com/settings/installations/4242',
        },
        amika_claude_cred_id: '',
      },
    });
  }
  const board = Object.entries(boards).find(([id]) => url.pathname.includes(id));
  if (url.pathname.endsWith('/board')) return json(board ? board[1] : boards.p1);
  if (url.pathname.endsWith('/feed')) return json(feedBody);
  if (url.pathname.endsWith('/activity')) return json({ thinking: false });
  // Empty by default, which closes the SSE connection at once, so the shell
  // settles into its `reconnecting` state — convenient: the disconnected
  // indication (13 §10) shows up in the screenshot for free.
  if (url.pathname.includes('/stream')) {
    return route.fulfill({ status: 200, contentType: 'text/event-stream', body: streamBody });
  }
  return json({});
});

const errors = [];
page.on('pageerror', (error) => errors.push(String(error)));
page.on('console', (message) => {
  if (message.type() === 'error') errors.push(message.text());
});

await page.goto('http://localhost:4173/app');
try {
  await page.waitForSelector('[data-role="desktop-screen"]', { timeout: 10_000 });
} catch {
  console.log('DID NOT RENDER. body =', (await page.innerHTML('body')).slice(0, 2000));
  console.log('ERRORS', errors);
  await browser.close();
  server.close();
  process.exit(1);
}
await page.waitForTimeout(800);
await page.screenshot({ path: '/tmp/desktop-shell.png' });

// The measurements jsdom cannot give us.
const geometry = await page.evaluate(() => {
  const rect = (selector) => {
    const el = document.querySelector(selector);
    return el ? el.getBoundingClientRect() : null;
  };
  const rail = rect('[data-role="desktop-rail"]');
  const feedRegion = rect('[data-role="desktop-feed"]');
  const composer = rect('[data-role="desktop-composer"]');
  const panel = rect('[data-role="desktop-working-panel"]');
  const feedList = rect('[data-role="desktop-feed-list"]');
  const blockerBody = document.querySelector(
    '[data-role="desktop-feed-row"][data-kind="blocker"] [data-role="feed-card-body"]',
  );
  const updateBody = document.querySelector(
    '[data-role="desktop-feed-row"][data-kind="update"] [data-role="feed-card-body"]',
  );
  const dot = (state) => {
    const el = document.querySelector(
      `[data-role="rail-project-state"][data-state="${state}"] [data-role="rail-project-dot"]`,
    );
    return el ? getComputedStyle(el).backgroundColor : 'missing';
  };
  return {
    // Three columns, side by side: rail, in-progress panel, feed.
    railRight: rail?.right,
    panelLeft: panel?.left,
    panelRight: panel?.right,
    feedLeft: feedRegion?.left,
    // The input sits UNDER the feed and never over it.
    composerBelowFeed: composer && feedRegion ? composer.top >= feedRegion.bottom - 1 : null,
    // The in-progress panel is BESIDE the feed's scroll region, not above it and
    // not inside it — it cannot be scrolled away with the history (13 §8.2).
    panelBesideFeed:
      panel && feedRegion ? Math.round(panel.right) <= Math.round(feedRegion.left) : 'missing',
    // The rule on the panel's feed-facing edge: the separator the whole region
    // is defined by, and the one thing jsdom cannot see at all.
    panelDivider: panel
      ? getComputedStyle(document.querySelector('[data-role="desktop-working-panel"]')).borderRight
      : 'missing',
    // The feed still gets a real reading measure with two columns to its left.
    feedWidth: feedRegion ? Math.round(feedRegion.width) : 'missing',
    feedListWidth: feedList ? Math.round(feedList.width) : 'missing',
    workingTitles: Array.from(document.querySelectorAll('[data-role="desktop-working-title"]')).map(
      (node) => node.textContent,
    ),
    // The per-ticket marks are the phone's `status-dot`, so a building session
    // must resolve to the accent and a failed one to danger — the check that the
    // shared rules actually reach this subtree.
    workingMarks: Array.from(
      document.querySelectorAll('[data-role="desktop-working-ticket"] [data-role="status-dot"]'),
    ).map((node) => [node.dataset.status, getComputedStyle(node).backgroundColor]),
    // The panel's second section: what is queued behind the running work. Ready
    // first, in the server's pull order, then the shaping proposals.
    backlogTitles: Array.from(document.querySelectorAll('[data-role="desktop-backlog-title"]')).map(
      (node) => node.textContent,
    ),
    // It sits UNDER the working list in the same column — one panel, two
    // sections, set apart by air rather than by a second rule.
    backlogBelowWorking: (() => {
      const working = rect('[data-role="desktop-working"]');
      const backlog = rect('[data-role="desktop-backlog"]');
      if (!working || !backlog) {
        return 'missing';
      }
      return {
        below: Math.round(backlog.top) >= Math.round(working.bottom),
        gap: Math.round(backlog.top - working.bottom),
        sameColumn: Math.round(backlog.left) === Math.round(working.left),
        borders: getComputedStyle(document.querySelector('[data-role="desktop-backlog"]'))
          .borderTop,
      };
    })(),
    // A backlog row has no session behind it, so its mark takes the flat faint
    // default — visibly quieter than a working row's ember, and never the accent.
    backlogMarks: Array.from(
      document.querySelectorAll('[data-role="desktop-backlog-ticket"] [data-role="status-dot"]'),
    ).map((node) => [node.dataset.state, getComputedStyle(node).backgroundColor]),
    // A blocker reads in full; an update still clamps.
    blockerClamped: blockerBody
      ? blockerBody.scrollHeight > blockerBody.clientHeight + 1
      : 'missing',
    updateClamped: updateBody ? updateBody.scrollHeight > updateBody.clientHeight + 1 : 'missing',
    // Warm near-black — because the OS asked for dark, and via the ONE theme
    // mechanism: `data-theme` on <html> (13 D6a). `bodyTheme` must stay
    // undefined; the shell pins no theme of its own any more.
    shellBg: getComputedStyle(document.querySelector('[data-role="desktop-screen"]'))
      .backgroundColor,
    htmlTheme: document.documentElement.dataset.theme,
    bodyTheme: document.body.dataset.theme,
    bodyShell: document.body.dataset.shell,
    // The whole contrast budget: needs-you carries the accent, working doesn't.
    accentDot: dot('needs-you'),
    workingDot: dot('working'),
    railStates: Array.from(document.querySelectorAll('[data-role="rail-project"]')).map((row) => [
      row.textContent,
      row.dataset.state,
    ]),
    // The document itself never scrolls; the feed owns all of it.
    documentScrollable: document.documentElement.scrollHeight > window.innerHeight,
  };
});
console.log('DESKTOP GEOMETRY', JSON.stringify(geometry, null, 2));

// ── "Show earlier" is at the foot with CARDS in the feed, not just empty ──────
// The empty-feed pass near the end of this script has always covered the resting
// reading. This is the other one, and the one that was wrong: the control simply
// ended the card list, so it sat wherever the list happened to stop — mid-region
// on a short feed, and off the bottom entirely on a long one. Nothing in the
// gate can see that (jsdom does no layout, and a CSS string can state the rules
// but not that they resolve to a pinned control). Measured in both scroll
// positions, because "regardless of scroll state" is half the claim.
//
// Run at two window heights on purpose. The tall one leaves the cards short of
// the region, which exercises the auto-margin half; the short one forces the
// feed to overflow, which is the only way to exercise the sticky half — and the
// reading that was worst before, since a control at the end of a scrolling
// column is not merely low, it is off the screen.
const readShowEarlier = async () =>
  JSON.stringify(
    await page.evaluate(async () => {
      const rect = (selector) => {
        const el = document.querySelector(selector);
        return el ? el.getBoundingClientRect() : null;
      };
      const region = document.querySelector('[data-role="desktop-feed"]');
      const read = () => {
        const button = rect('[data-role="feed-show-earlier"]');
        const composer = rect('[data-role="desktop-composer-region"]');
        const regionBox = region.getBoundingClientRect();
        const rows = document.querySelectorAll('[data-role="desktop-feed-row"]');
        const last = rows.length ? rows[rows.length - 1].getBoundingClientRect() : null;
        return {
          // Inside the region's visible box — i.e. reachable without scrolling
          // for it, which is the whole complaint.
          onScreen: button.top >= regionBox.top - 1 && button.bottom <= regionBox.bottom + 1,
          // Sitting on the region's floor rather than under the last card.
          gapToRegionFloor: Math.round(regionBox.bottom - button.bottom),
          gapToComposer: Math.round(composer.top - button.bottom),
          aboveComposer: button.bottom <= composer.top + 1,
          // And never covering the last card at rest.
          clearOfLastCard: last ? Math.round(button.top - last.bottom) : 'no rows',
          // The two numbers the anchoring is built out of, read back rather than
          // assumed: the sticky offset has to match the region's own bottom pad
          // or the control creeps as the end of the feed comes into view.
          stickyBottom: getComputedStyle(document.querySelector('[data-role="feed-show-earlier"]'))
            .bottom,
          regionPadBottom: getComputedStyle(region).paddingBottom,
        };
      };
      region.scrollTop = 0;
      await new Promise((r) => requestAnimationFrame(r));
      const atTop = read();
      region.scrollTop = region.scrollHeight;
      await new Promise((r) => requestAnimationFrame(r));
      const atBottom = read();
      return {
        scrollable: region.scrollHeight > region.clientHeight + 1,
        atTop,
        atBottom,
        // The pinned position and the natural one are matched offsets, so the
        // control must not travel as the end of the feed comes into view.
        driftPx: Math.abs(atTop.gapToComposer - atBottom.gapToComposer),
      };
    }),
    null,
    2,
  );

console.log('SHOW EARLIER — CARDS, TALL WINDOW', await readShowEarlier());
await page.setViewportSize({ width: 1440, height: 520 });
await page.waitForTimeout(200);
await page.screenshot({ path: '/tmp/desktop-shell-show-earlier-scrolling.png' });
console.log('SHOW EARLIER — CARDS, SCROLLING FEED', await readShowEarlier());
{
  const box = await page.evaluate(async () => {
    document.querySelector('[data-role="desktop-feed"]').scrollTop = 0;
    await new Promise((r) => requestAnimationFrame(r));
    const b = document.querySelector('[data-role="feed-show-earlier"]').getBoundingClientRect();
    return { x: b.left, y: b.top - 10, width: b.width, height: b.height + 60 };
  });
  await page.screenshot({ path: '/tmp/probe-foot.png', clip: box });
  console.log(
    'PROBE',
    JSON.stringify(
      await page.evaluate(() => {
        const el = document.querySelector('[data-role="feed-show-earlier"]');
        const s = getComputedStyle(el);
        return {
          boxShadow: s.boxShadow,
          height: el.getBoundingClientRect().height,
          zIndex: s.zIndex,
          position: s.position,
        };
      }),
    ),
  );
}
await page.setViewportSize({ width: 1440, height: 900 });
await page.waitForTimeout(200);

// ── "Show earlier" with the activity band up ───────────────────────────────
// The band is an opaque out-of-flow overlay anchored at the composer region's
// TOP edge (`bottom: 100%`, PrimaryScreen.css), which is the same edge the
// pinned control rests on — so it floats up over exactly the strip the control
// occupies, and the two have to be read against each other.
//
// Two things are asserted here, and the second one reverses what this pass first
// checked. The desk reserves the band's live height as feed padding
// (`--feed-bottom-inset`, published by ActivityRow on the shell root) so the
// CARDS can still be scrolled clear of it — a zero in that var means the
// publisher never found this shell's root, which is the shape of the original
// bug. But the pinned control must NOT ride that reserve: a toast overlays it
// and leaves it exactly where it sits, rather than pushing it up the feed. So
// `standoff` — the control's distance from the composer region's top edge — has
// to read the same with a band up as without one, and the band has to be what
// paints over the control, its top edge included.
const readFoot = () =>
  page.evaluate(() => {
    const rect = (selector) => {
      const el = document.querySelector(selector);
      return el ? el.getBoundingClientRect() : null;
    };
    const roleAt = (x, y) => {
      const el = document.elementFromPoint(x, y);
      if (!el) return null;
      const named = el.closest('[data-role]');
      return named ? named.getAttribute('data-role') : el.tagName;
    };
    const region = document.querySelector('[data-role="desktop-feed"]');
    const button = rect('[data-role="feed-show-earlier"]');
    const band = rect('[data-role="activity-row"]');
    const stack = rect('[data-role="toast-stack"]');
    const composer = rect('[data-role="desktop-composer-region"]');
    const regionBox = region.getBoundingClientRect();
    const cx = button.left + button.width / 2;
    return {
      bandHeight: stack ? Math.round(stack.height) : 0,
      // The reserve, read off the root the publisher actually wrote to.
      inset: getComputedStyle(
        document.querySelector('[data-role="desktop-screen"]'),
      ).getPropertyValue('--feed-bottom-inset'),
      regionPadBottom: getComputedStyle(region).paddingBottom,
      // The whole complaint, in one number: how far the control stands off the
      // edge the band is anchored to. It must not change when a toast arrives.
      standoff: Math.round(composer.top - button.bottom),
      bandGap: band ? Math.round(band.top - button.bottom) : null,
      // What actually paints where the control stands — mid-box and top edge.
      over: roleAt(cx, button.top + button.height / 2),
      overTopEdge: roleAt(cx, button.top + 1),
      // ...and it is still inside the region, i.e. it never left the feed.
      onScreen: button.top >= regionBox.top - 1 && button.bottom <= regionBox.bottom + 1,
    };
  });

await page.reload();
await page.waitForSelector('[data-role="feed-show-earlier"]', { timeout: 10_000 });
await page.waitForTimeout(400);
const footAtRest = await readFoot();
console.log('SHOW EARLIER — NO BAND', JSON.stringify(footAtRest, null, 2));

streamBody = bandStream;
await page.reload();
await page.waitForSelector('[data-role="toast-stack"]', { timeout: 10_000 });
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-show-earlier-toast.png' });
const footWithBand = await readFoot();
console.log('SHOW EARLIER — TOAST BAND UP', JSON.stringify(footWithBand, null, 2));
console.log(
  footWithBand.standoff === footAtRest.standoff &&
    footWithBand.over !== 'feed-show-earlier' &&
    footWithBand.overTopEdge !== 'feed-show-earlier'
    ? `SHOW EARLIER — OVERLAY OK: holds ${footAtRest.standoff}px off the composer with a ${footWithBand.bandHeight}px band over it`
    : `SHOW EARLIER — OVERLAY FAILED: rest ${footAtRest.standoff}px vs band ${footWithBand.standoff}px, painting ${footWithBand.over}`,
);
streamBody = '';
await page.reload();
await page.waitForSelector('[data-role="desktop-screen"]', { timeout: 10_000 });
await page.waitForTimeout(400);

// ── The narrow desk (1024px, the shell threshold) ──────────────────────────
// The tightest window that still gets this layout, and the one the in-progress
// column made newly risky: two fixed columns of furniture now come off the
// feed's width before anything is read. So the check is that the feed still has
// a real measure here, that nothing has been squeezed into overlapping, and that
// the page has not started scrolling sideways.
await page.setViewportSize({ width: 1024, height: 900 });
await page.waitForTimeout(200);
await page.screenshot({ path: '/tmp/desktop-shell-1024.png' });
console.log(
  'AT 1024px',
  JSON.stringify(
    await page.evaluate(() => {
      const rect = (selector) => {
        const el = document.querySelector(selector);
        return el ? el.getBoundingClientRect() : null;
      };
      const panel = rect('[data-role="desktop-working-panel"]');
      const feed = rect('[data-role="desktop-feed"]');
      const list = rect('[data-role="desktop-feed-list"]');
      const title = rect('[data-role="desktop-working-title"]');
      return {
        stillDesktop: document.querySelector('[data-role="desktop-screen"]') !== null,
        panelRight: panel ? Math.round(panel.right) : 'missing',
        feedWidth: feed ? Math.round(feed.width) : 'missing',
        // The reading column, once the feed's own gutters are taken out.
        readingWidth: list ? Math.round(list.width) : 'missing',
        // A title still has room to say something before it ellipsises.
        titleWidth: title ? Math.round(title.width) : 'missing',
        horizontallyScrollable: document.documentElement.scrollWidth > window.innerWidth,
      };
    }),
    null,
    2,
  ),
);
await page.setViewportSize({ width: 1440, height: 900 });
await page.waitForTimeout(200);

// ── Both registers (13 D6a) ────────────────────────────────────────────────
// The desk follows the OS preference, so flipping the emulated preference must
// repaint the window live — no reload, no remount. And every rule has to hold in
// the light palette too, which jsdom cannot check at all: it resolves no custom
// properties, so "does paper actually reach the shell, and is anything invisible
// once it does" is only answerable in a browser.
//
// The hover reading is the specific trap worth measuring. `--surface-raised` is
// a lift above `--surface-card` in the dark palette but sits three hex points
// off it in the light one, so a send button that "firms on hover" in the dark
// vanishes into the composer in daylight. Both deltas below must be non-trivial.
async function readRegister() {
  // Send is disabled on an empty draft, and the rule is `:not(:disabled):hover`.
  await page.fill('[data-role="desktop-input"]', 'hello');
  await page.hover('[data-role="desktop-send"]');
  await page.waitForTimeout(250);
  const measured = await page.evaluate(() => {
    const bg = (selector) => {
      const el = document.querySelector(selector);
      return el ? getComputedStyle(el).backgroundColor : 'missing';
    };
    const rgb = (value) => (value.match(/\d+/g) ?? []).map(Number).slice(0, 3);
    // Perceived distance is good enough here: we are asking "can the eye see a
    // difference at all", not grading a contrast ratio.
    const delta = (a, b) => {
      const [x, y] = [rgb(a), rgb(b)];
      return Math.round(Math.sqrt(x.reduce((sum, v, i) => sum + (v - y[i]) ** 2, 0)));
    };
    // The composer's painted surface is the FIELD, not the row: the mic leads the
    // row as a raised object of its own and the row itself carries no box
    // (13 D5a), so the row's background is `rgba(0,0,0,0)` and measuring it would
    // read as "the composer is invisible" while the field is plainly there.
    const composer = bg('[data-role="desktop-field"]');
    const send = bg('[data-role="desktop-send"]');
    return {
      htmlTheme: document.documentElement.dataset.theme,
      bodyTheme: document.body.dataset.theme,
      page: bg('[data-role="desktop-screen"]'),
      rail: bg('[data-role="desktop-rail"]'),
      composer,
      sendHovered: send,
      // The three separations the shell's legibility rests on.
      railVsPage: delta(bg('[data-role="desktop-rail"]'), bg('[data-role="desktop-screen"]')),
      composerVsPage: delta(composer, bg('[data-role="desktop-screen"]')),
      sendHoverVsComposer: delta(send, composer),
      accentDot: (() => {
        const el = document.querySelector(
          '[data-role="rail-project-state"][data-state="needs-you"] [data-role="rail-project-dot"]',
        );
        return el ? getComputedStyle(el).backgroundColor : 'missing';
      })(),
    };
  });
  await page.fill('[data-role="desktop-input"]', '');
  return measured;
}

console.log('DARK REGISTER', JSON.stringify(await readRegister(), null, 2));

await page.emulateMedia({ colorScheme: 'light' });
await page.waitForTimeout(500);
await page.screenshot({ path: '/tmp/desktop-shell-light.png' });
console.log(
  'LIGHT REGISTER (after a live flip, no reload)',
  JSON.stringify(await readRegister(), null, 2),
);

// And back, to prove the subscription is not a one-shot — and to leave the rest
// of this script measuring the register it was written against.
await page.emulateMedia({ colorScheme: 'dark' });
await page.waitForTimeout(500);
console.log(
  'FLIPPED BACK → html theme =',
  await page.evaluate(() => document.documentElement.dataset.theme),
);

// The rail is the switcher (13 §5).
await page.click('[data-role="rail-project"][data-project-id="p2"]');
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-switched.png' });
console.log(
  'AFTER SWITCH current =',
  await page.getAttribute('[data-role="rail-project"][data-current="true"]', 'data-project-id'),
);

// Ticket detail opens BESIDE the feed: a right-anchored, full-height panel
// rather than a bottom pop-up (13 D7a). What matters is that it is flush to the
// right edge, spans the window's height, and leaves the rail and a usable slice
// of the feed visible to its left — the whole reason it moved off the bottom.
await page.click('[data-role="rail-project"][data-project-id="p1"]');
await page.waitForTimeout(600);
await page.click('[aria-label="Open ticket: Rate-limit the webhook retry"]');
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-detail.png' });
console.log(
  'DETAIL PANEL',
  JSON.stringify(
    await page.evaluate(() => {
      const el = document.querySelector('[data-role="ticket-detail"]');
      if (!el) return 'missing';
      const r = el.getBoundingClientRect();
      const feed = document.querySelector('[data-role="desktop-feed"]');
      return {
        direction: el.getAttribute('data-vaul-drawer-direction'),
        left: Math.round(r.left),
        width: Math.round(r.width),
        top: Math.round(r.top),
        height: Math.round(r.height),
        flushRight: Math.round(window.innerWidth - r.right) === 0,
        fullHeight: Math.round(r.height) === window.innerHeight,
        // How much of the feed's own column is still uncovered to its left.
        feedVisible: feed ? Math.round(r.left - feed.getBoundingClientRect().left) : 'no feed',
        window: `${window.innerWidth}x${window.innerHeight}`,
      };
    }),
  ),
);

// The sheet's sandbox menu: one gear on the status row opening a dropdown with
// every sandbox decision for the ticket. It is absolutely positioned inside a
// header that sits above a scrolling body, inside a panel with `overflow:
// hidden` — so the thing to look at is that it opens OVER the body and stays
// within the panel's own bounds, neither clipped nor painted under the text.
// This is a shaping proposal with no sandbox behind it yet, so the menu holds
// the save toggle alone; a working ticket's adds Re-create (and Move, when the
// board has a free slot).
await page.click('[data-role="detail-sandbox-trigger"]');
await page.waitForTimeout(500);
await page.screenshot({ path: '/tmp/desktop-shell-sandbox-menu.png' });
console.log(
  'SANDBOX MENU',
  JSON.stringify(
    await page.evaluate(() => {
      const panel = document.querySelector('[data-role="detail-sandbox-panel"]');
      const sheet = document.querySelector('[data-role="ticket-detail"]');
      const trigger = document.querySelector('[data-role="detail-sandbox-trigger"]');
      const heading = document.querySelector('[data-role="ticket-detail-heading"]');
      if (!panel || !sheet || !trigger || !heading) return 'missing';
      const p = panel.getBoundingClientRect();
      const s = sheet.getBoundingClientRect();
      return {
        items: [...panel.querySelectorAll('button')].map((b) => b.textContent?.trim()),
        insideSheet: p.left >= s.left && p.right <= s.right && p.bottom <= s.bottom,
        // The gear sits at the status row's END: its glyph should land on the
        // heading column's own right edge (0 = flush) — `margin-left: auto` on
        // the menu carries it there, and the trigger's negative margin cancels
        // its hit-area padding so what aligns is the glyph, not the button box.
        gearOffHeadingRight: Math.round(
          heading.getBoundingClientRect().right -
            (trigger.firstElementChild ?? trigger).getBoundingClientRect().right,
        ),
        // The topmost element at the panel's own centre is the panel itself, not
        // the body text it opened over.
        onTop: document
          .elementFromPoint((p.left + p.right) / 2, (p.top + p.bottom) / 2)
          ?.closest('[data-role="detail-sandbox-panel"]')
          ? 'panel'
          : 'something else',
      };
    }),
  ),
);
// Escape closes the menu first and the sheet only on the second press — the key
// belongs to the topmost layer, and the sheet is a Radix dialog listening for it
// too.
await page.keyboard.press('Escape');
await page.waitForTimeout(300);
console.log(
  'ESCAPE CLOSES MENU, NOT SHEET =',
  await page.evaluate(
    () =>
      document.querySelector('[data-role="detail-sandbox-panel"]')?.dataset.open === 'false' &&
      document.querySelector('[data-role="ticket-detail"]') !== null,
  ),
);
await page.keyboard.press('Escape');
await page.waitForTimeout(500);

// "/" jumps to the input from anywhere (13 §9).
await page.click('[data-role="desktop-feed"]');
await page.keyboard.press('/');
console.log(
  'SLASH FOCUSES COMPOSER =',
  await page.evaluate(() => document.activeElement?.getAttribute('data-role')),
);

// The bell's panel opens UP and to the RIGHT of the rail foot. Inheriting the
// phone's down-and-left anchoring put it off the bottom and off the left edge at
// once, on a shell that cannot be scrolled — so what matters is not which corner
// it claims but that all four of its edges land inside the window.
await page.click('[data-role="notify-settings-trigger"]');
await page.waitForTimeout(400);
await page.screenshot({ path: '/tmp/desktop-shell-bell.png' });
console.log(
  'BELL PANEL',
  JSON.stringify(
    await page.evaluate(() => {
      const panel = document.querySelector('[data-role="notify-settings-panel"]');
      const bell = document.querySelector('[data-role="notify-settings-trigger"]');
      if (!panel || !bell) return 'missing';
      const p = panel.getBoundingClientRect();
      const b = bell.getBoundingClientRect();
      return {
        onScreen:
          p.top >= 0 &&
          p.left >= 0 &&
          p.bottom <= window.innerHeight &&
          p.right <= window.innerWidth,
        opensUp: p.bottom <= b.top,
        opensRight: p.right > b.right,
        panel: [Math.round(p.left), Math.round(p.top), Math.round(p.right), Math.round(p.bottom)],
        window: [window.innerWidth, window.innerHeight],
      };
    }),
  ),
);
await page.keyboard.press('Escape');
await page.waitForTimeout(300);

// ── Switching projects: the cache and the wait (12 §4.1) ───────────────────
// Both halves of this are invisible to the gate. The DOM tests can prove the
// loading line RENDERS; only a browser can say whether it lands in the feed's
// reading column above the cards rather than on top of them — and only a real
// switch, with the reads held open, shows whether the previous project's cards
// are still there underneath it or whether the window went blank again.
apiDelayMs = 1500;
await page.click('[data-role="rail-project"][data-project-id="p2"]');
await page.waitForTimeout(300);
await page.screenshot({ path: '/tmp/desktop-shell-loading.png' });
console.log(
  'SWITCH → LOADING',
  JSON.stringify(
    await page.evaluate(() => {
      const rect = (selector) => {
        const el = document.querySelector(selector);
        return el ? el.getBoundingClientRect() : null;
      };
      const line = rect('[data-role="desktop-loading-line"]');
      const feed = rect('[data-role="desktop-feed"]');
      const list = rect('[data-role="desktop-feed-list"]');
      return {
        shown: line !== null,
        text: document.querySelector('[data-role="desktop-loading-line"]')?.textContent?.trim(),
        // Above the scroll region, holding its own height — a fact about the
        // whole project, so never a card in the history.
        aboveFeed: line && feed ? line.bottom <= feed.top + 1 : 'missing',
        // …and in the same column as the cards it is about.
        alignedWithFeed: line && list ? Math.round(line.left - list.left) : 'missing',
        // The resting line is a statement of fact, so it is withheld until the
        // fact is known.
        restLineShown: document.querySelector('[data-role="desktop-rest"]') !== null,
      };
    }),
    null,
    2,
  ),
);

await page.waitForTimeout(2000);
console.log(
  'SWITCH → SETTLED',
  JSON.stringify(
    await page.evaluate(() => ({
      loadingGone: document.querySelector('[data-role="desktop-loading"]') === null,
      cards: document.querySelectorAll('[data-role="desktop-feed-row"]').length,
    })),
  ),
);

// Back to a project already loaded this session: its cards must be on screen in
// the same frame as the click, with the refresh running visibly behind them.
await page.click('[data-role="rail-project"][data-project-id="p1"]');
await page.waitForTimeout(120);
await page.screenshot({ path: '/tmp/desktop-shell-cached.png' });
console.log(
  'SWITCH BACK → CACHED',
  JSON.stringify(
    await page.evaluate(() => ({
      cardsPaintedImmediately: document.querySelectorAll('[data-role="desktop-feed-row"]').length,
      stillRefreshing: document.querySelector('[data-role="desktop-loading"]') !== null,
      workingStrip: Array.from(
        document.querySelectorAll('[data-role="desktop-working-title"]'),
      ).map((node) => node.textContent),
    })),
  ),
);
apiDelayMs = 0;
await page.waitForTimeout(2000);

// The resting state, which is the one this shell is optimised for and the one
// the CSS-string test can least see: whether the mark and the lines actually sit
// on the region's axis, and whether "Show earlier" is carried to the foot rather
// than left hanging under the text with the window blank beneath it.
feedBody = emptyFeed;
await page.reload();
await page.waitForSelector('[data-role="desktop-rest"]', { timeout: 10_000 });
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-empty.png' });
console.log(
  'EMPTY FEED',
  JSON.stringify(
    await page.evaluate(() => {
      const rect = (selector) => {
        const el = document.querySelector(selector);
        return el ? el.getBoundingClientRect() : null;
      };
      const region = rect('[data-role="desktop-feed"]');
      const mark = rect('[data-role="desktop-rest-mark"]');
      const line = rect('[data-role="desktop-rest-line"]');
      const button = rect('[data-role="feed-show-earlier"]');
      const composer = rect('[data-role="desktop-composer-region"]');
      const lineEl = document.querySelector('[data-role="desktop-rest-line"]');
      if (!region || !mark || !line || !button || !composer) return 'missing';
      const centre = (r) => Math.round(r.left + r.width / 2);
      return {
        // A bell, and a large one — the phone shows the same mark at 64.
        markSize: [Math.round(mark.width), Math.round(mark.height)],
        // On the region's axis, both of them, and stacked (mark over words).
        markCentred: Math.abs(centre(mark) - centre(region)) <= 1,
        lineCentred: Math.abs(centre(line) - centre(region)) <= 1,
        markAboveLine: mark.bottom <= line.top,
        textCentred: lineEl ? getComputedStyle(lineEl).textAlign : 'missing',
        // The block sits in the middle of the free height rather than at the
        // top, with the control carried down clear of it.
        restBelowRegionTop: Math.round(mark.top - region.top),
        buttonBelowLine: button.top > line.bottom,
        // …and the control lands directly above the input: a small, deliberate
        // gap, not the 40px of reading air a list of cards ends with.
        gapToComposer: Math.round(composer.top - button.bottom),
        buttonAboveComposer: button.bottom <= composer.top + 1,
        // Still on the cards' centred measure, not stretched across the region.
        buttonCentred: Math.abs(centre(button) - centre(region)) <= 1,
        buttonWidth: Math.round(button.width),
      };
    }),
  ),
);
feedBody = feed;

// Narrow the window: the mobile shell must take back over — and the theme must
// not so much as flicker, because it never depended on the shell in the first
// place (13 D6a). Both shells read the same `data-theme` off <html>.
await page.setViewportSize({ width: 480, height: 900 });
await page.waitForTimeout(500);
console.log(
  'AT 480px → mobile shell =',
  (await page.locator('[data-role="primary-screen"]').count()) === 1,
  '| desktop shell gone =',
  (await page.locator('[data-role="desktop-screen"]').count()) === 0,
  '| body theme still unset =',
  (await page.evaluate(() => document.body.dataset.theme)) === undefined,
  '| html theme unchanged =',
  await page.evaluate(() => document.documentElement.dataset.theme),
);
await page.screenshot({ path: '/tmp/mobile-shell.png' });

// ...and the phone keeps the anchoring it was written for: the bell is up in the
// header there, so down-and-left is the direction with room. The desktop rule is
// scoped under the shell root precisely so this is untouched — both stylesheets
// are loaded at once here, this viewport change did not unload either.
await page.click('[data-role="notify-settings-trigger"]');
await page.waitForTimeout(400);
console.log(
  'BELL PANEL AT 480px',
  JSON.stringify(
    await page.evaluate(() => {
      const panel = document.querySelector('[data-role="notify-settings-panel"]');
      const bell = document.querySelector('[data-role="notify-settings-trigger"]');
      if (!panel || !bell) return 'missing';
      const p = panel.getBoundingClientRect();
      const b = bell.getBoundingClientRect();
      return {
        onScreen:
          p.top >= 0 &&
          p.left >= 0 &&
          p.bottom <= window.innerHeight &&
          p.right <= window.innerWidth,
        opensDown: p.top >= b.bottom,
        opensLeft: p.left < b.left,
      };
    }),
  ),
);
await page.screenshot({ path: '/tmp/mobile-shell-bell.png' });
await page.keyboard.press('Escape');
await page.waitForTimeout(300);

// The shared status mark, from the OTHER side. The desktop in-progress panel
// renders the phone's `[data-role='status-dot']` so "building" cannot come to
// mean one colour on a desk and another in a pocket — which only holds if the
// phone's own ticket dropdown still resolves to the same ink. jsdom cannot see a
// computed colour at all, so this pair of readings is the only check there is
// that the rules did not drift when the status moved from the row to the dot.
await page.click('[data-role="feed-status"]');
await page.waitForTimeout(400);
console.log(
  'MOBILE STATUS MARKS',
  JSON.stringify(
    await page.evaluate(() =>
      Array.from(document.querySelectorAll('[data-role="header-status-row"]')).map((row) => {
        const dot = row.querySelector('[data-role="status-dot"]');
        return dot ? [dot.dataset.status, getComputedStyle(dot).backgroundColor] : 'no dot';
      }),
    ),
  ),
);

// ── The phone's half of the same anchoring ───────────────────────────────────
// "Show earlier" is styled by the UNSCOPED rules in PrimaryScreen.css, which the
// desktop shell then layers over — so the two shells share the mechanism and can
// still resolve differently, and the phone is the shell that actually has the
// dock, the transcript and the toast band stacked below the feed. Reload so the
// populated feed is the one on screen (the swap above only changed what the stub
// would answer with next), then read the control against the DOCK rather than a
// composer: on a phone the thing it has to sit above is the microphone.
await page.reload();
await page.waitForSelector('[data-role="feed-show-earlier"]', { timeout: 10_000 });
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/mobile-shell-show-earlier.png' });
const readMobileShowEarlier = async () =>
  JSON.stringify(
    await page.evaluate(async () => {
      const feedRegion = document.querySelector('[data-role="feed"]');
      const read = () => {
        const button = document.querySelector('[data-role="feed-show-earlier"]');
        const dock = document.querySelector('[data-role="dock-region"]');
        const backlog = document.querySelector('[data-role="backlog"]');
        if (!button || !dock || !backlog) return 'missing';
        const b = button.getBoundingClientRect();
        const d = dock.getBoundingClientRect();
        const region = feedRegion.getBoundingClientRect();
        return {
          onScreen: b.top >= region.top - 1 && b.bottom <= region.bottom + 1,
          aboveDock: b.bottom <= d.top + 1,
          gapToDock: Math.round(d.top - b.bottom),
          // The control spans the cards' own measure, not the region's padding.
          sameWidthAsCards:
            Math.round(b.width) === Math.round(backlog.getBoundingClientRect().width),
          // Opaque, because cards pass underneath it now.
          fill: getComputedStyle(button).backgroundColor,
          stickyBottom: getComputedStyle(button).bottom,
          feedPadBottom: getComputedStyle(feedRegion).paddingBottom,
          overlayVar: getComputedStyle(
            document.querySelector('[data-role="primary-screen"]'),
          ).getPropertyValue('--dock-overlay-height'),
          bandVar: getComputedStyle(
            document.querySelector('[data-role="primary-screen"]'),
          ).getPropertyValue('--feed-bottom-inset'),
        };
      };
      feedRegion.scrollTop = 0;
      await new Promise((r) => requestAnimationFrame(r));
      const atTop = read();
      feedRegion.scrollTop = feedRegion.scrollHeight;
      await new Promise((r) => requestAnimationFrame(r));
      const atBottom = read();
      return {
        scrollable: feedRegion.scrollHeight > feedRegion.clientHeight + 1,
        atTop,
        atBottom,
        driftPx: Math.abs(atTop.gapToDock - atBottom.gapToDock),
      };
    }),
    null,
    2,
  );

console.log('MOBILE SHOW EARLIER — TALL PHONE', await readMobileShowEarlier());
// And on a short one, where the backlog actually overflows — the reading the
// control used to fail outright, since the end of a scrolling column is off the
// screen rather than merely low on it.
await page.setViewportSize({ width: 480, height: 520 });
await page.waitForTimeout(400);
await page.screenshot({ path: '/tmp/mobile-shell-show-earlier-scrolling.png' });
console.log('MOBILE SHOW EARLIER — SCROLLING FEED', await readMobileShowEarlier());

console.log('PAGE ERRORS', errors.length === 0 ? 'none' : errors);

await browser.close();
server.close();
