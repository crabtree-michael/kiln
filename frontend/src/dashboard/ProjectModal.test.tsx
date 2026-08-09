// The project modal's shell (projects-in-a-modal): the parts that are the
// modal's own job rather than the form's — dismissal, focus, and closing on a
// successful write only. The fields themselves are covered by
// ConfigFields.test.tsx ("detail layout" / "sandbox info").
//
// The sandbox catalog is fetched per project by `useSandboxCatalog`, so the
// transport module is mocked at its boundary exactly as the other dashboard
// suites do.
import { describe, expect, it, vi } from 'vitest';
import type { Mock } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ProjectModal } from '@/dashboard/ProjectModal';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import type { MeProject, ProjectUpdateRequest } from '@/transport/transport';

// `fetchSnapshots` resolves null (no catalog) by default, which is what keeps
// the capture control out of the dismissal/focus tests below; the one test that
// wants it makes this resolve a list.
const fetchSnapshots: Mock<() => Promise<unknown>> = vi.fn(() => Promise.resolve(null));
const fetchDevBoxes: Mock<() => Promise<unknown>> = vi.fn(() => Promise.resolve([]));

vi.mock('@/transport/transport', () => ({
  fetchSnapshots: (): Promise<unknown> => fetchSnapshots(),
  fetchDevBoxes: (): Promise<unknown> => fetchDevBoxes(),
  saveSnapshot: vi.fn(() => Promise.resolve(null)),
}));

function connectedGitHub(): GitHubRepos {
  return {
    repos: [{ full_name: 'acme/demo', url: 'https://github.com/acme/demo', private: false }],
    connected: true,
    loading: false,
    error: null,
    refresh: () => Promise.resolve(),
  };
}

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

type SaveMock = Mock<(body: ProjectUpdateRequest) => Promise<boolean>>;

interface RenderOptions {
  project?: MeProject;
  onSave?: SaveMock;
  onDelete?: Mock<() => Promise<boolean>>;
  onClose?: Mock<() => void>;
}

async function renderModal(options: RenderOptions = {}): Promise<Required<RenderOptions>> {
  const project = options.project ?? baseProject();
  const onSave: SaveMock = options.onSave ?? vi.fn(() => Promise.resolve(true));
  const onDelete = options.onDelete ?? vi.fn(() => Promise.resolve(true));
  const onClose = options.onClose ?? vi.fn();
  render(
    <ProjectModal
      project={project}
      providers={[]}
      github={connectedGitHub()}
      saving={false}
      onSave={onSave}
      onDelete={onDelete}
      onClose={onClose}
    />,
  );
  // Let the project's own sandbox-catalog fetch land inside act, so no test
  // finishes with a state update still in flight behind it.
  await act(async () => {
    await Promise.resolve();
  });
  return { project, onSave, onDelete, onClose };
}

/** The dialog element itself — also the scrim's only child, so the scrim is
 * `parentElement`. */
function dialog(): HTMLElement {
  return screen.getByRole('dialog');
}

describe('ProjectModal', () => {
  it('names the dialog after the project it opened, and seeds the form from it', async () => {
    await renderModal({ project: baseProject({ name: 'kiln' }) });

    expect(screen.getByRole('dialog', { name: 'Project settings: kiln' })).toBeInTheDocument();
    expect(screen.getByLabelText('Project name')).toHaveValue('kiln');
  });

  it('takes focus on open and returns it to the opener on close', async () => {
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();
    expect(document.activeElement).toBe(opener);

    const { unmount } = render(
      <ProjectModal
        project={baseProject()}
        providers={[]}
        github={connectedGitHub()}
        saving={false}
        onSave={vi.fn(() => Promise.resolve(true))}
        onClose={vi.fn()}
      />,
    );
    // The keyboard lands inside the dialog rather than at the top of the page.
    expect(document.activeElement).toBe(dialog());

    // Let the project's catalog load settle before unmounting, so the teardown
    // under test isn't racing a mid-flight fetch.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Save project' })).toBeEnabled();
    });
    unmount();
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it('keeps Tab inside the dialog, and the page behind it from scrolling', async () => {
    await renderModal();
    expect(document.body.style.overflow).toBe('hidden');

    // Shift+Tab from the panel itself (where focus lands on open) wraps to the
    // last control — Save, at the trailing edge of the action bar — rather than
    // walking out into the settings page behind.
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Save project' }));

    // …and forwards off that last control wraps back to the first.
    fireEvent.keyDown(document, { key: 'Tab' });
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close' }));
  });

  it('closes on Escape', async () => {
    const { onClose } = await renderModal();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on a press that starts on the scrim, but not one inside the panel', async () => {
    const { onClose } = await renderModal();
    const scrim = dialog().parentElement;
    expect(scrim).not.toBeNull();

    // A press that begins inside the panel (dragging a select open, selecting
    // text) bubbles to the scrim — it must not dismiss.
    fireEvent.mouseDown(dialog());
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.mouseDown(scrim!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes via the explicit Close button', async () => {
    const { onClose } = await renderModal();
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('dismisses with the X glyph alone, named for a screen reader', async () => {
    await renderModal();
    const close = screen.getByRole('button', { name: 'Close' });
    // The glyph carries the whole control: no visible word beside it, so the
    // accessible name has to come from the label rather than the text.
    expect(close).toHaveTextContent('');
    expect(close.querySelector('svg[data-icon="close"]')).not.toBeNull();
  });

  it('saves the project and closes once the write lands', async () => {
    const { onSave, onClose } = await renderModal();

    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(onSave.mock.calls[0]?.[0].name).toBe('renamed');
    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });

  it('keeps the dialog — and everything typed into it — open when the save fails', async () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve(false));
    const { onClose } = await renderModal({ onSave });

    const nameInput = screen.getByLabelText('Project name');
    fireEvent.change(nameInput, { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save project' }));

    await waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(onClose).not.toHaveBeenCalled();
    // The typed value survives, so a failed save is retryable rather than
    // discarded (the store surfaces the reason in the page's error banner).
    expect(nameInput).toHaveValue('renamed');
  });

  it('offers delete as an icon-only control that still has a name', async () => {
    await renderModal();
    const remove = screen.getByRole('button', { name: 'Delete project' });

    // The trash glyph alone — no label text — so the accessible name has to come
    // from the aria-label rather than from anything visible.
    expect(remove.textContent).toBe('');
    expect(remove.querySelector('svg[data-icon="trash"]')).not.toBeNull();
    // …and it sits in the form's action bar, ahead of Save, not in a danger
    // section of its own below the form.
    expect(remove.closest('[data-role="project-form-actions"]')).not.toBeNull();
  });

  it('deletes behind a confirm, then closes', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    try {
      const { onDelete, onClose } = await renderModal();
      fireEvent.click(screen.getByRole('button', { name: 'Delete project' }));
      expect(confirm).toHaveBeenCalledTimes(1);
      await waitFor(() => {
        expect(onDelete).toHaveBeenCalledTimes(1);
      });
      await waitFor(() => {
        expect(onClose).toHaveBeenCalledTimes(1);
      });
    } finally {
      confirm.mockRestore();
    }
  });

  it('does not delete when the confirm is declined', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    try {
      const { onDelete, onClose } = await renderModal();
      fireEvent.click(screen.getByRole('button', { name: 'Delete project' }));
      expect(onDelete).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
    } finally {
      confirm.mockRestore();
    }
  });

  // The capture belongs to the project being looked at, so it is mounted here
  // rather than on the panel behind — and it reads the dev boxes through a
  // callback that has to survive a re-render, since it loads them in an effect
  // keyed on that callback. Wrapping it per render would refetch on every one.
  it('offers the snapshot capture for a project whose provider has a catalog, and loads its dev boxes once', async () => {
    // A non-null list is what "this provider has a catalog" means to the hook.
    fetchSnapshots.mockResolvedValueOnce([
      {
        ref: 'org/base:1',
        name: 'base',
        description: '',
        source: '',
        state: 'ready',
        created_at: '2026-07-01T10:00:00Z',
      },
    ]);
    fetchDevBoxes.mockResolvedValueOnce([{ ref: 'sb-1', name: 'pacman', status: 'ready' }]);

    await renderModal();

    expect(await screen.findByRole('combobox', { name: /dev box/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /save snapshot/i })).toBeInTheDocument();
    // Typing in the form re-renders the whole tree; the dev-box load must not
    // run again behind it.
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'renamed' } });
    await act(async () => {
      await Promise.resolve();
    });
    expect(fetchDevBoxes).toHaveBeenCalledTimes(1);
  });

  it('create mode: the repo alone, no name field, no delete, same save path', async () => {
    const onSave: SaveMock = vi.fn(() => Promise.resolve(true));
    const onClose = vi.fn();
    render(
      <ProjectModal
        providers={[]}
        github={connectedGitHub()}
        saving={false}
        onSave={onSave}
        onClose={onClose}
      />,
    );

    expect(screen.getByRole('dialog', { name: 'New project' })).toBeInTheDocument();
    // No name to type: a new project takes the picked repo's name (auto-name
    // from repository), so the identity header is the picker alone.
    expect(screen.queryByLabelText('Project name')).toBeNull();
    // Nothing to delete yet, so the action bar is the create button alone.
    expect(screen.queryByRole('button', { name: 'Delete project' })).toBeNull();

    // It stays disabled until a repo is picked — a project without one can't be
    // created, so the modal blocks it rather than posting a doomed body.
    expect(screen.getByRole('button', { name: 'Create project' })).toBeDisabled();
    fireEvent.change(screen.getByRole('combobox', { name: 'Repository' }), {
      target: { value: 'https://github.com/acme/demo' },
    });
    expect(document.querySelector('[data-role="project-name-value"]')).toHaveTextContent('demo');
    fireEvent.click(screen.getByRole('button', { name: 'Create project' }));
    await waitFor(() => {
      expect(onSave).toHaveBeenCalledTimes(1);
    });
    expect(onSave.mock.calls[0]?.[0]).toMatchObject({
      name: 'demo',
      repo_url: 'https://github.com/acme/demo',
    });
    await waitFor(() => {
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });
});
