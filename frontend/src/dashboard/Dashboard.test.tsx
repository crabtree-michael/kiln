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
    expect(document.querySelector('[data-role="dashboard-error"]')).toBeNull();
  });

  it('initial load failure: sign-in view renders the error notice above the link', async () => {
    vi.mocked(transport.fetchMe).mockRejectedValue(new Error('fetchMe: HTTP 500'));
    renderDashboard();

    const link = await screen.findByRole('link', { name: 'Continue with GitHub' });
    const errorEl = document.querySelector('[data-role="dashboard-error"]');
    expect(errorEl).not.toBeNull();
    expect(errorEl?.textContent).toContain('fetchMe: HTTP 500');
    expect(link).toHaveAttribute('href', '/auth/github/login');
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
          agent_provider: '',
          amika_snapshot: 'snap-1',
          brain_model: 'claude-sonnet',
          worker_count: 3,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
        settings: {
          anthropic_api_key: { set: false, tail: '' },
          amika_api_key: { set: true, tail: 'x4Kd' },
          github_auth_token: { set: true, tail: 'abcd' },
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    renderDashboard();

    const status = await screen.findByText('configured · …x4Kd');
    expect(status).toHaveAttribute('data-role', 'secret-status');
    expect(status).toHaveAttribute('data-name', 'amika_api_key');
    expect(status).toHaveAttribute('data-set', 'true');
  });

  it('per-user Anthropic key entry is hidden (now a global env setting)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    renderDashboard();

    // The Amika field still renders — the settings form is mounted — but the
    // Anthropic key input and its status row are gone (SHOW_ANTHROPIC_KEY_FIELD).
    await screen.findByLabelText('Amika API key');
    expect(screen.queryByLabelText('Anthropic API key')).toBeNull();
    expect(
      document.querySelector('[data-role="secret-status"][data-name="anthropic_api_key"]'),
    ).toBeNull();
  });

  it('blurring a filled credential field auto-saves only that field, then auto-verifies', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    // The save response must keep the project — dropping it (e.g. a bare
    // `makeMe()`) would bounce the view back to onboarding after the save.
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    const response: VerifyResponse = {
      checks: [
        { name: 'amika', status: 'ok', message: 'reachable' },
        { name: 'repo', status: 'skipped', message: 'not configured' },
      ],
    };
    vi.mocked(transport.postVerify).mockResolvedValue(response);
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-new-ab' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-new-ab' });
    });
    // Only the one filled field made it into the request — the untouched
    // secret/text fields (empty by default here) are left out entirely.
    expect(transport.putSettings).toHaveBeenCalledTimes(1);

    // A successful credential save automatically chains a verify run — no
    // manual "Test connections" step exists anymore.
    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });

    const indicator = await screen.findByText('✓');
    expect(indicator).toHaveAttribute('data-role', 'credential-status');
    expect(indicator).toHaveAttribute('data-name', 'amika_api_key');
    expect(indicator).toHaveAttribute('data-status', 'ok');
  });

  it('blurring an empty credential field does not save', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    fireEvent.focus(input);
    fireEvent.blur(input);

    // Nothing to await on success, so give any errant async work a tick to
    // land before asserting the negative.
    await Promise.resolve();
    expect(transport.putSettings).not.toHaveBeenCalled();
    expect(transport.postVerify).not.toHaveBeenCalled();
  });

  it('Enter in a filled credential field fires exactly one save, with no form submission', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    const form = document.querySelector('[data-role="settings-form"]');
    const submitSpy = vi.fn();
    form?.addEventListener('submit', submitSpy);

    fireEvent.change(input, { target: { value: 'sk-enter' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-enter' });
    });
    // Let the chained verify settle so a late duplicate would have surfaced.
    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });
    expect(transport.putSettings).toHaveBeenCalledTimes(1);
    // Enter is preventDefault-ed inside the handler — it must never bubble up
    // into a form submission (the credentials form has no submit path at all).
    expect(submitSpy).not.toHaveBeenCalled();
  });

  it('Enter followed immediately by blur fires exactly one save (per-field in-flight guard)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-once' } });
    // The classic double-fire: committing with Enter also moves focus away
    // (or the user tabs out immediately) — the blur lands while the Enter
    // save is still in flight and must be swallowed by the guard.
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-once' });
    });
    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });
    expect(transport.putSettings).toHaveBeenCalledTimes(1);
  });

  it('a failed verify check renders a failed credential-status indicator with the message as its title', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: '',
          brain_model: '',
          worker_count: 1,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      }),
    );
    const response: VerifyResponse = {
      checks: [
        { name: 'amika', status: 'failed', message: 'invalid key' },
        { name: 'repo', status: 'skipped', message: 'not configured' },
      ],
    };
    vi.mocked(transport.postVerify).mockResolvedValue(response);
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-bad' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const indicator = document.querySelector(
        '[data-role="credential-status"][data-name="amika_api_key"]',
      );
      expect(indicator).toHaveAttribute('data-status', 'failed');
      expect(indicator).toHaveAttribute('title', 'invalid key');
      expect(indicator?.textContent).toBe('✗');
    });
  });

  it('settings view offers a "Go to app" link back to the board (a router Link to /)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: 'snap-1',
          brain_model: 'claude-sonnet',
          worker_count: 3,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: true, tail: 'y7Bc' },
          github_auth_token: { set: true, tail: 'abcd' },
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    renderDashboard();

    // A router Link (relative href '/app'), not a full-page anchor — client nav
    // back to the SPA-owned board. The ← glyph is aria-hidden, so the name is "Go to app".
    const link = await screen.findByRole('link', { name: 'Go to app' });
    expect(link).toHaveAttribute('href', '/app');
  });

  it('matches the DOM-structure snapshot: settings view', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        project: {
          name: 'kiln',
          repo_url: 'https://github.com/crabtree-michael/kiln',
          agent_provider: '',
          amika_snapshot: 'snap-1',
          brain_model: 'claude-sonnet',
          worker_count: 3,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: false, tail: '' },
          github_auth_token: { set: true, tail: 'abcd' },
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    const { container } = renderDashboard();

    await screen.findByRole('button', { name: 'Sign out' });
    expect(container).toMatchSnapshot();
  });
});
