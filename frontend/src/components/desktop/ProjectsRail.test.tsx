// The projects rail (13 §5): the switcher and the ambient layer in one surface.
// The assertions here are the design's non-negotiables — the accent is spent on
// `needs-you` and nowhere else, ordering is passed through untouched, and moving
// through the rail with the keyboard reveals without acting.
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ProjectsRail, type RailProject } from '@/components/desktop/ProjectsRail';

const PROJECTS: RailProject[] = [
  { id: 'p1', name: 'kiln', state: 'needs-you' },
  { id: 'p2', name: 'atlas', state: 'working' },
  { id: 'p3', name: 'ledger', state: 'quiet' },
  { id: 'p4', name: 'foundry', state: 'unknown' },
];

function renderRail(overrides: Partial<React.ComponentProps<typeof ProjectsRail>> = {}) {
  const onSelectProject = vi.fn();
  const result = render(
    <ProjectsRail
      projects={PROJECTS}
      currentProjectId="p1"
      onSelectProject={onSelectProject}
      {...overrides}
    />,
  );
  return { ...result, onSelectProject };
}

function rows(container: HTMLElement): HTMLButtonElement[] {
  return Array.from(container.querySelectorAll<HTMLButtonElement>('[data-role="rail-project"]'));
}

/** The rail's keydown handler lives on the list, so the arrow-key cases fire at
 * it directly. Narrows without a type assertion (the lint gate bans both `as`
 * and `!`) by failing the test if the element is missing. */
function list(container: HTMLElement): HTMLElement {
  const node = container.querySelector<HTMLElement>('[data-role="rail-list"]');
  if (node === null) {
    throw new Error('rail-list not found');
  }
  return node;
}

describe('ProjectsRail', () => {
  it('renders every project, in the order given — never re-sorted by urgency', () => {
    const { container } = renderRail();
    expect(rows(container).map((row) => row.textContent)).toEqual([
      'kilnneeds you',
      'atlasworking',
      'ledger',
      'foundry',
    ]);
  });

  it('carries each project state as a data hook the stylesheet keys off', () => {
    const { container } = renderRail();
    expect(rows(container).map((row) => row.getAttribute('data-state'))).toEqual([
      'needs-you',
      'working',
      'quiet',
      'unknown',
    ]);
  });

  it('names the state for assistive tech only where there IS a state — quiet is absence', () => {
    const { container } = renderRail();
    const marks = Array.from(
      container.querySelectorAll<HTMLElement>('[data-role="rail-project-state"]'),
    );
    expect(marks.map((mark) => mark.textContent)).toEqual(['needs you', 'working', '', '']);
  });

  it('marks the current project, once', () => {
    const { container } = renderRail();
    const current = rows(container).filter((row) => row.dataset.current === 'true');
    expect(current).toHaveLength(1);
    expect(current[0]).toHaveAttribute('data-project-id', 'p1');
    expect(current[0]).toHaveAttribute('aria-current', 'true');
  });

  it('selects a project on click — the rail IS the switcher', () => {
    const { onSelectProject } = renderRail();
    fireEvent.click(screen.getByRole('button', { name: /atlas/ }));
    expect(onSelectProject).toHaveBeenCalledWith('p2');
  });

  it('arrow keys move focus down and up through the rail', () => {
    const { container } = renderRail();

    rows(container)[0]?.focus();
    fireEvent.keyDown(list(container), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(rows(container)[1]);

    fireEvent.keyDown(list(container), { key: 'ArrowUp' });
    expect(document.activeElement).toBe(rows(container)[0]);
  });

  it('Home and End jump to the ends, and neither wraps past them', () => {
    const { container } = renderRail();

    fireEvent.keyDown(list(container), { key: 'End' });
    expect(document.activeElement).toBe(rows(container)[3]);
    // Already at the end: ArrowDown holds rather than wrapping round, so a held
    // key can't cycle the rail under the eye.
    fireEvent.keyDown(list(container), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(rows(container)[3]);

    fireEvent.keyDown(list(container), { key: 'Home' });
    expect(document.activeElement).toBe(rows(container)[0]);
    fireEvent.keyDown(list(container), { key: 'ArrowUp' });
    expect(document.activeElement).toBe(rows(container)[0]);
  });

  it('arrowing through the rail moves focus but does NOT select — pointing never acts (13 §9)', () => {
    const { container, onSelectProject } = renderRail();
    rows(container)[0]?.focus();
    fireEvent.keyDown(list(container), { key: 'ArrowDown' });
    fireEvent.keyDown(list(container), { key: 'ArrowDown' });
    expect(onSelectProject).not.toHaveBeenCalled();
  });

  it('adding a project routes to the project-management page, not into the rail', () => {
    renderRail();
    expect(screen.getByRole('link', { name: /New project/ })).toHaveAttribute(
      'href',
      '/projects?new=1',
    );
  });

  it('renders nothing but the add affordance when the user has no projects', () => {
    const { container } = renderRail({ projects: [], currentProjectId: null });
    expect(rows(container)).toHaveLength(0);
    expect(screen.getByRole('link', { name: /New project/ })).toBeInTheDocument();
  });
});
