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
    expect(link).toHaveAttribute('href', '/auth/github/login');
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

  it('the ?new=1 deep-link opens the create form on mount', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ projects: [makeProject()] }));
    renderManager('/projects?new=1');

    await screen.findByRole('heading', { name: 'New project' });
    expect(document.querySelector('[data-role="new-project-form"]')).not.toBeNull();
  });

  it('the Add button opens a blank create form that posts through createProject', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    vi.mocked(transport.createProject).mockResolvedValue(
      makeProject({ id: 'proj-new', name: 'new-one' }),
    );
    renderManager();

    fireEvent.click(await screen.findByRole('button', { name: 'Add project' }));
    const form = document.querySelector('[data-role="new-project-form"] form');
    expect(form).not.toBeNull();

    // Fill the two required fields and submit.
    const nameInput = screen.getByLabelText('Project name');
    const repoInput = screen.getByLabelText('Repo URL');
    fireEvent.change(nameInput, { target: { value: 'new-one' } });
    fireEvent.change(repoInput, { target: { value: 'https://github.com/a/b' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    await waitFor(() => {
      expect(transport.createProject).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'new-one', repo_url: 'https://github.com/a/b' }),
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
