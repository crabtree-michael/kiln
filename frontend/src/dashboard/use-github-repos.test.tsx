// The connected-GitHub-account hook behind the settings repo picker. The point
// of these tests is the three-way distinction the picker depends on: connected,
// not-connected (a normal 200 — the connect prompt), and a genuinely failed
// request (an error worth showing, which must NOT masquerade as either).
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { useGitHubRepos } from '@/dashboard/use-github-repos';
import * as transport from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchGitHubRepos: vi.fn(),
}));

/** Renders the hook's state as data attributes so assertions read off the DOM
 * without a renderHook dependency. */
function Probe(): JSX.Element {
  const github = useGitHubRepos();
  return (
    <div
      data-testid="probe"
      data-connected={String(github.connected)}
      data-loading={String(github.loading)}
      data-error={github.error ?? ''}
      data-names={github.repos.map((repo) => repo.full_name).join(',')}
    >
      <button
        type="button"
        onClick={() => {
          void github.refresh();
        }}
      >
        refresh
      </button>
    </div>
  );
}

function probe(): HTMLElement {
  return screen.getByTestId('probe');
}

describe('useGitHubRepos', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchGitHubRepos).mockReset();
  });

  it('loads the connected account’s repos on mount', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({
      connected: true,
      repos: [
        { full_name: 'acme/api', url: 'https://github.com/acme/api', private: true },
        { full_name: 'acme/web', url: 'https://github.com/acme/web', private: false },
      ],
    });

    render(<Probe />);

    await waitFor(() => {
      expect(probe().dataset.loading).toBe('false');
    });
    expect(probe().dataset.connected).toBe('true');
    expect(probe().dataset.names).toBe('acme/api,acme/web');
    expect(probe().dataset.error).toBe('');
  });

  // Not connected is a normal answer, not a failure: no error, so the form shows
  // the connect prompt rather than an error banner.
  it('treats a disconnected account as state, not an error', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({ connected: false, repos: [] });

    render(<Probe />);

    await waitFor(() => {
      expect(probe().dataset.loading).toBe('false');
    });
    expect(probe().dataset.connected).toBe('false');
    expect(probe().dataset.error).toBe('');
  });

  it('surfaces a failed request as an error', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockRejectedValue(new Error('HTTP 502'));

    render(<Probe />);

    await waitFor(() => {
      expect(probe().dataset.loading).toBe('false');
    });
    expect(probe().dataset.error).toBe('HTTP 502');
    expect(probe().dataset.connected).toBe('false');
  });

  // A refresh that fails must not demote a working picker to the connect
  // prompt — that would lose the user's list mid-edit over a transient blip.
  it('keeps a working connection when a refresh fails', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValueOnce({
      connected: true,
      repos: [{ full_name: 'acme/api', url: 'https://github.com/acme/api', private: false }],
    });
    render(<Probe />);
    await waitFor(() => {
      expect(probe().dataset.connected).toBe('true');
    });

    vi.mocked(transport.fetchGitHubRepos).mockRejectedValueOnce(new Error('HTTP 502'));
    act(() => {
      screen.getByRole('button', { name: 'refresh' }).click();
    });

    await waitFor(() => {
      expect(probe().dataset.error).toBe('HTTP 502');
    });
    expect(probe().dataset.connected).toBe('true');
    expect(probe().dataset.names).toBe('acme/api');
  });
});
