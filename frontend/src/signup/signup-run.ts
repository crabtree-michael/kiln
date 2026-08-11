// The sign-up rehearsal's engine (`/signup`): the two paths a tester can walk,
// and a SIMULATED dashboard store the real guided setup flow runs against.
//
// Why a simulated store rather than the real one. `/signup` exists so the team
// can walk the sign-up experience again and again while iterating on it, from
// an ordinary allow-listed account, without first wiping that account. The real
// store cannot do that: `saveProject` is `PUT /api/project`, an UPSERT over the
// caller's FIRST project (identity.UpsertProject) — so an already-onboarded
// tester finishing the flow would silently rewrite the repo, name and provider
// of the project they actually work in. `saveSettings` is worse: provider keys
// are user-scoped, so a throwaway key typed during a rehearsal would replace the
// real one every project of theirs runs on.
//
// So the rehearsal reads for real and writes nowhere. `GET /api/me` and
// `GET /api/github/repos` are the live calls the flow always makes — the tester
// sees their own login, their own repos, this deployment's real provider list —
// and every write is applied to this in-memory account instead of the server.
// Nothing here can create, overwrite or delete anything.
//
// The two paths differ only in the account handed to the flow, which is the
// whole point: the flow itself is the unmodified `Onboarding` component, not a
// copy of it. See `Signup.tsx` for the surface.
import { useCallback, useMemo, useRef, useState } from 'react';
import type {
  Me,
  ProjectUpdateRequest,
  SettingsUpdateRequest,
  VerifyCheck,
} from '@/transport/transport';
import type { components } from '@/schema/generated';
import type { CredentialName, DashboardStoreValue } from '@/dashboard/dashboard-context';
import { credentialKeyIn } from '@/dashboard/integrations-config';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import type { WebPush, WebPushStatus } from '@/stores/use-web-push';

// Not among transport.ts's re-exports (it only re-exports the types its own
// functions traffic in) — pulled straight off the generated wire schema, the
// same way `Onboarding` derives its local aliases.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];

/** Which sign-up experience the tester is replaying.
 *
 * `new` — nothing set up: no GitHub connection, no project, no provider key.
 * The full first-time path, starting at the sign-in card.
 * `returning` — the account as it really is (connected GitHub, whatever keys are
 * really stored), minus its projects, so the flow runs as the connected user. */
export type SignupPath = 'new' | 'returning';

export interface SignupPathDef {
  id: SignupPath;
  /** The switch's button label. */
  label: string;
  /** One line under the switch: what this path is pretending. */
  blurb: string;
}

export const SIGNUP_PATHS: readonly SignupPathDef[] = [
  {
    id: 'new',
    label: 'New user',
    blurb:
      'Nothing is set up: no GitHub connection, no project, no provider key. The full ' +
      'first-time experience, from the sign-in card.',
  },
  {
    id: 'returning',
    label: 'Returning user',
    blurb:
      'Your GitHub account is already connected, and any provider key you have really ' +
      'stored counts as stored. The flow as a returning account meets it.',
  },
];

/** Visiting `/signup` with no `?as=` runs the first-time path — "kick off the
 * whole thing from the start" is the plainest reading of an unqualified visit,
 * and the other path is one click away in the banner. */
export const DEFAULT_SIGNUP_PATH: SignupPath = 'new';

/** Reads the path out of `?as=`. Unknown (and absent) falls back to the default
 * rather than erroring — a mistyped query param should still hand the tester a
 * working flow. `connected` is accepted as a synonym for `returning` because
 * that is the other name the two paths go by. */
export function parseSignupPath(value: string | null): SignupPath {
  switch (value) {
    case 'new':
      return 'new';
    case 'returning':
    case 'connected':
      return 'returning';
    default:
      return DEFAULT_SIGNUP_PATH;
  }
}

const UNSET: SecretStatus = { set: false, tail: '' };

/** The GitHub App installation the rehearsal pretends the user just created,
 * and the link the real card would offer beside it. Obviously fake: `/signup`
 * saves nothing and reaches nothing, so neither must be mistaken for something
 * anybody could act on. */
const SIMULATED_INSTALLATION_ID = 1;
const SIMULATED_CONFIGURE_URL = 'https://github.com/settings/installations/1';

/** The account the flow is handed at the start of a run.
 *
 * BOTH paths drop `projects`, and that is what makes `/signup` repeatable: the
 * guided flow is the view of a user with no project, so an already-onboarded
 * tester would otherwise never see it at all. Nothing is deleted — this is a
 * copy of the account view, and the real one is untouched. */
export function accountForPath(account: Me, path: SignupPath): Me {
  if (path === 'returning') {
    return { ...account, projects: [] };
  }
  return {
    ...account,
    projects: [],
    settings: {
      anthropic_api_key: UNSET,
      amika_api_key: UNSET,
      devin_api_key: UNSET,
      github_auth_token: UNSET,
      github_connection: {
        status: 'disconnected',
        login: '',
        installation_id: 0,
        configure_url: '',
      },
      amika_claude_cred_id: '',
    },
  };
}

/** A saved secret as the account view reports it back: presence and tail only,
 * exactly what `PUT /api/settings` answers with (11 §3 D7, write-only). */
function storedFrom(value: string): SecretStatus {
  return { set: true, tail: value.slice(-4) };
}

/** Applies one auto-save to the simulated settings. Field by field rather than
 * by computed key: TS escape hatches are banned (02 §4b), so an index signature
 * is not available, and an exhaustive list makes a new credential slot a
 * compile-time visit here instead of a silently-ignored write. */
function applySettingsUpdate(settings: MeSettings, body: SettingsUpdateRequest): MeSettings {
  const next: MeSettings = { ...settings };
  if (body.anthropic_api_key !== undefined && body.anthropic_api_key !== '') {
    next.anthropic_api_key = storedFrom(body.anthropic_api_key);
  }
  if (body.amika_api_key !== undefined && body.amika_api_key !== '') {
    next.amika_api_key = storedFrom(body.amika_api_key);
  }
  if (body.devin_api_key !== undefined && body.devin_api_key !== '') {
    next.devin_api_key = storedFrom(body.devin_api_key);
  }
  if (body.github_auth_token !== undefined && body.github_auth_token !== '') {
    next.github_auth_token = storedFrom(body.github_auth_token);
  }
  if (body.amika_claude_cred_id !== undefined) {
    next.amika_claude_cred_id = body.amika_claude_cred_id;
  }
  return next;
}

/** What the rehearsal's ✓ actually means, in the one place a user can read it
 * (the indicator's `title`, shown on a failed check — so it is really only ever
 * read from the code, which is the point of stating it). */
const SIMULATED_CHECK_MESSAGE = 'Simulated — the sign-up rehearsal contacts no provider.';

function simulatedCheck(name: VerifyCheck['name'], present: boolean): VerifyCheck {
  return {
    name,
    // `skipped` is the same reading the real verify gives a credential nobody
    // has entered, and it renders as no glyph at all — so an untouched field
    // looks untouched rather than broken.
    status: present ? 'ok' : 'skipped',
    message: present ? SIMULATED_CHECK_MESSAGE : '',
  };
}

/** The verify run's answer, derived from what the simulated account now holds.
 * A stored credential passes: the rehearsal deliberately reaches no provider,
 * so the alternative would be a permanent ✗ on a key that is very likely fine,
 * which teaches the tester to ignore the indicator they came to look at. */
function simulatedChecks(settings: MeSettings): VerifyCheck[] {
  return [
    simulatedCheck('anthropic', settings.anthropic_api_key.set),
    simulatedCheck('amika', settings.amika_api_key.set),
    simulatedCheck('devin', settings.devin_api_key.set),
    simulatedCheck('repo', settings.github_connection.status === 'connected'),
  ];
}

export interface SignupRun {
  /** Handed to `DashboardStoreContext` so the real flow runs against it. */
  store: DashboardStoreValue;
  /** `Onboarding`'s `overrideGitHub`: hides the live connection until the run
   * has (simulated) granting it. */
  overrideGitHub: (live: GitHubRepos) => GitHubRepos;
  /** `Onboarding`'s `onConnect`: the simulated grant. */
  connect: () => void;
  /** `Onboarding`'s `webPush`: the simulated opt-in. Unlike every other write
   * in the flow this one does NOT go through the store — `useWebPush` calls the
   * transport directly — so the store interception cannot catch it and the
   * rehearsal has to replace the hook's value instead. */
  webPush: WebPush;
  /** Non-null once "Finish setup" ran: the project the real flow would have
   * created. The run is over; nothing was written. */
  created: ProjectUpdateRequest | null;
}

/**
 * One run of the rehearsal. Everything it holds is per-run, so restarting is a
 * remount (`Signup` keys the subtree by path + run number) rather than a reset
 * method that could miss a field.
 */
export function useSignupRun(account: Me, path: SignupPath): SignupRun {
  const [me, setMeState] = useState<Me>(() => accountForPath(account, path));
  // The simulated account is read back by the write methods below (a save has
  // to verify against the settings it just produced), and a `useState` value
  // captured in a `useCallback` would be the settings from BEFORE the save —
  // the chained verify would then report the key as missing, which is precisely
  // the indicator this flow exists to exercise. The ref is the current value;
  // the state is what renders. They only ever move together, through `setMe`.
  const meRef = useRef(me);
  const setMe = useCallback((next: Me): void => {
    meRef.current = next;
    setMeState(next);
  }, []);

  // The returning path arrives already connected — signing in IS connecting
  // (11 §2, amended 2026-08-03) — so its step 1 opens on the confirmation
  // reading, exactly as a real returning user's does.
  const [granted, setGranted] = useState(path === 'returning');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [verifyChecks, setVerifyChecks] = useState<VerifyCheck[] | null>(null);
  const [pendingCredentials, setPendingCredentials] = useState<ReadonlySet<CredentialName>>(
    () => new Set(),
  );
  const [created, setCreated] = useState<ProjectUpdateRequest | null>(null);
  // The rehearsal opens step 4 on the OFFER (`default`) whatever this browser
  // really holds — same reason both paths drop `projects`. A tester walking the
  // sign-up flow has almost certainly opted in already, and the real hook would
  // hand them the "already on" chip, i.e. never the step they came to look at.
  const [pushStatus, setPushStatus] = useState<WebPushStatus>('default');

  const overrideGitHub = useCallback(
    (live: GitHubRepos): GitHubRepos =>
      granted
        ? live
        : // Not merely `connected: false`: `loading` has to be false too, or the
          // step sits on "Checking your GitHub connection…" forever (the reading
          // that deliberately wins first, so the connect prompt never flashes).
          { repos: [], connected: false, loading: false, error: null, refresh: live.refresh },
    [granted],
  );

  const connect = useCallback((): void => {
    setGranted(true);
    // Keep the simulated account honest with what the step now says: the repo
    // check reads this, and the returning path starts here already.
    const current = meRef.current;
    setMe({
      ...current,
      settings: {
        ...current.settings,
        github_connection: {
          status: 'connected',
          login: current.user.github_login,
          installation_id: SIMULATED_INSTALLATION_ID,
          configure_url: SIMULATED_CONFIGURE_URL,
        },
      },
    });
  }, [setMe]);

  // Mirrors the real store's `saveSettings` step for step — pending set, save,
  // chained verify on a credential field, pending cleared — because the
  // indicator sequence it drives (… then ✓) is part of the experience being
  // rehearsed. The only difference is where the bytes go.
  const saveSettings = useCallback(
    async (body: SettingsUpdateRequest): Promise<boolean> => {
      const credentialKey = credentialKeyIn(body);
      if (credentialKey !== null) {
        setPendingCredentials((prev) => new Set(prev).add(credentialKey));
      }
      setSaving(true);
      setError(null);
      await Promise.resolve();
      const current = meRef.current;
      const settings = applySettingsUpdate(current.settings, body);
      setMe({ ...current, settings });
      setSaving(false);
      if (credentialKey !== null) {
        setVerifying(true);
        await Promise.resolve();
        setVerifyChecks(simulatedChecks(settings));
        setVerifying(false);
        setPendingCredentials((prev) => {
          const next = new Set(prev);
          next.delete(credentialKey);
          return next;
        });
      }
      return true;
    },
    [setMe],
  );

  // "Finish setup". The real one is `PUT /api/project`, an upsert over the
  // caller's first project — see this module's header for why the rehearsal
  // must not make that call. It records what would have been created and ends
  // the run instead; `me.projects` deliberately stays empty so "Start over"
  // hands back the same first-run flow.
  const saveProject = useCallback(async (body: ProjectUpdateRequest): Promise<void> => {
    setSaving(true);
    setError(null);
    await Promise.resolve();
    setCreated(body);
    setSaving(false);
  }, []);

  // The push opt-in, simulated. The real `enable()` asks the OS for permission
  // and POSTs a subscription — a prompt the tester would have to dismiss on
  // every run, and a real server-side write on an account the rehearsal
  // promises not to touch. Like the store's writes it mirrors the real
  // SEQUENCE (enabling → enabled), because the button going busy and settling
  // is part of the experience being rehearsed.
  const webPush = useMemo<WebPush>(
    () => ({
      status: pushStatus,
      error: null,
      enable: async (): Promise<void> => {
        setPushStatus('enabling');
        await Promise.resolve();
        setPushStatus('enabled');
      },
      disable: async (): Promise<void> => {
        await Promise.resolve();
        setPushStatus('default');
      },
    }),
    [pushStatus],
  );

  const runVerify = useCallback(async (): Promise<void> => {
    setVerifying(true);
    setError(null);
    await Promise.resolve();
    setVerifyChecks(simulatedChecks(meRef.current.settings));
    setVerifying(false);
  }, []);

  // The multi-project mutations and sign-out belong to `Settings` / the projects
  // page, which the rehearsal never renders — it stops at the flow's last step.
  // They are part of the store contract, so they exist and write nothing.
  const noProjectMutation = useCallback(async (): Promise<boolean> => {
    await Promise.resolve();
    return true;
  }, []);
  const noSignOut = useCallback(async (): Promise<void> => {
    await Promise.resolve();
  }, []);

  const store = useMemo<DashboardStoreValue>(
    () => ({
      // The rehearsal only mounts once an account has loaded, so the flow is
      // never handed a phase it would render a spinner or a sign-in card for.
      phase: 'ready',
      me,
      saving,
      error,
      verifying,
      verifyChecks,
      pendingCredentials,
      saveSettings,
      saveProject,
      createProject: noProjectMutation,
      updateProject: noProjectMutation,
      removeProject: noProjectMutation,
      runVerify,
      signOut: noSignOut,
    }),
    [
      me,
      saving,
      error,
      verifying,
      verifyChecks,
      pendingCredentials,
      saveSettings,
      saveProject,
      runVerify,
      noProjectMutation,
      noSignOut,
    ],
  );

  return { store, overrideGitHub, connect, webPush, created };
}
