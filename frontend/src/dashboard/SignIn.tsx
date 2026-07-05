// Signed-out view (11 §5): a single, centered card whose only affordance is
// the GitHub OAuth start link. This is a plain full-page navigation — NOT a
// router `Link` — because `/auth/github/login` is a backend route the SPA
// itself does not own; the browser must actually leave the app.
import type { JSX } from 'react';

export function SignIn(): JSX.Element {
  return (
    <div data-role="sign-in">
      <div data-role="sign-in-card">
        <div data-role="dashboard-wordmark">Kiln</div>
        <a href="/auth/github/login" data-role="sign-in-link">
          Continue with GitHub
        </a>
      </div>
    </div>
  );
}
