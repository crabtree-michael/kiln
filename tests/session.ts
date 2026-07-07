import type { APIRequestContext } from '@playwright/test';

// Dev-session auth for the e2e suites (spec 11, phase 2): every /api/* endpoint
// now requires a session cookie, and the dev-gated public endpoint
// POST /api/dev/session {github_login} mints one. Each Playwright transport has
// its OWN cookie jar — the `request` fixture and the browser context
// (`page` / `page.request`) do not share cookies — so a spec must mint in EACH
// context it drives, and a page-driven spec must mint via `page.request` BEFORE
// the first page.goto() (the app's session gate calls GET /api/me on boot).
//
// URL handling: the browser context has a baseURL (the frontend, which proxies
// /api to the backend), so a relative POST works there — call
// `mintSession(page.request)`. The `request` fixture is used with absolute
// backend URLs (KILN_E2E_API_URL), so pass that base explicitly —
// `mintSession(request, { base: apiBase })` — to mint against the same origin
// the spec's other calls hit (cookies are host-scoped; minting against the
// proxy origin would not cover a differently-hosted backend).
export async function mintSession(
  rc: APIRequestContext,
  opts: { base?: string; login?: string } = {},
): Promise<void> {
  const github_login = opts.login ?? process.env.KILN_BOOTSTRAP_GITHUB_USER ?? 'e2e-user';
  const base = (opts.base ?? '').replace(/\/+$/, '');
  const res = await rc.post(`${base}/api/dev/session`, { data: { github_login } });
  if (res.status() !== 200) {
    throw new Error(
      `dev session mint failed: POST ${base}/api/dev/session -> ${res.status()} — ` +
        `is the stack up with KILN_DEV_ENDPOINTS=1 and identity configured ` +
        `(GITHUB_OAUTH_CLIENT_ID, KILN_SECRETS_KEY)?`,
    );
  }
}
