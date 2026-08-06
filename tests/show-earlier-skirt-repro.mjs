// Hand-run repro for "the Show earlier skirt covers the dock separator and the
// toast band" (phone shell).
//
// Same stance and harness as `toast-mic-glow-repro.mjs`: serve `frontend/dist`,
// stub every `/api` call, then look at one thing jsdom cannot see — where the
// sticky "Show earlier" control's `box-shadow` skirt lands relative to the toast
// band and the dock's separator hairline.
//
//     cd frontend && pnpm build
//     cd ../tests && node show-earlier-skirt-repro.mjs
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
await new Promise((resolve) => server.listen(4175, resolve));

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

const board = {
  shaping: [],
  ready: [],
  blocked: [ticket('t1', 'auth refresh', 'blocked')],
  working: [ticket('t2', 'poller', 'working')],
  done: [],
  worker_total: 3,
  worker_free: 1,
  agents: [{ worker_id: 'w1', ticket_id: 't2', status: 'building' }],
  alerts: [],
};

const card = (n) => ({
  kind: 'update',
  id: `c${n}`,
  label: `poller ${n}`,
  body: 'Landed the retry and backed it with a table test.',
  created_at: '2026-08-04T11:05:00Z',
  notification_id: 40 + n,
});

const feed = {
  summary: {
    blocker_count: 1,
    update_count: 1,
    stream_count: 1,
    building: 1,
    idle: 1,
    last_word_at: '2026-08-04T11:52:00Z',
    last_seen_notification_id: 0,
  },
  // What puts the "Show earlier" control on screen.
  has_more_history: true,
  cards: [card(1), card(2), card(3), card(4), card(5), card(6)],
};

const streamBody = [
  `event: say\ndata: ${JSON.stringify({ message_id: 1, text: 'Rate-limiting the webhook retry now — backing off on 5xx, capped at five attempts.', at: '2026-08-04T11:55:00Z' })}\n\n`,
  `event: activity\ndata: ${JSON.stringify({ kind: 'toast', verb: 'started', ticket_title: 'auth refresh', ticket_id: 't1' })}\n\n`,
].join('');

const browser = await chromium.launch();

async function shot(name, { width, height, colorScheme, typed = false }) {
  const page = await browser.newPage({
    viewport: { width, height },
    colorScheme,
    deviceScaleFactor: 3,
  });
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/me')) {
      return route.fulfill({
        json: {
          user: {
            github_login: 'amika',
            display_name: 'Amika',
            avatar_url: '',
          },
          projects: [project('p1', 'kiln')],
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
        },
      });
    }
    if (url.pathname.endsWith('/board')) return route.fulfill({ json: board });
    if (url.pathname.endsWith('/feed')) return route.fulfill({ json: feed });
    if (url.pathname.endsWith('/activity')) return route.fulfill({ json: { thinking: false } });
    if (url.pathname.includes('/stream')) {
      return route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: streamBody,
      });
    }
    return route.fulfill({ json: {} });
  });

  await page.goto('http://localhost:4175/app');
  await page.waitForSelector('[data-role="feed-show-earlier"]', {
    timeout: 10_000,
  });
  await page.waitForSelector('[data-role="toast-stack"]', { timeout: 10_000 });
  // The transcript panel is the OTHER thing standing in the feed's bottom
  // reserve, and it carries the dock's separator hairline while it is up. The
  // typed draft mounts the same panel, so a keyboard tap + some words is enough
  // to put that state under the control without a microphone.
  if (typed) {
    await page.click('[data-role="dock-keyboard"]');
    await page.fill(
      '[data-role="dock-input"]',
      'Take another look at the retry cap before the release goes out, and say whether it wants a second pass.',
    );
    await page.waitForTimeout(400);
  }
  await page.waitForTimeout(500);

  const geometry = await page.evaluate(() => {
    const rect = (selector) => {
      const el = document.querySelector(selector);
      return el ? el.getBoundingClientRect().toJSON() : null;
    };
    // What paints at a point: the topmost element the hit test finds.
    const roleAt = (x, y) => {
      const el = document.elementFromPoint(x, y);
      if (!el) return null;
      const named = el.closest('[data-role]');
      return named ? named.getAttribute('data-role') : el.tagName;
    };
    const control = rect('[data-role="feed-show-earlier"]');
    const band = rect('[data-role="toast-stack"]');
    const pill = rect('[data-role="toast-pill"]') ?? rect('[data-role="say-pill"]');
    const cx = control ? control.left + control.width / 2 : 100;
    return {
      control,
      band,
      pill,
      transcript: rect('[data-role="dock-transcript"]'),
      dock: rect('[data-role="dock"]'),
      region: rect('[data-role="dock-region"]'),
      feed: rect('[data-role="feed"]'),
      // Sample down the strip between the control and the dock.
      samples: control
        ? [4, 12, 20, 28, 36, 44].map((dy) => ({
            dy,
            role: roleAt(cx, control.bottom + dy),
          }))
        : [],
      overBand: band ? roleAt(cx, band.top + 4) : null,
      overPill: pill ? roleAt(pill.left + pill.width / 2, pill.top + pill.height / 2) : null,
    };
  });
  console.log(`\n=== ${name} ===`);
  console.log(JSON.stringify(geometry, null, 2));
  await page.screenshot({ path: `/tmp/show-earlier-${name}.png` });
  // The strip the bug lived in, enlarged: the control's bottom edge, the reserve
  // its skirt paints, and the top of the band below it.
  if (geometry.control && geometry.band) {
    const top = Math.max(0, geometry.control.top - 8);
    await page.screenshot({
      path: `/tmp/show-earlier-${name}-strip.png`,
      clip: {
        x: 0,
        y: top,
        width: Math.min(width, 390),
        height: Math.min(
          height - top,
          Math.max(geometry.band.bottom, geometry.control.bottom) + 8 - top,
        ),
      },
      // Device pixels, so the 3x capture stays 3x — this strip is read by eye and
      // the artefacts it is read for are a pixel or two tall.
      scale: 'device',
    });
  }
  await page.close();
}

await shot('phone-light', { width: 390, height: 720, colorScheme: 'light' });
await shot('phone-short', { width: 390, height: 520, colorScheme: 'light' });
await shot('phone-typing', {
  width: 390,
  height: 720,
  colorScheme: 'light',
  typed: true,
});
await shot('phone-dark', { width: 390, height: 720, colorScheme: 'dark' });
await shot('desk-light', { width: 1280, height: 800, colorScheme: 'light' });

await browser.close();
server.close();
