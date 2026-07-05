// Feed store tests (08 §3): first-paint fetch, wholesale replacement of
// board-derived cards, session-hold of already-seen update cards, and the
// seen-only-when-visible high-water ack. Transport is mocked at the module
// boundary — no real network, no real EventSource — and the captured
// `StreamHandlers` drive live `feed` events (copy of board-store.test.tsx's
// Probe pattern).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { FeedProvider } from '@/stores/feed-store';
import { useFeedStore } from '@/stores/feed-context';
import * as transport from '@/transport/transport';
import type { StreamConnection, StreamHandlers } from '@/transport/transport';
import { makeFeedCard, makeFeedSnapshot } from '@/test/fixtures';

vi.mock('@/transport/transport', () => ({
  fetchFeed: vi.fn(),
  postFeedSeen: vi.fn(),
  acceptTicket: vi.fn(),
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
}));

function setVisibility(state: DocumentVisibilityState): void {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  });
}

function Probe(): JSX.Element {
  const { feed, connectionState } = useFeedStore();
  return (
    <div
      data-testid="probe"
      data-connection-state={connectionState}
      data-card-count={feed?.cards.length ?? -1}
      data-card-ids={(feed?.cards ?? []).map((card) => card.id).join(',')}
    />
  );
}

describe('FeedProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;
  const closeStream = vi.fn();

  beforeEach(() => {
    setVisibility('visible');
    capturedHandlers = undefined;
    closeStream.mockClear();
    vi.mocked(transport.postFeedSeen).mockResolvedValue(undefined);
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: closeStream };
    });
  });

  afterEach(() => {
    vi.mocked(transport.fetchFeed).mockReset();
    vi.mocked(transport.postFeedSeen).mockReset();
    vi.mocked(transport.openStream).mockReset();
  });

  it('fetches the feed once on mount for first paint (08 §3)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:t1',
            label: 'T1',
            body: 'stuck',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 't1',
          }),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardCount).toBe('1');
    });
    expect(transport.fetchFeed).toHaveBeenCalledTimes(1);
  });

  it('opens exactly one stream connection on mount', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(makeFeedSnapshot());

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(transport.openStream).toHaveBeenCalledTimes(1);
    });
  });

  it('replaces board-derived cards wholesale on each `feed` event, never merging', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(makeFeedSnapshot());

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(capturedHandlers).not.toBeUndefined();
    });

    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'proposal',
            id: 'proposal:p1',
            label: 'P1',
            body: 'plan',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 'p1',
          }),
        ],
      }),
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');
    });

    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'proposal',
            id: 'proposal:p2',
            label: 'P2',
            body: 'plan',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 'p2',
          }),
        ],
      }),
    );
    await waitFor(() => {
      // Wholesale replacement: the first proposal must be gone, not merged.
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p2');
    });
  });

  it('acks unseen update cards with the high-water id when the screen is visible (08 §3)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        summary: { update_count: 2 },
        cards: [
          makeFeedCard({
            kind: 'update',
            id: 'update:7',
            label: '',
            body: 'newer',
            createdAt: '2026-07-01T00:02:00Z',
            notificationId: 7,
          }),
          makeFeedCard({
            kind: 'update',
            id: 'update:5',
            label: '',
            body: 'older',
            createdAt: '2026-07-01T00:01:00Z',
            notificationId: 5,
          }),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(transport.postFeedSeen).toHaveBeenCalledWith(7);
    });
    expect(transport.postFeedSeen).toHaveBeenCalledTimes(1);
  });

  it('does NOT ack while hidden, then acks once the screen becomes visible', async () => {
    setVisibility('hidden');
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'update',
            id: 'update:9',
            label: '',
            body: 'while away',
            createdAt: '2026-07-01T00:03:00Z',
            notificationId: 9,
          }),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardCount).toBe('1');
    });
    expect(transport.postFeedSeen).not.toHaveBeenCalled();

    setVisibility('visible');
    document.dispatchEvent(new Event('visibilitychange'));

    await waitFor(() => {
      expect(transport.postFeedSeen).toHaveBeenCalledWith(9);
    });
  });

  it('holds already-seen update cards for the session after the server drops them (08 §3)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'update',
            id: 'update:3',
            label: '',
            body: 'note',
            createdAt: '2026-07-01T00:01:00Z',
            notificationId: 3,
          }),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:3');
    });

    // The server-driven MarkSeen re-emits a `feed` with the update dropped, but
    // now carrying a blocker. The seen update must persist for the session.
    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:b1',
            label: 'B1',
            body: 'stuck',
            createdAt: '2026-07-01T00:04:00Z',
            ticketId: 'b1',
          }),
        ],
      }),
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1,update:3');
    });
  });

  it('reflects connection-state changes from the stream (07 §8)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(makeFeedSnapshot());

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(capturedHandlers).not.toBeUndefined();
    });

    capturedHandlers?.onConnectionStateChange('reconnecting');
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.connectionState).toBe('reconnecting');
    });
  });

  it('retries the initial fetch after a transient failure (no connect-time feed push to fall back on)', async () => {
    // The feed's only guaranteed initial delivery is this fetch — nothing is
    // pushed on stream connect — so a swallowed failure would strand the view
    // blank. It must retry until a snapshot lands.
    vi.mocked(transport.fetchFeed)
      .mockRejectedValueOnce(new Error('transient 500'))
      .mockResolvedValueOnce(
        makeFeedSnapshot({
          cards: [
            makeFeedCard({
              kind: 'blocker',
              id: 'blocker:t1',
              label: 'T1',
              body: 'stuck',
              createdAt: '2026-07-01T00:00:00Z',
              ticketId: 't1',
            }),
          ],
        }),
      );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:t1');
    });
    expect(transport.fetchFeed).toHaveBeenCalledTimes(2);
  });

  it('refetches the feed on reconnect to close the gap (reconnecting -> connected)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValueOnce(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:t1',
            label: 'T1',
            body: 'stuck',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 't1',
          }),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:t1');
    });

    // While disconnected, the feed changed server-side. A bare `connected` with
    // no prior drop must NOT refetch; only a reconnecting -> connected does.
    vi.mocked(transport.fetchFeed).mockResolvedValueOnce(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:t2',
            label: 'T2',
            body: 'stuck',
            createdAt: '2026-07-01T00:05:00Z',
            ticketId: 't2',
          }),
        ],
      }),
    );

    capturedHandlers?.onConnectionStateChange('reconnecting');
    capturedHandlers?.onConnectionStateChange('connected');

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:t2');
    });
    expect(transport.fetchFeed).toHaveBeenCalledTimes(2);
  });
});
