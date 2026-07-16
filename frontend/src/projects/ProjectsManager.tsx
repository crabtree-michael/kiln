// Native project-management page (12 follow-up): a dedicated, app-native mobile
// surface for managing and configuring the signed-in user's projects — list,
// create, reconfigure, delete. It replaces the old flow where the app's "Add
// project" affordance dumped the user on the `/dashboard` account view. Where
// `/dashboard` is a settings surface (account + credentials + projects, visited
// as often from a laptop), this page is styled like the app itself and is
// project-only: the header switcher's "Add" routes here (opening on the create
// form via `?new=1`).
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
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';
import '@/projects/ProjectsManager.css';

interface ProjectRowProps {
  project: MeProject;
  providers: ProviderDescriptor[];
  saving: boolean;
  onSave: (id: string, body: ProjectUpdateRequest) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

/** One project as a collapsible row: a summary header (name + repo) that toggles
 * an inline `ProjectFields` edit form + delete. Collapsed by default so a user
 * with several projects sees a compact list, not a wall of forms. Keyed on the
 * project id by the caller (12 §4.2). */
function ProjectRow({
  project,
  providers,
  saving,
  onSave,
  onDelete,
}: ProjectRowProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const save = useCallback(
    (body: ProjectUpdateRequest): Promise<void> => onSave(project.id, body),
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
          <ProjectFields project={project} providers={providers} saving={saving} onSave={save} />
          <button type="button" data-role="delete-project" disabled={saving} onClick={handleDelete}>
            Delete project
          </button>
        </div>
      ) : null}
    </section>
  );
}

/** The signed-in body of the page: the project list + a create affordance. Only
 * rendered once the store's `phase` is `ready` with a populated `me`. */
function ProjectsBody(): JSX.Element {
  const { me, saving, error, createProject, updateProject, removeProject } = useDashboardStore();
  const [params, setParams] = useSearchParams();
  // The switcher's "Add" routes here with `?new=1`, so the create form opens
  // straight away (preserving the old one-tap "Add" feel); a bare `/projects`
  // visit lands on the list.
  const [creating, setCreating] = useState(() => params.get('new') === '1');

  if (me === null) {
    // ProjectsManager only mounts this body for a populated `me` — this guard
    // just narrows the type without a TS escape hatch, mirroring Settings.tsx.
    throw new Error('ProjectsBody rendered without a signed-in account');
  }
  const providers = me.providers ?? [];

  const openCreate = useCallback((): void => {
    setCreating(true);
  }, []);

  const closeCreate = useCallback((): void => {
    setCreating(false);
    // Drop the `?new=1` deep-link once the form is dismissed so a refresh doesn't
    // silently reopen it.
    if (params.has('new')) {
      const next = new URLSearchParams(params);
      next.delete('new');
      setParams(next, { replace: true });
    }
  }, [params, setParams]);

  const handleCreate = useCallback(
    async (body: ProjectUpdateRequest): Promise<void> => {
      await createProject(body);
      closeCreate();
    },
    [createProject, closeCreate],
  );

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
              saving={saving}
              onSave={updateProject}
              onDelete={removeProject}
            />
          ))}
        </div>
      )}

      {creating ? (
        <section data-role="new-project-form">
          <h2>New project</h2>
          <ProjectFields providers={providers} saving={saving} onSave={handleCreate} />
          <button
            type="button"
            data-role="cancel-new-project"
            onClick={() => {
              closeCreate();
            }}
          >
            Cancel
          </button>
        </section>
      ) : (
        <button
          type="button"
          data-role="add-project"
          onClick={() => {
            openCreate();
          }}
        >
          Add project
        </button>
      )}

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
 * edge case), or the project list. */
function ProjectsScreen(): JSX.Element {
  const { phase, me } = useDashboardStore();

  return (
    <div data-role="projects-manager">
      <header data-role="projects-header">
        <Link to="/app" data-role="projects-back" aria-label="Back to the app">
          <span aria-hidden="true">←</span>
        </Link>
        <h1>Projects</h1>
      </header>

      {phase === 'signed-out' ? (
        <div data-role="projects-signed-out">
          <p>Sign in to manage your projects.</p>
          <a href="/auth/github/login" data-role="projects-sign-in-link">
            Continue with GitHub
          </a>
        </div>
      ) : phase === 'loading' || me === null ? (
        <div data-role="projects-loading">
          <span data-role="projects-spinner" />
          Loading…
        </div>
      ) : (
        <ProjectsBody />
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
