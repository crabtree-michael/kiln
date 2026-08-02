// Account view (11 §5, 12 §4.2): the signed-in user owns one or more projects —
// this is where per-user credentials and the project list live together.
//
// Desktop-first layout (settings redesign). The page is a two-column shell — a
// sticky section nav on the left, a column of section cards on the right — which
// is the pattern mature desktop settings surfaces converged on (Vercel/Stripe
// project settings, GitHub's account settings): a handful of named sections, a
// persistent index of them, and an active-state highlight so you always know
// where you are. Two deliberate choices inside that pattern:
//
//   * The nav scrolls to sections, it does not swap panes. Every field stays
//     mounted, so browser find-in-page works, a deep link to a section can't
//     hide the rest of the page, and no credential input is ever behind a tab
//     the user has to discover first.
//   * Icons decorate, they never replace a label. Every nav item, section
//     header, and control keeps its text; the icon is the thing your eye lands
//     on while scanning, which is what lets the type stay small and the rows
//     stay compact.
//
// Composes `CredentialFields` (auto-save per field + auto-verify) once at the
// account level (12 §6.2), then a list of project cards — each the reusable
// `ProjectFields` form targeting `PUT /api/projects/{id}` with a Delete — plus a
// "New project" affordance that runs the same form against `POST /api/projects`.
import { useCallback, useEffect, useState, type JSX, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { CredentialFields, ProjectFields } from '@/dashboard/ConfigFields';
import { NotificationsField } from '@/dashboard/NotificationsField';
import { useSandboxCatalog } from '@/dashboard/use-sandbox-catalog';
import {
  GITHUB_CONNECT_PATH,
  useGitHubRepos,
  type GitHubRepos,
} from '@/dashboard/use-github-repos';
import {
  ArrowLeftIcon,
  BellIcon,
  BoxIcon,
  FolderIcon,
  GitHubIcon,
  PlugIcon,
  PlusIcon,
  SignOutIcon,
  TrashIcon,
  UserIcon,
} from '@/dashboard/icons';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';

interface SectionDef {
  /** The DOM id the nav anchors to; also the `data-section` value. */
  id: string;
  /** The nav item's text. */
  label: string;
  /** The one-line "what lives here" under the section heading. */
  description: string;
  Icon: () => JSX.Element;
}

// The four sections, each named so the JSX below references it directly — no
// id lookup, no index, nothing that can silently miss.
const ACCOUNT_SECTION: SectionDef = {
  id: 'account',
  label: 'Account',
  description: 'The GitHub identity this Kiln account is signed in as.',
  Icon: UserIcon,
};
const INTEGRATIONS_SECTION: SectionDef = {
  id: 'integrations',
  label: 'Integrations',
  description: 'Credentials Kiln uses to reach your coding agents, sandboxes, and repositories.',
  Icon: PlugIcon,
};
const NOTIFICATIONS_SECTION: SectionDef = {
  id: 'notifications',
  label: 'Notifications',
  description: 'How Kiln reaches you when the app is closed or in the background.',
  Icon: BellIcon,
};
const PROJECTS_SECTION: SectionDef = {
  id: 'projects',
  label: 'Projects',
  description: 'Each project has its own repository, worker pool, and sandbox configuration.',
  Icon: BoxIcon,
};

/** The page's sections in reading order — one source of truth for both the nav
 * and the section headers, so the two can never drift apart. Kept to four:
 * scannable at a glance, which is the whole point of grouping. */
const SECTIONS: readonly SectionDef[] = [
  ACCOUNT_SECTION,
  INTEGRATIONS_SECTION,
  NOTIFICATIONS_SECTION,
  PROJECTS_SECTION,
];

/** Which section the nav highlights: the topmost one currently in the reading
 * band near the top of the viewport. Falls back to the first section — and never
 * throws — where `IntersectionObserver` is missing (jsdom, older browsers), so
 * the nav degrades to a plain, still-working table of contents. */
function useActiveSection(): string {
  const [active, setActive] = useState(ACCOUNT_SECTION.id);

  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') {
      return;
    }
    const visible = new Set<string>();
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            visible.add(entry.target.id);
          } else {
            visible.delete(entry.target.id);
          }
        }
        // First in document order wins, so scrolling down promotes the next
        // section only once the previous one has left the band.
        const current = SECTIONS.find((section) => visible.has(section.id));
        if (current !== undefined) {
          setActive(current.id);
        }
      },
      // A band across the top of the viewport: a section counts as "the one
      // you're reading" from when its heading reaches ~12% down until it passes
      // the 30% line.
      { rootMargin: '-12% 0px -70% 0px' },
    );
    for (const section of SECTIONS) {
      const element = document.getElementById(section.id);
      if (element !== null) {
        observer.observe(element);
      }
    }
    return () => {
      observer.disconnect();
    };
  }, []);

  return active;
}

interface SettingsSectionProps {
  section: SectionDef;
  /** Optional right-aligned control in the section header (e.g. "New project"). */
  action?: ReactNode;
  children: ReactNode;
}

/** One section card: an icon tile + heading + description, then its content. The
 * heading is an `h2` under the page's single `h1`, so the section list doubles as
 * the document outline a screen reader navigates by. */
function SettingsSection({ section, action, children }: SettingsSectionProps): JSX.Element {
  const { Icon } = section;
  return (
    <section
      id={section.id}
      data-role="settings-section"
      data-section={section.id}
      aria-labelledby={`${section.id}-heading`}
    >
      <header data-role="section-header">
        <span data-role="section-icon">
          <Icon />
        </span>
        <div data-role="section-heading-text">
          <h2 id={`${section.id}-heading`}>{section.label}</h2>
          <p data-role="section-description">{section.description}</p>
        </div>
        {action !== undefined ? <div data-role="section-action">{action}</div> : null}
      </header>
      <div data-role="section-body">{children}</div>
    </section>
  );
}

/** The account-level GitHub connection, shown at the top of Integrations. Sign-in
 * IS the GitHub connection (one OAuth flow, one callback — see `useGitHubRepos`),
 * so "connect" and "switch account" are both just `/auth/github/login` again; this
 * row exists to make that state legible in one place instead of only surfacing
 * inside each project's repo picker. */
function GitHubConnectionRow({ github }: { github: GitHubRepos }): JSX.Element {
  const state = github.loading ? 'loading' : github.connected ? 'connected' : 'disconnected';
  const detail =
    state === 'loading'
      ? 'Checking your GitHub connection…'
      : state === 'connected'
        ? `${String(github.repos.length)} ${github.repos.length === 1 ? 'repository' : 'repositories'} available to link to a project.`
        : 'Connect to pick project repositories, including private ones.';

  return (
    <div data-role="github-connection" data-state={state}>
      <span data-role="integration-icon">
        <GitHubIcon />
      </span>
      <div data-role="integration-text">
        <span data-role="integration-name">GitHub account</span>
        <span data-role="integration-detail">{detail}</span>
      </div>
      <span data-role="integration-state" data-state={state}>
        {state === 'loading' ? 'Checking…' : state === 'connected' ? 'Connected' : 'Not connected'}
      </span>
      {/* A backend route, so a real navigation — never a router Link. */}
      {state !== 'loading' ? (
        <a href={GITHUB_CONNECT_PATH} data-role="github-connection-action">
          {state === 'connected' ? 'Switch account' : 'Connect'}
        </a>
      ) : null}
    </div>
  );
}

interface ProjectCardProps {
  project: MeProject;
  providers: ProviderDescriptor[];
  /** The account-level GitHub connection, loaded once by `Settings` and shared
   * by every card — the credential is per-user, so per-card fetching would be
   * the same request N times. */
  github: GitHubRepos;
  saving: boolean;
  onSave: (id: string, body: ProjectUpdateRequest) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}

/** One project's edit form + delete, keyed on its id (12 §4.2). The card leads
 * with an identity header (name + repo) so a multi-project account can be scanned
 * without reading any form; Delete is a compact icon button in that header,
 * behind a confirm since the cascade is destructive and irreversible (12 §5). */
function ProjectCard({
  project,
  providers,
  github,
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
  // This project's own snapshot catalog (sandbox selection): the picker + capture
  // form appear only when its provider exposes a catalog (a managed-sandbox
  // provider), else ProjectFields falls back to the free-text snapshot handle.
  const catalog = useSandboxCatalog(project.id);

  return (
    <section data-role="project-card" data-project-id={project.id}>
      <header data-role="project-card-header">
        <span data-role="project-card-icon">
          <FolderIcon />
        </span>
        <div data-role="project-card-identity">
          <span data-role="project-card-name">{project.name}</span>
          <span data-role="project-card-repo">{project.repo_url || 'No repository linked'}</span>
        </div>
        <button
          type="button"
          data-role="delete-project"
          data-variant="danger"
          disabled={saving}
          onClick={handleDelete}
        >
          <TrashIcon />
          Delete project
        </button>
      </header>
      <ProjectFields
        project={project}
        github={github}
        providers={providers}
        snapshots={catalog.snapshots}
        catalogAvailable={catalog.catalogAvailable}
        devBoxes={catalog.devBoxes}
        onRefreshDevBoxes={() => {
          void catalog.refreshDevBoxes();
        }}
        onSaveSnapshot={catalog.saveSnapshot}
        saving={saving}
        onSave={save}
      />
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
  // One fetch for the whole page: the GitHub connection is per-user, so every
  // project card and the new-project form pick from the same repo list.
  const github = useGitHubRepos();
  const activeSection = useActiveSection();
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
          sits at the very top, above everything else, so returning is always one
          click away. `/app` is an SPA route, so it's a router Link (client nav),
          not a full-page anchor like the backend-owned sign-in link. */}
      <header data-role="settings-masthead">
        <Link to="/app" data-role="go-to-app">
          <ArrowLeftIcon />
          Go to app
        </Link>
        <h1>Settings</h1>
      </header>

      {error !== null ? (
        <p data-role="dashboard-error" role="alert">
          {error}
        </p>
      ) : null}

      <div data-role="settings-layout">
        <nav data-role="settings-nav" aria-label="Settings sections">
          {SECTIONS.map((section) => {
            const { Icon } = section;
            return (
              <a
                key={section.id}
                href={`#${section.id}`}
                data-role="settings-nav-item"
                data-section={section.id}
                data-active={section.id === activeSection}
                aria-current={section.id === activeSection ? 'true' : undefined}
              >
                <Icon />
                {section.label}
              </a>
            );
          })}
        </nav>

        <div data-role="settings-panes">
          <SettingsSection section={ACCOUNT_SECTION}>
            <div data-role="account-card">
              <img src={me.user.avatar_url} alt="" data-role="account-avatar" />
              <div data-role="account-identity">
                <div data-role="account-name">
                  {me.user.display_name || `@${me.user.github_login}`}
                </div>
                <div data-role="account-login">@{me.user.github_login}</div>
              </div>
              <button
                type="button"
                data-role="sign-out"
                disabled={saving}
                onClick={() => {
                  void signOut();
                }}
              >
                <SignOutIcon />
                Sign out
              </button>
            </div>
          </SettingsSection>

          <SettingsSection section={INTEGRATIONS_SECTION}>
            <GitHubConnectionRow github={github} />
            <CredentialFields
              settings={me.settings}
              pendingCredentials={pendingCredentials}
              verifying={verifying}
              verifyChecks={verifyChecks}
              onSave={saveSettings}
            />
          </SettingsSection>

          <SettingsSection section={NOTIFICATIONS_SECTION}>
            <NotificationsField />
          </SettingsSection>

          <SettingsSection
            section={PROJECTS_SECTION}
            action={
              creating ? null : (
                <button
                  type="button"
                  data-role="new-project"
                  data-variant="primary"
                  onClick={() => {
                    setCreating(true);
                  }}
                >
                  <PlusIcon />
                  New project
                </button>
              )
            }
          >
            {creating ? (
              <section data-role="new-project-card">
                <header data-role="project-card-header">
                  <span data-role="project-card-icon">
                    <FolderIcon />
                  </span>
                  <div data-role="project-card-identity">
                    <span data-role="project-card-name">New project</span>
                    <span data-role="project-card-repo">
                      Pick a repository from your GitHub account.
                    </span>
                  </div>
                  <button
                    type="button"
                    data-role="cancel-new-project"
                    onClick={() => {
                      setCreating(false);
                    }}
                  >
                    Cancel
                  </button>
                </header>
                <ProjectFields
                  github={github}
                  providers={providers}
                  saving={saving}
                  onSave={handleCreate}
                />
              </section>
            ) : null}

            <div data-role="project-list">
              {me.projects.map((project) => (
                <ProjectCard
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
          </SettingsSection>

          <p data-role="dashboard-footnote">
            Open kiln on your phone at trykiln.dev — the app itself doesn&apos;t need sign-in yet.
          </p>
        </div>
      </div>
    </div>
  );
}
