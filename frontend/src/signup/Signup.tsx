// `/signup` — the sign-up flow as a rehearsal you can run again on demand.
//
// The team iterates on the sign-up experience, and until now looking at it
// required an account that had never been through it: `/dashboard` shows the
// guided flow only while `me.projects` is empty, so the second look cost a fresh
// GitHub account or a wiped one. This route removes that. It is NOT a second
// implementation of onboarding — it mounts the very same `SignIn` and
// `Onboarding` components the real surface does, over a simulated store
// (`signup-run.ts`), so what a tester walks here is what a new user walks there.
//
// Three properties it holds, each of them a line in the ticket:
//
//   1. **No gating.** Visiting always starts the flow from the beginning, for
//      anyone signed in — the account handed to the flow has its projects
//      dropped, so "already onboarded" is not a state this route can be in.
//   2. **Both paths, switchable.** `?as=new` (default) is the first-time
//      experience with nothing set up; `?as=returning` is the flow as the
//      already-connected account meets it. The switch is in the banner and
//      writes the query param, so a path is linkable and survives a reload.
//   3. **Nothing is written.** Reads are live (`GET /api/me`,
//      `GET /api/github/repos` — the tester's real login, repos and the
//      deployment's real providers); every write lands in memory. Running the
//      flow does not require wiping the account first, and finishing it does not
//      create, overwrite or delete anything.
//
// The one thing that is faked rather than real is GitHub's consent screen: the
// callback always redirects to `/dashboard`, so a real grant mid-rehearsal would
// end the replay. Step 1's Connect is a simulated grant instead, and the banner
// says so.
import { useState, type JSX } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { DashboardProvider } from '@/dashboard/dashboard-store';
import { DashboardStoreContext, useDashboardStore } from '@/dashboard/dashboard-context';
import { SignIn } from '@/dashboard/SignIn';
import { Onboarding } from '@/dashboard/Onboarding';
import { SIGNUP_PATHS, parseSignupPath, useSignupRun, type SignupPath } from '@/signup/signup-run';
import type { Me, ProjectUpdateRequest } from '@/transport/transport';
import '@/dashboard/Dashboard.css';
import '@/signup/Signup.css';

/** The query param the path rides on, so a tester can link straight at one. */
const PATH_PARAM = 'as';

interface SignupBannerProps {
  path: SignupPath;
  onPath: (path: SignupPath) => void;
  onRestart: () => void;
}

/** Always on screen, above the flow. It carries the two things a tester needs
 * at every step and would otherwise have to leave the page to get: which path
 * they are on (and the one click to try the other), and the fact that none of
 * this is being saved. */
function SignupBanner({ path, onPath, onRestart }: SignupBannerProps): JSX.Element {
  const current = SIGNUP_PATHS.find((candidate) => candidate.id === path);

  return (
    <aside data-role="signup-banner" data-path={path}>
      <div data-role="signup-banner-row">
        <p data-role="signup-banner-title">Sign-up rehearsal</p>
        <div data-role="signup-paths" role="group" aria-label="Sign-up path">
          {SIGNUP_PATHS.map((candidate) => (
            <button
              key={candidate.id}
              type="button"
              data-role="signup-path"
              data-path={candidate.id}
              data-selected={String(candidate.id === path)}
              aria-pressed={candidate.id === path}
              onClick={() => {
                onPath(candidate.id);
              }}
            >
              {candidate.label}
            </button>
          ))}
        </div>
        <button type="button" data-role="signup-restart" onClick={onRestart}>
          Start over
        </button>
      </div>
      <p data-role="signup-path-blurb">{current?.blurb}</p>
      <p data-role="signup-banner-note">
        Your real account, repos and providers — but nothing is saved: no project is created, no key
        is stored, and GitHub’s consent screen is skipped so the run stays on this page.
      </p>
    </aside>
  );
}

interface SignupDoneProps {
  created: ProjectUpdateRequest;
  account: Me;
  onRestart: () => void;
}

/** Where a run ends. The real flow hands over to Settings the moment the project
 * lands; there is no project here, so this states what the real one would have
 * created and puts the next run one click away. */
function SignupDone({ created, account, onRestart }: SignupDoneProps): JSX.Element {
  const providerKey = created.agent_provider ?? '';
  const provider = (account.providers ?? []).find((candidate) => candidate.key === providerKey);

  return (
    <section data-role="signup-done">
      <h1>That’s the whole flow.</h1>
      <p data-role="signup-done-blurb">
        The real flow would have created this project and dropped you into Settings. Nothing was
        written.
      </p>
      <dl data-role="signup-done-summary">
        <div data-role="signup-done-item" data-field="name">
          <dt>Project name</dt>
          <dd>{created.name}</dd>
        </div>
        <div data-role="signup-done-item" data-field="repo">
          <dt>Repository</dt>
          <dd>{created.repo_url}</dd>
        </div>
        <div data-role="signup-done-item" data-field="provider">
          <dt>Coding agent</dt>
          {/* An omitted provider is not an empty choice — it is the flow saying
              "use the deployment default", which is what a deployment serving no
              descriptors leaves it at. */}
          <dd>{providerKey === '' ? 'Deployment default' : (provider?.label ?? providerKey)}</dd>
        </div>
      </dl>
      <div data-role="signup-done-actions">
        <button type="button" data-role="signup-done-restart" onClick={onRestart}>
          Run it again
        </button>
        <Link to="/dashboard" data-role="signup-done-dashboard">
          Back to your dashboard
        </Link>
      </div>
    </section>
  );
}

interface SignupRunViewProps {
  account: Me;
  path: SignupPath;
  onRestart: () => void;
}

/** One run. Mounted under a key of path + run number, so "Start over" and the
 * path switch are remounts: every piece of run state — the simulated account,
 * the step the flow is on, a half-typed key — goes with it, and there is no
 * reset routine that can forget a field. */
function SignupRunView({ account, path, onRestart }: SignupRunViewProps): JSX.Element {
  const run = useSignupRun(account, path);
  // The first-time path opens where a first-time user does: the sign-in card.
  // The returning path is already signed in, by definition, so it opens on the
  // flow's first step.
  const [signedIn, setSignedIn] = useState(path === 'returning');

  let body: JSX.Element;
  if (!signedIn) {
    body = (
      <SignIn
        onStart={() => {
          setSignedIn(true);
        }}
      />
    );
  } else if (run.created === null) {
    body = <Onboarding overrideGitHub={run.overrideGitHub} onConnect={run.connect} />;
  } else {
    body = <SignupDone created={run.created} account={account} onRestart={onRestart} />;
  }

  // The flow reads its account and drives its writes through whichever store is
  // nearest — so nesting this provider inside the real one is the whole
  // interception. `Onboarding` is unaware it is being rehearsed.
  return <DashboardStoreContext.Provider value={run.store}>{body}</DashboardStoreContext.Provider>;
}

function SignupBody(): JSX.Element {
  const { phase, me } = useDashboardStore();
  const [params, setParams] = useSearchParams();
  const path = parseSignupPath(params.get(PATH_PARAM));
  const [runId, setRunId] = useState(0);

  // Signed out for real: there is no account to rehearse as, and no repos to
  // list. This is the real sign-in card with the real link — the one part of the
  // flow that cannot be simulated, because the allowlist check behind it is the
  // thing that decides whether this tester gets in at all.
  if (phase === 'signed-out') {
    return <SignIn />;
  }

  if (phase === 'loading' || me === null) {
    // `me === null` only guards `phase === 'ready'` against a store contract
    // violation — it narrows the type without an `as`, exactly as `Dashboard`'s
    // own body does.
    return (
      <div data-role="dashboard-loading">
        <span data-role="dashboard-spinner" />
        Loading…
      </div>
    );
  }

  return (
    <>
      <SignupBanner
        path={path}
        onPath={(next) => {
          // The param IS the state: a path is then linkable and survives a
          // reload, and the run key below picks the change up as a remount.
          setParams({ [PATH_PARAM]: next });
          setRunId((n) => n + 1);
        }}
        onRestart={() => {
          setRunId((n) => n + 1);
        }}
      />
      <SignupRunView
        key={`${path}-${String(runId)}`}
        account={me}
        path={path}
        onRestart={() => {
          setRunId((n) => n + 1);
        }}
      />
    </>
  );
}

export function Signup(): JSX.Element {
  // `data-role="dashboard"` is the root every onboarding/sign-in rule in
  // Dashboard.css is written against, so the rehearsal renders in the same
  // chrome as the real thing rather than a lookalike. `data-surface` is what
  // Signup.css hangs the banner off.
  return (
    <DashboardProvider>
      <div data-role="dashboard" data-surface="signup">
        <SignupBody />
      </div>
    </DashboardProvider>
  );
}
