// Signed-out view (11 §5): a single, centered card whose only affordance is
// the GitHub OAuth start link — `GITHUB_CONNECT_PATH`, the one flow. This is a
// plain full-page navigation — NOT a router `Link` — because that is a backend
// route the SPA itself does not own; the browser must actually leave the app.
//
// When the store landed here because the initial `GET /api/me` failed
// outright (a 500, a network blip, an unconfigured deployment — final review,
// Important #2) rather than because there's simply no session, `error` is
// non-null and rendered above the sign-in link so the operator sees why,
// instead of a card that looks identical to an ordinary signed-out state.
import type { JSX } from 'react';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { GITHUB_CONNECT_PATH } from '@/auth/github-connect';

export function SignIn(): JSX.Element {
  const { error } = useDashboardStore();

  return (
    <div data-role="sign-in">
      <div data-role="sign-in-card">
        <div data-role="dashboard-wordmark">Kiln</div>
        {error !== null && <p data-role="dashboard-error">{error}</p>}
        <a href={GITHUB_CONNECT_PATH} data-role="sign-in-link">
          Continue with GitHub
        </a>
      </div>
    </div>
  );
}
