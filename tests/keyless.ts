// Shared helpers for the keyless e2e specs (design docs/keyless-e2e-tests-design.md).
// These run against the mocked stack from docker-compose.keyless.yml: AGENT_MODE=mock,
// the scripted brain, the mock STT minter, the offline verifier, a test VAPID pair, and
// the mock-stt / mock-push services. No provider keys anywhere.
import { createECDH, randomBytes } from 'node:crypto';
import { expect, type APIRequestContext } from '@playwright/test';

// The backend, reached directly (the request fixture uses absolute URLs). Override with
// KILN_E2E_API_URL. The browser context still talks same-origin through the vite proxy.
export const apiBase = (process.env.KILN_E2E_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

// The mock Web Push service, reached from the HOST (the spec) via its published port. The
// backend reaches it on the compose network as http://mock-push:7072 — that is the endpoint
// a subscription registers (see pushEndpoint), so the backend can deliver to it.
export const mockPushHostURL = (process.env.KILN_MOCK_PUSH_URL ?? 'http://localhost:7072').replace(/\/+$/, '');
export const mockPushBackendURL = (process.env.KILN_MOCK_PUSH_BACKEND_URL ?? 'http://mock-push:7072').replace(
  /\/+$/,
  '',
);

// seedTicket drives POST /api/dev/tickets (KILN_DEV_ENDPOINTS=1) — a deterministic board
// seed with no brain. Returns the created ticket's id.
export async function seedTicket(
  request: APIRequestContext,
  spec: { title: string; body?: string; state?: string; blocked_reason?: string; approval_requested?: boolean },
): Promise<string> {
  const res = await request.post(`${apiBase}/api/dev/tickets`, { data: { body: 'seeded by keyless e2e', ...spec } });
  expect(res.ok(), `POST /api/dev/tickets -> ${res.status()} (needs KILN_DEV_ENDPOINTS=1)`).toBeTruthy();
  return ((await res.json()) as { id: string }).id;
}

// A browser-shaped push subscription with REAL P-256 material, so the backend's push.Sender
// (RFC 8291) can encrypt to it and actually deliver. We never decrypt — the assertion is that
// a signed push reached the endpoint. `id` scopes the endpoint so /_pushes?id= isolates it.
export function makeSubscription(id: string): {
  endpoint: string;
  keys: { p256dh: string; auth: string };
} {
  const ecdh = createECDH('prime256v1');
  ecdh.generateKeys();
  return {
    endpoint: `${mockPushBackendURL}/push/${id}`,
    keys: {
      p256dh: ecdh.getPublicKey().toString('base64url'), // uncompressed point (0x04 …)
      auth: randomBytes(16).toString('base64url'),
    },
  };
}

// resetMockPush clears the mock push service's record for per-test isolation.
export async function resetMockPush(request: APIRequestContext): Promise<void> {
  await request.post(`${mockPushHostURL}/_reset`).catch(() => undefined);
}

// pushesFor returns the pushes the mock service has recorded for a subscription id.
export async function pushesFor(
  request: APIRequestContext,
  id: string,
): Promise<{ id: string; headers: Record<string, string>; bytes: number }[]> {
  const res = await request.get(`${mockPushHostURL}/_pushes?id=${encodeURIComponent(id)}`);
  expect(res.ok(), `GET ${mockPushHostURL}/_pushes -> ${res.status()}`).toBeTruthy();
  return (await res.json()) as { id: string; headers: Record<string, string>; bytes: number }[];
}
