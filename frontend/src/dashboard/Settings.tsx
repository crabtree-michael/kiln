// Account view (11 §5): the signed-in user has a project — this is where
// credentials and project config live together. Composes `CredentialFields`
// (auto-save per field + auto-verify, each with its own right-of-input
// validity indicator — dashboard UX update superseding the old manual "Save
// credentials" / "Test connections" controls) + `ProjectFields` (still an
// explicit "Save project" submit) around the account card and sign-out.
import type { JSX } from 'react';
import { Link } from 'react-router-dom';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { CredentialFields, ProjectFields } from '@/dashboard/ConfigFields';
import { NotificationsField } from '@/dashboard/NotificationsField';

export function Settings(): JSX.Element {
  const {
    me,
    saving,
    saveSettings,
    saveProject,
    verifying,
    verifyChecks,
    pendingCredentials,
    signOut,
    error,
  } = useDashboardStore();
  if (me === null) {
    // See Onboarding's identical guard: Dashboard only mounts this view for a
    // populated `me` — narrows the type without an escape hatch.
    throw new Error('Settings rendered without a signed-in account');
  }
  if (me.project === undefined) {
    throw new Error('Settings rendered without a project');
  }
  const project = me.project;

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

      <ProjectFields
        project={project}
        providers={me.providers ?? []}
        saving={saving}
        onSave={saveProject}
      />

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
