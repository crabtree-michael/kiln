// Reconnection integration test (07 §5, §8): "on every stream (re)open the
// client refetches /api/messages once to fill any gap (board needs
// nothing — the first `board` event is the resync)." This crosses the
// board/chat store boundary (scaffold report "Open decisions" #1), so it is
// tested here at the `<App />` level against the transport module, mocked at
// the boundary, rather than against either store's internals — this way the
// assertions hold regardless of exactly which component ends up calling
// `transport.openStream` (the scaffold report recommends a single call site,
// e.g. lifted into `App.tsx`, and spec 07 §5 wants exactly one `/api/stream`
// connection either way).
//
// Left deliberately separate from `App.test.tsx` (the existing smoke test),
// which stays untouched per the scaffold-phase convention.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { App } from '@/App';
import * as transport from '@/transport/transport';
import type { StreamConnection, StreamHandlers } from '@/transport/transport';
import { makeBoard, makeTicket } from '@/test/fixtures';

vi.mock('@/transport/transport', () => ({
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
}));

describe('App reconnection (07 §8)', () => {
  let capturedHandlers: StreamHandlers[] = [];

  beforeEach(() => {
    capturedHandlers = [];
    vi.mocked(transport.fetchBoard).mockResolvedValue(
      makeBoard({
        ready: [
          makeTicket({
            id: 'r1',
            title: 'Ready ticket',
            body: 'body',
            state: 'ready',
            priority: 0,
            createdAt: '2026-07-01T00:00:00Z',
            updatedAt: '2026-07-01T00:00:00Z',
          }),
        ],
      }),
    );
    vi.mocked(transport.fetchMessages).mockResolvedValue([]);
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers.push(handlers);
      return { close: vi.fn() };
    });
  });

  afterEach(() => {
    vi.mocked(transport.fetchBoard).mockReset();
    vi.mocked(transport.fetchMessages).mockReset();
    vi.mocked(transport.openStream).mockReset();
  });

  it('opens exactly one /api/stream connection for the whole app (07 §5 — one thin module, one connection)', async () => {
    render(<App />);

    await waitFor(() => {
      expect(transport.fetchBoard).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(transport.openStream).toHaveBeenCalledTimes(1);
    });

    // Give any stray effect a chance to run a second time before asserting.
    await waitFor(() => {
      expect(transport.openStream).toHaveBeenCalledTimes(1);
    });
  });

  it('refetches /api/messages exactly once per stream reopen, not per event or not at all', async () => {
    render(<App />);

    await waitFor(() => {
      expect(transport.fetchMessages).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(capturedHandlers).toHaveLength(1);
    });
    const handlers = capturedHandlers[0];
    if (handlers === undefined) {
      throw new Error('openStream was not called with handlers');
    }
    const callsBeforeReopen = vi.mocked(transport.fetchMessages).mock.calls.length;

    // A genuine reopen cycle: the connection drops (reconnecting) and comes
    // back (connected) — exactly one extra refetch should result.
    handlers.onConnectionStateChange('reconnecting');
    handlers.onConnectionStateChange('connected');

    await waitFor(() => {
      expect(transport.fetchMessages).toHaveBeenCalledTimes(callsBeforeReopen + 1);
    });

    // A redundant `connected` with no intervening drop must not trigger
    // another refetch.
    handlers.onConnectionStateChange('connected');
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(transport.fetchMessages).toHaveBeenCalledTimes(callsBeforeReopen + 1);
  });

  it('dims but keeps the board visible while reconnecting — stale-but-visible, never blank (07 §8)', async () => {
    render(<App />);

    await waitFor(() => {
      expect(screen.getByText('Ready ticket')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(capturedHandlers).toHaveLength(1);
    });
    const handlers = capturedHandlers[0];
    if (handlers === undefined) {
      throw new Error('openStream was not called with handlers');
    }

    handlers.onConnectionStateChange('reconnecting');

    await waitFor(() => {
      expect(screen.getByRole('region', { name: 'Board' })).toHaveAttribute(
        'data-connection-state',
        'reconnecting',
      );
    });
    // Stale-but-visible: the previously-fetched ticket must still be on screen.
    expect(screen.getByText('Ready ticket')).toBeInTheDocument();
  });
});
