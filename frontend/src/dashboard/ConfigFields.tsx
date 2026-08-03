// The dashboard's project form (11 §5): name, repo, snapshot, and workers,
// seeded from the current `Me` and submitted explicitly via its save button.
//
// The repo is CHOSEN, not typed: `RepoField` lists the repos of the connected
// GitHub account. Connecting is the repo-scoped "Connect GitHub" grant — a
// separate, explicit act from signing in, which grants nothing (11 §2 D2) — so
// the picker's disconnected state is the ordinary state until the user runs it,
// and it points at the same grant the Integrations card does.
//
// Per-user credentials are NOT here — they live in `Integrations.tsx` as a card
// per provider (GitHub via that OAuth grant, the rest via a paste-your-key
// modal).
import { useState, type ChangeEvent, type FormEvent, type JSX, type ReactNode } from 'react';
import type {
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  Snapshot,
} from '@/transport/transport';
import { GITHUB_CONNECT_PATH, type GitHubRepos } from '@/dashboard/use-github-repos';

// The merge-gate knob (06 §7): which condition marks a ticket done — its work
// merged to main, or merely in a pull request. Non-optional here (the form
// always carries a concrete choice) even though the request field is optional.
type MergeGateMode = NonNullable<ProjectUpdateRequest['merge_gate_mode']>;

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
 *  2. disconnected — the "Connect GitHub account" link, pointed at the same
 *     repo-scoped grant the Integrations card uses. Signing in grants no scopes,
 *     so that grant — not a second sign-in — is what yields an account able to
 *     list repos. A project that already has a repo_url keeps it: it is
 *     shown read-only and still submitted, so editing an unrelated field on an
 *     older project can't silently unlink its repo;
 *  3. connected — the repo dropdown, plus a "Switch account" link that re-runs
 *     the same grant against a different GitHub login. The dropdown carries no
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
  /** An extra control for the leading (left) edge of the `detail` footer, beside
   * the save button — in practice the modal's delete. A rendered node rather
   * than an `onDelete` callback on purpose: deleting a project is the shell's
   * business (it owns the confirm and closes on success), and the form has no
   * reason to grow an action that isn't its own submit. Ignored by the `form`
   * layout, whose DOM must not move under the surfaces that style it. */
  footerLead?: ReactNode;
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
  footerLead,
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

      {/* One action bar: the destructive action at the leading edge (the shell's,
          passed in), the committing one at the trailing edge. Save is the thing
          you came here to press, so it sits where the eye finishes. */}
      <footer data-role="project-form-actions">
        {footerLead}
        {submitButton}
      </footer>
    </form>
  );
}
