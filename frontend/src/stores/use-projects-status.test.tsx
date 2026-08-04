// The rail's cross-project polling (13 §5, §8.1). The properties worth locking
// down are the ones that keep the rail honest and cheap: the selected project is
// never polled (its board is already live), a single-project user costs nothing,
// a failed read does not flicker a row's mark off, and a hidden tab goes quiet.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import {
  useProjectsStatus,
  POLL_INTERVAL_MS,
  resetProjectStatesCache,
} from '@/stores/use-projects-status';
import { makeBoard, makeTicket } from '@/test/fixtures';
import type { Board, MeProject } from '@/transport/transport';

const fetchProjectBoard = vi.fn<(projectId: string) => Promise<Board>>();

vi.mock('@/transport/transport', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/transport/transport')>();
  return {
    ...actual,
    fetchProjectBoard: (projectId: string): Promise<Board> => fetchProjectBoard(projectId),
  };
});

function project(id: string): MeProject {
  return {
    id,
    name: id,
    repo_url: `https://github.com/acme/${id}`,
    agent_provider: '',
    amika_snapshot: '',
    worker_count: 2,
    merge_gate_mode: 'main',
    amika_secrets: [],
  };
}

const workingBoard = makeBoard({
  working: [
    makeTicket({
      id: 't2',
      title: 'mid-turn',
      body: 'body',
      state: 'working',
      priority: 1,
      createdAt: '2026-08-04T09:00:00Z',
      updatedAt: '2026-08-04T09:00:00Z',
    }),
  ],
});

const blockedBoard = makeBoard({
  blocked: [
    makeTicket({
      id: 't1',
      title: 'needs a call',
      body: 'body',
      state: 'blocked',
      priority: 1,
      createdAt: '2026-08-04T09:00:00Z',
      updatedAt: '2026-08-04T09:00:00Z',
    }),
  ],
});

describe('useProjectsStatus', () => {
  beforeEach(() => {
    fetchProjectBoard.mockReset();
    fetchProjectBoard.mockResolvedValue(makeBoard());
    // The cross-remount seed is module state, and vitest shares a module registry
    // across the cases in a file — without this, one case's readings would show
    // up as the next case's first paint.
    resetProjectStatesCache();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('never polls the selected project — its board is already live on screen', async () => {
    const projects = [project('kiln'), project('atlas')];
    renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));

    await waitFor(() => {
      expect(fetchProjectBoard).toHaveBeenCalled();
    });
    expect(fetchProjectBoard.mock.calls.map((call) => call[0])).toEqual(['atlas']);
  });

  it('costs nothing for a single-project user — the overwhelming case', () => {
    renderHook(() => useProjectsStatus([project('kiln')], 'kiln', makeBoard()));
    expect(fetchProjectBoard).not.toHaveBeenCalled();
  });

  it('reads the selected project straight off the live board, with no fetch', () => {
    const { result } = renderHook(() => useProjectsStatus([project('kiln')], 'kiln', blockedBoard));
    expect(result.current.kiln?.state).toBe('needs-you');
  });

  it('derives a polled project state from its own board', async () => {
    fetchProjectBoard.mockResolvedValue(blockedBoard);
    const projects = [project('kiln'), project('atlas')];
    const { result } = renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));

    await waitFor(() => {
      expect(result.current.atlas?.state).toBe('needs-you');
    });
    // The selected one is still read locally, unaffected by the poll.
    expect(result.current.kiln?.state).toBe('quiet');
  });

  it('a project not yet heard from is unknown, not quiet', async () => {
    const projects = [project('kiln'), project('atlas')];
    const { result } = renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));
    // Asserted on the very first render, before the in-flight poll can land.
    expect(result.current.atlas?.state).toBe('unknown');
    // Then let it settle inside an act scope so the resolved fetch's state
    // update doesn't land after the test has finished.
    await act(async () => {
      await Promise.resolve();
    });
  });

  it('a failed read keeps the last reading rather than flickering the mark off', async () => {
    fetchProjectBoard.mockResolvedValue(blockedBoard);
    const projects = [project('kiln'), project('atlas')];
    const { result } = renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));
    await waitFor(() => {
      expect(result.current.atlas?.state).toBe('needs-you');
    });

    fetchProjectBoard.mockRejectedValue(new Error('offline'));
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });

    await waitFor(() => {
      expect(fetchProjectBoard.mock.calls.length).toBeGreaterThan(1);
    });
    expect(result.current.atlas?.state).toBe('needs-you');
  });

  it('goes quiet while the tab is hidden, and catches up the moment it comes back', async () => {
    vi.useFakeTimers();
    const projects = [project('kiln'), project('atlas')];
    renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));
    const initial = fetchProjectBoard.mock.calls.length;

    const visibility = vi.spyOn(document, 'visibilityState', 'get');
    visibility.mockReturnValue('hidden');
    // `await act` so the state updates each resolved fetch queues are flushed
    // inside the act scope — fake timers otherwise leave them for React to apply
    // after the assertion, which is what the act() warning is about.
    await act(async () => {
      vi.advanceTimersByTime(POLL_INTERVAL_MS * 3);
      await vi.runOnlyPendingTimersAsync();
    });
    expect(fetchProjectBoard.mock.calls.length).toBe(initial);

    visibility.mockReturnValue('visible');
    await act(async () => {
      document.dispatchEvent(new Event('visibilitychange'));
      await Promise.resolve();
    });
    expect(fetchProjectBoard.mock.calls.length).toBe(initial + 1);
    visibility.mockRestore();
  });

  it('keeps the other rows marked across a project switch, which remounts this hook', async () => {
    // Switching projects re-keys `CurrentProjectProvider`'s subtree on purpose
    // (12 §4.1) — the stream and stores tear down and re-open, and the desktop
    // shell rides inside that subtree. Without the cross-remount seed, every
    // other project's mark would blank for a round-trip at exactly the moment
    // the user is looking at the rail.
    fetchProjectBoard.mockResolvedValue(blockedBoard);
    const projects = [project('kiln'), project('atlas'), project('ledger')];
    const first = renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));
    await waitFor(() => {
      expect(first.result.current.ledger?.state).toBe('needs-you');
    });
    first.unmount();

    // The switch: kiln → atlas. `ledger` was not selected before and is not
    // selected now, so its mark must survive the remount rather than blanking
    // back to `unknown` while its board is re-fetched.
    const second = renderHook(() => useProjectsStatus(projects, 'atlas', makeBoard()));
    expect(second.result.current.ledger?.state).toBe('needs-you');
    // The newly-selected project switches to the live board immediately.
    expect(second.result.current.atlas?.state).toBe('quiet');
    await act(async () => {
      await Promise.resolve();
    });
  });

  it('re-polls on the interval while visible', async () => {
    vi.useFakeTimers();
    const projects = [project('kiln'), project('atlas')];
    renderHook(() => useProjectsStatus(projects, 'kiln', makeBoard()));
    const initial = fetchProjectBoard.mock.calls.length;

    await act(async () => {
      vi.advanceTimersByTime(POLL_INTERVAL_MS);
      await Promise.resolve();
    });
    expect(fetchProjectBoard.mock.calls.length).toBe(initial + 1);
  });

  it("carries each project's working count, not just its mark", () => {
    const { result } = renderHook(() => useProjectsStatus([project('kiln')], 'kiln', workingBoard));
    expect(result.current.kiln).toEqual({ state: 'working', working: 1 });
  });
});
