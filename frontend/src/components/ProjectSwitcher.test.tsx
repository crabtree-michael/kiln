// ProjectSwitcher tests (12 §4.1): the current project's name is the wordmark
// trigger; opening it lists the user's projects, marks the current one, switches
// on click (by project_id), and offers an "Add" affordance. Rendered under a stub
// current-project context + MemoryRouter (it navigates).
import type { JSX } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { ProjectSwitcher } from '@/components/ProjectSwitcher';
import {
  CurrentProjectContext,
  type CurrentProjectStoreValue,
} from '@/stores/current-project-context';
import type { MeProject } from '@/transport/transport';

function makeProject(id: string, name: string): MeProject {
  return {
    id,
    name,
    repo_url: '',
    agent_provider: '',
    amika_snapshot: '',
    worker_count: 3,
    merge_gate_mode: 'main',
    amika_secrets: [],
  };
}

/** Reads back where the switcher navigated — MemoryRouter keeps its history to
 * itself, so an item that routes is only observable from inside it. */
function LocationProbe(): JSX.Element {
  const { pathname, search } = useLocation();
  return <span data-role="location-probe">{`${pathname}${search}`}</span>;
}

function renderSwitcher(value: CurrentProjectStoreValue): void {
  render(
    <MemoryRouter>
      <CurrentProjectContext.Provider value={value}>
        <ProjectSwitcher />
      </CurrentProjectContext.Provider>
      <LocationProbe />
    </MemoryRouter>,
  );
}

describe('ProjectSwitcher', () => {
  it('shows the current project name in the trigger wordmark and lists all projects when opened', () => {
    const projects = [makeProject('p1', 'one'), makeProject('p2', 'two')];
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject: vi.fn() });

    // Collapsed, only the trigger is in the a11y tree (the list is aria-hidden),
    // so a name query resolves it unambiguously despite the current project also
    // appearing in the list.
    const trigger = screen.getByRole('button', { name: /one/ });
    expect(trigger).toHaveAttribute('data-role', 'project-switcher-current');
    // The wordmark carries the current project's name in Kiln's brand styling,
    // not the literal "Kiln".
    expect(trigger.querySelector('[data-role="kiln-wordmark"]')).toHaveTextContent('one');
    // The panel starts collapsed — the CSS hide keys off `data-open`/`aria-hidden`,
    // so an unopened switcher must not present its list (the bug this replaced
    // rendered the full project list permanently, over the screen).
    const panel = document.querySelector('[data-role="project-switcher-panel"]');
    expect(panel).toHaveAttribute('data-open', 'false');
    expect(panel).toHaveAttribute('aria-hidden', 'true');
    fireEvent.click(trigger);
    expect(panel).toHaveAttribute('data-open', 'true');
    const items = screen.getAllByRole('button', { name: /one|two/ });
    // Both project list items (plus the trigger, which also reads "one").
    expect(items.length).toBeGreaterThanOrEqual(2);
  });

  it('switches the current project by id on select', () => {
    const projects = [makeProject('p1', 'one'), makeProject('p2', 'two')];
    const selectProject = vi.fn();
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject });

    fireEvent.click(screen.getByRole('button', { name: /one/ }));
    const item = document.querySelector(
      '[data-role="project-switcher-item"][data-project-id="p2"]',
    );
    expect(item).not.toBeNull();
    if (item !== null) {
      fireEvent.click(item);
    }
    expect(selectProject).toHaveBeenCalledWith('p2');
  });

  it('offers an "Add" affordance', () => {
    const projects = [makeProject('p1', 'one')];
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: /one/ }));
    expect(screen.getByRole('button', { name: 'Add' })).toBeInTheDocument();
  });

  it('ends the menu with "Settings", separated from the projects, opening the account view', () => {
    const projects = [makeProject('p1', 'one')];
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: /one/ }));

    const settings = screen.getByRole('button', { name: 'Settings' });
    const panel = document.querySelector("[data-role='project-switcher-panel']");
    // Last in the panel, with the rule that divides it from the project rows
    // immediately before it.
    expect(panel?.lastElementChild).toBe(settings);
    expect(settings.previousElementSibling).toHaveAttribute(
      'data-role',
      'project-switcher-divider',
    );

    fireEvent.click(settings);
    // The same account view the header's gear used to open, and the menu closes
    // behind it.
    expect(screen.getByText('/dashboard')).toBeInTheDocument();
    expect(panel).toHaveAttribute('data-open', 'false');
  });

  it('renders nothing when there is no current project', () => {
    const { container } = render(
      <MemoryRouter>
        <CurrentProjectContext.Provider
          value={{ current: null, projects: [], selectProject: vi.fn() }}
        >
          <ProjectSwitcher />
        </CurrentProjectContext.Provider>
      </MemoryRouter>,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
