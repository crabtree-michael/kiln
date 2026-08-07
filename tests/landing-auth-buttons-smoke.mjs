// Hand-run check for the landing page's two auth buttons (same harness and
// stance as desktop-shell-smoke.mjs: serve frontend/dist, no stack needed).
//
// It exists because the requirement is a LAYOUT fact and jsdom does no layout:
// "Sign up" and "Log in" must both be visible on a phone, on one row, inside the
// viewport. The bar this replaced hid the sign-in link under 720px, and the DOM
// test for it passed the whole time — the element was in the tree, just
// `display: none`. Only a browser can tell the difference.
//
//   cd frontend && pnpm build
//   cd ../tests && node landing-auth-buttons-smoke.mjs
import { chromium } from '@playwright/test';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join } from 'node:path';

const DIST = new URL('../frontend/dist/', import.meta.url).pathname;
const TYPES = {
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
};

// Static file server with an SPA fallback — every unmatched path serves
// index.html so the client router can take it.
const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  void (async () => {
    for (const candidate of [url.pathname, '/index.html']) {
      try {
        const body = await readFile(join(DIST, candidate));
        res.writeHead(200, {
          'Content-Type': TYPES[extname(candidate)] ?? 'application/octet-stream',
        });
        res.end(body);
        return;
      } catch {
        /* fall through to index.html */
      }
    }
    res.writeHead(404).end();
  })();
});
await new Promise((resolve) => server.listen(0, resolve));
const base = `http://localhost:${server.address().port}`;

const browser = await chromium.launch();
let failures = 0;

function check(label, ok, detail) {
  console.log(`${ok ? '  ok  ' : ' FAIL '} ${label}${detail ? ` — ${detail}` : ''}`);
  if (!ok) failures += 1;
}

for (const [name, viewport] of [
  ['phone', { width: 390, height: 844 }],
  ['desk', { width: 1440, height: 900 }],
]) {
  const page = await browser.newPage({ viewport });
  await page.goto(`${base}/landing`, { waitUntil: 'networkidle' });

  console.log(`\n${name} (${viewport.width}px)`);

  const nav = page.locator('header.kiln-nav');
  const signUp = nav.getByRole('link', { name: 'Sign up', exact: true });
  const logIn = nav.getByRole('link', { name: 'Log in', exact: true });

  const upBox = await signUp.boundingBox();
  const inBox = await logIn.boundingBox();

  check('Sign up is visible', await signUp.isVisible());
  check('Log in is visible', await logIn.isVisible());

  if (upBox && inBox) {
    // Both on one row, both inside the viewport, neither overlapping the brand.
    check('both sit on one row', Math.abs(upBox.y - inBox.y) < 2, `y ${upBox.y} vs ${inBox.y}`);
    const right = Math.max(upBox.x + upBox.width, inBox.x + inBox.width);
    check(
      'both fit inside the viewport',
      right <= viewport.width,
      `right edge ${right.toFixed(1)}`,
    );

    const brand = await page.locator('.kiln-nav__brand').boundingBox();
    const left = Math.min(upBox.x, inBox.x);
    check(
      'neither collides with the Kiln brand',
      left > brand.x + brand.width,
      `left ${left.toFixed(1)} vs brand end ${(brand.x + brand.width).toFixed(1)}`,
    );

    // The ticket's placement: the pair lives at the top RIGHT, brand at the left.
    check('the pair sits right of centre', left > viewport.width / 2, `left ${left.toFixed(1)}`);

    // Left-to-right order, which differs by viewport: a phone puts Log in
    // first and Sign up outermost (a CSS `order` swap under 720px), the desk
    // keeps the markup order. Compare x, not the DOM — `order` moves the box
    // and leaves the tree alone, so only a real layout can tell.
    const expected = name === 'phone' ? 'Log in then Sign up' : 'Sign up then Log in';
    const actual = inBox.x < upBox.x ? 'Log in then Sign up' : 'Sign up then Log in';
    check(`reads ${expected}`, actual === expected, `got ${actual}`);

    // Tap targets stay usable on a phone.
    if (name === 'phone') {
      check(
        'tap targets stay >=32px tall',
        Math.min(upBox.height, inBox.height) >= 32,
        `${upBox.height.toFixed(1)} / ${inBox.height.toFixed(1)}`,
      );
    }
  } else {
    check('both buttons have a box', false, 'one is not laid out');
  }

  // Both go through the ONE GitHub flow.
  check(
    'both point at /auth/github/connect',
    (await signUp.getAttribute('href')) === '/auth/github/connect' &&
      (await logIn.getAttribute('href')) === '/auth/github/connect',
  );

  // Nothing left of the retired email capture.
  check(
    'no email field anywhere on the page',
    (await page.locator('input[type=email]').count()) === 0,
  );
  check('no "Join the beta" control', (await page.getByText('Join the beta').count()) === 0);

  await page.screenshot({ path: `landing-auth-${name}.png` });
}

// The private-beta screen renders standalone.
const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
await page.goto(`${base}/beta/pending`, { waitUntil: 'networkidle' });
console.log('\n/beta/pending');
check('states the private beta', await page.getByRole('heading', { level: 1 }).isVisible());
check('offers nothing to chase', (await page.locator('a, button, input').count()) === 0);
await page.screenshot({ path: 'landing-auth-private-beta.png' });

await browser.close();
server.close();
console.log(failures === 0 ? '\nALL CHECKS PASSED' : `\n${failures} CHECK(S) FAILED`);
process.exit(failures === 0 ? 0 : 1);
