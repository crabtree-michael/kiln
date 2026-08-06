// Layout check for the `/kanban` board view. Hand-run, NOT part of the
// Playwright suite and needing no stack — the same harness and the same stance
// as `desktop-shell-smoke.mjs`: it serves the built frontend, stubs every
// `/api` call, then measures + screenshots the board at a desk-sized viewport.
//
// It exists for the same reason its sibling does: jsdom performs no layout, so
// `Kanban.layout.test.ts` can only assert the CSS *text*. That catches a deleted
// rule but not a rule that is present and wrong — five columns that render fine
// in the DOM and lay out on top of each other, or a column whose list never
// scrolls because a `min-height: 0` went missing. This is the only thing in the
// repo that can see that, so run it after any change to `Kanban.css`:
//
//     cd frontend && pnpm build
//     cd ../tests && node kanban-smoke.mjs
//
// Screenshots land in /tmp; the measurements print to stdout.
//
// Format it with the frontend's config STATED EXPLICITLY:
//
//     cd frontend && pnpm exec prettier --config .prettierrc.json --write ../tests/kanban-smoke.mjs
//
// /tests carries no prettier config of its own, and prettier resolves config
// from the FILE's directory rather than the cwd — so merely running it from
// /frontend is not enough, and the plain invocation silently reformats the whole
// file to prettier's defaults (double quotes). Same for every hand-run script on
// this path.
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
  res.writeHead(200, {
    'content-type': TYPES[extname(file)] ?? 'application/octet-stream',
  });
  res.end(body);
});
await new Promise((resolve) => server.listen(4174, resolve));

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

const ticket = (id, title, state, extra = {}) => ({
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
  ...extra,
});

// Deliberately lopsided: Done is deep enough to need its own scroller (which is
// the thing worth measuring — the other four headings must stay put), and a long
// title plus a long blocked reason are in there to prove the clamps bite.
const board = {
  shaping: [ticket('t9', 'Rate-limit the webhook retry across every delivery endpoint', 'shaping')],
  ready: [
    ticket('t8', 'Retry rotated tokens once', 'ready'),
    ticket('t7', 'Cookie flags', 'ready'),
  ],
  working: [ticket('t2', 'poller', 'working'), ticket('t4', 'index migration', 'working')],
  blocked: [
    ticket('t1', 'auth refresh', 'blocked', {
      blocked_reason:
        'The refresh endpoint returns 401 for tokens rotated while a session was open. Retry once with a fresh token, or surface it to the user and let them re-authenticate?',
    }),
  ],
  done: Array.from({ length: 14 }, (_, i) => ticket(`d${i}`, `finished item ${i + 1}`, 'done')),
  worker_total: 3,
  worker_free: 0,
  agents: [
    { worker_id: 'w1', ticket_id: 't2', status: 'building' },
    { worker_id: 'w2', ticket_id: 't4', status: 'errored' },
  ],
  alerts: [],
};

const browser = await chromium.launch();
// Dark because the OS says dark — the shell pins no theme (13 D6a). The light
// pass at the end flips exactly this and nothing else.
const page = await browser.newPage({
  viewport: { width: 1440, height: 900 },
  colorScheme: 'dark',
});

await page.route('**/api/**', async (route) => {
  const url = new URL(route.request().url());
  if (url.pathname.endsWith('/me')) {
    return route.fulfill({
      json: {
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
            scopes: ['repo'],
          },
          amika_claude_cred_id: '',
        },
      },
    });
  }
  if (url.pathname.endsWith('/board')) return route.fulfill({ json: board });
  if (url.pathname.endsWith('/activity')) return route.fulfill({ json: { thinking: false } });
  if (url.pathname.includes('/stream')) {
    return route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: '',
    });
  }
  return route.fulfill({ json: {} });
});

const errors = [];
page.on('pageerror', (error) => errors.push(String(error)));
page.on('console', (message) => {
  if (message.type() === 'error') errors.push(message.text());
});

await page.goto('http://localhost:4174/kanban');
try {
  await page.waitForSelector('[data-role="kanban-board"]', { timeout: 10_000 });
} catch {
  console.log('DID NOT RENDER. body =', (await page.innerHTML('body')).slice(0, 2000));
  console.log('ERRORS', errors);
  await browser.close();
  server.close();
  process.exit(1);
}
await page.waitForTimeout(600);
await page.screenshot({ path: '/tmp/kanban-dark.png' });

const geometry = await page.evaluate(() => {
  const rect = (selector) => {
    const el = document.querySelector(selector);
    return el ? el.getBoundingClientRect() : null;
  };
  const rail = rect('[data-role="desktop-rail"]');
  const boardRegion = rect('[data-role="kanban-board"]');
  const columns = Array.from(document.querySelectorAll('[data-role="kanban-column"]'));
  const lists = Array.from(document.querySelectorAll('[data-role="kanban-column-list"]'));
  const reason = document.querySelector('[data-role="kanban-card-reason"]');
  const title = document.querySelector('[data-role="kanban-card-title"]');
  return {
    // Two regions, side by side. The rail keeps the feed shell's width.
    railWidth: Math.round(rail?.width ?? 0),
    boardLeftClearsRail: boardRegion && rail ? boardRegion.left >= rail.right - 1 : 'missing',
    // Five columns, all on one row, none of them zero-width.
    columnCount: columns.length,
    columnTops: [...new Set(columns.map((c) => Math.round(c.getBoundingClientRect().top)))],
    columnWidths: columns.map((c) => Math.round(c.getBoundingClientRect().width)),
    // …and all five fit without the board scrolling sideways at 1440px.
    boardScrollsSideways: (() => {
      const el = document.querySelector('[data-role="kanban-board"]');
      return el.scrollWidth > el.clientWidth + 1;
    })(),
    // The deep column scrolls on its OWN, so the other four headings hold still.
    deepColumnScrolls: (() => {
      const done = lists[lists.length - 1];
      return done ? done.scrollHeight > done.clientHeight + 1 : 'missing';
    })(),
    headingsAllOnOneLine: [
      ...new Set(
        Array.from(document.querySelectorAll('[data-role="kanban-column-head"]')).map((h) =>
          Math.round(h.getBoundingClientRect().top),
        ),
      ),
    ],
    // The clamps bite: a long title stops at two lines, a long reason at three.
    titleClamped: title ? title.scrollHeight > title.clientHeight + 1 : 'missing',
    reasonClamped: reason ? reason.scrollHeight > reason.clientHeight + 1 : 'missing',
    // The blocked card's accent edge, and the building mark reaching this
    // subtree from PrimaryScreen.css's shared status-dot rules.
    // All four sides, not just the left: the accent is meant to be ONE edge, and
    // a rule that reached the other three would spend the whole budget on a
    // ring around the loudest text on the screen.
    blockedEdge: (() => {
      const el = document.querySelector('[data-role="kanban-card"][data-state="blocked"]');
      if (!el) return 'missing';
      const s = getComputedStyle(el);
      return {
        left: `${s.borderLeftWidth} ${s.borderLeftColor}`,
        top: `${s.borderTopWidth} ${s.borderTopColor}`,
        right: `${s.borderRightWidth} ${s.borderRightColor}`,
        bottom: `${s.borderBottomWidth} ${s.borderBottomColor}`,
      };
    })(),
    marks: Array.from(
      document.querySelectorAll('[data-role="kanban-card"] [data-role="status-dot"]'),
    ).map((n) => [n.dataset.status, getComputedStyle(n).backgroundColor]),
    pageBackground: getComputedStyle(document.body).backgroundColor,
  };
});
console.log('KANBAN 1440px, dark:', geometry);

// A card opens the SAME sheet the feed opens, at the right edge — the panel must
// land inside the window and paint over the board rather than under it.
await page.click('[data-role="kanban-card"][data-state="blocked"]');
await page.waitForTimeout(500);
await page.screenshot({ path: '/tmp/kanban-detail.png' });
console.log(
  'DETAIL PANEL:',
  await page.evaluate(() => {
    const panel = document.querySelector('[data-vaul-drawer]');
    if (!panel) return 'missing';
    const r = panel.getBoundingClientRect();
    return {
      shellFlag: document.body.dataset.shell,
      atRightEdge: Math.round(r.right) === window.innerWidth,
      fullHeight: Math.round(r.height) === window.innerHeight,
      width: Math.round(r.width),
      insideWindow: r.left >= 0 && r.top >= -1,
    };
  }),
);
await page.keyboard.press('Escape');
await page.waitForTimeout(400);

// The narrow end of the shell's range: five columns no longer fit, so the board
// must scroll sideways rather than squeeze them to illegibility.
await page.setViewportSize({ width: 1024, height: 900 });
await page.waitForTimeout(400);
await page.screenshot({ path: '/tmp/kanban-1024.png' });
console.log(
  'KANBAN 1024px:',
  await page.evaluate(() => {
    const el = document.querySelector('[data-role="kanban-board"]');
    const columns = Array.from(document.querySelectorAll('[data-role="kanban-column"]'));
    return {
      scrollsSideways: el.scrollWidth > el.clientWidth + 1,
      narrowestColumn: Math.min(...columns.map((c) => Math.round(c.getBoundingClientRect().width))),
    };
  }),
);

// The light register. Every rule has to hold in both — the trap is picking the
// surface token that only looks right in whichever theme was open.
await page.setViewportSize({ width: 1440, height: 900 });
await page.emulateMedia({ colorScheme: 'light' });
await page.waitForTimeout(400);
await page.screenshot({ path: '/tmp/kanban-light.png' });
console.log(
  'LIGHT:',
  await page.evaluate(() => {
    const card = document.querySelector('[data-role="kanban-card"]');
    return {
      page: getComputedStyle(document.body).backgroundColor,
      cardFill: getComputedStyle(card).backgroundColor,
      cardBorder: getComputedStyle(card).borderColor,
    };
  }),
);

console.log('ERRORS:', errors);
console.log(
  'screenshots: /tmp/kanban-dark.png /tmp/kanban-detail.png /tmp/kanban-1024.png /tmp/kanban-light.png',
);
await browser.close();
server.close();
