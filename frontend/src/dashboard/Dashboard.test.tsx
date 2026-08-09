// Dashboard screen tests (11 §5 + the selector contract task-13's e2e binds
// to). Transport is mocked at the module boundary, mirroring
// dashboard-store.test.tsx / App.integration.test.tsx. `Dashboard` owns its
// own `DashboardProvider`, so each test renders the whole mounted tree inside
// a `MemoryRouter` (the real app always mounts this screen under one) rather
// than reaching into the store directly.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
  // The Notifications section's frequency dropdown reads the global mode on
  // mount; the recommended default keeps the row in its ordinary state.
  fetchNotificationMode: vi.fn(() => Promise.resolve('default')),
  putNotificationMode: vi.fn((mode: string) => Promise.resolve(mode)),
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

function renderDashboard(): RenderResult {
  return render(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );
}

/** An integration card by provider, asserting it rendered. */
function integrationCard(provider: string): HTMLElement {
  const card = document.querySelector<HTMLElement>(
    `[data-role="integration-card"][data-provider="${provider}"]`,
  );
  if (card === null) {
    throw new Error(`expected an integration card for ${provider}`);
  }
  return card;
}

/** Click a provider card's Connect / Configure button and return the API-key
 * modal's input — the only way to enter a key now that the flat credential
 * form is gone. */
async function openConnectModal(provider: string, label: string): Promise<HTMLElement> {
  const card = await waitFor(() => integrationCard(provider));
  fireEvent.click(within(card).getByRole('button'));
  return screen.getByLabelText(label);
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
    // The dashboard-return form: this card is only ever met by someone who came
    // to the dashboard, so signing in from it puts them back here rather than in
    // the app (which is where the landing page's and session gate's links go).
    expect(link).toHaveAttribute('href', '/auth/github/connect?next=dashboard');
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
    expect(link).toHaveAttribute('href', '/auth/github/connect?next=dashboard');
  });

  it('signed in, no project: opens the guided setup flow on its first step', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    renderDashboard();

    // The flow starts at "Connect GitHub" — not at a project form. The whole
    // point of the sequence is that the repo listing (step 2) can only exist
    // once the GitHub credential does, so this ordering is the feature.
    await screen.findByRole('heading', { name: 'Connect GitHub' });
    expect(document.querySelector('[data-role="onboarding"]')).toHaveAttribute(
      'data-step',
      'github',
    );
    // No form at all any more: the old single crammed `project-form` is what
    // this flow replaces.
    expect(document.querySelectorAll('form')).toHaveLength(0);
  });

  it('signed in with project + configured secrets: settings view reports the stored key', async () => {
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
          github_connection: {
            status: 'unknown',
            login: '',
            installation_id: 0,
            configure_url: '',
          },
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    renderDashboard();

    // The row's dot shows connected and the button offers to replace the key;
    // the stored key's fingerprint shows in the dialog, where you'd act on it.
    const card = await waitFor(() => integrationCard('amika'));
    expect(card).toHaveAttribute('data-connected', 'true');
    expect(within(card).getByRole('img', { name: 'Connected' })).toHaveAttribute(
      'data-role',
      'integration-connected',
    );

    const input = await openConnectModal('amika', 'Amika API key');
    expect(input).toHaveAttribute('placeholder', 'configured · …x4Kd');
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

    // The Amika card still renders — the Integrations section is mounted — but
    // the Anthropic card is gone entirely (SHOW_ANTHROPIC_KEY_FIELD).
    await waitFor(() => integrationCard('amika'));
    expect(
      document.querySelector('[data-role="integration-card"][data-provider="anthropic"]'),
    ).toBeNull();
    expect(
      document.querySelector('[data-role="secret-status"][data-name="anthropic_api_key"]'),
    ).toBeNull();
  });

  // GitHub is connected through the one repo-scoped OAuth grant (11 §2, amended
  // 2026-08-03), which the setup flow and the project form ask for in place. The
  // Integrations section covers only the providers you connect by pasting a key,
  // so it holds neither a GitHub card nor a token field.
  it('the Integrations section covers only API-key providers — no GitHub anywhere in it', async () => {
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

    const section = await waitFor(() => {
      const el = document.querySelector('[data-role="integrations"]');
      if (el === null) {
        throw new Error('expected the integrations section');
      }
      return el;
    });
    expect(
      section.querySelector('[data-role="integration-card"][data-provider="github"]'),
    ).toBeNull();
    expect(section.querySelector('a[href^="/auth/github/connect"]')).toBeNull();
    // The manual "GitHub token" input is gone from the whole screen.
    expect(screen.queryByLabelText('GitHub token')).toBeNull();
    expect(document.querySelector('[data-role="api-key-modal"]')).toBeNull();
  });

  it('connecting an API-key provider saves only that field from the modal, then auto-verifies', async () => {
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

    const input = await openConnectModal('amika', 'Amika API key');
    fireEvent.change(input, { target: { value: 'sk-new-ab' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-new-ab' });
    });
    // Only the one field the modal collects made it into the request — no
    // other provider's credential rides along.
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

    // The modal dismisses itself once the key is actually stored.
    await waitFor(() => {
      expect(document.querySelector('[data-role="api-key-modal"]')).toBeNull();
    });
  });

  it('connecting Devin saves its own field and reads its own verify check', async () => {
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

    const input = await openConnectModal('devin', 'Devin API key');
    fireEvent.change(input, { target: { value: 'cog-new-xy' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

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

  it('an empty modal cannot be submitted, and Cancel closes it without saving', async () => {
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

    const input = await openConnectModal('amika', 'Amika API key');
    // Whitespace only still counts as empty — Save stays disabled.
    fireEvent.change(input, { target: { value: '   ' } });
    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    fireEvent.click(save);

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(document.querySelector('[data-role="api-key-modal"]')).toBeNull();

    // Nothing to await on success, so give any errant async work a tick to
    // land before asserting the negative.
    await Promise.resolve();
    expect(transport.putSettings).not.toHaveBeenCalled();
    expect(transport.postVerify).not.toHaveBeenCalled();
  });

  it('Enter in the modal fires exactly one save', async () => {
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

    const input = await openConnectModal('amika', 'Amika API key');
    const form = document.querySelector('[data-role="api-key-modal"]');
    if (form === null) {
      throw new Error('expected the api-key modal to be open');
    }

    fireEvent.change(input, { target: { value: '  sk-enter  ' } });
    // Enter in a single-input form is an implicit submit — the same path the
    // Save button takes. The value is trimmed on its way out.
    fireEvent.submit(form);

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-enter' });
    });
    // Let the chained verify settle so a late duplicate would have surfaced.
    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });
    expect(transport.putSettings).toHaveBeenCalledTimes(1);
  });

  it('a second submit while the first is in flight is swallowed (one save)', async () => {
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

    const input = await openConnectModal('amika', 'Amika API key');
    const form = document.querySelector('[data-role="api-key-modal"]');
    if (form === null) {
      throw new Error('expected the api-key modal to be open');
    }
    fireEvent.change(input, { target: { value: 'sk-once' } });
    // The classic double-fire: Enter submits, and an impatient second Enter (or
    // a click on Save) lands while the first save is still in flight.
    fireEvent.submit(form);
    fireEvent.submit(form);

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

    const input = await openConnectModal('amika', 'Amika API key');
    fireEvent.change(input, { target: { value: 'sk-bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

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
          github_connection: {
            status: 'unknown',
            login: '',
            installation_id: 0,
            configure_url: '',
          },
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

  /** As mockPopulatedSettings, but with the repo credential the "Connect
   * GitHub" grant stores — the state a user reaches after connecting. */
  function mockConnectedSettings(): void {
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
        settings: {
          anthropic_api_key: { set: false, tail: '' },
          amika_api_key: { set: false, tail: '' },
          devin_api_key: { set: false, tail: '' },
          github_auth_token: { set: true, tail: 'abcd' },
          github_connection: {
            status: 'connected',
            login: 'octocat',
            installation_id: 4242,
            configure_url: 'https://github.com/settings/installations/4242',
          },
          amika_claude_cred_id: '',
        },
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
    expect(integrationCard('amika')).toBeInTheDocument();
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

  it('lists each API-key provider as one row, brand mark and all', async () => {
    mockConnectedSettings();
    renderDashboard();
    await screen.findByRole('button', { name: 'Sign out' });

    const section = document.querySelector('[data-role="integrations"]');
    const cards = section?.querySelectorAll('[data-role="integration-card"]') ?? [];
    expect([...cards].map((card) => card.getAttribute('data-provider'))).toEqual([
      'amika',
      'devin',
    ]);
    for (const card of cards) {
      // One row, nothing under it, and the provider's mark leading it.
      expect(card.children).toHaveLength(1);
      expect(card.querySelector('[data-role="integration-mark"] img')).not.toBeNull();
    }
  });

  it("a credential field's accessible name stays the label once its validity glyph lands", async () => {
    // The glyph now lives on the CARD, not beside the modal input, so it can no
    // longer join the input's computed name. Asserted anyway: the Playwright
    // suite still reaches this field by label, and the card's ✓ landing behind
    // the open dialog must not change what that label resolves to.
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

    const input = await openConnectModal('amika', 'Amika API key');
    // The label resolves to the input, and the validity glyph is nowhere inside
    // it — the card owns the glyph, so it cannot join the computed name.
    expect(screen.getByLabelText('Amika API key')).toBe(input);
    const modal = document.querySelector('[data-role="api-key-modal"]');
    expect(modal?.querySelector('[data-role="credential-status"]')).toBeNull();

    fireEvent.change(input, { target: { value: 'sk-named' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    // The glyph lands on the card once the verify returns, and the dialog closes.
    const glyph = await screen.findByText('✓');
    expect(glyph).toHaveAttribute('data-name', 'amika_api_key');
    expect(document.querySelector('[data-role="api-key-modal"]')).toBeNull();
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
          github_connection: {
            status: 'unknown',
            login: '',
            installation_id: 0,
            configure_url: '',
          },
          amika_claude_cred_id: 'cred-1',
        },
      }),
    );
    const { container } = renderDashboard();

    await screen.findByRole('button', { name: 'Sign out' });
    expect(container).toMatchSnapshot();
  });
});
