// Account view (11 §5): the signed-in user has a project — this is where
// credentials, project config, and connectivity verification all live
// together. Composes `CredentialFields` + `ProjectFields` (the controlled
// forms) around the account card, the "Test connections" verify section, and
// sign-out.
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
    runVerify,
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

      <CredentialFields settings={me.settings} saving={saving} onSave={saveSettings} />

      <ProjectFields project={project} saving={saving} onSave={saveProject} />

      <section data-role="verify-section">
        <button
          type="button"
          disabled={verifying}
          onClick={() => {
            void runVerify();
          }}
        >
          Test connections
        </button>
        <ul data-role="verify-checks">
          {(verifyChecks ?? []).map((check) => (
            <li
              key={check.name}
              data-role="verify-check"
              data-name={check.name}
              data-status={check.status}
            >
              <span data-role="verify-check-name">{check.name}</span>
              <span data-role="verify-check-message">{check.message}</span>
            </li>
          ))}
        </ul>
      </section>

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
