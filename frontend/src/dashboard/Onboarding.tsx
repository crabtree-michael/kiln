// First-run view (11 §5): the signed-in user has no project yet. This is a
// GUIDED SETUP FLOW — one screen per decision, in the order the decisions
// actually depend on each other — not the single crammed form it replaces:
//
//   1. Connect GitHub   — the repo-scoped OAuth grant Kiln reads repos through.
//   2. Choose a project — pick the repo, from that account's listing.
//   3. Choose a provider — which coding agent runs the work, and its API key.
//
// The ordering is load-bearing, not cosmetic: step 2 can only list repos once
// step 1's credential exists, and step 3's key field can only know WHICH key to
// ask for once a provider is chosen. Each step asks for exactly what it needs at
// that moment and nothing else — worker count, merge gate and sandbox snapshot
// all keep their server defaults and are tuned later in Settings.
//
// Step 1 sends the browser to `GITHUB_CONNECT_PATH` — the one GitHub grant
// (11 §2, amended 2026-08-03), the same route the Integrations card and every
// sign-in link use. The repo picker is settings' `RepoField` and the
// provider/credential vocabulary comes from `integrations-config`, so there is
// one definition of each shared fact.
//
// The flow never tracks "did I just finish": once `saveProject` succeeds the
// store's refreshed `me.projects` is non-empty and `Dashboard` itself swaps this
// whole view out for `Settings`. A FAILED save leaves `projects` empty, so the
// user simply stays on the last step with `error` rendered beneath it.
import { useRef, useState, type ChangeEvent, type JSX } from 'react';
import { useDashboardStore, type CredentialName } from '@/dashboard/dashboard-context';
import { RepoField } from '@/dashboard/ConfigFields';
import { secretStatusText } from '@/dashboard/secret-status';
import {
  CHECK_NAME_FOR_CREDENTIAL,
  GITHUB_ACCESS_NOTE,
  apiKeyProviderFor,
  credentialIndicatorStatus,
  updateBodyFor,
  type ApiKeyProvider,
} from '@/dashboard/integrations-config';
import { GITHUB_CONNECT_PATH } from '@/auth/github-connect';
import { useGitHubRepos, type GitHubRepos } from '@/dashboard/use-github-repos';
import { BotIcon, BoxIcon, CheckIcon, FolderIcon, GitHubIcon, SparkIcon } from '@/dashboard/icons';
import type { ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';
import type { components } from '@/schema/generated';

// Not among transport.ts's re-exports (it only re-exports the types its own
// functions traffic in) — pulled straight off the generated wire schema, the
// same way ConfigFields derives its local aliases.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];

/** The glyph beside each provider's choice. Decorative only — the accessible
 * name is always the provider's own label. Unlisted providers fall back to the
 * neutral box, so an unknown key renders correctly rather than blank. */
const PROVIDER_ICON: Readonly<Record<string, () => JSX.Element>> = {
  amika: SparkIcon,
  devin: BotIcon,
};

/** Reads one credential's stored presence + tail off the account view. An
 * exhaustive switch rather than an index, so a new credential slot is a compile
 * error here instead of a silent `undefined` at runtime. */
function storedSecret(settings: MeSettings, name: CredentialName): SecretStatus {
  switch (name) {
    case 'anthropic_api_key':
      return settings.anthropic_api_key;
    case 'amika_api_key':
      return settings.amika_api_key;
    case 'devin_api_key':
      return settings.devin_api_key;
    case 'github_auth_token':
      return settings.github_auth_token;
  }
}

type StepId = 'github' | 'project' | 'provider';

interface StepDef {
  id: StepId;
  /** The step's screen heading, and its label in the progress rail. */
  title: string;
  /** The single line under the heading: what this step is for. */
  blurb: string;
  Icon: () => JSX.Element;
}

const GITHUB_STEP: StepDef = {
  id: 'github',
  title: 'Connect GitHub',
  blurb: 'Kiln reads your repositories through your GitHub account, so this comes first.',
  Icon: GitHubIcon,
};
const PROJECT_STEP: StepDef = {
  id: 'project',
  title: 'Choose your project',
  blurb: 'Pick the repository Kiln should work in. You can add more projects later.',
  Icon: FolderIcon,
};
const PROVIDER_STEP: StepDef = {
  id: 'provider',
  title: 'Choose your provider',
  blurb: 'Which coding agent runs this project’s work, and the key Kiln reaches it with.',
  Icon: BoxIcon,
};

function stepState(index: number, current: number): 'done' | 'current' | 'todo' {
  if (index < current) {
    return 'done';
  }
  if (index === current) {
    return 'current';
  }
  return 'todo';
}

interface GitHubStepProps {
  github: GitHubRepos;
  /** The signed-in account's GitHub login, for the connected reading. */
  login: string;
  /** Simulate the grant instead of navigating to it — see `OnboardingProps`.
   * Explicitly `| undefined` because `exactOptionalPropertyTypes` is on and the
   * caller passes its own optional prop straight through. */
  onConnect?: (() => void) | undefined;
}

/** Step 1. Three readings, mirroring `RepoField`'s — and in the same order, so
 * the connect prompt never flashes before we know whether the account is
 * already connected.
 *
 * Since the OAuth flows merged (11 §2, amended 2026-08-03) signing in IS
 * connecting, so the CONNECTED reading is the common case here, not the rare
 * one: a user who just signed in arrives already authorized and this step reads
 * as a confirmation — which account, how many repos — before they pick one.
 * The disconnected branch still earns its place: a user who signed in before
 * the merge, or who revoked the grant, lands on it, and the note says out loud
 * what GitHub's consent screen will ask for. */
function GitHubStep({ github, login, onConnect }: GitHubStepProps): JSX.Element {
  if (github.loading) {
    return (
      <div data-role="github-connect" data-state="loading">
        <p>Checking your GitHub connection…</p>
      </div>
    );
  }

  if (!github.connected) {
    return (
      <div data-role="github-connect" data-state="disconnected">
        <p>Authorize Kiln to reach your repositories, including private ones.</p>
        <p data-role="github-access-note">{GITHUB_ACCESS_NOTE}</p>
        {/* The one grant, and a backend route — so a real navigation, never a
            router Link. It always asks for `repo`, so clearing it lands the user
            back here connected. */}
        {onConnect === undefined ? (
          <a href={GITHUB_CONNECT_PATH} data-role="connect-github">
            Connect GitHub
          </a>
        ) : (
          <button type="button" data-role="connect-github" onClick={onConnect}>
            Connect GitHub
          </button>
        )}
        {github.error !== null && (
          <p data-role="onboarding-error" role="alert">
            {github.error}
          </p>
        )}
      </div>
    );
  }

  return (
    <div data-role="github-connect" data-state="connected">
      <p data-role="github-connected-as">
        Connected as <strong>{login}</strong>. Kiln can see{' '}
        {github.repos.length === 1 ? '1 repository' : `${String(github.repos.length)} repositories`}
        .
      </p>
      {onConnect === undefined ? (
        <a href={GITHUB_CONNECT_PATH} data-role="switch-github">
          Use a different account
        </a>
      ) : (
        <button type="button" data-role="switch-github" onClick={onConnect}>
          Use a different account
        </button>
      )}
    </div>
  );
}

interface ProviderStepProps {
  providers: ProviderDescriptor[];
  selected: string;
  onSelect: (key: string) => void;
}

/** Step 3's choice itself: one radio per provider the deployment offers, rendered
 * from `me.providers` so adding a provider surfaces here with no edit. Radios
 * rather than a `<select>` because this is the flow's one real decision and
 * deserves the room — and because each option carries a line of its own.
 *
 * Each radio is wired to its label by `htmlFor`/`id`, and the row is a plain
 * `div`, NOT a wrapping `<label>`: a wrapping label absorbs everything inside it
 * into the control's accessible name, so the "Needs your Amika API key." hint
 * would silently become part of the radio's name and break every
 * `getByRole('radio', { name: 'Amika' })`. Same rule, same reason, as the
 * credential rows' validity glyph. */
function ProviderChoice({ providers, selected, onSelect }: ProviderStepProps): JSX.Element {
  return (
    <fieldset data-role="provider-choice">
      <legend>Coding agent</legend>
      {providers.map((provider) => {
        const Icon = PROVIDER_ICON[provider.key] ?? BoxIcon;
        const keyProvider = apiKeyProviderFor(provider.key);
        const id = `provider-${provider.key}`;
        return (
          <div
            key={provider.key}
            data-role="provider-option"
            data-provider={provider.key}
            data-selected={String(selected === provider.key)}
          >
            <input
              id={id}
              type="radio"
              name="agent-provider"
              value={provider.key}
              checked={selected === provider.key}
              onChange={() => {
                onSelect(provider.key);
              }}
            />
            <span data-role="provider-option-icon" aria-hidden="true">
              <Icon />
            </span>
            <label htmlFor={id} data-role="provider-option-label">
              {provider.label}
            </label>
            <span data-role="provider-option-hint">
              {keyProvider === undefined
                ? 'No API key needed.'
                : `Needs your ${keyProvider.label}.`}
            </span>
          </div>
        );
      })}
    </fieldset>
  );
}

interface ProviderKeyFieldProps {
  spec: ApiKeyProvider;
  status: SecretStatus;
  value: string;
  onChange: (value: string) => void;
  /** Commit this key. Fired on blur AND on Enter. */
  onCommit: () => void;
  pendingCredentials: ReadonlySet<CredentialName>;
  verifying: boolean;
  check: Parameters<typeof credentialIndicatorStatus>[3];
}

/** The chosen provider's API key, inline on the step.
 *
 * Deliberately NOT the Integrations card + modal: that shape exists because the
 * settings page lists every provider at once and must not show four key inputs
 * side by side. Here exactly one provider is in question — the one just picked —
 * so a modal would be a click in front of the only thing the step is asking for.
 *
 * The contracts it shares with the cards are the ones tests and e2e bind to, and
 * they are shared as code, not by coincidence: the label string and credential
 * come from `integrations-config`, the status line from `secretStatusText`, and
 * the indicator state from `credentialIndicatorStatus`.
 *
 * The label is wired by `htmlFor`/`id` rather than by wrapping the input: a
 * wrapping label's accessible name absorbs everything inside it, so the validity
 * glyph would silently append itself to the field's name (a
 * `getByLabel('Amika API key')` that works right up until the first ✓ lands). */
function ProviderKeyField({
  spec,
  status,
  value,
  onChange,
  onCommit,
  pendingCredentials,
  verifying,
  check,
}: ProviderKeyFieldProps): JSX.Element {
  const id = `credential-${spec.credential}`;
  const indicator = credentialIndicatorStatus(
    spec.credential,
    pendingCredentials,
    verifying,
    check,
  );
  let glyph: string | null = null;
  if (indicator === 'ok') {
    glyph = '✓';
  } else if (indicator === 'failed') {
    glyph = '✗';
  } else if (indicator === 'pending') {
    glyph = '…';
  }

  return (
    <div data-role="credential-row" data-name={spec.credential}>
      <label htmlFor={id}>{spec.label}</label>
      <span data-role="credential-input-row">
        <input
          id={id}
          type="password"
          value={value}
          placeholder={status.set ? secretStatusText(status) : ''}
          disabled={pendingCredentials.has(spec.credential)}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            onChange(event.target.value);
          }}
          onBlur={onCommit}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              // Never let Enter bubble into a form submission — this field
              // commits itself, and the step's own action is the footer button.
              event.preventDefault();
              onCommit();
            }
          }}
        />
        <span
          data-role="credential-status"
          data-name={spec.credential}
          data-status={indicator}
          title={indicator === 'failed' ? check?.message : undefined}
        >
          {glyph}
        </span>
      </span>
      <span data-role="secret-status" data-name={spec.credential} data-set={String(status.set)}>
        {secretStatusText(status)}
      </span>
    </div>
  );
}

export interface OnboardingProps {
  /** Rewrites the live GitHub reading before the flow sees it. Absent
   * everywhere in the real app; the sign-up rehearsal (`/signup`) passes one so
   * it can present an account as not-yet-connected — the first-time experience —
   * without touching the real credential. A transform rather than the reading
   * itself so the fetch still happens exactly once, here, whoever is watching.
   */
  overrideGitHub?: (live: GitHubRepos) => GitHubRepos;
  /** Simulate the GitHub grant instead of navigating to it. Absent everywhere in
   * the real app; the rehearsal passes it because the real round trip returns to
   * `/dashboard`, which would end the replay. When set, step 1's Connect and
   * Switch-account affordances are buttons on this handler rather than
   * `GITHUB_CONNECT_PATH` links — same `data-role`, same accessible name. */
  onConnect?: () => void;
}

export function Onboarding({ overrideGitHub, onConnect }: OnboardingProps = {}): JSX.Element {
  const {
    me,
    saveProject,
    saveSettings,
    saving,
    error,
    pendingCredentials,
    verifying,
    verifyChecks,
  } = useDashboardStore();
  // User-scoped, so one mount serves every step: the GitHub step reads its
  // connected/disconnected state from it and the project step lists from it.
  const live = useGitHubRepos();
  const github = overrideGitHub === undefined ? live : overrideGitHub(live);

  const [step, setStep] = useState<StepId>('github');
  const [repoUrl, setRepoUrl] = useState('');
  const [name, setName] = useState('');
  // Whether the user has typed a project name themselves. Until they do, picking
  // a repo keeps the name in sync with it; after, their text is never overwritten.
  const [nameEdited, setNameEdited] = useState(false);
  const [providerKey, setProviderKey] = useState('');
  const [keyDraft, setKeyDraft] = useState('');
  // Guards the key save the same way CredentialFields' does: "Finish setup" is a
  // click, and a click blurs the input first, so the blur commit and the finish
  // commit would otherwise both fire for one typed key. Synchronous on purpose —
  // the store's `pendingCredentials` re-renders far too late to catch this pair.
  const keyInFlight = useRef(false);

  if (me === null) {
    // Dashboard only mounts this view once `phase` is 'ready', which the store
    // contract pairs with a populated `me` — this guard just lets TS narrow
    // without an escape hatch (should never actually trip in practice).
    throw new Error('Onboarding rendered without a signed-in account');
  }

  const providers = me.providers ?? [];
  // A deployment that serves no descriptors offers no choice to make, so the
  // provider step is dropped entirely rather than shown empty: the project keeps
  // resolving to the deployment default (AGENT_MODE) exactly as it does today,
  // and its key is entered in Settings afterwards.
  const steps: StepDef[] =
    providers.length > 0 ? [GITHUB_STEP, PROJECT_STEP, PROVIDER_STEP] : [GITHUB_STEP, PROJECT_STEP];
  const currentIndex = Math.max(
    0,
    steps.findIndex((candidate) => candidate.id === step),
  );
  const current = steps[currentIndex] ?? GITHUB_STEP;
  const isLastStep = currentIndex === steps.length - 1;

  // The API-key provider serving the chosen coding agent, or undefined when it
  // takes no pasted key (mock, or anything authenticating another way).
  const keyProvider = apiKeyProviderFor(providerKey);
  const storedStatus =
    keyProvider === undefined ? undefined : storedSecret(me.settings, keyProvider.credential);

  /** Saves the typed provider key, if there is one to save. Resolves whether the
   * flow may proceed: `true` when the key landed, when the provider needs none,
   * or when one is already stored; `false` when the save failed (the message is
   * in `error`) or when the provider needs a key nobody has entered yet.
   *
   * Deliberately NOT a `useCallback`: it would be a hook sitting below the
   * `me === null` guard above, which throws — and nothing here memoizes on its
   * identity (no dependency array, no memo'd child), so it would buy nothing in
   * exchange for a conditional hook. */
  const commitKey = async (): Promise<boolean> => {
    if (keyProvider === undefined || storedStatus === undefined) {
      return true;
    }
    const trimmed = keyDraft.trim();
    if (trimmed === '') {
      return storedStatus.set;
    }
    if (keyInFlight.current) {
      return false;
    }
    keyInFlight.current = true;
    try {
      const saved = await saveSettings(updateBodyFor(keyProvider.credential, trimmed));
      if (saved) {
        // Only clear on success — a failed save leaves the typed key in the box
        // rather than silently discarding it (11 §3 D7's write-only contract).
        setKeyDraft('');
      }
      return saved;
    } finally {
      keyInFlight.current = false;
    }
  };

  const pickRepo = (url: string): void => {
    setRepoUrl(url);
    if (nameEdited) {
      return;
    }
    // Default the project name to the repo's own name — the answer is right
    // essentially always, so the step reduces to a single decision.
    const picked = github.repos.find((repo) => repo.url === url);
    setName(picked === undefined ? '' : (picked.full_name.split('/').at(-1) ?? ''));
  };

  const finish = (): void => {
    void (async () => {
      if (!(await commitKey())) {
        return;
      }
      const body: ProjectUpdateRequest = { name: name.trim(), repo_url: repoUrl.trim() };
      // Only sent when the user actually chose one; otherwise the project
      // resolves to the deployment default, unchanged. Worker count, merge gate
      // and snapshot are deliberately omitted — the server defaults them, and
      // this flow asks only for what it needs.
      if (providerKey !== '') {
        body.agent_provider = providerKey;
      }
      await saveProject(body);
    })();
  };

  // What each step needs before the flow may leave it.
  const stepReady: Record<StepId, boolean> = {
    github: github.connected,
    project: repoUrl.trim() !== '' && name.trim() !== '',
    provider:
      providerKey !== '' &&
      (storedStatus === undefined || storedStatus.set || keyDraft.trim() !== ''),
  };

  const advance = (): void => {
    if (isLastStep) {
      finish();
      return;
    }
    const next = steps[currentIndex + 1];
    if (next !== undefined) {
      setStep(next.id);
    }
  };

  const goBack = (): void => {
    const previous = steps[currentIndex - 1];
    if (previous !== undefined) {
      setStep(previous.id);
    }
  };

  return (
    <div data-role="onboarding" data-step={current.id}>
      <ol data-role="onboarding-steps">
        {steps.map((candidate, index) => {
          const state = stepState(index, currentIndex);
          return (
            <li
              key={candidate.id}
              data-role="onboarding-step"
              data-step={candidate.id}
              data-state={state}
              aria-current={state === 'current' ? 'step' : undefined}
            >
              <span data-role="onboarding-step-mark" aria-hidden="true">
                {state === 'done' ? <CheckIcon /> : String(index + 1)}
              </span>
              <span data-role="onboarding-step-label">{candidate.title}</span>
            </li>
          );
        })}
      </ol>

      <section data-role="onboarding-panel">
        <header data-role="onboarding-heading">
          <p data-role="onboarding-eyebrow">
            Step {String(currentIndex + 1)} of {String(steps.length)}
          </p>
          <h1>
            <span data-role="onboarding-heading-icon" aria-hidden="true">
              <current.Icon />
            </span>
            {current.title}
          </h1>
          <p data-role="onboarding-blurb">{current.blurb}</p>
        </header>

        <div data-role="onboarding-fields">
          {current.id === 'github' && (
            <GitHubStep github={github} login={me.user.github_login} onConnect={onConnect} />
          )}

          {current.id === 'project' && (
            <>
              <RepoField value={repoUrl} onChange={pickRepo} github={github} />
              <label>
                Project name
                <input
                  type="text"
                  value={name}
                  onChange={(event: ChangeEvent<HTMLInputElement>) => {
                    setNameEdited(true);
                    setName(event.target.value);
                  }}
                  required
                />
              </label>
            </>
          )}

          {current.id === 'provider' && (
            <>
              <ProviderChoice
                providers={providers}
                selected={providerKey}
                onSelect={setProviderKey}
              />
              {keyProvider !== undefined && storedStatus !== undefined && (
                <ProviderKeyField
                  spec={keyProvider}
                  status={storedStatus}
                  value={keyDraft}
                  onChange={setKeyDraft}
                  onCommit={() => {
                    void commitKey();
                  }}
                  pendingCredentials={pendingCredentials}
                  verifying={verifying}
                  check={verifyChecks?.find(
                    (candidate) =>
                      candidate.name === CHECK_NAME_FOR_CREDENTIAL[keyProvider.credential],
                  )}
                />
              )}
            </>
          )}
        </div>

        {error !== null ? (
          <p data-role="dashboard-error" role="alert">
            {error}
          </p>
        ) : null}

        <footer data-role="onboarding-actions">
          {currentIndex > 0 && (
            <button type="button" data-role="onboarding-back" onClick={goBack}>
              Back
            </button>
          )}
          <button
            type="button"
            data-role="onboarding-next"
            onClick={advance}
            disabled={saving || !stepReady[current.id]}
          >
            {isLastStep ? 'Finish setup' : 'Continue'}
          </button>
        </footer>
      </section>
    </div>
  );
}
