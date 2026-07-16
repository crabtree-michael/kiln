// Account view (11 §5, 12 §4.2): the signed-in user owns one or more projects —
// this is where per-user credentials and the project list live together.
// Composes `CredentialFields` (auto-save per field + auto-verify) once at the
// account level (12 §6.2), then a list of project cards — each the reusable
// `ProjectFields` form targeting `PUT /api/projects/{id}` with a Delete — plus a
// "New project" affordance that runs the same form against `POST /api/projects`.
import { useCallback, useState, type JSX } from 'react';
import { Link } from 'react-router-dom';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { CredentialFields, ProjectFields } from '@/dashboard/ConfigFields';
import { NotificationsField } from '@/dashboard/NotificationsField';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';

interface ProjectCardProps {
  project: MeProject;
  providers: ProviderDescriptor[];
  saving: boolean;
  onSave: (id: string, body: ProjectUpdateRequest) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

/** One project's edit form + delete, keyed on its id (12 §4.2). Delete is
 * behind a confirm since the cascade is destructive and irreversible (12 §5). */
function ProjectCard({
  project,
  providers,
  saving,
  onSave,
  onDelete,
}: ProjectCardProps): JSX.Element {
  const save = useCallback(
    (body: ProjectUpdateRequest): Promise<void> => onSave(project.id, body),
    [onSave, project.id],
  );
  const handleDelete = useCallback((): void => {
    // A native confirm keeps the destructive gate simple; the app never mutates
    // the board directly, but a project delete is a real cascade (12 §5).
    if (window.confirm(`Delete project “${project.name}”? This can't be undone.`)) {
      void onDelete(project.id);
    }
  }, [onDelete, project.id, project.name]);

  return (
    <section data-role="project-card" data-project-id={project.id}>
      <ProjectFields project={project} providers={providers} saving={saving} onSave={save} />
      <button type="button" data-role="delete-project" disabled={saving} onClick={handleDelete}>
        Delete project
      </button>
    </section>
  );
}

export function Settings(): JSX.Element {
  const {
    me,
    saving,
    saveSettings,
    createProject,
    updateProject,
    removeProject,
    verifying,
    verifyChecks,
    pendingCredentials,
    signOut,
    error,
  } = useDashboardStore();
  const [creating, setCreating] = useState(false);
  if (me === null) {
    // See Onboarding's identical guard: Dashboard only mounts this view for a
    // populated `me` — narrows the type without an escape hatch.
    throw new Error('Settings rendered without a signed-in account');
  }
  const providers = me.providers ?? [];

  const handleCreate = useCallback(
    async (body: ProjectUpdateRequest): Promise<void> => {
      await createProject(body);
      setCreating(false);
    },
    [createProject],
  );

  return (
    <div data-role="settings">
      {/* Settings is a detour off the board; a quiet link back to the main app
          sits at the very top, above the profile card, so returning is always one
          tap away. `/app` is an SPA route, so it's a router Link (client nav), not
          a full-page anchor like the backend-owned sign-in link. */}
      <Link to="/app" data-role="go-to-app">
        <span aria-hidden="true">←</span> Go to app
      </Link>

      <section data-role="account-card">
        <img src={me.user.avatar_url} alt="" data-role="account-avatar" />
        <div data-role="account-identity">
          <div data-role="account-name">{me.user.display_name || `@${me.user.github_login}`}</div>
          <div data-role="account-login">@{me.user.github_login}</div>
        </div>
        <button
          type="button"
          disabled={saving}
          onClick={() => {
            void signOut();
          }}
        >
          Sign out
        </button>
      </section>

      <CredentialFields
        settings={me.settings}
        pendingCredentials={pendingCredentials}
        verifying={verifying}
        verifyChecks={verifyChecks}
        onSave={saveSettings}
      />

      <NotificationsField />

      <div data-role="project-list">
        {me.projects.map((project) => (
          <ProjectCard
            key={project.id}
            project={project}
            providers={providers}
            saving={saving}
            onSave={updateProject}
            onDelete={removeProject}
          />
        ))}
      </div>

      {creating ? (
        <section data-role="new-project-card">
          <h2>New project</h2>
          <ProjectFields providers={providers} saving={saving} onSave={handleCreate} />
          <button
            type="button"
            data-role="cancel-new-project"
            onClick={() => {
              setCreating(false);
            }}
          >
            Cancel
          </button>
        </section>
      ) : (
        <button
          type="button"
          data-role="new-project"
          onClick={() => {
            setCreating(true);
          }}
        >
          New project
        </button>
      )}

      {error !== null ? (
        <p data-role="dashboard-error" role="alert">
          {error}
        </p>
      ) : null}

      <p data-role="dashboard-footnote">
        Open kiln on your phone at trykiln.dev — the app itself doesn&apos;t need sign-in yet.
      </p>
    </div>
  );
}
