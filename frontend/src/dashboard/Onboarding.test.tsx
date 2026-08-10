// The guided setup flow (Onboarding.tsx): Connect GitHub → choose a project →
// choose a provider → finish. Transport is mocked at the module boundary and the
// whole `Dashboard` is rendered, mirroring Dashboard.test.tsx — `Onboarding`
// reads the store, so mounting it through the real provider is what exercises
// the flow rather than a hand-fed props object.
//
// The contract these lock down is the ORDERING and what each step is allowed to
// ask for, because that is the whole feature: a step must not ask for something
// it cannot yet know (a repo before the GitHub credential exists, a provider key
// before a provider is chosen), and finishing must write the key BEFORE the
// project so the project never comes alive pointing at a provider it has no
// credential for.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { RenderResult } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Dashboard } from '@/dashboard/Dashboard';
import * as transport from '@/transport/transport';
import type { Me, ProviderDescriptor } from '@/transport/transport';

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

/** The providers a deployment offers, in the sorted order `GET /api/me` serves
 * them. Amika and Devin are what the shipped registry lists (backend registry.go);
 * `keyless` is a stand-in for a provider that authenticates some other way — it has
 * no credential slot in `integrations-config`, which is what makes it the "no API
 * key needed" case below. The flow names no provider, so a descriptor the registry
 * does not (yet) serve is exactly what proves it renders from data. */
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
    key: 'devin',
    label: 'Devin',
    capabilities: {
      managed_sandbox: true,
      reports_cost: true,
      snapshots: false,
      secrets_inject: false,
    },
  },
  {
    key: 'keyless',
    label: 'Keyless',
    capabilities: {
      managed_sandbox: false,
      reports_cost: false,
      snapshots: false,
      secrets_inject: false,
    },
  },
];

/** What `PUT /api/settings` actually answers once an Amika key lands: the same
 * account view with that secret now reported as stored (write-only, so only its
 * presence + tail come back). The flow reads `set` to decide it has everything
 * it needs, so a fixture that forgot this would misrepresent a successful save. */
function meWithAmikaKey(): Me {
  return makeMe({
    settings: {
      anthropic_api_key: { set: false, tail: '' },
      amika_api_key: { set: true, tail: 'mika' },
      devin_api_key: { set: false, tail: '' },
      github_auth_token: { set: false, tail: '' },
      github_connection: {
        status: 'connected',
        login: 'octocat',
        installation_id: 4242,
        configure_url: 'https://github.com/settings/installations/4242',
      },
      amika_claude_cred_id: '',
    },
  });
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
      github_connection: {
        status: 'connected',
        login: 'octocat',
        installation_id: 4242,
        configure_url: 'https://github.com/settings/installations/4242',
      },
      amika_claude_cred_id: '',
    },
    providers: PROVIDERS,
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

/** The primary action; its label switches from "Continue" to "Finish setup" on
 * the last step, so tests address it by role. */
function nextButton(): HTMLElement {
  const button = document.querySelector('[data-role="onboarding-next"]');
  if (!(button instanceof HTMLElement)) {
    throw new Error('the flow rendered no primary action');
  }
  return button;
}

/** Walks a connected account from step 1 to step 2 (the repo picker). */
async function reachProjectStep(): Promise<void> {
  await screen.findByRole('heading', { name: 'Connect GitHub' });
  await waitFor(() => {
    expect(nextButton()).toBeEnabled();
  });
  fireEvent.click(nextButton());
  await screen.findByRole('heading', { name: 'Choose your project' });
}

/** Walks a connected account all the way to step 3, picking the first repo. */
async function reachProviderStep(): Promise<void> {
  await reachProjectStep();
  fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
    target: { value: REPOS[0]?.url },
  });
  fireEvent.click(nextButton());
  await screen.findByRole('heading', { name: 'Choose your provider' });
}

describe('Onboarding — the guided setup flow', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    vi.mocked(transport.putSettings).mockReset();
    vi.mocked(transport.putProject).mockReset();
    vi.mocked(transport.postVerify).mockReset();
    vi.mocked(transport.fetchGitHubRepos).mockReset();
    vi.mocked(transport.fetchMe).mockResolvedValue(makeMe());
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({ connected: true, repos: REPOS });
  });

  afterEach(() => {
    vi.mocked(transport.fetchMe).mockReset();
    vi.mocked(transport.putSettings).mockReset();
    vi.mocked(transport.putProject).mockReset();
    vi.mocked(transport.postVerify).mockReset();
    vi.mocked(transport.fetchGitHubRepos).mockReset();
  });

  // ------------------------------------------------------------- step 1

  it('starts on Connect GitHub, and blocks the flow there until the account is connected', async () => {
    vi.mocked(transport.fetchGitHubRepos).mockResolvedValue({ connected: false, repos: [] });
    renderDashboard();

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
        'data-state',
        'disconnected',
      );
    });
    // The one shared connect route — the same one the sign-in links and the
    // Integrations card use — and a backend route, so a real navigation rather
    // than a router Link. It always asks for `repo`, so clearing it returns the
    // user here connected: `next=dashboard` is what makes "here" true, since
    // this step is the middle of onboarding and not a way into the app.
    expect(screen.getByRole('link', { name: 'Connect GitHub' })).toHaveAttribute(
      'href',
      '/auth/github/connect?next=dashboard',
    );
    // The gate: you cannot reach the repo picker without a credential to list from.
    expect(nextButton()).toBeDisabled();
  });

  it('reads back the connected account and lets the flow move on', async () => {
    renderDashboard();

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    await waitFor(() => {
      expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
        'data-state',
        'connected',
      );
    });
    expect(document.querySelector('[data-role="github-connected-as"]')?.textContent).toContain(
      'octocat',
    );
    expect(nextButton()).toBeEnabled();
  });

  it('never flashes the connect prompt while the account is still loading', async () => {
    // The ordering `RepoField` established: `connected: false` means both "still
    // loading" and "must authorize", so the loading reading has to win first or
    // every already-connected user sees a wrong prompt for a frame.
    vi.mocked(transport.fetchGitHubRepos).mockReturnValue(new Promise(() => undefined));
    renderDashboard();

    await screen.findByRole('heading', { name: 'Connect GitHub' });
    expect(document.querySelector('[data-role="github-connect"]')).toHaveAttribute(
      'data-state',
      'loading',
    );
    expect(screen.queryByRole('link', { name: 'Connect GitHub' })).toBeNull();
  });

  // ------------------------------------------------------------- step 2

  it('asks for the repo and nothing else — no name field at all', async () => {
    renderDashboard();
    await reachProjectStep();

    // Only what this step needs: the picker. The name still comes from the repo
    // (auto-name from repository), but the step no longer says so — no field,
    // and no note about the naming either; worker count, merge gate and snapshot
    // are settings concerns, not first-run questions.
    expect(screen.getByRole('combobox', { name: 'Repository' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Project name')).toBeNull();
    expect(document.querySelector('[data-role="project-name-note"]')).toBeNull();
    expect(screen.queryByLabelText('Worker count')).toBeNull();
    expect(screen.queryByLabelText('Merge gate')).toBeNull();

    fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
      target: { value: 'https://github.com/octocat/atlas' },
    });

    expect(document.querySelector('[data-role="project-name-note"]')).toBeNull();
  });

  it('blocks the flow until a repo is picked', async () => {
    renderDashboard();
    await reachProjectStep();

    expect(nextButton()).toBeDisabled();
    fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
      target: { value: REPOS[0]?.url },
    });
    expect(nextButton()).toBeEnabled();
  });

  // ------------------------------------------------------------- step 3

  it('lists the deployment’s providers, and asks for no key until one is chosen', async () => {
    renderDashboard();
    await reachProviderStep();

    expect(
      [...document.querySelectorAll('[data-role="provider-option"]')].map((option) =>
        option.getAttribute('data-provider'),
      ),
    ).toEqual(['amika', 'devin', 'keyless']);
    // The step cannot know WHICH key to ask for yet — so it asks for none.
    expect(screen.queryByLabelText('Amika API key')).toBeNull();
    expect(screen.queryByLabelText('Devin API key')).toBeNull();
    expect(nextButton()).toBeDisabled();
  });

  it('asks for the chosen provider’s key, and only that provider’s', async () => {
    renderDashboard();
    await reachProviderStep();

    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    expect(screen.getByLabelText('Amika API key')).toBeInTheDocument();
    expect(screen.queryByLabelText('Devin API key')).toBeNull();

    fireEvent.click(screen.getByRole('radio', { name: 'Devin' }));
    expect(screen.getByLabelText('Devin API key')).toBeInTheDocument();
    expect(screen.queryByLabelText('Amika API key')).toBeNull();
  });

  it('asks for no key at all from a provider that authenticates some other way', async () => {
    renderDashboard();
    await reachProviderStep();

    fireEvent.click(screen.getByRole('radio', { name: 'Keyless' }));
    expect(document.querySelector('[data-role="credential-row"]')).toBeNull();
    expect(
      document.querySelector('[data-role="provider-option"][data-provider="keyless"]')?.textContent,
    ).toContain('No API key needed');
    // Nothing left to enter, so the flow can finish.
    expect(nextButton()).toBeEnabled();
  });

  it('will not finish while the chosen provider’s key is still missing', async () => {
    renderDashboard();
    await reachProviderStep();

    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    expect(nextButton()).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Amika API key'), { target: { value: 'sk-amika' } });
    expect(nextButton()).toBeEnabled();
  });

  // -------------------------------------------------------------- finish

  it('writes the provider key before creating the project, then hands over to settings', async () => {
    const withProject = makeMe({
      projects: [
        {
          id: 'proj-1',
          name: 'hello-world',
          repo_url: 'https://github.com/octocat/hello-world',
          agent_provider: 'amika',
          amika_snapshot: '',
          worker_count: 3,
          merge_gate_mode: 'main',
          amika_secrets: [],
        },
      ],
      settings: {
        anthropic_api_key: { set: false, tail: '' },
        amika_api_key: { set: true, tail: 'mika' },
        devin_api_key: { set: false, tail: '' },
        github_auth_token: { set: false, tail: '' },
        github_connection: {
          status: 'connected',
          login: 'octocat',
          installation_id: 4242,
          configure_url: 'https://github.com/settings/installations/4242',
        },
        amika_claude_cred_id: '',
      },
    });
    vi.mocked(transport.putSettings).mockResolvedValue(makeMe());
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });
    vi.mocked(transport.putProject).mockResolvedValue(withProject);

    renderDashboard();
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    fireEvent.change(screen.getByLabelText('Amika API key'), { target: { value: 'sk-amika' } });
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));

    await waitFor(() => {
      expect(transport.putProject).toHaveBeenCalledWith({
        name: 'hello-world',
        repo_url: 'https://github.com/octocat/hello-world',
        agent_provider: 'amika',
      });
    });
    expect(transport.putSettings).toHaveBeenCalledWith({ amika_api_key: 'sk-amika' });
    // Order matters: a project must never come alive pointing at a provider
    // whose credential has not landed yet.
    const keyOrder = vi.mocked(transport.putSettings).mock.invocationCallOrder[0];
    const projectOrder = vi.mocked(transport.putProject).mock.invocationCallOrder[0];
    expect(keyOrder).toBeDefined();
    expect(projectOrder).toBeDefined();
    expect(Number(keyOrder)).toBeLessThan(Number(projectOrder));

    // `me.projects` is now non-empty, so Dashboard swaps the flow out for
    // Settings — the flow itself never tracks "did I just finish".
    await screen.findByRole('button', { name: 'Sign out' });
    expect(document.querySelector('[data-role="onboarding"]')).toBeNull();
  });

  it('saves the key exactly once when it was already committed on blur', async () => {
    // The double-fire: the field auto-saves on blur, and clicking "Finish setup"
    // blurs it — so without the in-flight guard one typed key is written twice.
    vi.mocked(transport.putSettings).mockResolvedValue(meWithAmikaKey());
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });
    vi.mocked(transport.putProject).mockResolvedValue(makeMe());

    renderDashboard();
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Amika' }));
    const input = screen.getByLabelText('Amika API key');
    fireEvent.change(input, { target: { value: 'sk-amika' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(transport.putSettings).toHaveBeenCalledTimes(1);
    });
    // The draft cleared on success, so finishing has nothing left to commit.
    await waitFor(() => {
      expect(screen.getByLabelText('Amika API key')).toHaveValue('');
    });
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));

    await waitFor(() => {
      expect(transport.putProject).toHaveBeenCalledTimes(1);
    });
    expect(transport.putSettings).toHaveBeenCalledTimes(1);
  });

  it('keeps the user on the flow, with the reason, when the project write fails', async () => {
    vi.mocked(transport.putSettings).mockResolvedValue(makeMe());
    vi.mocked(transport.postVerify).mockResolvedValue({ checks: [] });
    vi.mocked(transport.putProject).mockRejectedValue(new Error('putProject: HTTP 500'));

    renderDashboard();
    await reachProviderStep();
    fireEvent.click(screen.getByRole('radio', { name: 'Keyless' }));
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));

    await waitFor(() => {
      expect(document.querySelector('[data-role="dashboard-error"]')?.textContent).toContain(
        'putProject: HTTP 500',
      );
    });
    // Still on the last step, with everything chosen — not bounced to the start.
    expect(screen.getByRole('heading', { name: 'Choose your provider' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Keyless' })).toBeChecked();
  });

  // ------------------------------------------------------------ the shell

  it('tracks progress in a rail, marking passed steps done', async () => {
    renderDashboard();
    await reachProjectStep();

    const rail = [...document.querySelectorAll('[data-role="onboarding-step"]')];
    expect(rail.map((step) => step.getAttribute('data-step'))).toEqual([
      'github',
      'project',
      'provider',
    ]);
    expect(rail.map((step) => step.getAttribute('data-state'))).toEqual([
      'done',
      'current',
      'todo',
    ]);
    expect(rail[1]).toHaveAttribute('aria-current', 'step');
  });

  it('goes back without losing what was already entered', async () => {
    renderDashboard();
    await reachProviderStep();

    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await screen.findByRole('heading', { name: 'Choose your project' });
    expect(screen.getByRole('combobox', { name: 'Repository' })).toHaveValue(REPOS[0]?.url);
    // Step 1 is the first step, so it offers no way further back.
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    await screen.findByRole('heading', { name: 'Connect GitHub' });
    expect(screen.queryByRole('button', { name: 'Back' })).toBeNull();
  });

  it('drops the provider step entirely on a deployment that offers no choice', async () => {
    // `providers` is omitted when the deployment has not enabled the descriptor
    // surface: there is no choice to present, so the flow must not show an empty
    // step. The project keeps resolving to the deployment default (AGENT_MODE).
    const noProviders = makeMe();
    delete noProviders.providers;
    vi.mocked(transport.fetchMe).mockResolvedValue(noProviders);
    vi.mocked(transport.putProject).mockResolvedValue(noProviders);

    renderDashboard();
    await reachProjectStep();

    expect(document.querySelectorAll('[data-role="onboarding-step"]')).toHaveLength(2);
    fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
      target: { value: REPOS[0]?.url },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Finish setup' }));

    await waitFor(() => {
      // No `agent_provider` key at all — not an empty string, which would be a
      // deliberate "use the default" the user never expressed.
      expect(transport.putProject).toHaveBeenCalledWith({
        name: 'hello-world',
        repo_url: 'https://github.com/octocat/hello-world',
      });
    });
  });
});
