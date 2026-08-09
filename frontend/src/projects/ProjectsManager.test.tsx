// Native project-management page tests (12 follow-up). Like Dashboard.test.tsx,
// transport is mocked at the module boundary and the whole tree renders inside a
// MemoryRouter (the page owns its own `DashboardProvider`, which loads `me` and
// runs the create/update/delete mutations). Covers the phase branches, the
// collapsible project rows, the `?new=1` deep-link, and the create/delete flows.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { RenderResult } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ProjectsManager } from '@/projects/ProjectsManager';
import * as transport from '@/transport/transport';
import type { Me, MeProject } from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchMe: vi.fn(),
  putSettings: vi.fn(),
  putProject: vi.fn(),
  createProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
  postVerify: vi.fn(),
  postLogout: vi.fn(),
  fetchGitHubRepos: vi.fn(),
}));

function makeProject(overrides: Partial<MeProject> = {}): MeProject {
  return {
    id: 'proj-1',
    name: 'kiln',
    repo_url: 'https://github.com/crabtree-michael/kiln',
    agent_provider: '',
    amika_snapshot: 'snap-1',
    worker_count: 3,
    merge_gate_mode: 'main',
    amika_secrets: [],
    ...overrides,
  };
}

function makeMe(overrides: Partial<Me> = {}): Me {
  return {
    user: {
      github_login: 'octocat',
      display_name: 'Octocat',
      avatar_url: 'https://example.com/a.png',
    },
    projects: [],
    settings: {
      anthropic_api_key: { set: false, tail: '' },
      amika_api_key: { set: false, tail: '' },
      devin_api_key: { set: false, tail: '' },
      github_auth_token: { set: false, tail: '' },
      github_connection: {
        status: 'disconnected',
        login: '',
        installation_id: 0,
        configure_url: '',
      },
      amika_claude_cred_id: '',
    },
    ...overrides,
  };
}

function renderManager(entry = '/projects'): RenderResult {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <ProjectsManager />
    </MemoryRouter>,
  );
}

describe('ProjectsManager', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    // Every row's repo picker reads the connected account; default to connected
    // with one repo so the create form can pick one.
    vi.mocked(transport.fetchGitHubRepos).mockReset();
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({
      connected: true,
      repos: [{ full_name: 'a/b', url: 'https://github.com/a/b', private: false }],
    });
    vi.mocked(transport.createProject).mockReset();
    vi.mocked(transport.updateProject).mockReset();
    vi.mocked(transport.deleteProject).mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('signed out: renders the GitHub sign-in as a full-page nav, not a router Link', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(null);
    renderManager();

    const link = await screen.findByRole('link', { name: 'Continue with GitHub' });
    expect(link).toHaveAttribute('href', '/auth/github/connect');
    expect(document.querySelector('[data-role="projects-list"]')).toBeNull();
  });

  it('empty: shows the empty-state prompt and the Add affordance, no rows', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    renderManager();

    await screen.findByRole('button', { name: 'Add project' });
    expect(document.querySelector('[data-role="projects-empty"]')).not.toBeNull();
    expect(document.querySelectorAll('[data-role="project-row"]')).toHaveLength(0);
  });

  it('lists a row per project, collapsed (no edit form until opened)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({ projects: [makeProject(), makeProject({ id: 'proj-2', name: 'ember' })] }),
    );
    renderManager();

    await waitFor(() => {
      expect(document.querySelectorAll('[data-role="project-row"]')).toHaveLength(2);
    });
    // Collapsed: the reused ProjectFields form is not mounted yet.
    expect(document.querySelector('[data-role="project-form"]')).toBeNull();
    expect(screen.getByText('kiln')).toBeInTheDocument();
    expect(screen.getByText('ember')).toBeInTheDocument();
  });

  it('expanding a row reveals its edit form and a delete control', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ projects: [makeProject()] }));
    renderManager();

    const toggle = await screen.findByRole('button', { name: /kiln/ });
    fireEvent.click(toggle);

    expect(document.querySelector('[data-role="project-form"]')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Delete project' })).toBeInTheDocument();
  });

  it('deleting a project confirms then calls deleteProject with its id', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ projects: [makeProject()] }));
    vi.mocked(transport.deleteProject).mockResolvedValue(undefined);
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderManager();

    fireEvent.click(await screen.findByRole('button', { name: /kiln/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete project' }));

    expect(confirmSpy).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(transport.deleteProject).toHaveBeenCalledWith('proj-1');
    });
  });

  it('the ?new=1 deep-link opens the create step on mount', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ projects: [makeProject()] }));
    renderManager('/projects?new=1');

    await screen.findByRole('heading', { name: 'New project' });
    expect(document.querySelector('[data-role="new-project-step"]')).not.toBeNull();
  });

  // The create step takes the SCREEN (full-screen repo picker): the list it was
  // reached from is out of the way, the page header names the step, and its back
  // control cancels rather than leaving for the app.
  it('the create step replaces the list, and cancelling gives it back', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ projects: [makeProject()] }));
    renderManager('/projects?new=1');

    await screen.findByRole('heading', { name: 'New project' });
    expect(document.querySelectorAll('[data-role="project-row"]')).toHaveLength(0);
    expect(screen.queryByRole('button', { name: 'Add project' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Back to the app' })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel new project' }));

    expect(screen.getByRole('heading', { name: 'Projects' })).toBeInTheDocument();
    expect(document.querySelectorAll('[data-role="project-row"]')).toHaveLength(1);
    expect(screen.getByRole('link', { name: 'Back to the app' })).toBeInTheDocument();
  });

  it('the Add button opens a create step that names the project after the repo', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    vi.mocked(transport.createProject).mockResolvedValue(
      makeProject({ id: 'proj-new', name: 'b' }),
    );
    renderManager();

    fireEvent.click(await screen.findByRole('button', { name: 'Add project' }));
    expect(document.querySelector('[data-role="new-project-step"] form')).not.toBeNull();

    // The repo is the only question: there is no name field, and the picked
    // repo's own name is what the project is called (auto-name from repository).
    expect(screen.queryByLabelText('Project name')).toBeNull();
    const repoSelect = await screen.findByRole('combobox', { name: 'Repository' });
    fireEvent.change(repoSelect, { target: { value: 'https://github.com/a/b' } });
    expect(document.querySelector('[data-role="project-name-value"]')).toHaveTextContent('b');
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }));

    await waitFor(() => {
      expect(transport.createProject).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'b', repo_url: 'https://github.com/a/b' }),
      );
    });
  });

  it('the header offers a back control to /app', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    renderManager();

    await screen.findByRole('button', { name: 'Add project' });
    const back = screen.getByRole('link', { name: 'Back to the app' });
    expect(back).toHaveAttribute('href', '/app');
  });
});
