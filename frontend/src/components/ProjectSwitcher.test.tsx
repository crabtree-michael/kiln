// ProjectSwitcher tests (12 §4.1): the "Kiln" wordmark is the trigger; opening
// it lists the user's projects, marks the current one, switches on click (by
// project_id), and offers an "Add" affordance. Rendered under a stub
// current-project context + MemoryRouter (it navigates).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
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

function renderSwitcher(value: CurrentProjectStoreValue): void {
  render(
    <MemoryRouter>
      <CurrentProjectContext.Provider value={value}>
        <ProjectSwitcher />
      </CurrentProjectContext.Provider>
    </MemoryRouter>,
  );
}

describe('ProjectSwitcher', () => {
  it('shows the "Kiln" trigger and lists all projects when opened', () => {
    const projects = [makeProject('p1', 'one'), makeProject('p2', 'two')];
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject: vi.fn() });

    expect(screen.getByRole('button', { name: /Kiln/ })).toHaveAttribute(
      'data-role',
      'project-switcher-current',
    );
    // The panel starts collapsed — the CSS hide keys off `data-open`/`aria-hidden`,
    // so an unopened switcher must not present its list (the bug this replaced
    // rendered the full project list permanently, over the screen).
    const panel = document.querySelector('[data-role="project-switcher-panel"]');
    expect(panel).toHaveAttribute('data-open', 'false');
    expect(panel).toHaveAttribute('aria-hidden', 'true');
    fireEvent.click(screen.getByRole('button', { name: /Kiln/ }));
    expect(panel).toHaveAttribute('data-open', 'true');
    const items = screen.getAllByRole('button', { name: /one|two/ });
    // Both project list items.
    expect(items.length).toBeGreaterThanOrEqual(2);
  });

  it('switches the current project by id on select', () => {
    const projects = [makeProject('p1', 'one'), makeProject('p2', 'two')];
    const selectProject = vi.fn();
    renderSwitcher({ current: projects[0] ?? null, projects, selectProject });

    fireEvent.click(screen.getByRole('button', { name: /Kiln/ }));
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
    fireEvent.click(screen.getByRole('button', { name: /Kiln/ }));
    expect(screen.getByRole('button', { name: 'Add' })).toBeInTheDocument();
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
