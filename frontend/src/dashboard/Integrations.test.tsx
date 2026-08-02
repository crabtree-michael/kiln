// Integrations section (11 §5): a connect card per provider. These cover the
// card-level contract in isolation — the GitHub card's two states and its
// right-aligned "switch account" action, and the API-key modal's open/save/
// dismiss behaviour. The wired-up flow (save → chained verify → indicator)
// lives in Dashboard.test.tsx against the real store.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Integrations } from '@/dashboard/Integrations';
import type { components } from '@/schema/generated';
import type { VerifyCheck } from '@/transport/transport';

type MeSettings = components['schemas']['MeSettings'];
type GitHubConnection = components['schemas']['GitHubConnection'];

/** The GitHub repo credential as `GET /api/me` reports it. Defaults to the
 * no-credential case; each test names the state it cares about. */
function connection(overrides: Partial<GitHubConnection> = {}): GitHubConnection {
  return { status: 'disconnected', login: '', scopes: [], ...overrides };
}

function settings(overrides: Partial<MeSettings> = {}): MeSettings {
  return {
    anthropic_api_key: { set: false, tail: '' },
    amika_api_key: { set: false, tail: '' },
    devin_api_key: { set: false, tail: '' },
    github_auth_token: { set: false, tail: '' },
    github_connection: connection(),
    amika_claude_cred_id: '',
    ...overrides,
  };
}

interface RenderOptions {
  githubLogin?: string;
  settingsOverrides?: Partial<MeSettings>;
  verifyChecks?: VerifyCheck[] | null;
  pending?: ReadonlySet<never>;
  onSave?: (body: unknown) => Promise<boolean>;
}

function renderIntegrations(options: RenderOptions = {}): {
  onSave: ReturnType<typeof vi.fn>;
} {
  const onSave = vi.fn(options.onSave ?? (() => Promise.resolve(true)));
  render(
    <Integrations
      settings={settings(options.settingsOverrides)}
      github={{ repos: [], connected: false, loading: false, error: null, refresh: noRefresh }}
      githubLogin={options.githubLogin ?? 'octocat'}
      pendingCredentials={new Set()}
      verifying={false}
      verifyChecks={options.verifyChecks ?? null}
      onSave={onSave}
    />,
  );
  return { onSave };
}

/** The picker's refresh action — never invoked by these tests. */
const noRefresh = (): Promise<void> => Promise.resolve();

function card(provider: string): HTMLElement {
  const el = document.querySelector<HTMLElement>(
    `[data-role="integration-card"][data-provider="${provider}"]`,
  );
  if (el === null) {
    throw new Error(`expected an integration card for ${provider}`);
  }
  return el;
}

describe('Integrations', () => {
  it('renders one card per provider and no free-text token field', () => {
    renderIntegrations();
    // Anthropic stays hidden behind its retained env flag (off by default).
    expect(document.querySelectorAll('[data-role="integration-card"]')).toHaveLength(3);
    expect(card('github')).toBeInTheDocument();
    expect(card('amika')).toBeInTheDocument();
    expect(card('devin')).toBeInTheDocument();
    expect(screen.queryByLabelText('GitHub token')).toBeNull();
  });

  it('GitHub disconnected: offers Connect through the repo-scoped OAuth route', () => {
    renderIntegrations({ githubLogin: '' });
    const github = card('github');
    expect(github).toHaveAttribute('data-connected', 'false');
    expect(within(github).getByText('Not connected')).toBeInTheDocument();

    const link = within(github).getByRole('link', { name: 'Connect' });
    // The repo-scoped grant, NOT the scopeless /auth/github/login sign-in —
    // this is the only path that yields a credential able to clone and push.
    expect(link).toHaveAttribute('href', '/auth/github/connect');
    // No account line until there is a credential.
    expect(github.querySelector('[data-role="github-account"]')).toBeNull();
  });

  it('states plainly that connecting grants repo read/write access', () => {
    renderIntegrations({ githubLogin: '' });
    const note = within(card('github')).getByText(/read and write access/i);
    expect(note).toHaveAttribute('data-role', 'github-access-note');
    expect(note.textContent).toMatch(/push/i);
  });

  it('GitHub connected: "Switch account" sits in the right-hand action slot', () => {
    renderIntegrations({
      settingsOverrides: {
        github_auth_token: { set: true, tail: 'abcd' },
        github_connection: connection({ status: 'connected', login: 'octocat', scopes: ['repo'] }),
      },
    });
    const github = card('github');
    expect(github).toHaveAttribute('data-connected', 'true');
    expect(within(github).getByText('@octocat')).toBeInTheDocument();

    const link = within(github).getByRole('link', { name: 'Switch account' });
    expect(link).toHaveAttribute('href', '/auth/github/connect');
    // The action slot — not the identity block, not the detail line. This is
    // what CSS pins to the card's right edge (`margin-left: auto`).
    expect(link.parentElement).toHaveAttribute('data-role', 'integration-action');
    // And it is NOT inline with the account text.
    const account = within(github).getByText('@octocat');
    expect(account.contains(link)).toBe(false);
  });

  // The migration guarantee, at the UI: a token carried over from the removed
  // manual field reports status "unknown" and must still read as connected.
  it('GitHub with a carried-over manual token reads as connected', () => {
    renderIntegrations({
      githubLogin: 'octocat',
      settingsOverrides: {
        github_auth_token: { set: true, tail: 'abcd' },
        github_connection: connection({ status: 'unknown' }),
      },
    });
    const github = card('github');
    expect(github).toHaveAttribute('data-connected', 'true');
    expect(within(github).getByText('Connected')).toBeInTheDocument();
    // Its own account was never recorded, so the card falls back to the
    // session's login rather than showing nothing.
    expect(within(github).getByText('@octocat')).toBeInTheDocument();
    expect(within(github).getByRole('link', { name: 'Switch account' })).toBeInTheDocument();
    expect(github.querySelector('[data-role="github-reconnect-note"]')).toBeNull();
  });

  it('GitHub whose credential lacks repo scope prompts a reconnect', () => {
    renderIntegrations({
      settingsOverrides: {
        github_auth_token: { set: true, tail: 'abcd' },
        github_connection: connection({ status: 'needs_reconnect', scopes: ['gist'] }),
      },
    });
    const github = card('github');
    expect(github).toHaveAttribute('data-connected', 'false');
    expect(within(github).getByText('Reconnect needed')).toBeInTheDocument();
    expect(within(github).getByText(/doesn’t include repository access/i)).toBeInTheDocument();

    const link = within(github).getByRole('link', { name: 'Reconnect' });
    expect(link).toHaveAttribute('href', '/auth/github/connect');
  });

  it('an unconfigured key card says Connect; a configured one says Update key', () => {
    renderIntegrations({ settingsOverrides: { devin_api_key: { set: true, tail: 'wxyz' } } });
    expect(within(card('amika')).getByRole('button', { name: 'Connect' })).toBeInTheDocument();
    expect(within(card('devin')).getByRole('button', { name: 'Update key' })).toBeInTheDocument();
    // The stored key's fingerprint is shown, never the value.
    expect(within(card('devin')).getByText('configured · …wxyz')).toBeInTheDocument();
  });

  it('Connect opens a modal that sends only that provider’s key', async () => {
    const { onSave } = renderIntegrations();
    fireEvent.click(within(card('devin')).getByRole('button', { name: 'Connect' }));

    const input = screen.getByLabelText('Devin API key');
    fireEvent.change(input, { target: { value: '  cog-abc  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({ devin_api_key: 'cog-abc' });
    });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('Escape and the backdrop both dismiss the modal without saving', () => {
    const { onSave } = renderIntegrations();
    const open = (): void => {
      fireEvent.click(within(card('amika')).getByRole('button', { name: 'Connect' }));
    };
    const modal = (): Element | null => document.querySelector('[data-role="api-key-modal"]');
    const backdrop = (): Element => {
      const el = document.querySelector('[data-role="api-key-backdrop"]');
      if (el === null) {
        throw new Error('expected the modal backdrop');
      }
      return el;
    };

    open();
    fireEvent.keyDown(screen.getByLabelText('Amika API key'), { key: 'Escape' });
    expect(modal()).toBeNull();

    open();
    fireEvent.click(backdrop());
    expect(modal()).toBeNull();

    // A click inside the dialog must NOT bubble up into the backdrop dismiss.
    open();
    fireEvent.click(screen.getByLabelText('Amika API key'));
    expect(modal()).not.toBeNull();

    expect(onSave).not.toHaveBeenCalled();
  });

  it('a failed save keeps the modal open with the typed value intact', async () => {
    renderIntegrations({ onSave: () => Promise.resolve(false) });
    fireEvent.click(within(card('amika')).getByRole('button', { name: 'Connect' }));
    const input = screen.getByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-bad' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => {
      expect(document.querySelector('[data-role="api-key-modal"]')).not.toBeNull();
    });
    expect(screen.getByLabelText('Amika API key')).toHaveValue('sk-bad');
  });

  it('the Amika card carries the non-secret Claude credential ID, committed on blur', async () => {
    const { onSave } = renderIntegrations();
    const input = within(card('amika')).getByLabelText('Amika Claude credential ID');
    fireEvent.change(input, { target: { value: ' cred-9 ' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledWith({ amika_claude_cred_id: 'cred-9' });
    });
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  it('maps each card’s indicator to its own verify check (GitHub reads "repo")', () => {
    renderIntegrations({
      verifyChecks: [
        { name: 'amika', status: 'ok', message: 'reachable' },
        { name: 'devin', status: 'failed', message: 'invalid key' },
        { name: 'repo', status: 'ok', message: 'repo reachable' },
      ],
    });
    const indicator = (name: string): Element | null =>
      document.querySelector(`[data-role="credential-status"][data-name="${name}"]`);

    expect(indicator('amika_api_key')).toHaveAttribute('data-status', 'ok');
    expect(indicator('devin_api_key')).toHaveAttribute('data-status', 'failed');
    expect(indicator('devin_api_key')).toHaveAttribute('title', 'invalid key');
    expect(indicator('github_auth_token')).toHaveAttribute('data-status', 'ok');
  });
});
