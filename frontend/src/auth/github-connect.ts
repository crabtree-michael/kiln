/** The one GitHub flow (11 §2, amended 2026-08-03 and 2026-08-06).
 *
 * Every affordance that reaches GitHub — the landing page's "Sign in", the
 * session gate, the projects screen, the dashboard's Connect and Switch account
 * cards — sends the browser here. There used to be two routes: a scopeless
 * `/auth/github/login` for identity and a repo-scoped one for repo access. They
 * looked interchangeable and weren't, which is how a settings card ended up
 * pointed at the sign-in route and quietly never granted repo access. One
 * constant, one route, nothing left to pick wrong.
 *
 * The backend redirects it to the GitHub App's install page, where GitHub asks
 * which repositories Kiln may use. That is a server-side detail — the client's
 * job is only to leave the SPA correctly — but it is why this link now ends on a
 * chooser rather than a plain consent screen.
 *
 * It is a backend route the SPA does not own, so every navigation to it must be
 * a real full-page load — a plain `<a href>`, NEVER a router `Link`. */
export const GITHUB_CONNECT_PATH = '/auth/github/connect';
