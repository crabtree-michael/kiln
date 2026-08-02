// Controlled forms for the dashboard's two config surfaces (11 §5): project
// (name/repo/snapshot/workers) and credentials (secrets + Amika config).
// Both are seeded from the current `Me`. Project still submits explicitly via
// its "Save project" button; credentials auto-save per field instead (
// dashboard UX update): each secret input commits on blur AND Enter, only
// when its draft is non-empty, sending just that one field — secrets are
// write-only (11 §3 D7): the input never carries the stored value, only a
// placeholder built from its status tail, and the draft only clears once the
// save actually succeeds. A successful credential save chains straight into a
// verify run (dashboard-store's `saveSettings`), so the right-of-input
// `credential-status` indicator picks up the fresh check result with no
// separate "test connections" step.
import {
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
  type JSX,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import type {
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  Snapshot,
  SettingsUpdateRequest,
  VerifyCheck,
} from '@/transport/transport';
import type { components } from '@/schema/generated';
import type { CredentialName } from '@/dashboard/dashboard-context';
import { GITHUB_CONNECT_PATH, type GitHubRepos } from '@/dashboard/use-github-repos';
import { BotIcon, GitHubIcon, IdIcon, KeyIcon, SparkIcon } from '@/dashboard/icons';

// `MeSettings`/`SecretStatus` aren't among transport.ts's re-exports (only the
// types its own functions traffic in are) — pull them the same way it derives
// its own local aliases, straight off the generated wire schema.
type MeSettings = components['schemas']['MeSettings'];
type SecretStatus = components['schemas']['SecretStatus'];
// The merge-gate knob (06 §7): which condition marks a ticket done — its work
// merged to main, or merely in a pull request. Non-optional here (the form
// always carries a concrete choice) even though the request field is optional.
type MergeGateMode = NonNullable<ProjectUpdateRequest['merge_gate_mode']>;

/** The exact contract string (task-13 e2e binds to it): "configured · …<tail>". */
function secretStatusText(status: SecretStatus): string {
  return status.set ? `configured · …${status.tail}` : 'not configured';
}

/** Compares two repo URLs for "same repo". Stored `repo_url` values predate the
 * picker and were typed by hand, so they vary in case and in the trailing `/`
 * and `.git` GitHub's own `html_url` never carries — normalizing all three is
 * what lets an existing project preselect its repo instead of falling through to
 * the "(current)" escape hatch. */
function sameRepo(a: string, b: string): boolean {
  const normalize = (url: string): string =>
    url
      .trim()
      .toLowerCase()
      .replace(/\/+$/, '')
      .replace(/\.git$/, '');
  return normalize(a) === normalize(b);
}

interface RepoFieldProps {
  /** The project's `repo_url` — '' for a project being created. */
  value: string;
  onChange: (repoUrl: string) => void;
  github: GitHubRepos;
}

/** The project's repo, chosen from the connected GitHub account rather than
 * typed (settings repo picker). Three states, in the order a user meets them:
 *
 *  1. loading — a quiet placeholder, so the connect prompt never flashes up
 *     before we know whether the account is actually connected;
 *  2. disconnected — the "Connect GitHub account" link. Connecting IS signing in
 *     again (one OAuth flow, one callback), which is what re-prompts the user to
 *     grant repo access. A project that already has a repo_url keeps it: it is
 *     shown read-only and still submitted, so editing an unrelated field on an
 *     older project can't silently unlink its repo;
 *  3. connected — the repo dropdown, plus a "Switch account" link that re-runs
 *     the same flow against a different GitHub login. The dropdown carries no
 *     filter box: a native select already types-to-jump, so a second search
 *     control beside it only raised the question of which one to use. */
function RepoField({ value, onChange, github }: RepoFieldProps): JSX.Element {
  if (github.loading) {
    return (
      <div data-role="repo-field" data-state="loading">
        <span>Repository</span>
        <p data-role="repo-loading">Loading your GitHub repos…</p>
      </div>
    );
  }

  if (!github.connected) {
    return (
      <div data-role="repo-field" data-state="disconnected">
        <span>Repository</span>
        <p data-role="repo-connect-hint">
          Connect your GitHub account to pick a repository. Kiln uses the same GitHub sign-in you
          already use — connecting just grants it access to your repos, including private ones.
        </p>
        {/* A backend route, so a real navigation — never a router Link. */}
        <a href={GITHUB_CONNECT_PATH} data-role="connect-github">
          Connect GitHub account
        </a>
        {value !== '' && (
          <p data-role="repo-current">
            Currently linked: <span data-role="repo-current-url">{value}</span>
          </p>
        )}
        {github.error !== null && (
          <p data-role="repo-error" role="alert">
            {github.error}
          </p>
        )}
      </div>
    );
  }

  // The stored URL matched against the live list. An unmatched non-empty value —
  // a repo the account can no longer see, or one beyond the listing cap — stays
  // selectable as "(current)" so saving the form never silently drops it (the
  // same guarantee the snapshot picker makes).
  const matched = github.repos.find((repo) => sameRepo(repo.url, value));

  return (
    <div data-role="repo-field" data-state="connected">
      <label>
        Repository
        <select
          data-role="repo-select"
          value={matched?.url ?? value}
          onChange={(event: ChangeEvent<HTMLSelectElement>) => {
            onChange(event.target.value);
          }}
          required
        >
          {/* Only offered until a repo is chosen — the field is required, so it
              must not become a way to un-set one. */}
          {value === '' && <option value="">Select a repository…</option>}
          {matched === undefined && value !== '' && (
            <option value={value}>{value} (current)</option>
          )}
          {github.repos.map((repo) => (
            <option key={repo.url} value={repo.url}>
              {repo.full_name}
              {repo.private ? ' (private)' : ''}
            </option>
          ))}
        </select>
      </label>
      <a href={GITHUB_CONNECT_PATH} data-role="switch-github">
        Switch GitHub account
      </a>
      {github.error !== null && (
        <p data-role="repo-error" role="alert">
          {github.error}
        </p>
      )}
    </div>
  );
}

/** The label a snapshot shows in the picker: its name, annotated when it is not
 * yet a selectable base image (still capturing, or a failed/unknown capture). */
function snapshotOptionLabel(snap: Snapshot): string {
  const label = snap.name === '' ? snap.ref : snap.name;
  if (snap.state === 'ready') {
    return label;
  }
  return `${label} (${snap.state})`;
}

interface SecretStatusRowProps {
  name: CredentialName;
  status: SecretStatus;
}

function SecretStatusRow({ name, status }: SecretStatusRowProps): JSX.Element {
  return (
    <span data-role="secret-status" data-name={name} data-set={String(status.set)}>
      {secretStatusText(status)}
    </span>
  );
}

/** Which `VerifyCheck.name` each credential field's indicator reads from —
 * the GitHub token guards repo access, so it maps to the "repo" check rather
 * than a standalone "github" one. */
const CHECK_NAME_FOR_CREDENTIAL: Record<CredentialName, VerifyCheck['name']> = {
  anthropic_api_key: 'anthropic',
  amika_api_key: 'amika',
  devin_api_key: 'devin',
  github_auth_token: 'repo',
};

type CredentialIndicatorStatus = 'ok' | 'failed' | 'skipped' | 'pending';

/** `pending` covers two windows: this field's own save request (it stays in
 * `pendingCredentials` for the whole save + chained-verify span, independent
 * of any other field's in-flight save), and any verify run at all (one
 * verify call checks all three at once, so every indicator goes pending
 * together while it's in flight) — either means "the last known result
 * can't be trusted yet". Absent any check result, the field reads `skipped`
 * (nothing has verified it — same as an explicit "skipped" check, and
 * rendered the same way: no glyph). */
function credentialIndicatorStatus(
  name: CredentialName,
  pendingCredentials: ReadonlySet<CredentialName>,
  verifying: boolean,
  check: VerifyCheck | undefined,
): CredentialIndicatorStatus {
  if (pendingCredentials.has(name) || verifying) {
    return 'pending';
  }
  return check?.status ?? 'skipped';
}

interface CredentialStatusIndicatorProps {
  name: CredentialName;
  status: CredentialIndicatorStatus;
  message?: string | undefined;
}

/** The right-of-input validity mark: a checkmark once its verify check comes
 * back ok, a cross (with the check's message as a hover title) when it
 * fails, a quiet ellipsis while its save or a verify is in flight, and
 * nothing rendered for `skipped` — `data-status` always carries the real
 * state (tests bind to it), the glyph is just the human-visible layer on
 * top. */
function CredentialStatusIndicator({
  name,
  status,
  message,
}: CredentialStatusIndicatorProps): JSX.Element {
  let glyph: string | null = null;
  if (status === 'ok') {
    glyph = '✓';
  } else if (status === 'failed') {
    glyph = '✗';
  } else if (status === 'pending') {
    glyph = '…';
  }
  return (
    <span
      data-role="credential-status"
      data-name={name}
      data-status={status}
      title={status === 'failed' ? message : undefined}
    >
      {glyph}
    </span>
  );
}

export interface ProjectFieldsProps {
  /** Absent in onboarding (no project yet) — every field starts blank. */
  project?: MeProject;
  /** The caller's connected GitHub account and its repos (settings repo picker).
   * Required, and deliberately not optional: the repo is always chosen from the
   * connected account, so there is no free-text fallback path to drift. Load it
   * once per view with `useGitHubRepos` — it is user-scoped, so several project
   * cards share one result. */
  github: GitHubRepos;
  /** The coding-agent providers this deployment offers (multi-provider design
   * §8, §9). The provider select renders from these; with 0–1 offered it is
   * hidden — a single-provider deployment is unchanged. */
  providers?: ProviderDescriptor[];
  /** The base-image snapshots the project's workers can start from (sandbox
   * selection). When `catalogAvailable` is true, the snapshot field renders as a
   * picker from these instead of a free-text handle. */
  snapshots?: Snapshot[];
  /** `true` when the project's provider is known to expose a snapshot catalog —
   * the snapshot field becomes a picker. `false` (the default) keeps the
   * free-text snapshot input, so a provider without a catalog (or onboarding,
   * before a project exists) is unchanged. */
  catalogAvailable?: boolean;
  /** Which shell the same fields render in (projects-in-a-modal):
   *
   *  * `form` (the default) — the flat field list onboarding and the app-native
   *    projects page have always used. Unchanged, deliberately: those two
   *    surfaces style it themselves and their DOM must not move under them.
   *  * `detail` — the settings project modal: an identity header (the name,
   *    edited in place, beside the repository it is linked to) over grouped
   *    Agent and Sandbox sections. Same state, same submit body — only the
   *    arrangement differs. */
  layout?: 'form' | 'detail';
  saving: boolean;
  onSave: (body: ProjectUpdateRequest) => Promise<void>;
}

/** One line of "what this snapshot actually is", for the modal's sandbox
 * section: where it was captured from and when, so picking a base image isn't a
 * guess from an opaque ref. */
function snapshotDetail(snap: Snapshot): string {
  const parts: string[] = [snap.ref];
  if (snap.source !== '') {
    parts.push(`captured from ${snap.source}`);
  }
  const captured = new Date(snap.created_at);
  if (!Number.isNaN(captured.getTime())) {
    parts.push(captured.toLocaleDateString(undefined, { dateStyle: 'medium' }));
  }
  return parts.join(' · ');
}

interface SandboxInfoProps {
  catalogAvailable: boolean;
  snapshots: Snapshot[];
  /** The `amika_snapshot` handle the form currently holds; '' means "default". */
  selectedRef: string;
}

/** What the snapshot picker above actually means, in words (projects-in-a-modal,
 * "sandbox info"). A snapshot ref on its own says nothing about which sandbox a
 * worker will boot into, so each of the four states the picker can be in gets a
 * plain-language reading: no catalog at all, the deployment default, a snapshot
 * from the catalog (with where it was captured from and when), or a stored handle
 * the catalog no longer lists. */
function SandboxInfo({ catalogAvailable, snapshots, selectedRef }: SandboxInfoProps): JSX.Element {
  if (!catalogAvailable) {
    return (
      <div data-role="sandbox-info" data-state="no-catalog">
        <p>
          This project&apos;s agent provider manages its own sandboxes, so there is no Amika
          snapshot catalog to pick from. The handle above is passed to the provider as written —
          leave it blank to use the deployment&apos;s default image.
        </p>
      </div>
    );
  }

  if (selectedRef === '') {
    return (
      <div data-role="sandbox-info" data-state="default">
        <p>
          Workers start from the deployment&apos;s default Amika image. Pick a snapshot to start
          them pre-warmed instead — dependencies installed, repo cloned, tools already authenticated
          — so a ticket begins with work rather than with setup.
        </p>
      </div>
    );
  }

  const selected = snapshots.find((snap) => snap.ref === selectedRef);
  if (selected === undefined) {
    return (
      <div data-role="sandbox-info" data-state="unlisted">
        <p>
          Workers start from <code>{selectedRef}</code>, which this project&apos;s catalog no longer
          lists — it may have been deleted, or belong to another Amika account. It stays in use
          until you pick another snapshot.
        </p>
      </div>
    );
  }

  return (
    <div data-role="sandbox-info" data-state="snapshot">
      <p>
        Workers start from{' '}
        <strong data-role="sandbox-snapshot-name">
          {selected.name === '' ? selected.ref : selected.name}
        </strong>
        {selected.state === 'ready' ? '.' : ` (${selected.state}).`}
      </p>
      {selected.description !== '' ? (
        <p data-role="sandbox-snapshot-description">{selected.description}</p>
      ) : null}
      <p data-role="sandbox-snapshot-detail">{snapshotDetail(selected)}</p>
    </div>
  );
}

export function ProjectFields({
  project,
  github,
  providers,
  snapshots,
  catalogAvailable = false,
  layout = 'form',
  saving,
  onSave,
}: ProjectFieldsProps): JSX.Element {
  const [name, setName] = useState(project?.name ?? '');
  const [repoUrl, setRepoUrl] = useState(project?.repo_url ?? '');
  // The per-project coding-agent provider (multi-provider design §9): the stored
  // registry key, or '' meaning "deployment default". The select below only shows
  // when the deployment offers more than one provider.
  const providerOptions = providers ?? [];
  const [agentProvider, setAgentProvider] = useState(project?.agent_provider ?? '');
  const [amikaSnapshot, setAmikaSnapshot] = useState(project?.amika_snapshot ?? '');
  const [workerCount, setWorkerCount] = useState(
    project?.worker_count === undefined ? '' : String(project.worker_count),
  );
  const [mergeGateMode, setMergeGateMode] = useState<MergeGateMode>(
    project?.merge_gate_mode ?? 'main',
  );
  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const body: ProjectUpdateRequest = { name: name.trim(), repo_url: repoUrl.trim() };
    // Send the provider choice only when the deployment offers a real choice; a
    // single-provider deployment leaves it empty so the project keeps resolving to
    // the deployment default (multi-provider design §9). '' is a valid value — it
    // is the "use the deployment default" sentinel — so it is sent explicitly.
    if (providerOptions.length > 1) {
      body.agent_provider = agentProvider;
    }
    const trimmedSnapshot = amikaSnapshot.trim();
    if (trimmedSnapshot !== '') {
      body.amika_snapshot = trimmedSnapshot;
    }
    const trimmedWorkerCount = workerCount.trim();
    if (trimmedWorkerCount !== '') {
      const parsed = Number(trimmedWorkerCount);
      if (!Number.isNaN(parsed)) {
        body.worker_count = parsed;
      }
    }
    // Always sent: the select carries a concrete choice ('main' by default), so
    // the server records the user's gate explicitly rather than inferring it.
    body.merge_gate_mode = mergeGateMode;
    // `amika_secrets` is deliberately never sent: the form no longer edits the
    // project's sandbox secrets, and the field is a wholesale upsert (11 §4), so
    // sending the list this form no longer holds would clear every stored secret
    // on an unrelated save. Omitting it leaves them exactly as they are (12 §3).
    void onSave(body);
  };

  // Every field is built once here and then arranged by the layout branch at the
  // bottom, so the two shells can never drift into rendering different controls
  // (or a different submit body) from each other.
  const nameField = (
    <label>
      Project name
      <input
        type="text"
        value={name}
        onChange={(event: ChangeEvent<HTMLInputElement>) => {
          setName(event.target.value);
        }}
        required
      />
    </label>
  );

  const repoField = <RepoField value={repoUrl} onChange={setRepoUrl} github={github} />;

  const providerField =
    providerOptions.length > 1 ? (
      <label>
        Agent provider
        <select
          data-role="agent-provider"
          value={agentProvider}
          onChange={(event: ChangeEvent<HTMLSelectElement>) => {
            setAgentProvider(event.target.value);
          }}
        >
          {/* Empty value = the deployment default (design §9), always offered. */}
          <option value="">Default</option>
          {providerOptions.map((provider) => (
            <option key={provider.key} value={provider.key}>
              {provider.label}
            </option>
          ))}
        </select>
      </label>
    ) : null;

  const snapshotField = catalogAvailable ? (
    <label>
      Sandbox snapshot
      <select
        data-role="amika-snapshot"
        value={amikaSnapshot}
        onChange={(event: ChangeEvent<HTMLSelectElement>) => {
          setAmikaSnapshot(event.target.value);
        }}
      >
        {/* Empty = the provider/deployment default snapshot. */}
        <option value="">Default</option>
        {/* Keep the currently-stored handle selectable even if it is no longer
            in the catalog (an older/custom snapshot), so saving the form never
            silently drops it. */}
        {amikaSnapshot !== '' && !(snapshots ?? []).some((snap) => snap.ref === amikaSnapshot) && (
          <option value={amikaSnapshot}>{amikaSnapshot} (current)</option>
        )}
        {(snapshots ?? []).map((snap) => (
          // A snapshot still capturing is listed but not selectable — only a
          // ready one is a valid base image (its capture has finished).
          <option key={snap.ref} value={snap.ref} disabled={snap.state !== 'ready'}>
            {snapshotOptionLabel(snap)}
          </option>
        ))}
      </select>
    </label>
  ) : (
    <label>
      Amika snapshot
      <input
        type="text"
        value={amikaSnapshot}
        onChange={(event: ChangeEvent<HTMLInputElement>) => {
          setAmikaSnapshot(event.target.value);
        }}
      />
    </label>
  );

  const workerCountField = (
    <label>
      Worker count
      <input
        type="number"
        value={workerCount}
        onChange={(event: ChangeEvent<HTMLInputElement>) => {
          setWorkerCount(event.target.value);
        }}
      />
    </label>
  );

  const mergeGateField = (
    <label>
      Merge gate
      <select
        data-role="merge-gate-mode"
        value={mergeGateMode}
        onChange={(event: ChangeEvent<HTMLSelectElement>) => {
          // Narrow the raw option value to the union without an assertion; the
          // select only ever offers these two, so anything else means 'main'.
          setMergeGateMode(event.target.value === 'pr' ? 'pr' : 'main');
        }}
      >
        <option value="main">Merged to main</option>
        <option value="pr">In a pull request</option>
      </select>
    </label>
  );

  // The repo is required and can only come from the picker, so a project with
  // none selected — a new one on a disconnected account — can't be saved yet.
  // Blocking here beats a server-side 400 the user can't act on.
  const submitButton = (
    <button type="submit" disabled={saving || repoUrl.trim() === ''}>
      Save project
    </button>
  );

  if (layout !== 'detail') {
    return (
      <form data-role="project-form" onSubmit={handleSubmit}>
        {nameField}
        {repoField}
        {providerField}
        {snapshotField}
        {workerCountField}
        {mergeGateField}
        {submitButton}
      </form>
    );
  }

  // The detail shell (projects-in-a-modal). The identity header answers "which
  // project is this, and what repo is it pointed at" in a single band — the two
  // facts every other field is relative to — and the rest is grouped by the
  // question it answers rather than listed flat.
  return (
    <form data-role="project-form" data-layout="detail" onSubmit={handleSubmit}>
      <header data-role="project-identity">
        {nameField}
        {repoField}
      </header>

      <section data-role="project-group" data-group="agent">
        <h3>Agent</h3>
        <p data-role="project-group-hint">
          {providerField === null
            ? 'How much of this project’s work runs at once, and what counts as finished.'
            : 'Which coding agent runs this project’s work, how much of it runs at once, and what counts as finished.'}
        </p>
        <div data-role="project-group-fields">
          {providerField}
          {workerCountField}
          {mergeGateField}
        </div>
      </section>

      <section data-role="project-group" data-group="sandbox">
        <h3>Sandbox</h3>
        {/* Provider-neutral on purpose — what a sandbox actually is here depends
            on the provider, and `SandboxInfo` below says which case this is. */}
        <p data-role="project-group-hint">
          The workspace each worker starts in: which base image its sandbox boots from.
        </p>
        <div data-role="project-group-fields">{snapshotField}</div>
        <SandboxInfo
          catalogAvailable={catalogAvailable}
          snapshots={snapshots ?? []}
          selectedRef={amikaSnapshot}
        />
      </section>

      <footer data-role="project-form-actions">{submitButton}</footer>
    </form>
  );
}

interface SecretCredentialRowProps {
  name: CredentialName;
  label: string;
  /** The row's leading glyph. Decorative: the accessible name is the label. */
  icon: ReactNode;
  /** The stored secret's presence + tail — drives the placeholder and the
   * "configured · …tail" line beneath the input. Never the value itself. */
  status: SecretStatus;
  value: string;
  onChange: (value: string) => void;
  /** Commit this one field. Fired on blur AND on Enter. */
  onCommit: () => void;
  pendingCredentials: ReadonlySet<CredentialName>;
  verifying: boolean;
  check: VerifyCheck | undefined;
}

/** One secret credential, as a compact icon-forward row: glyph, label, input
 * with its live validity mark pinned right, and the stored-secret status line
 * beneath. The four secrets differ only in name/label/icon, so they all render
 * through here rather than as four near-identical blocks.
 *
 * The label is wired by `htmlFor`/`id` rather than by wrapping the input,
 * deliberately: a wrapping label's accessible name absorbs everything inside it,
 * so the validity glyph would silently append itself to the field's name (a
 * `getByLabel('Amika API key')` that works until the first ✓ lands). */
function SecretCredentialRow({
  name,
  label,
  icon,
  status,
  value,
  onChange,
  onCommit,
  pendingCredentials,
  verifying,
  check,
}: SecretCredentialRowProps): JSX.Element {
  const id = `credential-${name}`;
  return (
    <div data-role="credential-row" data-name={name}>
      {/* The glyph rides inside the label so it sits on the label's line without
          any vertical-alignment guesswork; `aria-hidden` keeps it out of the
          computed name, which stays exactly the label text. */}
      <label htmlFor={id}>
        <span data-role="credential-icon" aria-hidden="true">
          {icon}
        </span>
        {label}
      </label>
      <span data-role="credential-input-row">
        <input
          id={id}
          type="password"
          value={value}
          placeholder={status.set ? secretStatusText(status) : ''}
          disabled={pendingCredentials.has(name)}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            onChange(event.target.value);
          }}
          onBlur={onCommit}
          onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
            if (event.key === 'Enter') {
              event.preventDefault();
              onCommit();
            }
          }}
        />
        <CredentialStatusIndicator
          name={name}
          status={credentialIndicatorStatus(name, pendingCredentials, verifying, check)}
          message={check?.message}
        />
      </span>
      <SecretStatusRow name={name} status={status} />
    </div>
  );
}

export interface CredentialFieldsProps {
  settings: MeSettings;
  /** The credential fields whose save/verify is currently in flight —
   * threaded straight through from the store; drives each field's indicator
   * and its input's disabled state independently. */
  pendingCredentials: ReadonlySet<CredentialName>;
  /** `true` while a verify run is in flight — applies to every indicator at
   * once (one run checks all three fields). */
  verifying: boolean;
  verifyChecks: VerifyCheck[] | null;
  onSave: (body: SettingsUpdateRequest) => Promise<boolean>;
}

/** Every field that auto-commits from this form — the three secrets plus the
 * plain-text Amika credential ID (which never chains a verify but still
 * needs the same re-entrancy guard). */
type CommitField = CredentialName | 'amika_claude_cred_id';

/** Per-user Anthropic key entry is HIDDEN for now: the deployment supplies the
 * Anthropic key as a global `ANTHROPIC_API_KEY` env setting, and onboarding no
 * longer asks each user for one. The field, its state, and its commit/verify
 * path are RETAINED (not deleted) behind this env flag so per-user Anthropic
 * keys can be brought back — set `VITE_SHOW_ANTHROPIC_KEY_FIELD=1` — when user
 * management expands, no code change needed. */
const SHOW_ANTHROPIC_KEY_FIELD = import.meta.env.VITE_SHOW_ANTHROPIC_KEY_FIELD === '1';

export function CredentialFields({
  settings,
  pendingCredentials,
  verifying,
  verifyChecks,
  onSave,
}: CredentialFieldsProps): JSX.Element {
  const [anthropicApiKey, setAnthropicApiKey] = useState('');
  const [amikaApiKey, setAmikaApiKey] = useState('');
  const [devinApiKey, setDevinApiKey] = useState('');
  const [githubAuthToken, setGithubAuthToken] = useState('');
  const [amikaClaudeCredId, setAmikaClaudeCredId] = useState(settings.amika_claude_cred_id);

  // Per-field in-flight guard, synchronous on purpose: Enter fires a commit
  // and the resulting focus loss (or an explicit Tab) fires blur in the same
  // task, long before the store's pending state re-renders — a ref is the
  // only thing that reliably makes that pair a single save. The store's
  // `pendingCredentials` mirrors this asynchronously for rendering (the
  // indicator + disabled input); this ref is what enforces it.
  const inFlight = useRef<Set<CommitField>>(new Set());

  const checkFor = (name: CredentialName): VerifyCheck | undefined =>
    verifyChecks?.find((candidate) => candidate.name === CHECK_NAME_FOR_CREDENTIAL[name]);

  const commit = (
    field: CommitField,
    body: SettingsUpdateRequest,
    onSuccess?: () => void,
  ): void => {
    if (inFlight.current.has(field)) {
      return;
    }
    inFlight.current.add(field);
    void onSave(body)
      .then((succeeded) => {
        if (succeeded) {
          onSuccess?.();
        }
      })
      .finally(() => {
        inFlight.current.delete(field);
      });
  };

  // Fires on blur AND on Enter — never gated behind a submit button anymore.
  // Only sends the one field, only when its draft is non-empty and no save
  // for that same field is already in flight; the draft clears once the save
  // actually succeeds (a failed save leaves the typed value in place rather
  // than silently discarding it).
  const commitAnthropic = (): void => {
    const trimmed = anthropicApiKey.trim();
    if (trimmed === '') {
      return;
    }
    commit('anthropic_api_key', { anthropic_api_key: trimmed }, () => {
      setAnthropicApiKey('');
    });
  };

  const commitAmika = (): void => {
    const trimmed = amikaApiKey.trim();
    if (trimmed === '') {
      return;
    }
    commit('amika_api_key', { amika_api_key: trimmed }, () => {
      setAmikaApiKey('');
    });
  };

  const commitDevin = (): void => {
    const trimmed = devinApiKey.trim();
    if (trimmed === '') {
      return;
    }
    commit('devin_api_key', { devin_api_key: trimmed }, () => {
      setDevinApiKey('');
    });
  };

  const commitGithub = (): void => {
    const trimmed = githubAuthToken.trim();
    if (trimmed === '') {
      return;
    }
    commit('github_auth_token', { github_auth_token: trimmed }, () => {
      setGithubAuthToken('');
    });
  };

  // Not a secret — the field just shows the live value, so there's nothing
  // to clear on success; only save when it actually changed.
  const commitCredId = (): void => {
    const trimmed = amikaClaudeCredId.trim();
    if (trimmed === '' || trimmed === settings.amika_claude_cred_id) {
      return;
    }
    commit('amika_claude_cred_id', { amika_claude_cred_id: trimmed });
  };

  return (
    <form data-role="settings-form">
      {SHOW_ANTHROPIC_KEY_FIELD && (
        <SecretCredentialRow
          name="anthropic_api_key"
          label="Anthropic API key"
          icon={<KeyIcon />}
          status={settings.anthropic_api_key}
          value={anthropicApiKey}
          onChange={setAnthropicApiKey}
          onCommit={commitAnthropic}
          pendingCredentials={pendingCredentials}
          verifying={verifying}
          check={checkFor('anthropic_api_key')}
        />
      )}

      <SecretCredentialRow
        name="amika_api_key"
        label="Amika API key"
        icon={<SparkIcon />}
        status={settings.amika_api_key}
        value={amikaApiKey}
        onChange={setAmikaApiKey}
        onCommit={commitAmika}
        pendingCredentials={pendingCredentials}
        verifying={verifying}
        check={checkFor('amika_api_key')}
      />

      <SecretCredentialRow
        name="devin_api_key"
        label="Devin API key"
        icon={<BotIcon />}
        status={settings.devin_api_key}
        value={devinApiKey}
        onChange={setDevinApiKey}
        onCommit={commitDevin}
        pendingCredentials={pendingCredentials}
        verifying={verifying}
        check={checkFor('devin_api_key')}
      />

      <SecretCredentialRow
        name="github_auth_token"
        label="GitHub token"
        icon={<GitHubIcon />}
        status={settings.github_auth_token}
        value={githubAuthToken}
        onChange={setGithubAuthToken}
        onCommit={commitGithub}
        pendingCredentials={pendingCredentials}
        verifying={verifying}
        check={checkFor('github_auth_token')}
      />

      {/* The one non-secret field: no placeholder tail, no verify check, so it
          renders through the same row shape but without an indicator. */}
      <div data-role="credential-row" data-name="amika_claude_cred_id">
        <label htmlFor="credential-amika-claude-cred-id">
          <span data-role="credential-icon" aria-hidden="true">
            <IdIcon />
          </span>
          Amika Claude credential ID
        </label>
        <span data-role="credential-input-row">
          <input
            id="credential-amika-claude-cred-id"
            type="text"
            value={amikaClaudeCredId}
            onChange={(event: ChangeEvent<HTMLInputElement>) => {
              setAmikaClaudeCredId(event.target.value);
            }}
            onBlur={commitCredId}
            onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                commitCredId();
              }
            }}
          />
        </span>
        <span data-role="credential-hint">
          Not a secret — the id Amika resolves your Claude credential by.
        </span>
      </div>
    </form>
  );
}
