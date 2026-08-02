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
  createProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
  postVerify: vi.fn(),
  postLogout: vi.fn(),
  // The repo picker's source: connected, listing the project's own repo so the
  // settings view renders its ordinary (preselected) state.
  fetchGitHubRepos: vi.fn(() =>
    Promise.resolve({
      connected: true,
      repos: [
        {
          full_name: 'crabtree-michael/kiln',
          url: 'https://github.com/crabtree-michael/kiln',
          private: false,
        },
      ],
    }),
  ),
  fetchSnapshots: vi.fn(() => Promise.resolve(null)),
}));

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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: 'snap-1',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
        settings: {
          anthropic_api_key: { set: false, tail: '' },
          amika_api_key: { set: true, tail: 'x4Kd' },
          devin_api_key: { set: false, tail: '' },
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    // The save response must keep the project — dropping it (e.g. a bare
    // `makeMe()`) would bounce the view back to onboarding after the save.
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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

  it('blurring the Devin API key auto-saves it and reads its own verify check', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: 'devin',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: 'devin',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    const response: VerifyResponse = {
      checks: [
        { name: 'devin', status: 'ok', message: 'reachable' },
        { name: 'repo', status: 'skipped', message: 'not configured' },
      ],
    };
    vi.mocked(transport.postVerify).mockResolvedValue(response);
    renderDashboard();

    const input = await screen.findByLabelText('Devin API key');
    fireEvent.change(input, { target: { value: 'cog-new-xy' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ devin_api_key: 'cog-new-xy' });
    });
    expect(transport.putSettings).toHaveBeenCalledTimes(1);

    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });

    const indicator = await screen.findByText('✓');
    expect(indicator).toHaveAttribute('data-role', 'credential-status');
    expect(indicator).toHaveAttribute('data-name', 'devin_api_key');
    expect(indicator).toHaveAttribute('data-status', 'ok');
  });

  it('blurring an empty credential field does not save', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
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
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: 'snap-1',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: true, tail: 'y7Bc' },
          devin_api_key: { set: false, tail: '' },
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

  // ---------------------------------------------------------------- layout
  // The settings redesign's contract: grouped sections + a nav that indexes
  // them, with every field still mounted (the nav scrolls, it never swaps
  // panes — see Settings.tsx's header).

  /** A settings view with one project and a couple of configured secrets — the
   * ordinary steady state the layout assertions below run against. */
  function mockPopulatedSettings(): void {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
  }

  it('groups settings into four sections, each indexed by a nav anchor', async () => {
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    const items = document.querySelectorAll('[data-role="settings-nav-item"]');
    expect([...items].map((item) => item.getAttribute('href'))).toEqual([
      '#account',
      '#integrations',
      '#notifications',
      '#projects',
    ]);
    // Every anchor resolves to a real section, and every section is named by a
    // heading (so the nav order IS the document outline).
    for (const id of ['account', 'integrations', 'notifications', 'projects']) {
      const section = document.getElementById(id);
      expect(section).not.toBeNull();
      expect(section).toHaveAttribute('data-role', 'settings-section');
    }
    expect(
      [...document.querySelectorAll('[data-role="settings-section"] h2')].map((h) => h.textContent),
    ).toEqual(['Account', 'Integrations', 'Notifications', 'Projects']);
  });

  it('keeps every section mounted — the nav scrolls, it does not hide fields behind tabs', async () => {
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    // One assertion per section, all true at once: nothing is behind a tab.
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeInTheDocument();
    expect(screen.getByLabelText('Amika API key')).toBeInTheDocument();
    expect(document.querySelector('[data-role="notifications-field"]')).not.toBeNull();
    // Projects is the one deliberate exception: the SECTION is mounted and its
    // list is visible, but a project's fields live in a dialog (see
    // ProjectModal.tsx) — several inline forms is what made the list
    // unscannable in the first place.
    expect(document.querySelector('[data-role="project-panel"]')).not.toBeNull();
    expect(document.querySelector('[data-role="project-form"]')).toBeNull();
  });

  it('highlights the first section by default, and survives a missing IntersectionObserver', async () => {
    // jsdom ships no IntersectionObserver, so this is also the real fallback
    // path: the nav must render as a plain table of contents, not throw.
    expect(typeof globalThis.IntersectionObserver).toBe('undefined');
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    const active = document.querySelectorAll('[data-role="settings-nav-item"][data-active="true"]');
    expect(active).toHaveLength(1);
    expect(active[0]).toHaveAttribute('data-section', 'account');
  });

  it('moves the nav highlight to the section scrolled into view', async () => {
    interface FakeEntry {
      target: Element;
      isIntersecting: boolean;
    }
    // A holder object rather than a bare `let`: TypeScript keeps the
    // `null`-narrowing of a local across a constructor call in a nested class,
    // so `fire?.()` below would be typed `never`.
    const spy: { fire: ((entries: FakeEntry[]) => void) | null; observed: Element[] } = {
      fire: null,
      observed: [],
    };
    class FakeIntersectionObserver {
      constructor(callback: (entries: FakeEntry[]) => void) {
        spy.fire = callback;
      }
      observe(element: Element): void {
        spy.observed.push(element);
      }
      disconnect(): void {
        spy.fire = null;
      }
    }
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);

    try {
      mockPopulatedSettings();
      renderDashboard();
      await screen.findByRole('button', { name: 'Sign out' });

      // All four sections are observed, in document order.
      expect(spy.observed.map((element) => element.id)).toEqual([
        'account',
        'integrations',
        'notifications',
        'projects',
      ]);

      const projects = document.getElementById('projects');
      const account = document.getElementById('account');
      expect(projects).not.toBeNull();
      expect(account).not.toBeNull();
      expect(spy.fire).not.toBeNull();
      await waitFor(() => {
        spy.fire?.([
          { target: account!, isIntersecting: false },
          { target: projects!, isIntersecting: true },
        ]);
        expect(
          document.querySelector('[data-role="settings-nav-item"][data-active="true"]'),
        ).toHaveAttribute('data-section', 'projects');
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it('surfaces the account-level GitHub connection in Integrations', async () => {
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connection"]')).toHaveAttribute(
        'data-state',
        'connected',
      );
    });
    // Connecting and switching are the same single OAuth flow, so this is a
    // full-page nav to the backend route — never a router Link.
    const action = screen.getByRole('link', { name: 'Switch account' });
    expect(action).toHaveAttribute('href', '/auth/github/login');
  });

  it('offers a connect link when the GitHub account is not connected', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValueOnce({ connected: false, repos: [] });
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connection"]')).toHaveAttribute(
        'data-state',
        'disconnected',
      );
    });
    expect(screen.getByRole('link', { name: 'Connect' })).toHaveAttribute(
      'href',
      '/auth/github/login',
    );
  });

  it("a credential field's accessible name stays the label once its validity glyph lands", async () => {
    // The regression the htmlFor/id wiring guards: with the input WRAPPED in its
    // label, the ✓ rendered beside it joins the computed name, and every
    // getByLabel('Amika API key') — here and in the Playwright suite — breaks
    // the moment a verify comes back.
    mockPopulatedSettings();
    vi.mocked(transport.putSettings).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
      }),
    );
    vi.mocked(transport.postVerify).mockResolvedValue({
      checks: [{ name: 'amika', status: 'ok', message: 'reachable' }],
    });
    renderDashboard();

    const input = await screen.findByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-named' } });
    fireEvent.blur(input);

    await screen.findByText('✓');
    expect(screen.getByLabelText('Amika API key')).toBe(input);
  });

  // ------------------------------------------------------------ projects
  // One panel per project, everything else behind a click (projects-in-a-modal).

  it('lists one compact panel per project, summarising it without any form', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
          {
            id: 'proj-2',
            name: 'atlas',
            repo_url: 'https://github.com/crabtree-michael/atlas.git',
            agent_provider: '',
            amika_snapshot: '',
            worker_count: 1,
            merge_gate_mode: 'pr',
            amika_secrets: [],
          },
        ],
      }),
    );
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    const panels = document.querySelectorAll('[data-role="project-panel"]');
    expect([...panels].map((panel) => panel.getAttribute('data-project-id'))).toEqual([
      'proj-1',
      'proj-2',
    ]);
    // The repo reads as GitHub names it, not as a full URL that would push the
    // rest of the row off the panel (and `.git` is normalised away).
    expect(
      [...document.querySelectorAll('[data-role="project-panel-repo"]')].map(
        (el) => el.textContent,
      ),
    ).toEqual(['crabtree-michael/kiln', 'crabtree-michael/atlas']);
    expect(document.querySelector('[data-role="project-form"]')).toBeNull();
  });

  it('opens a project panel into its settings dialog, and closes again', async () => {
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    expect(screen.queryByRole('dialog')).toBeNull();

    const panel = document.querySelector('[data-role="project-panel"]');
    expect(panel).not.toBeNull();
    fireEvent.click(panel!);

    const modal = await screen.findByRole('dialog', { name: 'Project settings: kiln' });
    // The header carries the two facts the modal promises together: the name,
    // editable in place, and the repository picker itself.
    expect(screen.getByLabelText('Project name')).toHaveValue('kiln');
    expect(screen.getByRole('combobox', { name: 'Repository' })).toHaveValue(
      'https://github.com/crabtree-michael/kiln',
    );
    expect(modal.querySelector('[data-role="sandbox-info"]')).not.toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
  });

  it('saves a project from the dialog, then closes it', async () => {
    mockPopulatedSettings();
    vi.mocked(transport.updateProject).mockResolvedValue({
      id: 'proj-1',
      name: 'renamed',
      repo_url: 'https://github.com/crabtree-michael/kiln',
      agent_provider: '',
      amika_snapshot: '',
      worker_count: 3,
      merge_gate_mode: 'main',
      amika_secrets: [],
    });
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    fireEvent.click(document.querySelector('[data-role="project-panel"]')!);
    await screen.findByRole('dialog');

    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    await waitFor(() => {
      expect(transport.updateProject).toHaveBeenCalledWith('proj-1', {
        name: 'renamed',
        repo_url: 'https://github.com/crabtree-michael/kiln',
        worker_count: 3,
        merge_gate_mode: 'main',
      });
    });
    // Closed, with the saved name folded back into the list behind it.
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull();
    });
    expect(document.querySelector('[data-role="project-panel-name"]')?.textContent).toBe('renamed');
  });

  it('"New project" opens the same dialog in create mode', async () => {
    mockPopulatedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    fireEvent.click(screen.getByRole('button', { name: 'New project' }));

    await screen.findByRole('dialog', { name: 'New project' });
    expect(screen.getByLabelText('Project name')).toHaveValue('');
    // Nothing to delete before the project exists.
    expect(screen.queryByRole('button', { name: 'Delete project' })).toBeNull();
  });

  it('matches the DOM-structure snapshot: settings view', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(
      makeMe({
        projects: [
          {
            id: 'proj-1',
            name: 'kiln',
            repo_url: 'https://github.com/crabtree-michael/kiln',
            agent_provider: '',
            amika_snapshot: 'snap-1',
            worker_count: 3,
            merge_gate_mode: 'main',
            amika_secrets: [],
          },
        ],
        settings: {
          anthropic_api_key: { set: true, tail: 'x4Kd' },
          amika_api_key: { set: false, tail: '' },
          devin_api_key: { set: false, tail: '' },
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
