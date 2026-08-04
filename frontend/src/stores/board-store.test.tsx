// Board store tests (07 §5, §8): wholesale snapshot replacement, first-paint
// fetch, and live `board`/connection-state updates via the transport module.
// Transport is mocked at the module boundary — no real network, no real
// EventSource. Scaffold's `BoardProvider` currently seeds `board: null` via a
// bare `useState` with no subscription at all, so every test here is red
// until the solution phase wires `transport.fetchBoard`/`openStream`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { BoardProvider } from '@/stores/board-store';
import { useBoardStore } from '@/stores/board-context';
import * as transport from '@/transport/transport';
import type { StreamConnection, StreamHandlers } from '@/transport/transport';
import { resetProjectCache } from '@/stores/project-cache';
import { makeBoard, makeTicket } from '@/test/fixtures';

vi.mock('@/transport/transport', () => ({
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
  // Defaults to "no project resolved", which makes the per-project cache a no-op
  // — so every case below sees a cold store unless it opts in by naming a
  // project (see the project-switch cases).
  getActiveProjectId: vi.fn(() => null),
}));

function Probe(): JSX.Element {
  const { board, connectionState, loading } = useBoardStore();
  return (
    <div
      data-testid="probe"
      data-connection-state={connectionState}
      data-loading={loading}
      data-ready-count={board?.ready.length ?? -1}
      data-shaping-count={board?.shaping.length ?? -1}
      data-working-count={board?.working.length ?? -1}
    />
  );
}

describe('BoardProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;
  const closeStream = vi.fn();

  beforeEach(() => {
    capturedHandlers = undefined;
    closeStream.mockClear();
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: closeStream };
    });
  });

  afterEach(() => {
    vi.mocked(transport.fetchBoard).mockReset();
    vi.mocked(transport.openStream).mockReset();
    vi.mocked(transport.getActiveProjectId).mockReturnValue(null);
    // The cache is module scope and vitest shares a module registry across the
    // cases in a file, so one case's snapshot would seed the next one's mount.
    resetProjectCache();
  });

  it('fetches the board once on mount for first paint (07 §5)', async () => {
    const initialBoard = makeBoard({
      shaping: [
        makeTicket({
          id: 's1',
          title: 'Shaping ticket',
          body: 'body',
          state: 'shaping',
          priority: 0,
          createdAt: '2026-07-01T00:00:00Z',
          updatedAt: '2026-07-01T00:00:00Z',
        }),
      ],
    });
    vi.mocked(transport.fetchBoard).mockResolvedValue(initialBoard);

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.shapingCount).toBe('1');
    });
    expect(transport.fetchBoard).toHaveBeenCalledTimes(1);
  });

  it('opens exactly one stream connection on mount (07 §5)', async () => {
    vi.mocked(transport.fetchBoard).mockResolvedValue(makeBoard());

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    await waitFor(() => {
      expect(transport.openStream).toHaveBeenCalledTimes(1);
    });
  });

  it('replaces the snapshot wholesale on each `board` event, never merging (04 D7)', async () => {
    vi.mocked(transport.fetchBoard).mockResolvedValue(makeBoard());

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    await waitFor(() => {
      expect(capturedHandlers).not.toBeUndefined();
    });

    const firstUpdate = makeBoard({
      shaping: [
        makeTicket({
          id: 's1',
          title: 'a',
          body: 'b',
          state: 'shaping',
          priority: 0,
          createdAt: '2026-07-01T00:00:00Z',
          updatedAt: '2026-07-01T00:00:00Z',
        }),
      ],
      ready: [],
    });
    capturedHandlers?.onBoard(firstUpdate);

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.shapingCount).toBe('1');
    });

    const secondUpdate = makeBoard({
      shaping: [],
      ready: [
        makeTicket({
          id: 'r1',
          title: 'c',
          body: 'd',
          state: 'ready',
          priority: 5,
          createdAt: '2026-07-01T00:00:00Z',
          updatedAt: '2026-07-01T00:00:00Z',
        }),
      ],
    });
    capturedHandlers?.onBoard(secondUpdate);

    await waitFor(() => {
      const probe = screen.getByTestId('probe');
      expect(probe.dataset.readyCount).toBe('1');
      // Wholesale replacement means the first update's shaping ticket must be
      // gone, not merged with the second update's ready ticket.
      expect(probe.dataset.shapingCount).toBe('0');
    });
  });

  it('refreshBoard() pulls a fresh snapshot on demand, independent of the stream', async () => {
    const first = makeBoard();
    const refreshed = makeBoard({
      working: [
        makeTicket({
          id: 'w1',
          title: 'now working',
          body: '',
          state: 'working',
          priority: 0,
          createdAt: '2026-07-01T00:00:00Z',
          updatedAt: '2026-07-01T00:00:00Z',
        }),
      ],
    });
    vi.mocked(transport.fetchBoard).mockResolvedValueOnce(first).mockResolvedValueOnce(refreshed);

    function RefreshProbe(): JSX.Element {
      const { board, refreshBoard, refreshing } = useBoardStore();
      return (
        <div>
          <div data-testid="refresh-probe" data-refreshing={refreshing}>
            {board?.working.length ?? -1}
          </div>
          <button type="button" onClick={refreshBoard}>
            refresh
          </button>
        </div>
      );
    }

    render(
      <BoardProvider>
        <RefreshProbe />
      </BoardProvider>,
    );

    // Mount fetch lands first; no working tickets yet.
    await waitFor(() => {
      expect(screen.getByTestId('refresh-probe').textContent).toBe('0');
    });
    expect(transport.fetchBoard).toHaveBeenCalledTimes(1);

    screen.getByText('refresh').click();

    // The on-demand pull replaces the snapshot without any `board` event.
    await waitFor(() => {
      expect(screen.getByTestId('refresh-probe').textContent).toBe('1');
    });
    expect(transport.fetchBoard).toHaveBeenCalledTimes(2);
    // Flag settles back to false once the fetch resolves.
    await waitFor(() => {
      expect(screen.getByTestId('refresh-probe').dataset.refreshing).toBe('false');
    });
  });

  it('paints a previously-loaded project’s board at once on switching back (12 §4.1)', async () => {
    // A switch remounts this store against the new project's stream, so without
    // the per-project cache coming back to a project looks exactly like opening
    // it cold: no board until a round-trip lands.
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    const alpha = makeBoard({
      working: [
        makeTicket({
          id: 'w1',
          title: 'alpha work',
          body: '',
          state: 'working',
          priority: 0,
          createdAt: '2026-07-01T00:00:00Z',
          updatedAt: '2026-07-01T00:00:00Z',
        }),
      ],
    });
    vi.mocked(transport.fetchBoard).mockResolvedValue(alpha);

    const first = render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.workingCount).toBe('1');
    });
    first.unmount();

    // Away to another project and back. The refetch is left pending, so what the
    // assertion sees can only have come from the cache.
    let release: ((board: transport.Board) => void) | undefined;
    vi.mocked(transport.fetchBoard).mockImplementation(
      () =>
        new Promise<transport.Board>((resolve) => {
          release = resolve;
        }),
    );

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );
    expect(screen.getByTestId('probe').dataset.workingCount).toBe('1');
    // …and it is still refreshed, never served as final (loading stays up).
    expect(screen.getByTestId('probe').dataset.loading).toBe('true');
    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
    release?.(makeBoard());
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.workingCount).toBe('0');
    });
  });

  it('keeps each project’s board separate — a switch never shows the other one', async () => {
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    vi.mocked(transport.fetchBoard).mockResolvedValue(
      makeBoard({
        working: [
          makeTicket({
            id: 'w1',
            title: 'alpha work',
            body: '',
            state: 'working',
            priority: 0,
            createdAt: '2026-07-01T00:00:00Z',
            updatedAt: '2026-07-01T00:00:00Z',
          }),
        ],
      }),
    );
    const first = render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.workingCount).toBe('1');
    });
    first.unmount();

    // A project never loaded before: the cache has nothing for it, so it opens
    // blank rather than borrowing the last project's board. Its fetch is
    // captured and never released, so nothing but the cache could have painted.
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-beta');
    let release: ((board: transport.Board) => void) | undefined;
    vi.mocked(transport.fetchBoard).mockImplementation(
      () =>
        new Promise<transport.Board>((resolve) => {
          release = resolve;
        }),
    );
    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );
    expect(screen.getByTestId('probe').dataset.workingCount).toBe('-1');
    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
  });

  it('reports loading until the project’s own snapshot lands, then stops', async () => {
    let release: ((board: transport.Board) => void) | undefined;
    vi.mocked(transport.fetchBoard).mockImplementation(
      () =>
        new Promise<transport.Board>((resolve) => {
          release = resolve;
        }),
    );

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    // Up from the first frame: a blank board with no indication is the state
    // that reads as "frozen".
    expect(screen.getByTestId('probe').dataset.loading).toBe('true');

    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
    release?.(makeBoard());

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.loading).toBe('false');
    });
  });

  it('stops reporting loading when the fetch fails, rather than claiming work', async () => {
    vi.mocked(transport.fetchBoard).mockRejectedValue(new Error('offline'));

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.loading).toBe('false');
    });
  });

  it('reflects connection-state changes from the stream (07 §8)', async () => {
    vi.mocked(transport.fetchBoard).mockResolvedValue(makeBoard());

    render(
      <BoardProvider>
        <Probe />
      </BoardProvider>,
    );

    await waitFor(() => {
      expect(capturedHandlers).not.toBeUndefined();
    });

    capturedHandlers?.onConnectionStateChange('connected');
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.connectionState).toBe('connected');
    });

    capturedHandlers?.onConnectionStateChange('reconnecting');
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.connectionState).toBe('reconnecting');
    });
  });
});
