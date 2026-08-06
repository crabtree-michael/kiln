// Hand-run repro for "a toast clips the mic's radiation" (desktop shell).
//
// Same stance and harness as `desktop-shell-smoke.mjs`: serve `frontend/dist`,
// stub every `/api` call, then look at one thing jsdom cannot see — where the
// toast band's opaque fill lands relative to the mic orb's glow.
//
//     cd frontend && pnpm build
//     cd ../tests && node toast-mic-glow-repro.mjs
//
// It drives a real `say` pill over the stubbed SSE stream (30s dwell, so it is
// still up when the screenshot is taken) and forces the mic into its listening
// reading by hand — a real one needs getUserMedia + the STT socket, and the glow
// is pure CSS keyed off `data-dock-state`, so the attribute IS the state as far
// as layout is concerned.
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
  has_more_history: false,
  cards: [
    {
      kind: 'update',
      id: 'c3',
      label: 'poller',
      body: 'Landed the retry and backed it with a table test.',
      created_at: '2026-08-04T11:05:00Z',
      notification_id: 44,
    },
  ],
};

// One `say` (30s dwell) plus one board `toast`, so the band is two pills tall —
// the state the report describes, and the one that reaches furthest down over
// the composer.
const streamBody = [
  `event: say\ndata: ${JSON.stringify({ message_id: 1, text: 'Rate-limiting the webhook retry now — backing off on 5xx, capped at five attempts.', at: '2026-08-04T11:55:00Z' })}\n\n`,
  `event: activity\ndata: ${JSON.stringify({ kind: 'toast', verb: 'started', ticket_title: 'auth refresh', ticket_id: 't1' })}\n\n`,
].join('');

const browser = await chromium.launch();

async function shot(name, { width, height, colorScheme }) {
  const page = await browser.newPage({
    viewport: { width, height },
    colorScheme,
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

  await page.goto('http://localhost:4174/app');
  await page.waitForSelector('[data-role="toast-stack"]', { timeout: 10_000 });

  // Force the mic's listening reading: set the state attribute the CSS keys off
  // and mount the orb the MicButton only renders while listening, with the level
  // pinned near the top of its range so the glow is at full reach.
  await page.evaluate(() => {
    const talk = document.querySelector('[data-role="dock-talk"]');
    talk.setAttribute('data-dock-state', 'listening');
    let orb = talk.querySelector('[data-role="dock-mic-orb"]');
    if (!orb) {
      orb = document.createElement('span');
      orb.setAttribute('data-role', 'dock-mic-orb');
      talk.insertBefore(orb, talk.firstChild);
    }
    orb.style.setProperty('--mic-level', '0.9');
  });
  // Park the breathing pulse at its widest frame (50%) so the screenshot catches
  // the glow at full reach rather than wherever the loop happened to be.
  await page.addStyleTag({
    content: `[data-role='dock-mic-orb'] { animation-delay: calc(-0.5 * var(--pulse-duration)) !important; animation-play-state: paused !important; }`,
  });
  await page.waitForTimeout(300);

  const geometry = await page.evaluate(() => {
    const rect = (selector) => {
      const el = document.querySelector(selector);
      return el ? el.getBoundingClientRect().toJSON() : null;
    };
    return {
      band: rect('[data-role="toast-stack"]'),
      row: rect('[data-role="activity-row"]'),
      mic: rect('[data-role="dock-talk"]'),
      orb: rect('[data-role="dock-mic-orb"]'),
      region: rect('[data-role="desktop-composer-region"]') ?? rect('[data-role="dock-region"]'),
    };
  });

  const clip = geometry.band.bottom - (geometry.orb.top - 20);
  console.log(`\n== ${name} ==`);
  console.log('  toast band bottom :', geometry.band.bottom.toFixed(1));
  console.log('  mic orb top       :', geometry.orb.top.toFixed(1));
  console.log('  glow reach (~20px):', (geometry.orb.top - 20).toFixed(1));
  console.log(`  ${clip > 0 ? 'CLIPPED' : 'clear'} — band covers ${clip.toFixed(1)}px of the glow`);

  const shotBox = {
    x: Math.max(0, geometry.mic.left - 60),
    y: Math.max(0, geometry.band.top - 40),
    width: 520,
    height: Math.min(height, geometry.mic.bottom + 40) - Math.max(0, geometry.band.top - 40),
  };
  await page.screenshot({ path: `/tmp/${name}.png` });
  await page.screenshot({ path: `/tmp/${name}-crop.png`, clip: shotBox });
  await page.close();
}

await shot('toast-mic-desktop', {
  width: 1440,
  height: 900,
  colorScheme: 'dark',
});
await shot('toast-mic-desktop-light', {
  width: 1440,
  height: 900,
  colorScheme: 'light',
});
await shot('toast-mic-mobile', {
  width: 390,
  height: 844,
  colorScheme: 'dark',
});

await browser.close();
server.close();
