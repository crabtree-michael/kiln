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
// Composes the `Integrations` section — a connect card per provider, with
// auto-verify — once at the account level (12 §6.2), then a list of project cards — each the reusable
// `ProjectFields` form targeting `PUT /api/projects/{id}` with a Delete — plus a
// "New project" affordance that runs the same form against `POST /api/projects`.
import { useCallback, useEffect, useState, type JSX, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useDashboardStore } from '@/dashboard/dashboard-context';
import { Integrations } from '@/dashboard/Integrations';
import { NotificationsField } from '@/dashboard/NotificationsField';
import { ProjectModal } from '@/dashboard/ProjectModal';
import { useGitHubRepos } from '@/dashboard/use-github-repos';
import {
  ArrowLeftIcon,
  BellIcon,
  BoxIcon,
  ChevronRightIcon,
  FolderIcon,
  PlugIcon,
  PlusIcon,
  SignOutIcon,
  UserIcon,
  UsersIcon,
} from '@/dashboard/icons';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';

/** The `openProjectId` value that means "the create form", not a project. A
 * project id is server-generated, so this can never collide with one. */
const NEW_PROJECT_ID = 'new';

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

/** The short `owner/name` a repo URL reads as in a list. The panel is a summary,
 * so it shows the repo the way GitHub itself names it rather than a full URL that
 * would push everything else off the row (the modal's picker shows the same
 * `full_name`). Anything that doesn't look like `…/owner/name` — an enterprise
 * host, a hand-typed value — falls through unchanged rather than being mangled. */
function repoLabel(repoUrl: string): string {
  const parts = repoUrl
    .trim()
    .replace(/\/+$/, '')
    .replace(/\.git$/, '')
    .split('/')
    .filter((part) => part !== '');
  return parts.length < 2 ? repoUrl.trim() : parts.slice(-2).join('/');
}

interface ProjectPanelProps {
  project: MeProject;
  /** Used only to name the project's provider on its chip — the select itself
   * lives in the modal. */
  providers: ProviderDescriptor[];
  onOpen: (id: string) => void;
}

/** One project as a compact panel (projects-in-a-modal): the identity that tells
 * projects apart — name, repo, worker pool, agent — and nothing else. The whole
 * panel is the button, so the click target is the row rather than a small
 * disclosure caret, and everything configurable sits one deliberate click away in
 * `ProjectModal`. */
function ProjectPanel({ project, providers, onOpen }: ProjectPanelProps): JSX.Element {
  const provider = providers.find((candidate) => candidate.key === project.agent_provider);
  return (
    <button
      type="button"
      data-role="project-panel"
      data-project-id={project.id}
      onClick={() => {
        onOpen(project.id);
      }}
    >
      <span data-role="project-panel-icon">
        <FolderIcon />
      </span>
      <span data-role="project-panel-identity">
        <span data-role="project-panel-name">{project.name}</span>
        <span data-role="project-panel-repo">
          {project.repo_url === '' ? 'No repository linked' : repoLabel(project.repo_url)}
        </span>
      </span>
      <span data-role="project-panel-meta">
        <span data-role="project-panel-chip" data-chip="workers">
          <UsersIcon />
          {project.worker_count} {project.worker_count === 1 ? 'worker' : 'workers'}
        </span>
        <span data-role="project-panel-chip" data-chip="provider">
          {provider?.label ?? 'Default agent'}
        </span>
      </span>
      <span data-role="project-panel-caret">
        <ChevronRightIcon />
      </span>
    </button>
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
  // Which project's modal is open, by id — `'new'` for the create form, `null`
  // for none. An id (not the project object) so the open modal always renders the
  // store's current copy after a save folds the response back in.
  const [openProjectId, setOpenProjectId] = useState<string | null>(null);
  // One fetch for the whole page: the GitHub connection is per-user, so every
  // project panel and the new-project form pick from the same repo list.
  const github = useGitHubRepos();
  const activeSection = useActiveSection();

  const closeModal = useCallback((): void => {
    setOpenProjectId(null);
  }, []);
  const openModal = useCallback((id: string): void => {
    setOpenProjectId(id);
  }, []);

  if (me === null) {
    // See Onboarding's identical guard: Dashboard only mounts this view for a
    // populated `me` — narrows the type without an escape hatch.
    throw new Error('Settings rendered without a signed-in account');
  }
  const providers = me.providers ?? [];
  const creating = openProjectId === NEW_PROJECT_ID;
  // A stale id (the project was deleted in another tab) simply resolves to no
  // project, so the modal doesn't render — never to a crash.
  const openProject = creating
    ? undefined
    : me.projects.find((project) => project.id === openProjectId);

  // The modal's two writes, both resolving "did it land" so it can close on
  // success and keep the typed form on failure (the error shows in the page
  // banner above). Which one a save is depends only on how the modal was opened.
  const saveOpenProject = (body: ProjectUpdateRequest): Promise<boolean> =>
    openProject === undefined ? createProject(body) : updateProject(openProject.id, body);
  const deleteOpenProject = (): Promise<boolean> =>
    openProject === undefined ? Promise.resolve(false) : removeProject(openProject.id);

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
            <Integrations
              settings={me.settings}
              githubLogin={me.user.github_login}
              github={github}
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
              <button
                type="button"
                data-role="new-project"
                data-variant="primary"
                onClick={() => {
                  openModal(NEW_PROJECT_ID);
                }}
              >
                <PlusIcon />
                New project
              </button>
            }
          >
            {me.projects.length === 0 ? (
              <p data-role="projects-empty">
                No projects yet. Add one to give Kiln a repository to work in.
              </p>
            ) : (
              <div data-role="project-list">
                {me.projects.map((project) => (
                  <ProjectPanel
                    key={project.id}
                    project={project}
                    providers={providers}
                    onOpen={openModal}
                  />
                ))}
              </div>
            )}
          </SettingsSection>

          <p data-role="dashboard-footnote">
            Open kiln on your phone at trykiln.dev — the app itself doesn&apos;t need sign-in yet.
          </p>
        </div>
      </div>

      {/* Rendered last, and only while open: the whole of one project's
          configuration, over the list it was opened from. */}
      {creating || openProject !== undefined ? (
        <ProjectModal
          project={openProject}
          providers={providers}
          github={github}
          saving={saving}
          onSave={saveOpenProject}
          onDelete={openProject === undefined ? undefined : deleteOpenProject}
          onClose={closeModal}
        />
      ) : null}
    </div>
  );
}
