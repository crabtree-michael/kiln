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

/**
 * The row's state in words: what the mark means, plus how many tickets are
 * being worked when any are.
 *
 * **The count is text, never a badge.** 13 §8 rules out counts and badges in the
 * resting screen by name, and the rail is the surface that can least afford
 * them — it is read out of the corner of an eye. So the number never paints: it
 * rides in the row's `title` (revealed by pointing, which is all pointing ever
 * does here, 13 §9) and in the mark's assistive text. What it buys is the case
 * the mark alone cannot state — a project that is blocked on a question *and*
 * running three agents reads as `needs-you`, correctly, and the running work is
 * still one hover away instead of gone.
 *
 * Zero-width geometry is the other half of the rule: a row that grew a numeral
 * when a ticket started would reflow the rail, and a rail that reflows is the
 * opposite of ambient (13 §5).
 */
function railHint(state: ProjectState, working: number): string {
  const label = STATE_LABEL[state];
  if (working === 0) {
    return label;
  }
  const count = `${working.toString()} working`;
  // `working` state: the count IS the label, said with a number. Anything else
  // that also has work running gets both, most important first.
  return state === 'working' || label === '' ? count : `${label} · ${count}`;
}

export interface RailProject {
  id: string;
  name: string;
  state: ProjectState;
  /** How many of this project's tickets are being worked right now. Required,
   * not optional: every render site should have to say, so a new caller can't
   * silently drop the one signal the mark's precedence throws away. */
  working: number;
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
          const hint = railHint(project.state, project.working);
          return (
            <li key={project.id}>
              <button
                type="button"
                data-role="rail-project"
                data-project-id={project.id}
                data-state={project.state}
                data-current={project.id === currentProjectId ? 'true' : undefined}
                aria-current={project.id === currentProjectId ? 'true' : undefined}
                // Hover reveals what the mark means, and how much is running
                // behind it, without the rail painting a single extra pixel at
                // rest. A resting project has nothing to say, so it gets no
                // tooltip at all rather than an empty one.
                title={hint === '' ? undefined : hint}
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
                  {hint !== '' && <span data-role="visually-hidden">{hint}</span>}
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
