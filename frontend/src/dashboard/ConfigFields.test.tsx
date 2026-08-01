// ProjectFields owns the Amika sandbox-secrets editor (02 §8): a zero-or-more
// list saved with the rest of the project on "Save project". Each secret is a
// name (env var) plus a write-only value (11 §3 D7): the value input seeds
// blank and shows a "configured · …tail" placeholder for a stored secret. These
// tests cover seeding, add/remove, and the exact submit payload (name-blank rows
// dropped; value omitted when the draft is blank so the stored value is kept).
import { describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ProjectFields } from '@/dashboard/ConfigFields';
import type {
  DevBox,
  MeProject,
  ProjectUpdateRequest,
  ProviderDescriptor,
  SaveSnapshotRequest,
  Snapshot,
} from '@/transport/transport';

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
    render(<ProjectFields project={baseProject()} saving={false} onSave={onSave} />);

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
        project={baseProject({ merge_gate_mode: 'pr' })}
        saving={false}
        onSave={vi.fn(() => Promise.resolve())}
      />,
    );
    expect(screen.getByRole('combobox', { name: /merge gate/i })).toHaveValue('pr');
  });

  it('submits the chosen gate mode', () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve());
    render(<ProjectFields project={baseProject()} saving={false} onSave={onSave} />);
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
    render(<ProjectFields project={baseProject()} saving={false} onSave={vi.fn()} />);
    // No picker; the back-compat text input is present.
    expect(screen.queryByRole('combobox', { name: /snapshot/i })).toBeNull();
    expect(screen.getByRole('textbox', { name: /amika snapshot/i })).toBeInTheDocument();
  });

  it('renders a snapshot picker from the catalog, with capturing snapshots disabled', () => {
    render(
      <ProjectFields
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
