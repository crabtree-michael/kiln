import { expect, test } from '@playwright/test';
import { mintSession } from '../session';
import { apiBase, makeSubscription, pushesFor, resetMockPush, seedTicket } from '../keyless';

// KEYLESS E2E — the Web Push notification transport (spec 02 §10), run with NO
// provider keys (design docs/keyless-e2e-tests-design.md §Test 2). The user
// registers a push subscription whose endpoint is the mock push service; a board
// transition that pushes (a ticket starting work — RunPull ready→working, one of
// the three notify.send triggers) drives the backend's REAL push.Sender (RFC 8291
// encryption + VAPID signing from the test VAPID pair), and the mock service
// captures the delivered, signed push. VAPID is self-issued, not a paid
// credential, and the push service is mocked — so the whole transport runs
// keyless, verifying the encryption/signing code, not just that a row was written.
test('@keyless a starting ticket delivers a signed Web Push to the subscription', async ({
  page,
  request,
}) => {
  test.setTimeout(60_000);
  await mintSession(page.request);
  await mintSession(request, { base: apiBase });

  // Push routes are per-session; register from the browser context (same user the
  // board renders for) so notify.send resolves owner→user→this subscription.
  const subId = `sub-${Date.now().toString(36)}`;
  await resetMockPush(request);
  const sub = makeSubscription(subId);
  const subRes = await page.request.post('/api/push/subscribe', { data: sub });
  expect(subRes.ok(), `POST /api/push/subscribe -> ${subRes.status()}`).toBeTruthy();
  // Loud mode so the start notification is delivered regardless of the default.
  await page.request.put('/api/push/mode', { data: { mode: 'all' } });

  // Seed a Ready ticket: the dev endpoint marks it ready, the deterministic pull
  // binds a mock worker, and ready→working fires notify.send("Started working on
  // this.") — the push under test.
  await seedTicket(request, { title: 'Ship the settings page', state: 'ready' });

  // The mock push service recorded a delivery for our endpoint.
  await expect
    .poll(async () => (await pushesFor(request, subId)).length, {
      message: 'no Web Push was delivered to the mock service (notify.send → push.Sender path broke)',
      timeout: 30_000,
    })
    .toBeGreaterThan(0);

  // It is a real encrypted+signed push, not a bare POST: RFC 8291 body bytes, the
  // aes128gcm content encoding, and a VAPID Authorization header.
  const [push] = await pushesFor(request, subId);
  expect(push.bytes, 'push body is empty — encryption did not run').toBeGreaterThan(0);
  const headers = push.headers;
  expect(headers['content-encoding'], 'missing aes128gcm content-encoding').toBe('aes128gcm');
  expect(String(headers['authorization'] ?? ''), 'missing VAPID Authorization').toContain('vapid');
});
