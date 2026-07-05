// Feed store tests (08 §3, D2′): first-paint fetch, wholesale replacement of
// board-derived cards, RETAINED update history (seen updates persist as history,
// only retracted ones drop), the frozen last-seen divider boundary, keyset
// history pagination, and the seen-only-when-visible high-water ack. Transport
// is mocked at the module boundary — no real network, no real EventSource — and
// the captured `StreamHandlers` drive live `feed` events (copy of
// board-store.test.tsx's Probe pattern).
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
  fetchFeedHistory: vi.fn(),
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

function update(id: number, body: string): ReturnType<typeof makeFeedCard> {
  return makeFeedCard({
    kind: 'update',
    id: `update:${String(id)}`,
    label: '',
    body,
    createdAt: '2026-07-01T00:00:00Z',
    notificationId: id,
  });
}

function Probe(): JSX.Element {
  const { feed, connectionState, lastSeenId, hasMoreHistory, loadingMoreHistory, loadMoreHistory } =
    useFeedStore();
  return (
    <div
      data-testid="probe"
      data-connection-state={connectionState}
      data-card-count={feed?.cards.length ?? -1}
      data-card-ids={(feed?.cards ?? []).map((card) => card.id).join(',')}
      data-last-seen={lastSeenId ?? ''}
      data-has-more={String(hasMoreHistory)}
      data-loading-more={String(loadingMoreHistory)}
    >
      <button data-testid="load-more" type="button" onClick={loadMoreHistory}>
        more
      </button>
    </div>
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
    vi.mocked(transport.fetchFeedHistory).mockReset();
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
      // Wholesale replacement of board-derived cards: p1 must be gone, not merged.
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p2');
    });
  });

  it('acks unseen update cards with the high-water id when the screen is visible (08 §3)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        summary: { update_count: 2 },
        cards: [update(7, 'newer'), update(5, 'older')],
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
      makeFeedSnapshot({ cards: [update(9, 'while away')] }),
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

  it('freezes the last-seen divider boundary from the first snapshot (08 D2′)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        summary: { last_seen_notification_id: 5, update_count: 1 },
        cards: [update(7, 'new'), update(5, 'seen history')],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.lastSeen).toBe('5');
    });

    // A later snapshot advances the server's mark (we just acked up to 7), but
    // the client-side divider must stay frozen at 5 for the session.
    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({
        summary: { last_seen_notification_id: 7 },
        cards: [update(7, 'new'), update(5, 'seen history')],
      }),
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7,update:5');
    });
    expect(screen.getByTestId('probe').dataset.lastSeen).toBe('5');
  });

  it('retains a seen update as history when it scrolls below the newest page window (08 D2′)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(5, 'e'), update(4, 'd'), update(3, 'c')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:5,update:4,update:3');
    });

    // A newer update arrives; the server's newest page now floors at id 4 and
    // flags older history remains. The retained id 3 (below the floor) must NOT
    // be dropped — it stays as history.
    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({
        cards: [update(6, 'f'), update(5, 'e'), update(4, 'd')],
        hasMoreHistory: true,
      }),
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe(
        'update:6,update:5,update:4,update:3',
      );
    });
  });

  it('drops a retracted update when a complete snapshot no longer carries it (08 D2′)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(5, 'e'), update(4, 'd')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:5,update:4');
    });

    // The brain retracts id 4; the complete snapshot (has_more_history=false)
    // omits it, so it must be dropped, not retained.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [update(5, 'e')] }));
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:5');
    });
  });

  it('pages in older history on demand and toggles has-more (08 D2′)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(9, 'i'), update(8, 'h')], hasMoreHistory: true }),
    );
    vi.mocked(transport.fetchFeedHistory).mockResolvedValue({
      cards: [update(7, 'g'), update(6, 'f')],
      has_more: false,
    });

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.hasMore).toBe('true');
    });

    screen.getByTestId('load-more').click();

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe(
        'update:9,update:8,update:7,update:6',
      );
    });
    // The keyset cursor is the oldest held id (8) with the default page size.
    expect(transport.fetchFeedHistory).toHaveBeenCalledWith(8, 30);
    // The page reported no more, so the affordance turns off.
    expect(screen.getByTestId('probe').dataset.hasMore).toBe('false');
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
