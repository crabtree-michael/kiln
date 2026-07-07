// Session gate tests (11 phase 2): the three post-load states off a mocked
// transport `fetchMe` — signed out (sign-in link, children absent), signed in
// without a project (dashboard pointer, children absent), signed in with a
// project (children render). Transport is mocked at the module boundary,
// mirroring `dashboard/dashboard-store.test.tsx`.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { SessionProvider } from '@/stores/session';
import { SessionGate } from '@/components/SessionGate';
import * as transport from '@/transport/transport';
import type { Me } from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchMe: vi.fn(),
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

function makeProject(): NonNullable<Me['project']> {
  return {
    name: 'proj',
    repo_url: 'https://github.com/a/b',
    amika_snapshot: 'snap',
    brain_model: 'model',
    worker_count: 1,
  };
}

function renderGate(): ReturnType<typeof render> {
  return render(
    <SessionProvider>
      <SessionGate>
        <div data-testid="app-children">the app</div>
      </SessionGate>
    </SessionProvider>,
  );
}

function Boom(): JSX.Element {
  throw new Error('children must not mount before the session is known');
}

describe('SessionGate', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
  });

  it('renders nothing while the session load is in flight', () => {
    // A never-resolving fetchMe pins the store at `loading`; the gate must
    // render neither a screen nor the children (which would open SSE/fetches).
    vi.mocked(transport.fetchMe).mockReturnValue(new Promise<Me | null>(() => undefined));
    const { container } = render(
      <SessionProvider>
        <SessionGate>
          <Boom />
        </SessionGate>
      </SessionProvider>,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows the GitHub sign-in and no children when signed out', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(null);
    renderGate();
    const link = await screen.findByRole('link', { name: 'Continue with GitHub' });
    expect(link).toHaveAttribute('href', '/auth/github/login');
    expect(screen.queryByTestId('app-children')).toBeNull();
  });

  it('points to the dashboard and hides children when signed in without a project', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    renderGate();
    const link = await screen.findByRole('link', { name: 'Finish setup on your dashboard' });
    expect(link).toHaveAttribute('href', '/dashboard');
    expect(screen.queryByTestId('app-children')).toBeNull();
  });

  it('renders the children when signed in with a project', async () => {
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe({ project: makeProject() }));
    renderGate();
    await waitFor(() => {
      expect(screen.getByTestId('app-children')).toBeInTheDocument();
    });
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('lands on the sign-in screen when the session load fails outright', async () => {
    vi.mocked(transport.fetchMe).mockRejectedValue(new Error('HTTP 500'));
    renderGate();
    expect(await screen.findByRole('link', { name: 'Continue with GitHub' })).toBeInTheDocument();
    expect(screen.queryByTestId('app-children')).toBeNull();
  });
});
