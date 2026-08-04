// The projects rail (13 §5) — the desktop shell's left region.
//
// It does two jobs at once, on purpose (13 D3): it is the project switcher, and
// it is the ambient "is anything wrong anywhere" layer. Keeping them in one
// place is the whole point — a separate status surface would be a second thing
// to look at, against a premise of one thing you mostly don't look at.
//
// Presentational: it takes rows and callbacks. The polling that produces each
// row's state lives in `use-projects-status`; the selection it drives lives in
// the current-project store.
import { useRef, type JSX, type KeyboardEvent } from 'react';
import type { ProjectState } from '@/stores/project-status';

/** The words behind each state's mark, for assistive tech. `quiet`/`unknown`
 * carry no text for the same reason they carry no dot: absence IS the reading,
 * and narrating "quiet" for every resting project turns the rail into noise in
 * exactly the medium that can least afford it. */
const STATE_LABEL: Record<ProjectState, string> = {
  'needs-you': 'needs you',
  working: 'working',
  quiet: '',
  unknown: '',
};

export interface RailProject {
  id: string;
  name: string;
  state: ProjectState;
}

export interface ProjectsRailProps {
  /** The user's projects, in a STABLE order (13 §5): the session's own
   * created-at ordering, passed straight through. Never re-sorted by urgency —
   * that would move rows under the pointer exactly when they matter. */
  projects: RailProject[];
  currentProjectId: string | null;
  onSelectProject: (id: string) => void;
}

export function ProjectsRail({
  projects,
  currentProjectId,
  onSelectProject,
}: ProjectsRailProps): JSX.Element {
  const listRef = useRef<HTMLUListElement>(null);

  // Arrow-key movement between projects (13 §9: "the keyboard is a first-class
  // way through the app"). Focus moves; it does NOT select — moving through the
  // rail with the keyboard must not re-scope the feed under you any more than
  // hovering does (13 §9: hover reveals, it never acts). Enter/Space on the
  // focused row is the commit, which the native button already gives us.
  const onKeyDown = (event: KeyboardEvent<HTMLUListElement>): void => {
    const keys = ['ArrowDown', 'ArrowUp', 'Home', 'End'];
    if (!keys.includes(event.key)) {
      return;
    }
    const list = listRef.current;
    if (list === null) {
      return;
    }
    const rows = Array.from(list.querySelectorAll<HTMLButtonElement>('[data-role="rail-project"]'));
    if (rows.length === 0) {
      return;
    }
    const active = rows.findIndex((row) => row === document.activeElement);
    let next: number;
    if (event.key === 'Home') {
      next = 0;
    } else if (event.key === 'End') {
      next = rows.length - 1;
    } else if (event.key === 'ArrowDown') {
      next = active === -1 ? 0 : Math.min(active + 1, rows.length - 1);
    } else {
      next = active === -1 ? rows.length - 1 : Math.max(active - 1, 0);
    }
    event.preventDefault();
    rows[next]?.focus();
  };

  return (
    <nav data-role="projects-rail" aria-label="Projects">
      <ul data-role="rail-list" ref={listRef} onKeyDown={onKeyDown}>
        {projects.map((project) => {
          const stateLabel = STATE_LABEL[project.state];
          return (
            <li key={project.id}>
              <button
                type="button"
                data-role="rail-project"
                data-project-id={project.id}
                data-state={project.state}
                data-current={project.id === currentProjectId ? 'true' : undefined}
                aria-current={project.id === currentProjectId ? 'true' : undefined}
                onClick={() => {
                  onSelectProject(project.id);
                }}
              >
                <span data-role="rail-project-name">{project.name}</span>
                {/* The mark. `needs-you` is a filled accent dot; `working` is a
                    muted dot that breathes (the one element permitted to animate
                    on its own, 13 §8); `quiet`/`unknown` render an empty box, so
                    the row's geometry never shifts as a project changes state —
                    a rail that reflows is the opposite of ambient. */}
                <span data-role="rail-project-state" data-state={project.state}>
                  <span data-role="rail-project-dot" aria-hidden="true" />
                  {stateLabel !== '' && <span data-role="visually-hidden">{stateLabel}</span>}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
      {/* Adding a project routes to the app-native project-management page, as
          the mobile switcher's "Add" already does (12 follow-up). A plain anchor,
          not a router Link: `/projects` mounts its own provider tree, and this
          shell is deliberately router-free (same stance as the mobile header's
          dashboard link). The rail switches between projects; it does not
          administer them (13 §5). */}
      <a data-role="rail-new" href="/projects?new=1">
        <span data-role="rail-new-glyph" aria-hidden="true">
          +
        </span>
        New project
      </a>
    </nav>
  );
}
