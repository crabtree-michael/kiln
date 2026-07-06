// Account view (11 §5): the signed-in user has a project — this is where
// credentials and project config live together. Composes `CredentialFields`
// (auto-save per field + auto-verify, each with its own right-of-input
// validity indicator — dashboard UX update superseding the old manual "Save
// credentials" / "Test connections" controls) + `ProjectFields` (still an
// explicit "Save project" submit) around the account card and sign-out.
import type { JSX } from 'react';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { CredentialFields, ProjectFields } from '@/dashboard/ConfigFields';

export function Settings(): JSX.Element {
  const {
    me,
    saving,
    saveSettings,
    saveProject,
    verifying,
    verifyChecks,
    pendingCredential,
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
      <section data-role="account-card">
        <img src={me.user.avatar_url} alt="" data-role="account-avatar" />
        <div data-role="account-identity">
          <div data-role="account-name">
            {me.user.display_name || `@${me.user.github_login}`}
          </div>
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
        pendingCredential={pendingCredential}
        verifying={verifying}
        verifyChecks={verifyChecks}
        onSave={saveSettings}
      />

      <ProjectFields project={project} saving={saving} onSave={saveProject} />

      {error !== null ? (
        <p data-role="dashboard-error" role="alert">
          {error}
        </p>
      ) : null}

      <p data-role="dashboard-footnote">
        Open kiln on your phone at this URL — the app itself doesn&apos;t need sign-in yet.
      </p>
    </div>
  );
}
