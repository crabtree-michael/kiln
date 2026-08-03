// ProjectFields' editors worth testing directly.
//
// The repo picker (settings repo picker): the repo is chosen from the connected
// GitHub account, never typed. Those tests cover the connect prompt, preselecting
// an existing repo_url across the URL spellings hand-entry produced, and keeping
// an unlistable repo selectable so a save can't silently drop it.
import { describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { ProjectFields } from '@/dashboard/ConfigFields';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import type {
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  Snapshot,
} from '@/transport/transport';

/** A connected account whose listing contains `baseProject`'s repo — the
 * ordinary state, so the snapshot tests below exercise the rest of the form
 * with the picker settled. */
function connectedGitHub(overrides: Partial<GitHubRepos> = {}): GitHubRepos {
  return {
    repos: [{ full_name: 'acme/demo', url: 'https://github.com/acme/demo', private: false }],
    connected: true,
    loading: false,
    error: null,
    refresh: () => Promise.resolve(),
    ...overrides,
  };
}

/** ProjectFields' onSave, typed so the captured call body is ProjectUpdateRequest
 * (no assertion needed to read a field off it). */
type SaveMock = Mock<(body: ProjectUpdateRequest) => Promise<void>>;

function baseProject(overrides: Partial<MeProject> = {}): MeProject {
  return {
    id: 'proj-1',
    name: 'demo',
    repo_url: 'https://github.com/acme/demo',
    agent_provider: '',
    amika_snapshot: '',
    worker_count: 3,
    merge_gate_mode: 'main',
    amika_secrets: [],
    ...overrides,
  };
}

/** The last ProjectUpdateRequest body a mocked onSave received. */
function lastBody(onSave: SaveMock): ProjectUpdateRequest {
  const last = onSave.mock.calls.at(-1);
  if (last === undefined) {
    throw new Error('onSave was never called');
  }
  return last[0];
}

describe('ProjectFields — sandbox secrets are no longer edited here', () => {
  it('renders no secrets editor', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({
          amika_secrets: [{ name: 'OPENAI_API_KEY', value: { set: true, tail: 'cdef' } }],
        })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(screen.queryByText('Sandbox secrets')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add secret' })).not.toBeInTheDocument();
  });

  it('omits amika_secrets from the save body so stored secrets survive', () => {
    // `amika_secrets` is a wholesale upsert (11 §4): sending the list this form
    // no longer holds would clear every stored secret on an unrelated save, so
    // the key must be absent — not [].
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({
          amika_secrets: [{ name: 'OPENAI_API_KEY', value: { set: true, tail: 'cdef' } }],
        })}
        saving={false}
        onSave={onSave}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(lastBody(onSave)).not.toHaveProperty('amika_secrets');
  });
});

describe('ProjectFields — merge gate (06 §7)', () => {
  it('seeds the select from the project and defaults to main', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(screen.getByRole('combobox', { name: /merge gate/i })).toHaveValue('main');
  });

  it('seeds the select from a project set to the pr gate', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ merge_gate_mode: 'pr' })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(screen.getByRole('combobox', { name: /merge gate/i })).toHaveValue('pr');
  });

  it('submits the chosen gate mode', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByRole('combobox', { name: /merge gate/i }), {
      target: { value: 'pr' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).merge_gate_mode).toBe('pr');
  });
});

// Per-project provider select (multi-provider design §8, §9). The select only
// appears when the deployment offers more than one provider; a single-provider
// deployment is unchanged, and the choice round-trips as agent_provider.
describe('ProjectFields — provider select', () => {
  const caps = { managed_sandbox: true, reports_cost: true, snapshots: true, secrets_inject: true };
  const providers: ProviderDescriptor[] = [
    { key: 'amika', label: 'Amika', capabilities: caps },
    { key: 'devin', label: 'Devin', capabilities: { ...caps, managed_sandbox: false } },
  ];

  it('is hidden when the deployment offers one or zero providers', () => {
    const single: ProviderDescriptor[] = [{ key: 'amika', label: 'Amika', capabilities: caps }];
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        providers={single}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(screen.queryByRole('combobox', { name: /agent provider/i })).toBeNull();
  });

  it('seeds from the project and lists every offered provider plus the default', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ agent_provider: 'devin' })}
        providers={providers}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    const select = screen.getByRole('combobox', { name: /agent provider/i });
    expect(select).toHaveValue('devin');
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Default', 'Amika', 'Devin']);
  });

  it('submits the chosen provider key', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        providers={providers}
        saving={false}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByRole('combobox', { name: /agent provider/i }), {
      target: { value: 'devin' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).agent_provider).toBe('devin');
  });
});

describe('ProjectFields — snapshot selection (sandbox selection)', () => {
  const snapshots: Snapshot[] = [
    {
      ref: 'org/base:1',
      name: 'base',
      description: '',
      source: 'dev-a',
      state: 'ready',
      created_at: '2026-07-01T10:00:00Z',
    },
    {
      ref: 'org/wip:2',
      name: 'wip',
      description: '',
      source: '',
      state: 'capturing',
      created_at: '2026-07-02T10:00:00Z',
    },
  ];

  it('renders a free-text snapshot input when no catalog is available', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    // No picker; the back-compat text input is present.
    expect(screen.queryByRole('combobox', { name: /snapshot/i })).toBeNull();
    expect(screen.getByRole('textbox', { name: /amika snapshot/i })).toBeInTheDocument();
  });

  it('renders a snapshot picker from the catalog, with capturing snapshots disabled', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ amika_snapshot: 'org/base:1' })}
        snapshots={snapshots}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    const select = screen.getByRole('combobox', { name: /sandbox snapshot/i });
    expect(select).toHaveValue('org/base:1');
    const options = within(select).getAllByRole('option');
    expect(options.map((o) => o.textContent)).toEqual(['Default', 'base', 'wip (capturing)']);
    // The still-capturing snapshot is listed but not selectable.
    const wip = options.find((o) => o.textContent === 'wip (capturing)');
    expect(wip).toHaveProperty('disabled', true);
  });

  it('keeps a stored handle that is no longer in the catalog selectable', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ amika_snapshot: 'legacy-handle' })}
        snapshots={snapshots}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    const select = screen.getByRole('combobox', { name: /sandbox snapshot/i });
    expect(select).toHaveValue('legacy-handle');
    expect(within(select).getByText('legacy-handle (current)')).toBeInTheDocument();
  });

  it('submits the chosen snapshot ref as amika_snapshot', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        snapshots={snapshots}
        catalogAvailable
        saving={false}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByRole('combobox', { name: /sandbox snapshot/i }), {
      target: { value: 'org/base:1' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).amika_snapshot).toBe('org/base:1');
  });
});

// The project card no longer captures a sandbox as a snapshot: saving a sandbox
// is a per-TICKET choice now (the ticket detail sheet's sandbox switch), so the
// project form must offer no trace of the old capture section.
describe('ProjectFields — no sandbox capture section', () => {
  it('offers no dev-box capture form, even with a catalog', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(screen.queryByRole('combobox', { name: /dev box/i })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save snapshot' })).toBeNull();
    expect(screen.queryByRole('textbox', { name: /snapshot name/i })).toBeNull();
  });
});

describe('ProjectFields — repo picker', () => {
  /** The repo dropdown, as a select so option state is readable. */
  function repoSelect(): HTMLSelectElement {
    const el = screen.getByRole('combobox', { name: 'Repository' });
    if (!(el instanceof HTMLSelectElement)) {
      throw new Error('expected the repo field to be a select');
    }
    return el;
  }

  const manyRepos = (count: number): GitHubRepos['repos'] =>
    Array.from({ length: count }, (_unused, i) => ({
      full_name: `acme/repo-${String(i)}`,
      url: `https://github.com/acme/repo-${String(i)}`,
      private: false,
    }));

  it('offers the connected account’s repos and submits the picked URL', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub({
          repos: [
            { full_name: 'acme/demo', url: 'https://github.com/acme/demo', private: false },
            { full_name: 'acme/secret', url: 'https://github.com/acme/secret', private: true },
          ],
        })}
        project={baseProject()}
        saving={false}
        onSave={onSave}
      />,
    );

    // Private repos are listed (that is what the repo scope buys) and marked.
    expect(screen.getByRole('option', { name: 'acme/secret (private)' })).toBeInTheDocument();

    fireEvent.change(repoSelect(), { target: { value: 'https://github.com/acme/secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    expect(lastBody(onSave).repo_url).toBe('https://github.com/acme/secret');
  });

  // The migration case: repo_url values were typed by hand before the picker
  // existed, so they carry casing/.git/trailing-slash variants GitHub's own
  // html_url never has. Each must still resolve to the listed repo.
  it.each([
    ['https://github.com/acme/demo'],
    ['https://github.com/acme/demo.git'],
    ['https://github.com/acme/demo/'],
    ['https://GitHub.com/Acme/Demo'],
  ])('preselects the stored repo_url %s', (storedUrl) => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ repo_url: storedUrl })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(repoSelect().value).toBe('https://github.com/acme/demo');
    // No "(current)" fallback option — it matched a real listed repo.
    expect(screen.queryByRole('option', { name: /current/ })).not.toBeInTheDocument();
  });

  // A repo the account can no longer see (or one past the listing cap) must stay
  // selected and re-submittable — editing the worker count can't unlink it.
  it('keeps an unlistable stored repo selectable and submits it unchanged', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({ repo_url: 'https://github.com/gone/away' })}
        saving={false}
        onSave={onSave}
      />,
    );

    expect(repoSelect().value).toBe('https://github.com/gone/away');
    expect(
      screen.getByRole('option', { name: 'https://github.com/gone/away (current)' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).repo_url).toBe('https://github.com/gone/away');
  });

  it('prompts to connect GitHub instead of showing a picker when disconnected', () => {
    render(
      <ProjectFields
        github={connectedGitHub({ connected: false, repos: [] })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(screen.queryByRole('combobox', { name: 'Repository' })).not.toBeInTheDocument();
    // A backend route, so it must be a real link (full-page navigation), and it
    // is the repo-scoped grant — NOT /auth/github/login, which grants no scopes
    // and so would leave the picker disconnected after a round trip.
    const connect = screen.getByRole('link', { name: 'Connect GitHub account' });
    expect(connect).toHaveAttribute('href', '/auth/github/connect');
    // Nothing to save: the repo is required and can't be supplied yet.
    expect(screen.getByRole('button', { name: 'Save project' })).toBeDisabled();
  });

  // An existing project keeps its repo while the account is disconnected — the
  // form still submits it, so editing another field can't silently unlink it.
  it('shows and preserves an existing repo while disconnected', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub({ connected: false, repos: [] })}
        project={baseProject()}
        saving={false}
        onSave={onSave}
      />,
    );

    expect(screen.getByText('https://github.com/acme/demo')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).repo_url).toBe('https://github.com/acme/demo');
  });

  it('shows a loading placeholder rather than flashing the connect prompt', () => {
    render(
      <ProjectFields
        github={connectedGitHub({ connected: false, loading: true, repos: [] })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(screen.queryByRole('link', { name: 'Connect GitHub account' })).not.toBeInTheDocument();
    expect(screen.getByText('Loading your GitHub repos…')).toBeInTheDocument();
  });

  it('offers a switch-account link once connected', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(screen.getByRole('link', { name: 'Switch GitHub account' })).toHaveAttribute(
      'href',
      '/auth/github/connect',
    );
  });

  // The filter box beside the dropdown is gone (projects-in-a-modal): a native
  // select already types-to-jump, so a second search control next to it only
  // raised the question of which one to use. However long the list, the picker
  // is one control.
  it('lists every repo in one control, with no filter box at any length', () => {
    render(
      <ProjectFields
        github={connectedGitHub({ repos: manyRepos(12) })}
        project={baseProject({ repo_url: 'https://github.com/acme/repo-0' })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    // Scoped to the repo select — the form has other selects (merge gate, …)
    // whose options would otherwise be counted here.
    expect(within(repoSelect()).getAllByRole('option')).toHaveLength(12);
    expect(screen.queryByLabelText('Filter')).not.toBeInTheDocument();
    expect(document.querySelector('[data-role="repo-filter"]')).toBeNull();
  });
});

// The modal shell's arrangement of the same fields (projects-in-a-modal). Same
// state, same submit body — so these assert the arrangement, and the specific
// thing the modal promises: the name and the repo picker together in a header,
// with no raw URL field anywhere.
describe('ProjectFields — detail layout', () => {
  /** One of the detail shell's regions, narrowed without a type assertion
   * (escape hatches are banned, 02 §4b) — it throws rather than silently
   * scoping a `within` query to the whole document. */
  function region(selector: string): HTMLElement {
    const element = document.querySelector(selector);
    if (!(element instanceof HTMLElement)) {
      throw new Error(`expected an element matching ${selector}`);
    }
    return element;
  }

  it('puts the name and the repo picker in the identity header', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    const header = region('[data-role="project-identity"]');
    expect(within(header).getByLabelText('Project name')).toHaveValue('demo');
    // The repo is the picker itself, in the header — not a second, raw URL field.
    expect(within(header).getByRole('combobox', { name: 'Repository' })).toHaveValue(
      'https://github.com/acme/demo',
    );
    expect(screen.queryByRole('textbox', { name: /repo/i })).toBeNull();
  });

  it('groups the remaining fields under Agent and Sandbox', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(
      [...document.querySelectorAll('[data-role="project-group"] h3')].map((h) => h.textContent),
    ).toEqual(['Agent', 'Sandbox']);
    const agent = region('[data-role="project-group"][data-group="agent"]');
    expect(within(agent).getByRole('spinbutton', { name: /worker count/i })).toBeInTheDocument();
    expect(within(agent).getByRole('combobox', { name: /merge gate/i })).toBeInTheDocument();
  });

  it('submits the same body the flat form does', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={onSave}
      />,
    );

    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    expect(lastBody(onSave)).toEqual({
      name: 'renamed',
      repo_url: 'https://github.com/acme/demo',
      worker_count: 3,
      merge_gate_mode: 'main',
    });
  });
});

// "Relevant Amika info about which sandbox to use": a snapshot ref alone says
// nothing about what a worker boots into, so each state the picker can be in
// gets a plain-language reading beside it.
describe('ProjectFields — sandbox info', () => {
  const snapshot: Snapshot = {
    ref: 'org/base:1',
    name: 'warm-base',
    description: 'node 22, repo cloned',
    source: 'dev-a',
    state: 'ready',
    created_at: '2026-07-01T10:00:00Z',
  };

  /** The sandbox-info block's declared state — the readings differ, so this is
   * what distinguishes them without binding to the copy. */
  function infoState(): string | null {
    return document.querySelector('[data-role="sandbox-info"]')?.getAttribute('data-state') ?? null;
  }

  it('explains the deployment default when no snapshot is picked', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject()}
        snapshots={[snapshot]}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(infoState()).toBe('default');
  });

  it('names the picked snapshot, what it holds, and where it came from', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject({ amika_snapshot: 'org/base:1' })}
        snapshots={[snapshot]}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(infoState()).toBe('snapshot');
    // Scoped to the info block: the snapshot's name is also an option label in
    // the picker above it.
    expect(document.querySelector('[data-role="sandbox-snapshot-name"]')).toHaveTextContent(
      'warm-base',
    );
    expect(screen.getByText('node 22, repo cloned')).toBeInTheDocument();
    expect(document.querySelector('[data-role="sandbox-snapshot-detail"]')?.textContent).toContain(
      'captured from dev-a',
    );
  });

  it('flags a stored handle the catalog no longer lists', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject({ amika_snapshot: 'legacy-handle' })}
        snapshots={[snapshot]}
        catalogAvailable
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(infoState()).toBe('unlisted');
    expect(screen.getByText('legacy-handle')).toBeInTheDocument();
  });

  it('says so when the provider exposes no catalog at all', () => {
    render(
      <ProjectFields
        layout="detail"
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(infoState()).toBe('no-catalog');
  });
});
