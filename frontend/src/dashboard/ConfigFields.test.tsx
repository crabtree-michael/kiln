// ProjectFields owns two editors worth testing directly.
//
// The Amika sandbox-secrets editor (02 §8): a zero-or-more list saved with the
// rest of the project on "Save project". Each secret is a name (env var) plus a
// write-only value (11 §3 D7): the value input seeds blank and shows a
// "configured · …tail" placeholder for a stored secret. Those tests cover
// seeding, add/remove, and the exact submit payload (name-blank rows dropped;
// value omitted when the draft is blank so the stored value is kept).
//
// The repo picker (settings repo picker): the repo is chosen from the connected
// GitHub account, never typed. Those tests cover the connect prompt, preselecting
// an existing repo_url across the URL spellings hand-entry produced, and keeping
// an unlistable repo selectable so a save can't silently drop it.
import { describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ProjectFields } from '@/dashboard/ConfigFields';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import type {
  DevBox,
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  SaveSnapshotRequest,
  Snapshot,
} from '@/transport/transport';

/** A connected account whose listing contains `baseProject`'s repo — the
 * ordinary state, so the secrets/snapshot tests below exercise the rest of the
 * form with the picker settled. */
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
 * (no assertion needed to read amika_secrets off it). */
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

function secretRows(): HTMLElement[] {
  return screen.queryAllByRole('generic').filter((el) => el.dataset.role === 'amika-secret-row');
}

/** The nth secret row, asserting it exists — keeps the strict index checker
 * happy without a banned non-null assertion. */
function secretRow(index: number): HTMLElement {
  const row = secretRows()[index];
  if (row === undefined) {
    throw new Error(`expected a secret row at index ${String(index)}`);
  }
  return row;
}

/** The last ProjectUpdateRequest body a mocked onSave received. */
function lastBody(onSave: SaveMock): ProjectUpdateRequest {
  const last = onSave.mock.calls.at(-1);
  if (last === undefined) {
    throw new Error('onSave was never called');
  }
  return last[0];
}

describe('ProjectFields — Amika secrets', () => {
  it('seeds names from the stored project and keeps values write-only', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject({
          amika_secrets: [
            { name: 'OPENAI_API_KEY', value: { set: true, tail: 'cdef' } },
            { name: 'STRIPE_KEY', value: { set: true, tail: 'wxyz' } },
          ],
        })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(secretRows()).toHaveLength(2);
    // Name round-trips; the value input is blank but advertises the stored tail.
    const nameInput = within(secretRow(0)).getByLabelText('Env var name');
    expect(nameInput).toHaveValue('OPENAI_API_KEY');
    const valueInput = within(secretRow(0)).getByLabelText('Value');
    expect(valueInput).toHaveValue('');
    expect(valueInput).toHaveAttribute('placeholder', 'configured · …cdef');
  });

  it('adds and removes rows', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(secretRows()).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Add secret' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add secret' }));
    expect(secretRows()).toHaveLength(2);
    fireEvent.click(within(secretRow(0)).getByRole('button', { name: 'Remove' }));
    expect(secretRows()).toHaveLength(1);
  });

  it('sends {name,value} for a freshly typed secret and drops name-blank rows', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        saving={false}
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add secret' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add secret' }));
    // First row filled (whitespace trimmed); second row left entirely blank.
    fireEvent.change(within(secretRow(0)).getByLabelText('Env var name'), {
      target: { value: '  OPENAI_API_KEY  ' },
    });
    fireEvent.change(within(secretRow(0)).getByLabelText('Value'), {
      target: { value: '  sk-live-123  ' },
    });

    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(lastBody(onSave).amika_secrets).toEqual([
      { name: 'OPENAI_API_KEY', value: 'sk-live-123' },
    ]);
  });

  it('omits the value (keeps stored) when an existing secret is left untouched', () => {
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
    expect(lastBody(onSave).amika_secrets).toEqual([{ name: 'OPENAI_API_KEY' }]);
  });

  it('sends an empty list when every secret is removed', () => {
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
    fireEvent.click(within(secretRow(0)).getByRole('button', { name: 'Remove' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));
    expect(lastBody(onSave).amika_secrets).toEqual([]);
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

describe('ProjectFields — save a dev box as a snapshot', () => {
  const devBoxes: DevBox[] = [
    { ref: 'sb-dev', name: 'my-dev-box', status: 'ready' },
    { ref: 'sb-old', name: 'old-box', status: 'stopped' },
  ];

  it('loads dev boxes when the capture section is available', () => {
    const onRefreshDevBoxes = vi.fn();
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        catalogAvailable
        devBoxes={devBoxes}
        onRefreshDevBoxes={onRefreshDevBoxes}
        onSaveSnapshot={vi.fn(() => Promise.resolve())}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(onRefreshDevBoxes).toHaveBeenCalled();
    const select = screen.getByRole('combobox', { name: /dev box/i });
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Select a dev box…', 'my-dev-box (ready)', 'old-box (stopped)']);
  });

  it('is hidden without a catalog', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        onSaveSnapshot={vi.fn(() => Promise.resolve())}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    expect(screen.queryByRole('combobox', { name: /dev box/i })).toBeNull();
  });

  it('disables Save snapshot until a dev box and a name are chosen', () => {
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        catalogAvailable
        devBoxes={devBoxes}
        onRefreshDevBoxes={vi.fn()}
        onSaveSnapshot={vi.fn(() => Promise.resolve())}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    const button = screen.getByRole('button', { name: 'Save snapshot' });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByRole('combobox', { name: /dev box/i }), {
      target: { value: 'sb-dev' },
    });
    expect(button).toBeDisabled(); // still needs a name
    fireEvent.change(screen.getByRole('textbox', { name: /snapshot name/i }), {
      target: { value: 'my-base' },
    });
    expect(button).toBeEnabled();
  });

  it('captures the selected dev box with the given name and clears the form', async () => {
    const onSaveSnapshot = vi.fn((_body: SaveSnapshotRequest) => Promise.resolve());
    render(
      <ProjectFields
        github={connectedGitHub()}
        project={baseProject()}
        catalogAvailable
        devBoxes={devBoxes}
        onRefreshDevBoxes={vi.fn()}
        onSaveSnapshot={onSaveSnapshot}
        saving={false}
        onSave={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByRole('combobox', { name: /dev box/i }), {
      target: { value: 'sb-dev' },
    });
    const nameInput = screen.getByRole('textbox', { name: /snapshot name/i });
    fireEvent.change(nameInput, { target: { value: 'my-base' } });
    fireEvent.change(screen.getByRole('textbox', { name: /description/i }), {
      target: { value: 'warm tree' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save snapshot' }));
    expect(onSaveSnapshot).toHaveBeenCalledWith({
      dev_box_ref: 'sb-dev',
      name: 'my-base',
      description: 'warm tree',
    });
    // The form clears once the capture resolves.
    await waitFor(() => {
      expect(nameInput).toHaveValue('');
    });
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
    // is the SAME sign-in endpoint — connecting is re-authorizing, not a second
    // OAuth app.
    const connect = screen.getByRole('link', { name: 'Connect GitHub account' });
    expect(connect).toHaveAttribute('href', '/auth/github/login');
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
      '/auth/github/login',
    );
  });

  it('filters a long repo list, always keeping the current selection listed', () => {
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
    fireEvent.change(screen.getByLabelText('Filter'), { target: { value: 'repo-11' } });

    const names = within(repoSelect())
      .getAllByRole('option')
      .map((option) => option.textContent);
    expect(names).toContain('acme/repo-11');
    // The selected repo survives a query that doesn't match it — otherwise the
    // select would silently lose its value.
    expect(names).toContain('acme/repo-0');
    expect(names).not.toContain('acme/repo-5');
  });

  it('hides the filter for a short list', () => {
    render(
      <ProjectFields
        github={connectedGitHub({ repos: manyRepos(3) })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );

    expect(screen.queryByLabelText('Filter')).not.toBeInTheDocument();
  });
});
