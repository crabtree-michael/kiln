// Dashboard store tests (11 §5, §9): loads the signed-in account view on
// mount, swaps in the fresh `Me` after a settings/project save, runs the
// connectivity verify, and signs out. Transport is mocked at the module
// boundary, mirroring `stores/chat-store.test.tsx`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { DashboardProvider } from '@/dashboard/dashboard-store';
import { useDashboardStore } from '@/dashboard/dashboard-context';
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
      github_auth_token: { set: true, tail: 'abcd' },
      amika_claude_cred_id: '',
    },
    ...overrides,
  };
}

function Probe(): JSX.Element {
  const store = useDashboardStore();
  return (
    <div>
      <div data-testid="phase">{store.phase}</div>
      <div data-testid="saving">{String(store.saving)}</div>
      <div data-testid="error">{store.error ?? 'none'}</div>
      <div data-testid="verifying">{String(store.verifying)}</div>
      <div data-testid="login">{store.me?.user.github_login ?? 'none'}</div>
      <div data-testid="checks">
        {store.verifyChecks === null ? 'none' : String(store.verifyChecks.length)}
      </div>
      <div data-testid="pending-credentials">
        {store.pendingCredentials.size === 0 ? 'none' : [...store.pendingCredentials].join(',')}
      </div>
      <button
        type="button"
        onClick={() => void store.saveSettings({ anthropic_api_key: 'sk-new' })}
      >
        save-settings
      </button>
      <button type="button" onClick={() => void store.saveSettings({ amika_api_key: 'am-new' })}>
        save-settings-amika
      </button>
      <button
        type="button"
        onClick={() => void store.saveProject({ name: 'proj', repo_url: 'https://github.com/a/b' })}
      >
        save-project
      </button>
      <button type="button" onClick={() => void store.runVerify()}>
        run-verify
      </button>
      <button type="button" onClick={() => void store.signOut()}>
        sign-out
      </button>
    </div>
  );
}

describe('DashboardProvider', () => {
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

  it('loads me on mount → phase ready, me populated', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );

    expect(screen.getByTestId('phase').textContent).toBe('loading');

    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });
    expect(screen.getByTestId('login').textContent).toBe('octocat');
  });

  it('fetchMe resolves null → phase signed-out', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(null);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('signed-out');
    });
    expect(screen.getByTestId('login').textContent).toBe('none');
  });

  it('mount-time fetchMe rejection → phase signed-out with error set, never stuck loading', async () => {
    vi.mocked(transport.fetchMe).mockRejectedValue(new Error('fetchMe: HTTP 500'));

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('signed-out');
    });
    expect(screen.getByTestId('error').textContent).not.toBe('none');
    expect(screen.getByTestId('login').textContent).toBe('none');
  });

  it('saveSettings calls putSettings and swaps in the returned Me (saving toggles true→false)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    const updated = makeMe({
      settings: {
        anthropic_api_key: { set: true, tail: '-new' },
        amika_api_key: { set: false, tail: '' },
        github_auth_token: { set: true, tail: 'abcd' },
        amika_claude_cred_id: '',
      },
    });
    vi.mocked(transport.putSettings).mockResolvedValue(updated);
    vi.mocked(transport.postVerify).mockResolvedValue({
      checks: [{ name: 'anthropic', status: 'ok', message: 'reachable' }],
    });

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-settings'));
    });

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ anthropic_api_key: 'sk-new' });
    });
    await waitFor(() => {
      expect(screen.getByTestId('saving').textContent).toBe('false');
    });
    expect(screen.getByTestId('login').textContent).toBe('octocat');
  });

  it('saveSettings success on a credential field automatically chains runVerify (postVerify called)', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    const updated = makeMe({
      settings: {
        anthropic_api_key: { set: true, tail: '-new' },
        amika_api_key: { set: false, tail: '' },
        github_auth_token: { set: true, tail: 'abcd' },
        amika_claude_cred_id: '',
      },
    });
    vi.mocked(transport.putSettings).mockResolvedValue(updated);
    vi.mocked(transport.postVerify).mockResolvedValue({
      checks: [{ name: 'anthropic', status: 'ok', message: 'reachable' }],
    });

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-settings'));
    });

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledWith({ anthropic_api_key: 'sk-new' });
    });
    await waitFor(() => {
      expect(transport.postVerify).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(screen.getByTestId('checks').textContent).toBe('1');
    });
  });

  it('saveProject does NOT chain runVerify', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    const updated = makeMe({
      project: {
        name: 'proj',
        repo_url: 'https://github.com/a/b',
        amika_snapshot: '',
        brain_model: '',
        worker_count: 1,
      },
    });
    vi.mocked(transport.putProject).mockResolvedValue(updated);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-project'));
    });

    await waitFor(() => {
      expect(transport.putProject).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(screen.getByTestId('saving').textContent).toBe('false');
    });
    expect(transport.postVerify).not.toHaveBeenCalled();
  });

  it('pendingCredentials names the field mid-save, then clears once the chained verify settles', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    let resolvePut: ((me: Me) => void) | undefined;
    vi.mocked(transport.putSettings).mockImplementationOnce(
      () =>
        new Promise<Me>((resolve) => {
          resolvePut = resolve;
        }),
    );
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-settings'));
    });

    await waitFor(() => {
      expect(screen.getByTestId('pending-credentials').textContent).toBe('anthropic_api_key');
    });

    act(() => {
      resolvePut?.(makeMe());
    });

    await waitFor(() => {
      expect(screen.getByTestId('pending-credentials').textContent).toBe('none');
    });
  });

  it('two credential saves in flight stay pending independently — the first is not clobbered by the second', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    // Hold every PUT open, resolving them by hand in order.
    const resolvers: ((me: Me) => void)[] = [];
    vi.mocked(transport.putSettings).mockImplementation(
      () =>
        new Promise<Me>((resolve) => {
          resolvers.push(resolve);
        }),
    );
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-settings'));
      fireEvent.click(screen.getByText('save-settings-amika'));
    });

    // Both fields pending at once — a single-value `pendingCredential` would
    // have clobbered anthropic with amika here.
    await waitFor(() => {
      expect(screen.getByTestId('pending-credentials').textContent).toBe(
        'anthropic_api_key,amika_api_key',
      );
    });

    // First PUT resolves (its chained verify is mocked resolved) → only the
    // second field remains pending.
    act(() => {
      resolvers[0]?.(makeMe());
    });
    await waitFor(() => {
      expect(screen.getByTestId('pending-credentials').textContent).toBe('amika_api_key');
    });

    act(() => {
      resolvers[1]?.(makeMe());
    });
    await waitFor(() => {
      expect(screen.getByTestId('pending-credentials').textContent).toBe('none');
    });
  });

  it('saveProject calls putProject and swaps in the returned Me', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    const updated = makeMe({
      project: {
        name: 'proj',
        repo_url: 'https://github.com/a/b',
        amika_snapshot: '',
        brain_model: '',
        worker_count: 1,
      },
    });
    vi.mocked(transport.putProject).mockResolvedValue(updated);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-project'));
    });

    await waitFor(() => {
      expect(transport.putProject).toHaveBeenCalledWith({
        name: 'proj',
        repo_url: 'https://github.com/a/b',
      });
    });
    await waitFor(() => {
      expect(screen.getByTestId('saving').textContent).toBe('false');
    });
  });

  it('runVerify populates verifyChecks from postVerify', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    const response: VerifyResponse = {
      checks: [
        { name: 'anthropic', status: 'ok', message: 'reachable' },
        { name: 'amika', status: 'skipped', message: 'not configured' },
      ],
    };
    vi.mocked(transport.postVerify).mockResolvedValue(response);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('run-verify'));
    });

    await waitFor(() => {
      expect(screen.getByTestId('checks').textContent).toBe('2');
    });
    expect(screen.getByTestId('verifying').textContent).toBe('false');
  });

  it('transport rejection → error set, phase stays ready', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    vi.mocked(transport.putSettings).mockRejectedValue(new Error('putSettings: HTTP 400'));

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    act(() => {
      fireEvent.click(screen.getByText('save-settings'));
    });

    await waitFor(() => {
      expect(screen.getByTestId('error').textContent).not.toBe('none');
    });
    expect(screen.getByTestId('phase').textContent).toBe('ready');
  });

  it('signOut calls postLogout then re-fetches → signed-out', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValueOnce(makeMe());
    vi.mocked(transport.postLogout).mockResolvedValue(undefined);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    vi.mocked(transport.fetchMe).mockResolvedValueOnce(null);

    act(() => {
      fireEvent.click(screen.getByText('sign-out'));
    });

    await waitFor(() => {
      expect(transport.postLogout).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('signed-out');
    });
  });

  it('signOut passes through loading while the re-fetch is in flight', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValueOnce(makeMe());
    vi.mocked(transport.postLogout).mockResolvedValue(undefined);

    render(
      <DashboardProvider>
        <Probe />
      </DashboardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('ready');
    });

    // Hold the sign-out re-fetch open so the intermediate phase is observable.
    let resolveRefetch: ((me: Me | null) => void) | undefined;
    vi.mocked(transport.fetchMe).mockImplementationOnce(
      () =>
        new Promise<Me | null>((resolve) => {
          resolveRefetch = resolve;
        }),
    );

    act(() => {
      fireEvent.click(screen.getByText('sign-out'));
    });

    // Immediately after signOut is invoked — postLogout resolved, fetchMe
    // pending — the phase must read 'loading', not jump straight to
    // 'signed-out' (the documented phase contract).
    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('loading');
    });
    expect(screen.getByTestId('login').textContent).toBe('octocat');

    act(() => {
      resolveRefetch?.(null);
    });

    await waitFor(() => {
      expect(screen.getByTestId('phase').textContent).toBe('signed-out');
    });
    expect(screen.getByTestId('login').textContent).toBe('none');
  });
});
