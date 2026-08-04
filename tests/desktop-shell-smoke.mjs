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
    ready: [],
    blocked: [ticket('t1', 'auth refresh', 'blocked')],
    working: [ticket('t2', 'poller', 'working')],
    done: [],
    worker_total: 3,
    worker_free: 1,
    agents: [{ worker_id: 'w1', ticket_id: 't2', status: 'building' }],
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

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });

await page.route('**/api/**', async (route) => {
  const url = new URL(route.request().url());
  const json = (body) => route.fulfill({ json: body });
  if (url.pathname.endsWith('/me')) {
    return json({
      user: { github_login: 'amika', display_name: 'Amika', avatar_url: '' },
      projects: [project('p1', 'kiln'), project('p2', 'atlas'), project('p3', 'ledger')],
      settings: {
        anthropic_api_key: { set: true, tail: 'abcd' },
        amika_api_key: { set: true, tail: 'wxyz' },
        devin_api_key: { set: false, tail: '' },
        github_auth_token: { set: true, tail: '1234' },
        github_connection: { status: 'connected', login: 'amika', scopes: ['repo'] },
        amika_claude_cred_id: '',
      },
    });
  }
  const board = Object.entries(boards).find(([id]) => url.pathname.includes(id));
  if (url.pathname.endsWith('/board')) return json(board ? board[1] : boards.p1);
  if (url.pathname.endsWith('/feed')) return json(feed);
  if (url.pathname.endsWith('/activity')) return json({ thinking: false });
  // An empty body closes the SSE connection at once, so the shell settles into
  // its `reconnecting` state — which is convenient: the disconnected indication
  // (13 §10) shows up in the screenshot for free.
  if (url.pathname.includes('/stream')) {
    return route.fulfill({ status: 200, contentType: 'text/event-stream', body: '' });
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
    // Two regions, side by side, with the feed starting where the rail ends.
    railRight: rail?.right,
    feedLeft: feedRegion?.left,
    // The input sits UNDER the feed and never over it.
    composerBelowFeed: composer && feedRegion ? composer.top >= feedRegion.bottom - 1 : null,
    // A blocker reads in full; an update still clamps.
    blockerClamped: blockerBody ? blockerBody.scrollHeight > blockerBody.clientHeight + 1 : 'missing',
    updateClamped: updateBody ? updateBody.scrollHeight > updateBody.clientHeight + 1 : 'missing',
    // Warm near-black, and dark stamped on <body> (13 D6).
    shellBg: getComputedStyle(document.querySelector('[data-role="desktop-screen"]'))
      .backgroundColor,
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

// The rail is the switcher (13 §5).
await page.click('[data-role="rail-project"][data-project-id="p2"]');
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-switched.png' });
console.log(
  'AFTER SWITCH current =',
  await page.getAttribute('[data-role="rail-project"][data-current="true"]', 'data-project-id'),
);

// Ticket detail opens OVER the feed, capped rather than full-bleed (13 D7).
await page.click('[data-role="rail-project"][data-project-id="p1"]');
await page.waitForTimeout(600);
await page.click('[aria-label="Open ticket: Rate-limit the webhook retry"]');
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/desktop-shell-detail.png' });
console.log(
  'SHEET WIDTH =',
  await page.evaluate(() => {
    const el = document.querySelector('[data-role="ticket-detail"]');
    return el ? Math.round(el.getBoundingClientRect().width) : 'missing';
  }),
  '(window 1440)',
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

// Narrow the window: the mobile shell must take back over, and the theme the
// desktop shell stamped must be restored.
await page.setViewportSize({ width: 480, height: 900 });
await page.waitForTimeout(500);
console.log(
  'AT 480px → mobile shell =',
  (await page.locator('[data-role="primary-screen"]').count()) === 1,
  '| desktop shell gone =',
  (await page.locator('[data-role="desktop-screen"]').count()) === 0,
  '| body theme restored =',
  (await page.evaluate(() => document.body.dataset.theme)) === undefined,
);
await page.screenshot({ path: '/tmp/mobile-shell.png' });

console.log('PAGE ERRORS', errors.length === 0 ? 'none' : errors);

await browser.close();
server.close();
