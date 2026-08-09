// Native project-management page (12 follow-up): a dedicated, app-native mobile
// surface for managing and configuring the signed-in user's projects — list,
// create, reconfigure, delete. It replaces the old flow where the app's "Add
// project" affordance dumped the user on the `/dashboard` account view. Where
// `/dashboard` is a settings surface (account + credentials + projects, visited
// as often from a laptop), this page is styled like the app itself and is
// project-only: the desk rail's "New" routes here, opening on the create step
// via `?new=1` (the phone's project menu dropped its own "Add" — creation lives
// on this page, and that menu's subject is the projects that already exist).
//
// Creating is a STEP, not a card in the list: it takes the whole screen, asks
// for the repository and nothing that has an answer already, and names the
// project after the repo it is pointed at (auto-name from repository).
//
// It reuses the dashboard store as its data layer — `DashboardProvider` owns the
// `GET /api/me` load and the project create/update/delete mutations (12 §3.1,
// §5), folding each result back into its own `me.projects`. This page is a second
// *view* over that store, not a second store. Because it mounts its own
// `DashboardProvider` (independent of the app's `SessionProvider`), returning to
// `/app` remounts that provider and refetches `me`, so the header switcher picks
// up any project the user just added or removed here.
import { useCallback, useState, type JSX } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { DashboardProvider } from '@/dashboard/dashboard-store';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { ProjectFields } from '@/dashboard/ConfigFields';
import { useGitHubRepos, type GitHubRepos } from '@/dashboard/use-github-repos';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';
import { GITHUB_CONNECT_PATH } from '@/auth/github-connect';
import '@/projects/ProjectsManager.css';

interface ProjectRowProps {
  project: MeProject;
  providers: ProviderDescriptor[];
  /** The account-level GitHub connection backing the repo picker, loaded once by
   * `ProjectsBody` and shared by every row (the credential is per-user). */
  github: GitHubRepos;
  saving: boolean;
  /** Both resolve whether the write landed (the store folds failures into its
   * `error` rather than rejecting); this row doesn't act on the result. */
  onSave: (id: string, body: ProjectUpdateRequest) => Promise<boolean>;
  onDelete: (id: string) => Promise<boolean>;
}

/** One project as a collapsible row: a summary header (name + repo) that toggles
 * an inline `ProjectFields` edit form + delete. Collapsed by default so a user
 * with several projects sees a compact list, not a wall of forms. Keyed on the
 * project id by the caller (12 §4.2). */
function ProjectRow({
  project,
  providers,
  github,
  saving,
  onSave,
  onDelete,
}: ProjectRowProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const save = useCallback(
    async (body: ProjectUpdateRequest): Promise<void> => {
      await onSave(project.id, body);
    },
    [onSave, project.id],
  );
  const handleDelete = useCallback((): void => {
    // A native confirm keeps the destructive gate simple; a project delete is a
    // real cross-module cascade and can't be undone (12 §5).
    if (window.confirm(`Delete project “${project.name}”? This can't be undone.`)) {
      void onDelete(project.id);
    }
  }, [onDelete, project.id, project.name]);

  return (
    <section data-role="project-row" data-project-id={project.id} data-open={open}>
      <button
        type="button"
        data-role="project-row-toggle"
        aria-expanded={open}
        onClick={() => {
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        <span data-role="project-row-identity">
          <span data-role="project-row-name">{project.name}</span>
          <span data-role="project-row-repo">{project.repo_url || 'No repo set'}</span>
        </span>
        <span data-role="project-row-caret" aria-hidden="true" />
      </button>
      {open ? (
        <div data-role="project-row-body">
          <ProjectFields
            project={project}
            providers={providers}
            github={github}
            saving={saving}
            onSave={save}
          />
          <button type="button" data-role="delete-project" disabled={saving} onClick={handleDelete}>
            Delete project
          </button>
        </div>
      ) : null}
    </section>
  );
}

interface ProjectsBodyProps {
  /** Whether the create step has taken the screen. Owned by `ProjectsScreen`
   * because the header renders from it too — while the step is up it names it
   * and its back control cancels it rather than leaving the app. */
  creating: boolean;
  onOpenCreate: () => void;
  onCloseCreate: () => void;
}

/** The signed-in body of the page: the project list + a create affordance, or —
 * while creating — the create step alone. Only rendered once the store's `phase`
 * is `ready` with a populated `me`. */
function ProjectsBody({ creating, onOpenCreate, onCloseCreate }: ProjectsBodyProps): JSX.Element {
  const { me, saving, error, createProject, updateProject, removeProject } = useDashboardStore();
  // One fetch for the page: the GitHub connection is per-user, so every row's
  // picker and the create step share the same repo list.
  const github = useGitHubRepos();

  if (me === null) {
    // ProjectsManager only mounts this body for a populated `me` — this guard
    // just narrows the type without a TS escape hatch, mirroring Settings.tsx.
    throw new Error('ProjectsBody rendered without a signed-in account');
  }
  const providers = me.providers ?? [];

  const handleCreate = useCallback(
    async (body: ProjectUpdateRequest): Promise<void> => {
      await createProject(body);
      onCloseCreate();
    },
    [createProject, onCloseCreate],
  );

  if (creating) {
    // Starting a project is one question — which repository — so it gets the
    // whole screen rather than a card at the foot of the list, and the list is
    // out of the way while it is answered. The name is no longer asked for at
    // all: `ProjectFields` derives it from the repo (auto-name from repository).
    return (
      <section data-role="new-project-step">
        <p data-role="new-project-lede">
          Pick the repository Kiln should work in. The project takes its name from it.
        </p>
        <ProjectFields
          providers={providers}
          github={github}
          saving={saving}
          onSave={handleCreate}
        />
        {error !== null ? (
          <p data-role="projects-error" role="alert">
            {error}
          </p>
        ) : null}
      </section>
    );
  }

  return (
    <>
      {me.projects.length === 0 ? (
        <p data-role="projects-empty">
          You don&apos;t have any projects yet. Add one to light the kiln.
        </p>
      ) : (
        <div data-role="projects-list">
          {me.projects.map((project) => (
            <ProjectRow
              key={project.id}
              project={project}
              providers={providers}
              github={github}
              saving={saving}
              onSave={updateProject}
              onDelete={removeProject}
            />
          ))}
        </div>
      )}

      <button
        type="button"
        data-role="add-project"
        onClick={() => {
          onOpenCreate();
        }}
      >
        Add project
      </button>

      {error !== null ? (
        <p data-role="projects-error" role="alert">
          {error}
        </p>
      ) : null}

      <Link to="/dashboard" data-role="projects-account-link">
        Account &amp; credentials
      </Link>
    </>
  );
}

/** Branches on the shared dashboard store's `phase`, exactly like `Dashboard.tsx`
 * but rendered inside the app-native chrome: a loading spinner, a native GitHub
 * sign-in prompt (this page sits behind the app, so a signed-out visit is an
 * edge case), or the project list — and, while creating, the create step in
 * place of all of it.
 *
 * The create state lives here rather than in `ProjectsBody` because the header
 * renders from it: the create step is a screen of its own, so the title names it
 * and the back control cancels it instead of leaving for the app. */
function ProjectsScreen(): JSX.Element {
  const { phase, me } = useDashboardStore();
  const [params, setParams] = useSearchParams();
  // The desk rail's "New" routes here with `?new=1`, so the create step opens
  // straight away (one tap from the rail to the picker); a bare `/projects`
  // visit lands on the list.
  const [creating, setCreating] = useState(() => params.get('new') === '1');

  const openCreate = useCallback((): void => {
    setCreating(true);
  }, []);

  const closeCreate = useCallback((): void => {
    setCreating(false);
    // Drop the `?new=1` deep-link once the step is dismissed so a refresh doesn't
    // silently reopen it.
    if (params.has('new')) {
      const next = new URLSearchParams(params);
      next.delete('new');
      setParams(next, { replace: true });
    }
  }, [params, setParams]);

  const signedOut = phase === 'signed-out';
  const loading = !signedOut && (phase === 'loading' || me === null);
  // `?new=1` can arrive before `me` does; the header only takes on the step's
  // chrome once the step is actually on screen, so a slow load still offers the
  // way back to the app.
  const inCreate = creating && !signedOut && !loading;

  return (
    <div data-role="projects-manager" data-mode={inCreate ? 'new' : 'list'}>
      <header data-role="projects-header">
        {inCreate ? (
          // Icon-only, so its accessible name has to come from `aria-label`
          // (and `title` gives a pointer user the same word on hover).
          <button
            type="button"
            data-role="cancel-new-project"
            aria-label="Cancel new project"
            title="Cancel new project"
            onClick={closeCreate}
          >
            <span aria-hidden="true">←</span>
          </button>
        ) : (
          <Link to="/app" data-role="projects-back" aria-label="Back to the app">
            <span aria-hidden="true">←</span>
          </Link>
        )}
        <h1>{inCreate ? 'New project' : 'Projects'}</h1>
      </header>

      {signedOut ? (
        <div data-role="projects-signed-out">
          <p>Sign in to manage your projects.</p>
          <a href={GITHUB_CONNECT_PATH} data-role="projects-sign-in-link">
            Continue with GitHub
          </a>
        </div>
      ) : loading ? (
        <div data-role="projects-loading">
          <span data-role="projects-spinner" />
          Loading…
        </div>
      ) : (
        <ProjectsBody creating={creating} onOpenCreate={openCreate} onCloseCreate={closeCreate} />
      )}
    </div>
  );
}

export function ProjectsManager(): JSX.Element {
  return (
    <DashboardProvider>
      <ProjectsScreen />
    </DashboardProvider>
  );
}
