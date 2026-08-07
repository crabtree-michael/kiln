/** The one GitHub flow (11 §2, amended 2026-08-03, 2026-08-06 and again the
 * same day).
 *
 * Every affordance that reaches GitHub — the landing page's "Sign in", the
 * session gate, the projects screen, the dashboard's Connect card — sends the
 * browser here. There used to be two routes: a scopeless `/auth/github/login`
 * for identity and a repo-scoped one for repo access. They looked
 * interchangeable and weren't, which is how a settings card ended up pointed at
 * the sign-in route and quietly never granted repo access. One constant, one
 * route, nothing left to pick wrong.
 *
 * The backend redirects it to GitHub's authorize screen, and from there — for an
 * account that has not installed Kiln — on to the repository chooser. That
 * second hop is the server's business, not the client's, but it is why signing
 * in sometimes ends on a chooser and sometimes goes straight through.
 *
 * It ends in the app (`/app`), which is what someone clicking "Sign in" is
 * asking for — or on the dashboard when they have no project yet and onboarding
 * is what's next. `GITHUB_DASHBOARD_RETURN_PATH` below is for the callers that
 * want the other ending.
 *
 * They are backend routes the SPA does not own, so every navigation to either
 * must be a real full-page load — a plain `<a href>`, NEVER a router `Link`. */
export const GITHUB_CONNECT_PATH = '/auth/github/connect';

/** The same flow, ending back on the dashboard instead of in the app.
 *
 * For the affordances that live ON the dashboard — the sign-in card, the
 * connect prompt on a project's repo field — where the grant is a step in
 * something the user is already doing there. Landing them in the app would
 * answer "connect your GitHub account" by walking them out of the form they
 * were filling in.
 *
 * Everything reached from outside the dashboard uses the plain constant, and
 * `GITHUB_SETUP_PATH` needs no marker: asking for GitHub's chooser is something
 * only the dashboard does, so the backend reads it as this same request. */
export const GITHUB_DASHBOARD_RETURN_PATH = `${GITHUB_CONNECT_PATH}?next=dashboard`;

/** The same route, asking for GitHub's repository chooser explicitly — where a
 * user picks which repositories Kiln may reach, and which account it sits on.
 *
 * It exists for the CONNECTED user, who is the one case the plain route cannot
 * serve: they have already authorized, so signing in again is instant and
 * invisible, and the screen they actually wanted never appears. Sending them
 * here lands them on GitHub's configure page — a dead end as a sign-in target,
 * and exactly the destination when changing repositories is the point.
 *
 * Getting the two mixed up is harmless in both directions, which is the whole
 * reason a second constant is tolerable after the `/login` history above: this
 * one asks for a screen, not for a different set of permissions. */
export const GITHUB_SETUP_PATH = `${GITHUB_CONNECT_PATH}?setup=1`;
