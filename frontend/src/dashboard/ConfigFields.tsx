// The dashboard's project form (11 §5): name, repo, snapshot, and workers,
// seeded from the current `Me` and submitted explicitly via its save button.
//
// The repo is CHOSEN, not typed: `RepoField` lists the repos of the connected
// GitHub account. Connecting is the repo-scoped "Connect GitHub" grant — a
// separate, explicit act from signing in, which grants nothing (11 §2 D2) — so
// the picker's disconnected state is the ordinary state until the user runs it,
// and it points at the same grant the Integrations card does.
//
// CREATING a project asks for the repo and nothing else that has an answer
// already (auto-name from repository): there is no name field, the name comes
// from the picked repo via `projectNameFromRepoUrl`, and the picker leads the
// form instead of sitting under a name box the user had to fill in before they
// had chosen anything. Editing an existing project still offers the name field —
// the two modes differ only in that one field and in the arrangement around it.
//
// Per-user credentials are NOT here — they live in `Integrations.tsx` as a card
// per provider (GitHub via that OAuth grant, the rest via a paste-your-key
// modal).
import {
  useEffect,
  useState,
  type ChangeEvent,
  type FormEvent,
  type JSX,
  type ReactNode,
} from 'react';
import type {
  DevBox,
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  SaveSnapshotRequest,
  Snapshot,
} from '@/transport/transport';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import { projectNameFromRepoUrl } from '@/dashboard/project-name';
import { snapshotNameFor } from '@/dashboard/snapshot-name';
import { GITHUB_DASHBOARD_RETURN_PATH, GITHUB_SETUP_PATH } from '@/auth/github-connect';

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
 * typed (settings repo picker). Exported because the guided setup flow
 * (`Onboarding`) renders the same picker as its own step: there is exactly one
 * repo-picking control in the app, so a free-text fallback can't drift back in
 * through a second implementation.
 *
 * Three states, in the order a user meets them:
 *
 *  1. loading — a quiet placeholder, so the connect prompt never flashes up
 *     before we know whether the account is actually connected;
 *  2. disconnected — the "Connect GitHub account" link, pointed at the one
 *     grant, the same route every other entry point uses, asked to end back
 *     here rather than in the app. A user reaches this state by having signed
 *     in before Kiln asked for the
 *     repo scope, or by having revoked it. A project that already has a repo_url keeps it: it is
 *     shown read-only and still submitted, so editing an unrelated field on an
 *     older project can't silently unlink its repo;
 *  3. connected — the repo dropdown, plus a "Switch account" link into GitHub's
 *     own chooser, where the account and the repository selection live. The
 *     plain connect route would be a no-op here: this user has authorized
 *     already, so it completes without ever showing them a screen. The dropdown carries no
 *     filter box: a native select already types-to-jump, so a second search
 *     control beside it only raised the question of which one to use. */
export function RepoField({ value, onChange, github }: RepoFieldProps): JSX.Element {
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
        {/* A backend route, so a real navigation — never a router Link. The
            dashboard-return form: the grant is a step in editing this project,
            and landing in the app would abandon the form mid-edit. */}
        <a href={GITHUB_DASHBOARD_RETURN_PATH} data-role="connect-github">
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
      {/* The setup route, not the plain one: this user is already connected, so
          signing in again would complete silently and show them nothing. What
          they want is GitHub's own chooser. */}
      <a href={GITHUB_SETUP_PATH} data-role="switch-github">
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
  /** Absent when a project is being CREATED — the form then drops its name field
   * (the name comes from the picked repo) and leads with the repo picker. */
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
   * the snapshot field becomes a picker and the capture control appears beneath
   * it. `false` (the default) keeps the free-text snapshot input, so a provider
   * without a catalog (or onboarding, before a project exists) is unchanged. */
  catalogAvailable?: boolean;
  /** This project's running dev boxes — what the capture control offers as
   * sources. Populated by `onRefreshDevBoxes`. */
  devBoxes?: DevBox[];
  /** Loads/refreshes `devBoxes` (GET /api/projects/{id}/dev-boxes). Pass the
   * catalog's own function, not a wrapper: the capture control loads the list in
   * an effect keyed on this callback, so a fresh closure per render would refetch
   * on every one of them. */
  onRefreshDevBoxes?: () => Promise<void>;
  /** Captures a dev box as a new snapshot (POST /api/projects/{id}/snapshots).
   * Absent on every surface but the settings modal, which is what keeps the
   * capture off onboarding and the app-native projects page. */
  onSaveSnapshot?: (body: SaveSnapshotRequest) => Promise<Snapshot>;
  /** Which shell the same fields render in (projects-in-a-modal):
   *
   *  * `form` (the default) — the flat field list onboarding and the app-native
   *    projects page have always used. Unchanged, deliberately: those two
   *    surfaces style it themselves and their DOM must not move under them.
   *  * `detail` — the settings project modal: an identity header (the name,
   *    edited in place, beside the repository it is linked to — or, creating,
   *    the repository alone with the name it implies) over grouped Agent and
   *    Sandbox sections. Same state, same submit body — only the arrangement
   *    differs. */
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

interface SandboxInfoProps {
  catalogAvailable: boolean;
  snapshots: Snapshot[];
  /** The `amika_snapshot` handle the form currently holds; '' means "default". */
  selectedRef: string;
}

/** What the snapshot picker above actually means, in words (projects-in-a-modal,
 * "sandbox info"). Only rendered where the picker itself leaves something unsaid:
 * nothing picked (what "default" gets you, why a snapshot is worth picking, and
 * that a saved ticket sandbox lands here on its own), or a stored handle the
 * catalog no longer lists. A snapshot picked *from* the catalog needs no reading
 * — the option label above already names it — and with no catalog at all the
 * field is a free-text handle that speaks for itself.
 *
 * This copy is the only in-product explanation of how snapshots work, and the
 * only warning that a ticket's own sandbox option will change this selection by
 * itself, so it stays until something else says those things. */
function SandboxInfo({
  catalogAvailable,
  snapshots,
  selectedRef,
}: SandboxInfoProps): JSX.Element | null {
  if (!catalogAvailable) {
    return null;
  }

  if (selectedRef === '') {
    return (
      <div data-role="sandbox-info" data-state="default">
        <p>
          Workers start from the deployment&apos;s default Amika image. Pick a snapshot to start
          them pre-warmed instead — dependencies installed, repo cloned, tools already authenticated
          — so a ticket begins with work rather than with setup. Save one below, or turn on
          &ldquo;Start future tickets from this sandbox&rdquo; for a ticket, which adds its finished
          workspace here as a snapshot and selects it without asking again.
        </p>
      </div>
    );
  }

  if (snapshots.some((snap) => snap.ref === selectedRef)) {
    return null;
  }

  return (
    <div data-role="sandbox-info" data-state="unlisted">
      <p>
        Workers start from <code>{selectedRef}</code>, which this project&apos;s catalog no longer
        lists — it may have been deleted, or belong to another Amika account. It stays in use until
        you pick another snapshot.
      </p>
    </div>
  );
}

interface SnapshotCaptureProps {
  /** The project's name, which the captured snapshot is named after. */
  projectName: string;
  devBoxes: DevBox[];
  onRefresh: () => Promise<void>;
  onCapture: (body: SaveSnapshotRequest) => Promise<Snapshot>;
}

/** Save a snapshot: capture one of this project's running dev boxes as a base
 * image its workers can start from. The counterpart to the snapshot PICKER above
 * it — that one chooses among the images you have, this one makes another.
 *
 * It is deliberately not the same thing as the ticket sheet's "Start future
 * tickets from this sandbox", and the difference is worth keeping straight: that
 * switch is a standing instruction the server acts on when a ticket finishes,
 * and it repoints the project at what it captured. This is a thing the user does
 * now, to a box they picked, and it changes no selection — the new snapshot
 * lands in the picker and waits to be chosen.
 *
 * Three things shape it:
 *
 *  * **The capture consumes its source.** The provider scrubs the dev box's
 *    injected secrets and deletes it. That is not a detail to bury in a hint: it
 *    is what the user is agreeing to, so it is said above the control AND in a
 *    confirm naming the box, the same gate the ticket sheet's destructive
 *    overrides use.
 *  * **The name is derived, not typed** (`snapshotNameFor`) — the same
 *    `<project>-<timestamp>` shape the server gives a capture a finished ticket
 *    triggered, so a catalog holds one kind of name however its entries got
 *    there. The name is reported back after the fact, which is when it is
 *    actually useful: it is what to look for in the picker.
 *  * **A capture is slow and runs in the background**, so a submit resolves long
 *    before the snapshot is selectable. The status line says so rather than
 *    leaving the user watching a picker that hasn't changed. */
function SnapshotCapture({
  projectName,
  devBoxes,
  onRefresh,
  onCapture,
}: SnapshotCaptureProps): JSX.Element {
  const [selectedRef, setSelectedRef] = useState('');
  const [capturing, setCapturing] = useState(false);
  const [captured, setCaptured] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  // Load the dev boxes when the control appears, so its select is populated
  // without the user hunting for a refresh button. `onRefresh` is a stable
  // store-backed callback, so this runs once per open.
  useEffect(() => {
    void onRefresh();
  }, [onRefresh]);

  const selected = devBoxes.find((box) => box.ref === selectedRef);

  const handleCapture = (): void => {
    if (selected === undefined || capturing) {
      return;
    }
    // The one destructive gate. A native confirm naming the box it is about to
    // consume, like the ticket sheet's sandbox overrides — the user is spending
    // a dev box, not just filing a copy of it.
    if (
      !window.confirm(
        `Save “${selected.name}” as a snapshot? The dev box is deleted once it has been captured, and that can't be undone.`,
      )
    ) {
      return;
    }
    const name = snapshotNameFor(projectName, new Date());
    setCapturing(true);
    setFailed(false);
    setCaptured(null);
    void onCapture({ dev_box_ref: selected.ref, name })
      .then(() => {
        setCaptured(name);
        // The box this captured is gone, so the selection that named it is too.
        setSelectedRef('');
      })
      .catch(() => {
        setFailed(true);
      })
      .finally(() => {
        setCapturing(false);
      });
  };

  return (
    <div data-role="save-snapshot">
      <h4>Save a snapshot</h4>
      <p data-role="save-snapshot-hint">
        Capture a running dev box as a base image this project&apos;s workers can start from. The
        dev box is deleted once it has been captured.
      </p>
      {devBoxes.length === 0 ? (
        <p data-role="save-snapshot-empty">
          No running dev boxes to capture. Start one with your coding-agent provider and it appears
          here.
        </p>
      ) : (
        <div data-role="save-snapshot-controls">
          <label>
            Dev box
            <select
              data-role="dev-box-select"
              value={selectedRef}
              onChange={(event: ChangeEvent<HTMLSelectElement>) => {
                setSelectedRef(event.target.value);
              }}
            >
              <option value="">Select a dev box…</option>
              {devBoxes.map((box) => (
                <option key={box.ref} value={box.ref}>
                  {box.name} ({box.status})
                </option>
              ))}
            </select>
          </label>
          {/* type="button" is load-bearing: this control lives inside the
              project form, and a default submit button would save the project
              instead of capturing anything.

              Not dressed as destructive, though it consumes the box: what the
              user came here to do is SAVE something, and a red button on that
              verb reads as a warning about the wrong half of it. The consequence
              is said in words above and again in the confirm. */}
          <button
            type="button"
            data-role="save-snapshot-submit"
            disabled={selected === undefined || capturing}
            onClick={handleCapture}
          >
            {capturing ? 'Saving…' : 'Save snapshot'}
          </button>
        </div>
      )}
      {captured !== null && (
        <p data-role="save-snapshot-status" data-state="captured">
          Saving <code>{captured}</code>. It appears in the picker above, ready to select, once the
          capture finishes.
        </p>
      )}
      {failed && (
        <p data-role="save-snapshot-status" data-state="failed" role="alert">
          That capture didn&apos;t start, so the dev box is untouched. Try again.
        </p>
      )}
    </div>
  );
}

export function ProjectFields({
  project,
  github,
  providers,
  snapshots,
  catalogAvailable = false,
  devBoxes,
  onRefreshDevBoxes,
  onSaveSnapshot,
  layout = 'form',
  footerLead,
  saving,
  onSave,
}: ProjectFieldsProps): JSX.Element {
  // Creating vs editing. The only two things it changes: where the name comes
  // from (the repo, vs the field below) and how the fields are arranged.
  const creating = project === undefined;
  const [name, setName] = useState(project?.name ?? '');
  const [repoUrl, setRepoUrl] = useState(project?.repo_url ?? '');
  // The per-project coding-agent provider (multi-provider design §9): the stored
  // registry key. The select below only shows when the deployment offers more than
  // one provider.
  const providerOptions = providers ?? [];
  const [agentProvider, setAgentProvider] = useState(project?.agent_provider ?? '');
  // The select is a straight pick between the offered providers — there is no
  // "deployment default" entry — so a project that pinned nothing, or pinned a
  // provider this deployment does not offer (the dev-only mock is registered but
  // never listed), resolves to the first offered provider rather than to a blank
  // the select cannot render. Derived per render rather than seeded into state:
  // the descriptors arrive with `me`, which can land after this form mounts.
  const selectedProvider =
    providerOptions.find((candidate) => candidate.key === agentProvider)?.key ??
    providerOptions[0]?.key ??
    '';
  const [amikaSnapshot, setAmikaSnapshot] = useState(project?.amika_snapshot ?? '');
  const [workerCount, setWorkerCount] = useState(
    project?.worker_count === undefined ? '' : String(project.worker_count),
  );
  const [mergeGateMode, setMergeGateMode] = useState<MergeGateMode>(
    project?.merge_gate_mode ?? 'main',
  );
  // What a new project will be called. Derived on every render rather than held
  // in state, so it can never fall out of step with the picked repo — there is
  // no field to type into and therefore nothing to preserve.
  const derivedName = projectNameFromRepoUrl(repoUrl);
  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    const body: ProjectUpdateRequest = {
      name: creating ? derivedName : name.trim(),
      repo_url: repoUrl.trim(),
    };
    // Send the provider choice only when the deployment offers a real choice; a
    // single-provider deployment omits it so the project keeps resolving to the
    // deployment default (multi-provider design §9). When the select is shown it
    // always carries a concrete provider, so the save pins what the user sees.
    if (providerOptions.length > 1) {
      body.agent_provider = selectedProvider;
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
  const nameField = creating ? null : (
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

  // What the removed field is replaced by: the derived name, said out loud
  // beside the picker. Without it the naming is invisible — the user picks a
  // repo and a board appears called something nobody showed them.
  const nameNote = creating ? (
    <p data-role="project-name-note" data-state={derivedName === '' ? 'unpicked' : 'named'}>
      {derivedName === '' ? (
        'This project will take its name from the repository you pick.'
      ) : (
        <>
          This project will be called <strong data-role="project-name-value">{derivedName}</strong>.
        </>
      )}
    </p>
  ) : null;

  const repoField = <RepoField value={repoUrl} onChange={setRepoUrl} github={github} />;

  const providerField =
    providerOptions.length > 1 ? (
      <label>
        Agent provider
        <select
          data-role="agent-provider"
          value={selectedProvider}
          onChange={(event: ChangeEvent<HTMLSelectElement>) => {
            setAgentProvider(event.target.value);
          }}
        >
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
      {creating ? 'Create project' : 'Save project'}
    </button>
  );

  if (layout !== 'detail') {
    if (creating) {
      // The create step (auto-name from repository): the picker IS the step, so
      // it stands alone at the top with the name it implies under it. The rest
      // keep their server defaults for anyone who just wants a board, and are
      // demoted rather than dropped — a multi-provider deployment must still be
      // able to say which agent runs this project at the moment it is created.
      return (
        <form data-role="project-form" data-mode="new" onSubmit={handleSubmit}>
          <div data-role="new-project-repo">
            {repoField}
            {nameNote}
          </div>
          <section data-role="new-project-options">
            <h3>Options</h3>
            {providerField}
            {snapshotField}
            {workerCountField}
            {mergeGateField}
          </section>
          {submitButton}
        </form>
      );
    }
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
  // question it answers rather than listed flat. Creating, that band holds the
  // repo picker alone (there is no name to edit yet) plus the name it derives.
  return (
    <form
      data-role="project-form"
      data-layout="detail"
      data-mode={creating ? 'new' : undefined}
      onSubmit={handleSubmit}
    >
      <header data-role="project-identity">
        {nameField}
        {repoField}
        {nameNote}
      </header>

      <section data-role="project-group" data-group="agent">
        <h3>Agent</h3>
        <div data-role="project-group-fields">
          {providerField}
          {workerCountField}
          {mergeGateField}
        </div>
      </section>

      <section data-role="project-group" data-group="sandbox">
        <h3>Sandbox</h3>
        <div data-role="project-group-fields">{snapshotField}</div>
        <SandboxInfo
          catalogAvailable={catalogAvailable}
          snapshots={snapshots ?? []}
          selectedRef={amikaSnapshot}
        />
        {/* The capture self-gates on both its callbacks and on the provider
            actually having a catalog to capture into, so a surface that doesn't
            pass them renders nothing extra — which is what keeps it off
            onboarding and the app-native projects page. */}
        {catalogAvailable && onRefreshDevBoxes !== undefined && onSaveSnapshot !== undefined && (
          <SnapshotCapture
            projectName={creating ? derivedName : name}
            devBoxes={devBoxes ?? []}
            onRefresh={onRefreshDevBoxes}
            onCapture={onSaveSnapshot}
          />
        )}
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
