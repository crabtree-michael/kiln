// ProjectFields owns the Amika sandbox-secrets editor (02 §8): a zero-or-more
// list saved with the rest of the project on "Save project". Each secret is a
// name (env var) plus a write-only value (11 §3 D7): the value input seeds
// blank and shows a "configured · …tail" placeholder for a stored secret. These
// tests cover seeding, add/remove, and the exact submit payload (name-blank rows
// dropped; value omitted when the draft is blank so the stored value is kept).
import { describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { ProjectFields } from '@/dashboard/ConfigFields';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';

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
