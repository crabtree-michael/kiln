// Feed store tests (08 §3, D2′): first-paint fetch, wholesale replacement of
// board-derived cards, RETAINED update history (seen updates persist as history,
// only retracted ones drop), the frozen last-seen divider boundary, keyset
// history pagination, and the seen-only-when-visible high-water ack. Transport
// is mocked at the module boundary — no real network, no real EventSource — and
// the captured `StreamHandlers` drive live `feed` events (copy of
// board-store.test.tsx's Probe pattern).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { FeedProvider } from '@/stores/feed-store';
import { useFeedStore } from '@/stores/feed-context';
import * as transport from '@/transport/transport';
import type { FeedSnapshot, StreamConnection, StreamHandlers } from '@/transport/transport';
import { resetProjectCache } from '@/stores/project-cache';
import { makeFeedCard, makeFeedSnapshot } from '@/test/fixtures';

vi.mock('@/transport/transport', () => ({
  fetchFeed: vi.fn(),
  fetchFeedHistory: vi.fn(),
  postFeedSeen: vi.fn(),
  dismissFeedCard: vi.fn(),
  dismissAllFeedCards: vi.fn(),
  acceptTicket: vi.fn(),
  fetchBoard: vi.fn(),
  fetchMessages: vi.fn(),
  postMessage: vi.fn(),
  openStream: vi.fn(),
  // Defaults to "no project resolved", which makes the per-project cache a
  // no-op — so every case below sees a cold store unless it names a project
  // (see the project-switch cases at the end).
  getActiveProjectId: vi.fn(() => null),
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

/** A snapshot summary whose last-seen high-water is `mark` — the boundary the
 * store opens a visit against (08 D2‴): cards at or below it were caught up on
 * before now, so they collapse out of the default feed. */
function seenThrough(mark: number): { last_seen_notification_id: number } {
  return { last_seen_notification_id: mark };
}

function Probe({
  acceptId,
  deleteId,
  dismissId,
}: {
  acceptId?: string;
  deleteId?: string;
  dismissId?: number;
}): JSX.Element {
  const {
    feed,
    connectionState,
    loading,
    lastSeenId,
    hasEarlier,
    loadingEarlier,
    showEarlier,
    acceptProposal,
    deleteTicketCard,
    dismissCard,
    dismissAll,
  } = useFeedStore();
  return (
    <div
      data-testid="probe"
      data-connection-state={connectionState}
      data-loading={String(loading)}
      data-card-count={feed?.cards.length ?? -1}
      data-card-ids={(feed?.cards ?? []).map((card) => card.id).join(',')}
      data-last-seen={lastSeenId ?? ''}
      data-has-earlier={String(hasEarlier)}
      data-loading-earlier={String(loadingEarlier)}
    >
      <button data-testid="show-earlier" type="button" onClick={showEarlier}>
        show earlier
      </button>
      <button
        data-testid="accept"
        type="button"
        onClick={() => {
          if (acceptId !== undefined) {
            acceptProposal(acceptId);
          }
        }}
      >
        accept
      </button>
      <button
        data-testid="delete"
        type="button"
        onClick={() => {
          if (deleteId !== undefined) {
            deleteTicketCard(deleteId);
          }
        }}
      >
        delete
      </button>
      <button
        data-testid="dismiss"
        type="button"
        onClick={() => {
          if (dismissId !== undefined) {
            dismissCard(dismissId);
          }
        }}
      >
        dismiss
      </button>
      <button data-testid="dismiss-all" type="button" onClick={dismissAll}>
        dismiss all
      </button>
    </div>
  );
}

function proposal(id: string): ReturnType<typeof makeFeedCard> {
  return makeFeedCard({
    kind: 'proposal',
    id: `proposal:${id}`,
    label: id.toUpperCase(),
    body: 'plan',
    createdAt: '2026-07-01T00:00:00Z',
    ticketId: id,
  });
}

describe('FeedProvider', () => {
  let capturedHandlers: StreamHandlers | undefined;
  const closeStream = vi.fn();

  beforeEach(() => {
    setVisibility('visible');
    capturedHandlers = undefined;
    closeStream.mockClear();
    vi.mocked(transport.postFeedSeen).mockResolvedValue(undefined);
    vi.mocked(transport.dismissFeedCard).mockResolvedValue(undefined);
    vi.mocked(transport.dismissAllFeedCards).mockResolvedValue(undefined);
    vi.mocked(transport.openStream).mockImplementation((handlers): StreamConnection => {
      capturedHandlers = handlers;
      return { close: closeStream };
    });
  });

  afterEach(() => {
    vi.mocked(transport.fetchFeed).mockReset();
    vi.mocked(transport.fetchFeedHistory).mockReset();
    vi.mocked(transport.postFeedSeen).mockReset();
    vi.mocked(transport.dismissFeedCard).mockReset();
    vi.mocked(transport.dismissAllFeedCards).mockReset();
    vi.mocked(transport.openStream).mockReset();
    vi.mocked(transport.getActiveProjectId).mockReturnValue(null);
    // The cache is module scope and vitest shares a module registry across the
    // cases in a file, so one case's feed would seed the next one's mount.
    resetProjectCache();
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

  it('keeps a poke card in the rendered feed (steward stall nudge, 08 §3)', async () => {
    // Regression: a `poke` card is notification-backed (a row the runtime returns
    // in the feed's update stream), but the store once accumulated only
    // update/preview, so poke cards were silently dropped from the merged feed and
    // never rendered — the steward's stall nudge vanished.
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'poke',
            id: 'update:11',
            label: '',
            body: '',
            createdAt: '2026-07-01T00:00:00Z',
            notificationId: 11,
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
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:11');
    });
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
      // update(5) was already seen when this visit opened, so it is collapsed
      // (08 D2‴) — but update(7), seen only just now by our own ack, stays put:
      // a card must not evaporate while the user is reading it.
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
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

  it('pages in older history on demand and turns the control off when spent (08 D2′)', async () => {
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
      expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('true');
    });

    // Nothing is collapsed here (nothing was seen before this visit), so the one
    // control goes straight to paging.
    screen.getByTestId('show-earlier').click();

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe(
        'update:9,update:8,update:7,update:6',
      );
    });
    // The keyset cursor is the oldest held id (8) with the default page size.
    expect(transport.fetchFeedHistory).toHaveBeenCalledWith(8, 30);
    // The page reported no more and nothing is collapsed, so the one control has
    // nothing left to do and is withdrawn rather than left on screen dead.
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('false');
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

  it('optimistically drops an accepted proposal card immediately (tap-accept)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:b1',
            label: 'B1',
            body: 'stuck',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 'b1',
          }),
          proposal('p1'),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe acceptId="p1" />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1,proposal:p1');
    });

    // Accept hides the proposal at once, without waiting on the backend; the
    // blocker card is untouched.
    act(() => {
      screen.getByTestId('accept').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1');
    });
  });

  it('optimistically drops a deleted proposal card immediately (tap-delete)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:b1',
            label: 'B1',
            body: 'stuck',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 'b1',
          }),
          proposal('p1'),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe deleteId="p1" />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1,proposal:p1');
    });

    // Delete hides the proposal at once, without waiting on the backend; the
    // blocker card is untouched.
    act(() => {
      screen.getByTestId('delete').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1');
    });
  });

  it('optimistically drops a deleted blocked ticket’s blocker card (tap-delete)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        cards: [
          makeFeedCard({
            kind: 'blocker',
            id: 'blocker:b1',
            label: 'B1',
            body: 'duplicate of t9',
            createdAt: '2026-07-01T00:00:00Z',
            ticketId: 'b1',
          }),
          proposal('p1'),
        ],
      }),
    );

    render(
      <FeedProvider>
        <Probe deleteId="b1" />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('blocker:b1,proposal:p1');
    });

    // Deleting a blocked ticket hides its blocker card at once (the board-side
    // archive + worker release round-trips over the stream); the unrelated
    // proposal is untouched. The same optimistic-hide path as a proposal delete.
    act(() => {
      screen.getByTestId('delete').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');
    });
  });

  it('keeps an accepted proposal hidden across later snapshots that still list it', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [proposal('p1'), proposal('p2')] }),
    );

    render(
      <FeedProvider>
        <Probe acceptId="p1" />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1,proposal:p2');
    });

    act(() => {
      screen.getByTestId('accept').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p2');
    });

    // An unrelated feed event that still carries p1 must not resurrect it — the
    // optimistic hide outlives wholesale re-merges until it confirms or expires.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [proposal('p1'), proposal('p2')] }));
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p2');
    });
  });

  it('restores an optimistically-accepted proposal once the TTL lapses (self-heal)', async () => {
    vi.useFakeTimers();
    try {
      vi.mocked(transport.fetchFeed).mockResolvedValue(
        makeFeedSnapshot({ cards: [proposal('p1')] }),
      );

      render(
        <FeedProvider>
          <Probe acceptId="p1" />
        </FeedProvider>,
      );
      // Flush the async mount fetch under fake timers.
      await act(async () => {
        await Promise.resolve();
      });
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');

      act(() => {
        screen.getByTestId('accept').click();
      });
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');

      // The accept never confirmed (no snapshot ever drops p1); after the 5-minute
      // window the proposal must reappear so nothing is silently lost.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      });
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');
    } finally {
      vi.useRealTimers();
    }
  });

  it('clears the optimistic marker once the server confirms the acceptance', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(makeFeedSnapshot({ cards: [proposal('p1')] }));

    render(
      <FeedProvider>
        <Probe acceptId="p1" />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');
    });

    act(() => {
      screen.getByTestId('accept').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    });

    // The accept lands: the proposal leaves the snapshot, clearing the marker.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [] }));
    // A brand-new proposal for the same ticket later must NOT be suppressed by the
    // stale marker — it should render.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [proposal('p1')] }));
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:p1');
    });
  });

  it('optimistically clears a swiped update card and retracts it server-side (08 §3)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(9, 'keep'), update(8, 'clear me')] }),
    );

    render(
      <FeedProvider>
        <Probe dismissId={8} />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:8');
    });

    // Swiping the card away hides it at once — without waiting on the backend —
    // and fires the retract for its notification id; the sibling card is untouched.
    act(() => {
      screen.getByTestId('dismiss').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9');
    });
    expect(transport.dismissFeedCard).toHaveBeenCalledWith(8);
  });

  it('keeps a swiped card hidden across snapshots that still list it until the retract lands', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(8, 'clear me')] }),
    );

    render(
      <FeedProvider>
        <Probe dismissId={8} />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8');
    });

    act(() => {
      screen.getByTestId('dismiss').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    });

    // A snapshot that still carries the card (retract not processed yet) must not
    // resurrect it; only the confirmed retract (its absence) clears it for good.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [update(8, 'clear me')] }));
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    });

    // The retract lands: the card leaves the snapshot and stays gone.
    capturedHandlers?.onFeed?.(makeFeedSnapshot({ cards: [] }));
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    });
  });

  it('springs a swiped card back when the dismiss request fails (nothing lost)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(8, 'clear me')] }),
    );
    vi.mocked(transport.dismissFeedCard).mockRejectedValue(new Error('offline'));

    render(
      <FeedProvider>
        <Probe dismissId={8} />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8');
    });

    act(() => {
      screen.getByTestId('dismiss').click();
    });
    // The card comes back once the failed retract resolves.
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8');
    });
  });

  it('optimistically clears every update card at once and retracts them server-side (clear-all)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [proposal('a'), update(9, 'one'), update(8, 'two')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:a,update:9,update:8');
    });

    // Clearing all drops the notification-backed cards at once and fires the bulk
    // retract; the board-derived proposal is untouched (it is board state).
    act(() => {
      screen.getByTestId('dismiss-all').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('proposal:a');
    });
    expect(transport.dismissAllFeedCards).toHaveBeenCalledTimes(1);
  });

  it('springs the cleared cards back when the clear-all request fails (nothing lost)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(9, 'one'), update(8, 'two')] }),
    );
    vi.mocked(transport.dismissAllFeedCards).mockRejectedValue(new Error('offline'));

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:8');
    });

    act(() => {
      screen.getByTestId('dismiss-all').click();
    });
    // The cards come back once the failed bulk retract resolves.
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:8');
    });
  });
  // ------------------------------------------- seen cards collapse away (D2‴)

  it('collapses the cards seen before this visit and keeps the rest (08 D2‴)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        // The user had caught up through id 7 before opening this screen, so 7
        // and 6 are already-read history; 9 and 8 are new since.
        summary: seenThrough(7),
        cards: [update(9, 'fresh'), update(8, 'also fresh'), update(7, 'read'), update(6, 'read')],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:8');
    });
    // Collapsed, not lost — which is what puts the one control on screen.
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('true');
  });

  it('collapses on seen with no delay — there is no linger window to wait out (08 D2‴)', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-07-01T12:00:00Z'));
    try {
      vi.mocked(transport.fetchFeed).mockResolvedValue(
        // Seen one second ago, in a previous look at the screen. The old rule
        // kept this for another ten minutes; there is no timer now.
        makeFeedSnapshot({ summary: seenThrough(8), cards: [update(8, 'just read')] }),
      );

      render(
        <FeedProvider>
          <Probe />
        </FeedProvider>,
      );
      await act(async () => {
        await Promise.resolve();
      });

      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
      expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('true');
      // And no pending timer is what makes that stable: advancing the clock an
      // hour changes nothing, in either direction.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
      });
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    } finally {
      vi.useRealTimers();
    }
  });

  it('never collapses an unseen card, however old it is (08 D2‴)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      // Nothing has ever been seen here, so the floor is open: a card that has
      // sat unread for years still stays, because collapsing is about being
      // caught up, never about age.
      makeFeedSnapshot({ cards: [update(3, 'ancient but unread')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:3');
    });
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('false');
  });

  it('does not collapse a card the user is looking at right now (08 D2‴)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(9, 'arrived while away')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(transport.postFeedSeen).toHaveBeenCalledWith(9);
    });

    // The ack we just fired comes back stamped on the very next snapshot. It
    // must NOT take the card off the screen the user is reading it on — that
    // ack is about the next visit.
    capturedHandlers?.onFeed?.(
      makeFeedSnapshot({ summary: seenThrough(9), cards: [update(9, 'arrived while away')] }),
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9');
    });
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('false');
  });

  it('collapses what was read once the screen is come back to (08 D2‴)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(9, 'read this visit')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(transport.postFeedSeen).toHaveBeenCalledWith(9);
    });
    expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9');

    // Away and back: a return to the screen is a new visit, and everything acked
    // during the last one has now been looked at. This is what keeps a window
    // left open all day from silting up — nothing here is ever reloaded.
    setVisibility('hidden');
    document.dispatchEvent(new Event('visibilitychange'));
    setVisibility('visible');
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'));
    });

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('');
    });
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('true');
  });

  it('brings the collapsed cards back on "Show earlier", in their ordered place (08 D2‴)', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({
        summary: seenThrough(8),
        cards: [update(9, 'fresh'), update(8, 'read')],
      }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9');
    });

    act(() => {
      screen.getByTestId('show-earlier').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:8');
    });
    // One control with one meaning: it does not toggle back, and with no older
    // history left on the server it withdraws rather than sitting there dead.
    expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('false');
  });

  it('is visibility only — a collapsed card is never retracted server-side', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ summary: seenThrough(8), cards: [update(8, 'read')] }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.hasEarlier).toBe('true');
    });

    act(() => {
      screen.getByTestId('show-earlier').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8');
    });
    // Collapsing is declutter, not deletion: nothing was dismissed or retracted.
    expect(transport.dismissFeedCard).not.toHaveBeenCalled();
    expect(transport.dismissAllFeedCards).not.toHaveBeenCalled();
  });

  it('reveals collapsed cards when paging older history, so the page is not fetched into hiding', async () => {
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      // Caught up through id 4, but the newest page holds nothing that old — so
      // the control is offered for the server-side history alone.
      makeFeedSnapshot({
        summary: seenThrough(4),
        cards: [update(9, 'fresh')],
        hasMoreHistory: true,
      }),
    );
    vi.mocked(transport.fetchFeedHistory).mockResolvedValue({
      cards: [update(4, 'old')],
      has_more: false,
    });

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9');
    });

    // Every card an older page carries is long-seen; leaving the collapse on
    // would make "Show earlier" fetch a page straight into hiding and read as a
    // dead button.
    act(() => {
      screen.getByTestId('show-earlier').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:9,update:4');
    });
  });

  // --- project switching (12 §4.1): the per-project cache + the loading flag ---

  it('reports loading until the first snapshot lands, then stops', async () => {
    let release: ((snapshot: FeedSnapshot) => void) | undefined;
    vi.mocked(transport.fetchFeed).mockImplementation(
      () =>
        new Promise<FeedSnapshot>((resolve) => {
          release = resolve;
        }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );

    // Up from the very first frame: an empty feed with no indication is exactly
    // the state that reads as a frozen window.
    expect(screen.getByTestId('probe').dataset.loading).toBe('true');

    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
    await act(async () => {
      release?.(makeFeedSnapshot({ cards: [update(1, 'hello')] }));
      // Let the fetch's continuation run inside act, so the state it sets is
      // flushed before the assertion below.
      await Promise.resolve();
    });

    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.loading).toBe('false');
    });
  });

  it('paints a previously-loaded project’s feed at once on switching back, and still refreshes', async () => {
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(7, 'alpha update')] }),
    );

    const first = render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    });
    first.unmount();

    // Away and back. The refetch is left pending, so anything on screen before
    // it settles can only have come from the cache.
    let release: ((snapshot: FeedSnapshot) => void) | undefined;
    vi.mocked(transport.fetchFeed).mockImplementation(
      () =>
        new Promise<FeedSnapshot>((resolve) => {
          release = resolve;
        }),
    );

    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    // Synchronously on the first render — no effect, no frame of blank.
    expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    // …and the cached feed is never presented as final: the refresh is running
    // and says so.
    expect(screen.getByTestId('probe').dataset.loading).toBe('true');

    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
    await act(async () => {
      release?.(makeFeedSnapshot({ cards: [update(8, 'newer')] }));
      await Promise.resolve();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8');
      expect(screen.getByTestId('probe').dataset.loading).toBe('false');
    });
  });

  it('keeps each project’s feed separate — an unvisited project opens empty', async () => {
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(7, 'alpha update')] }),
    );
    const first = render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    });
    first.unmount();

    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-beta');
    let release: ((snapshot: FeedSnapshot) => void) | undefined;
    vi.mocked(transport.fetchFeed).mockImplementation(
      () =>
        new Promise<FeedSnapshot>((resolve) => {
          release = resolve;
        }),
    );
    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    // Borrowing the last project's cards here would be worse than any wait.
    expect(screen.getByTestId('probe').dataset.cardCount).toBe('-1');
    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
  });

  it('carries a swiped-away card’s suppression across a switch, so it cannot flash back', async () => {
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(7, 'keep'), update(8, 'swiped')] }),
    );

    const first = render(
      <FeedProvider>
        <Probe dismissId={8} />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:8,update:7');
    });
    act(() => {
      screen.getByTestId('dismiss').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    });
    first.unmount();

    // The retract is server-side and the next snapshot will drop the card for
    // good — but until it lands, the restored feed must not re-show a card the
    // user has already dealt with.
    let release: ((snapshot: FeedSnapshot) => void) | undefined;
    vi.mocked(transport.fetchFeed).mockImplementation(
      () =>
        new Promise<FeedSnapshot>((resolve) => {
          release = resolve;
        }),
    );
    render(
      <FeedProvider>
        <Probe dismissId={8} />
      </FeedProvider>,
    );
    expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    await waitFor(() => {
      expect(release).not.toBeUndefined();
    });
  });

  it('restores the frozen divider boundary rather than re-freezing it on return', async () => {
    // The divider is frozen once per session (08 D2′). A switch away and back is
    // not a new session, and our own acks have moved the server's mark in the
    // meantime — re-reading it would slide the "Earlier" line under the user.
    vi.mocked(transport.getActiveProjectId).mockReturnValue('p-alpha');
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(7, 'a')], summary: { last_seen_notification_id: 3 } }),
    );
    const first = render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.lastSeen).toBe('3');
    });
    first.unmount();

    // The server now reports the advanced mark, as it would after the ack.
    vi.mocked(transport.fetchFeed).mockResolvedValue(
      makeFeedSnapshot({ cards: [update(7, 'a')], summary: { last_seen_notification_id: 7 } }),
    );
    render(
      <FeedProvider>
        <Probe />
      </FeedProvider>,
    );
    expect(screen.getByTestId('probe').dataset.lastSeen).toBe('3');
    await waitFor(() => {
      expect(screen.getByTestId('probe').dataset.cardIds).toBe('update:7');
    });
    expect(screen.getByTestId('probe').dataset.lastSeen).toBe('3');
  });
});
