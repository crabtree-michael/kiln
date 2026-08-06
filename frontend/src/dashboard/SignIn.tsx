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

export interface SignInProps {
  /** Simulate the sign-in instead of running it. Absent everywhere in the real
   * app — the sign-up rehearsal (`/signup`) passes it so the card can be walked
   * again by an account that is already signed in, without a round trip to
   * GitHub that would land the tester back on `/dashboard` and end the replay.
   * When set, the action is a button on this handler rather than the
   * `GITHUB_CONNECT_PATH` navigation; everything else about the card is
   * identical, including its `data-role` and accessible name. */
  onStart?: () => void;
}

export function SignIn({ onStart }: SignInProps = {}): JSX.Element {
  const { error } = useDashboardStore();

  return (
    <div data-role="sign-in">
      <div data-role="sign-in-card">
        <div data-role="dashboard-wordmark">Kiln</div>
        {error !== null && <p data-role="dashboard-error">{error}</p>}
        {onStart === undefined ? (
          <a href={GITHUB_CONNECT_PATH} data-role="sign-in-link">
            Continue with GitHub
          </a>
        ) : (
          <button type="button" data-role="sign-in-link" onClick={onStart}>
            Continue with GitHub
          </button>
        )}
      </div>
    </div>
  );
}
