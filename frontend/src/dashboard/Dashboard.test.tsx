// Dashboard screen tests (11 §5 + the selector contract task-13's e2e binds
// to). Transport is mocked at the module boundary, mirroring
// dashboard-store.test.tsx / App.integration.test.tsx. `Dashboard` owns its
// own `DashboardProvider`, so each test renders the whole mounted tree inside
// a `MemoryRouter` (the real app always mounts this screen under one) rather
// than reaching into the store directly.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { RenderResult } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Dashboard } from '@/dashboard/Dashboard';
import * as transport from '@/transport/transport';
import type { Me, VerifyResponse } from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchMe: vi.fn(),
  putSettings: vi.fn(),
  putProject: vi.fn(),
  postVerify: vi.fn(),
  postLogout: vi.fn(),
}));

function makeMe(overrides: Partial<Me> = {}): Me {
  return {
    user: {
      github_login: 'octocat',
      display_name: 'Octocat',
      avatar_url: 'https://example.com/a.png',
    },
    settings: {
      anthropic_api_key: { set: false, tail: '' },
      amika_api_key: { set: false, tail: '' },
      github_auth_token: { set: false, tail: '' },
      amika_base_url: '',
      amika_claude_cred_id: '',
    },
    ...overrides,
  };
}

function renderDashboard(): RenderResult {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

describe('Dashboard', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    vi.mocked(transport.putSettings).mockReset();
    vi.mocked(transport.putProject).mockReset();
    vi.mocked(transport.postVerify).mockReset();
    vi.mocked(transport.postLogout).mockReset();
  });

  afterEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    vi.mocked(transport.putSettings).mockReset();
    vi.mocked(transport.putProject).mockReset();
    vi.mocked(transport.postVerify).mockReset();
    vi.mocked(transport.postLogout).mockReset();
  });

  it('signed out: renders the GitHub sign-in link as a full-page nav, not a router Link', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(null);
    renderDashboard();

    const link = await screen.findByRole('link', { name: 'Continue with GitHub' });
    expect(link).toHaveAttribute('href', '/auth/github/login');
    expect(document.querySelector('[data-role="dashboard"]')).not.toBeNull();
  });

  it('signed in, no project: shows the onboarding heading with the project form rendered first', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    renderDashboard();

    await screen.findByRole('heading', { name: 'Set up your project' });
    const forms = document.querySelectorAll('form');
    expect(forms).toHaveLength(1);
    expect(forms[0]).toHaveAttribute('data-role', 'project-form');
  });

  it('signed in with project + configured secrets: settings view shows the configured secret status', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          amika_snapshot: 'snap-1',
          brain_model: 'claude-sonnet',
          worker_count: 3,
        },
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: false, tail: '' },
          github_auth_token: { set: true, tail: 'abcd' },
          amika_base_url: 'https://amika.example',
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    renderDashboard();

    const status = await screen.findByText('configured · …x4Kd');
    expect(status).toHaveAttribute('data-role', 'secret-status');
    expect(status).toHaveAttribute('data-name', 'anthropic_api_key');
    expect(status).toHaveAttribute('data-set', 'true');
  });

  it('filling the credentials form and submitting calls putSettings with only the filled fields', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
        },
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(makeMe());
    renderDashboard();

    await screen.findByRole('button', { name: 'Save credentials' });
    fireEvent.change(screen.getByLabelText('Anthropic API key'), {
      target: { value: 'sk-new-ab' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ anthropic_api_key: 'sk-new-ab' });
    });
    // Only the one filled field made it into the request — the untouched
    // secret/text fields (empty by default here) are left out entirely.
    expect(transport.putSettings).toHaveBeenCalledTimes(1);
  });

  it('"Test connections" renders three verify-check rows with data-status from postVerify', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
        },
      }),
    );
    const response: VerifyResponse = {
      checks: [
        { name: 'anthropic', status: 'ok', message: 'reachable' },
        { name: 'amika', status: 'skipped', message: 'not configured' },
        { name: 'repo', status: 'failed', message: 'permission denied' },
      ],
    };
    vi.mocked(transport.postVerify).mockResolvedValue(response);
    renderDashboard();

    const button = await screen.findByRole('button', { name: 'Test connections' });
    fireEvent.click(button);

    await waitFor(() => {
      expect(document.querySelectorAll('[data-role="verify-check"]')).toHaveLength(3);
    });
    const rows = Array.from(document.querySelectorAll('[data-role="verify-check"]'));
    expect(rows.map((row) => row.getAttribute('data-name'))).toEqual(['anthropic', 'amika', 'repo']);
    expect(rows.map((row) => row.getAttribute('data-status'))).toEqual(['ok', 'skipped', 'failed']);
  });

  it('matches the DOM-structure snapshot: settings view', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          amika_snapshot: 'snap-1',
          brain_model: 'claude-sonnet',
          worker_count: 3,
        },
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: false, tail: '' },
          github_auth_token: { set: true, tail: 'abcd' },
          amika_base_url: 'https://amika.example',
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    const { container } = renderDashboard();

    await screen.findByRole('button', { name: 'Sign out' });
    expect(container).toMatchSnapshot();
  });
});
