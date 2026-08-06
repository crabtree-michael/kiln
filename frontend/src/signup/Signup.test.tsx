// The sign-up rehearsal (`/signup`). Transport is mocked at the module boundary
// and the whole `Signup` route is rendered, mirroring Onboarding.test.tsx — the
// point of this route is that it drives the REAL flow, so mounting the real
// components through it is the only thing that proves it.
//
// What these lock down is the ticket, line by line: the flow starts from the
// beginning for an account that is already onboarded, both paths are walkable
// and switchable, and finishing writes nothing. That last one is the load-
// bearing case — `PUT /api/project` upserts over the caller's first project, so
// a rehearsal that reached the network would rewrite the project the tester
// actually works in.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { RenderResult } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Signup } from '@/signup/Signup';
import * as transport from '@/transport/transport';
import type { Me, MeProject, ProviderDescriptor } from '@/transport/transport';

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
  fetchSnapshots: vi.fn(() => Promise.resolve(null)),
}));

const REPOS = [
  {
    full_name: 'octocat/hello-world',
    url: 'https://github.com/octocat/hello-world',
    private: false,
  },
  { full_name: 'octocat/atlas', url: 'https://github.com/octocat/atlas', private: true },
];

const PROVIDERS: ProviderDescriptor[] = [
  {
    key: 'amika',
    label: 'Amika',
    capabilities: {
      managed_sandbox: true,
      reports_cost: true,
      snapshots: true,
      secrets_inject: true,
    },
  },
  {
    key: 'mock',
    label: 'Mock',
    capabilities: {
      managed_sandbox: false,
      reports_cost: false,
      snapshots: false,
      secrets_inject: false,
    },
  },
];

/** The project this account already has. Its presence is the whole reason the
 * route exists: on `/dashboard` it is what swaps the guided flow out for
 * Settings, so a tester with one can never see the flow again. */
const EXISTING_PROJECT: MeProject = {
  id: 'proj-1',
  name: 'kiln',
  repo_url: 'https://github.com/octocat/kiln',
  agent_provider: 'amika',
  amika_snapshot: '',
  worker_count: 3,
  merge_gate_mode: 'main',
  amika_secrets: [],
};

/** An account that has BEEN through sign-up: a project, and a stored, connected
 * provider key. Every test starts here unless it says otherwise. */
function makeMe(overrides: Partial<Me> = {}): Me {
  return {
    user: {
      github_login: 'octocat',
      display_name: 'Octocat',
      avatar_url: 'https://example.com/a.png',
    },
    projects: [EXISTING_PROJECT],
    settings: {
      anthropic_api_key: { set: false, tail: '' },
      amika_api_key: { set: true, tail: 'mika' },
      devin_api_key: { set: false, tail: '' },
      github_auth_token: { set: true, tail: 'oken' },
      github_connection: { status: 'connected', login: 'octocat', scopes: ['repo'] },
      amika_claude_cred_id: '',
    },
    providers: PROVIDERS,
    ...overrides,
  };
}

function renderSignup(path = '/signup'): RenderResult {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Signup />
    </MemoryRouter>,
  );
}

function nextButton(): HTMLElement {
  const button = document.querySelector('[data-role="onboarding-next"]');
  if (!(button instanceof HTMLElement)) {
    throw new Error('the flow rendered no primary action');
  }
  return button;
}

/** Every write the flow could possibly reach the server with. */
function expectNothingWritten(): void {
  expect(transport.putProject).not.toHaveBeenCalled();
  expect(transport.putSettings).not.toHaveBeenCalled();
  expect(transport.createProject).not.toHaveBeenCalled();
  expect(transport.updateProject).not.toHaveBeenCalled();
  expect(transport.deleteProject).not.toHaveBeenCalled();
}

/** Walks the returning path from step 1 to the last step, picking the first repo. */
async function reachProviderStep(): Promise<void> {
  await screen.findByRole('heading', { name: 'Connect GitHub' });
  await waitFor(() => {
    expect(nextButton()).toBeEnabled();
  });
  fireEvent.click(nextButton());
  await screen.findByRole('heading', { name: 'Choose your project' });
  fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
    target: { value: REPOS[0]?.url },
  });
  fireEvent.click(nextButton());
  await screen.findByRole('heading', { name: 'Choose your provider' });
}

describe('/signup — the sign-up rehearsal', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    vi.mocked(transport.putSettings).mockReset();
    vi.mocked(transport.putProject).mockReset();
    vi.mocked(transport.createProject).mockReset();
    vi.mocked(transport.updateProject).mockReset();
    vi.mocked(transport.deleteProject).mockReset();
    vi.mocked(transport.postVerify).mockReset();
    vi.mocked(transport.fetchGitHubRepos).mockReset();
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({ connected: true, repos: REPOS });
  });

  afterEach(() => {
    vi.resetAllMocks();
  });

  // ------------------------------------------------------------ no gating

  it('runs the flow for an account that is already onboarded', async () => {
    // The gate this route exists to remove: `/dashboard` reads a non-empty
    // `me.projects` as "onboarded" and renders Settings instead.
    renderSignup('/signup?as=returning');

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    expect(document.querySelector('[data-role="onboarding"]')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Sign out' })).toBeNull();
  });

  it('starts from the beginning again on every visit', async () => {
    const { unmount } = renderSignup('/signup?as=returning');
    await reachProviderStep();
    unmount();

    renderSignup('/signup?as=returning');
    await screen.findByRole('heading', { name: 'Connect GitHub' });
  });

  // ------------------------------------------------------- the two paths

  it('opens on the first-time path, from the sign-in card', async () => {
    renderSignup();

    // An unqualified visit is the whole thing from the start — which begins
    // before the guided steps, at "Continue with GitHub".
    const start = await screen.findByRole('button', { name: 'Continue with GitHub' });
    expect(document.querySelector('[data-role="onboarding"]')).toBeNull();
    // Simulated, not the real navigation: the real callback lands on
    // `/dashboard`, which would end the replay.
    expect(screen.queryByRole('link', { name: 'Continue with GitHub' })).toBeNull();

    fireEvent.click(start);
    await screen.findByRole('heading', { name: 'Connect GitHub' });
  });

  it('shows the first-time path an account with nothing set up', async () => {
    renderSignup('/signup?as=new');
    fireEvent.click(await screen.findByRole('button', { name: 'Continue with GitHub' }));

    // Disconnected, even though this account really is connected — that is the
    // screen a first-time user meets, and the point of the path.
    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
        'data-state',
        'disconnected',
      );
    });
    expect(nextButton()).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Connect GitHub' }));

    // The simulated grant hands the flow the tester's real repos.
    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
        'data-state',
        'connected',
      );
    });
    expect(nextButton()).toBeEnabled();
  });

  it('shows the returning path the connected account, and its stored key', async () => {
    renderSignup('/signup?as=returning');

    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
        'data-state',
        'connected',
      );
    });
    expect(document.querySelector('[data-role="github-connected-as"]')?.textContent).toContain(
      'octocat',
    );

    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    // The account really does have an Amika key stored, so the returning user
    // is not asked to type one again.
    expect(
      document.querySelector('[data-role="secret-status"][data-name="amika_api_key"]'),
    ).toHaveAttribute('data-set', 'true');
    expect(nextButton()).toBeEnabled();
  });

  it('hides the stored key from the first-time path', async () => {
    renderSignup('/signup?as=new');
    fireEvent.click(await screen.findByRole('button', { name: 'Continue with GitHub' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Connect GitHub' }));
    await reachProviderStep();

    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    expect(
      document.querySelector('[data-role="secret-status"][data-name="amika_api_key"]'),
    ).toHaveAttribute('data-set', 'false');
    // Nothing stored, so the flow holds until a key is typed.
    expect(nextButton()).toBeDisabled();
  });

  it('switches path from the banner, restarting the run', async () => {
    renderSignup('/signup?as=returning');
    await reachProviderStep();

    fireEvent.click(screen.getByRole('button', { name: 'New user' }));

    // Back at the very start of the other path, not stranded on step 3.
    await screen.findByRole('button', { name: 'Continue with GitHub' });
    expect(document.querySelector('[data-role="signup-path"][data-path="new"]')).toHaveAttribute(
      'data-selected',
      'true',
    );
  });

  it('starts over on demand, from wherever the run got to', async () => {
    renderSignup('/signup?as=returning');
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));

    fireEvent.click(screen.getByRole('button', { name: 'Start over' }));

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    // A remount, so the run's own state went with it — not just the step index.
    fireEvent.click(nextButton());
    await screen.findByRole('heading', { name: 'Choose your project' });
    expect(screen.getByRole('combobox', { name: 'Repository' })).toHaveValue('');
  });

  // ------------------------------------------------------ nothing is written

  it('finishes the flow without writing anything', async () => {
    renderSignup('/signup?as=returning');
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));

    // The run ends on its own panel — the real flow's hand-over to Settings has
    // no meaning here, because no project was created.
    await screen.findByRole('heading', { name: 'That’s the whole flow.' });
    expect(
      document.querySelector('[data-role="signup-done-item"][data-field="repo"]')?.textContent,
    ).toContain('https://github.com/octocat/hello-world');
    expect(
      document.querySelector('[data-role="signup-done-item"][data-field="provider"]')?.textContent,
    ).toContain('Amika');
    expectNothingWritten();
  });

  it('never sends a typed provider key to the server', async () => {
    // The destructive one: provider keys are user-scoped, so a throwaway key
    // typed during a rehearsal would replace the real key every one of this
    // account's projects runs on.
    renderSignup('/signup?as=new');
    fireEvent.click(await screen.findByRole('button', { name: 'Continue with GitHub' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Connect GitHub' }));
    await reachProviderStep();

    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    const input = screen.getByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-throwaway' } });
    fireEvent.blur(input);

    // The save still LOOKS like the real one — the field reports the key stored
    // and the chained verify marks it good, which is the sequence a tester came
    // to look at.
    await waitFor(() => {
      expect(
        document.querySelector('[data-role="credential-status"][data-name="amika_api_key"]'),
      ).toHaveAttribute('data-status', 'ok');
    });
    expect(transport.putSettings).not.toHaveBeenCalled();
    expect(transport.postVerify).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));
    await screen.findByRole('heading', { name: 'That’s the whole flow.' });
    expectNothingWritten();
  });

  it('runs again from the end panel, with the account no less onboarded', async () => {
    renderSignup('/signup?as=returning');
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Mock' }));
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));
    await screen.findByRole('heading', { name: 'That’s the whole flow.' });

    fireEvent.click(screen.getByRole('button', { name: 'Run it again' }));

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    expectNothingWritten();
  });

  // ----------------------------------------------------------- signed out

  it('asks a signed-out visitor to sign in for real', async () => {
    // The one part that cannot be simulated: the allowlist check behind the
    // OAuth route is what decides whether this tester gets in at all.
    vi.mocked(transport.fetchMe).mockResolvedValue(null);
    renderSignup();

    expect(await screen.findByRole('link', { name: 'Continue with GitHub' })).toHaveAttribute(
      'href',
      '/auth/github/connect',
    );
  });
});
