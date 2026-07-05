// Feed store (08 §3): holds the latest `FeedSnapshot`. Every `feed` SSE event —
// and the initial `GET /api/feed` — replaces the board-derived cards
// (blocker/proposal) wholesale. Update/preview cards are notification-backed:
// once rendered on a visible screen the store marks them seen
// (`POST /api/feed/seen` up to the high-water id) AND holds them for the rest of
// the session so they don't vanish mid-session when the server drops them from
// subsequent snapshots. A fresh mount starts with an empty held-set, so it shows
// only what the server returns. Live updates ride the single app-wide stream
// connection (`@/stores/stream-connection`), shared with the board/chat stores.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import { fetchFeed, postFeedSeen } from '@/transport/transport';
import type { ConnectionState, FeedCard, FeedSnapshot } from '@/transport/transport';
import { FeedStoreContext, type FeedStoreValue } from '@/stores/feed-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface FeedProviderProps {
  children: ReactNode;
}

function isUpdateCard(card: FeedCard): boolean {
  return card.kind === 'update' || card.kind === 'preview';
}

/** Merge the server snapshot with the session-held (already-seen) update cards.
 * Order (08 §3): blockers, then proposals, then updates newest-first by
 * `notification_id`. Held updates are re-inserted only when the server no longer
 * carries them (deduped by `notification_id`). */
function mergeFeed(server: FeedSnapshot, held: Map<number, FeedCard>): FeedSnapshot {
  const blockers = server.cards.filter((card) => card.kind === 'blocker');
  const proposals = server.cards.filter((card) => card.kind === 'proposal');

  const updates = new Map<number, FeedCard>();
  for (const card of server.cards) {
    if (isUpdateCard(card) && typeof card.notification_id === 'number') {
      updates.set(card.notification_id, card);
    }
  }
  for (const [id, card] of held) {
    if (!updates.has(id)) {
      updates.set(id, card);
    }
  }
  const sortedUpdates = [...updates.values()].sort(
    (a, b) => (b.notification_id ?? 0) - (a.notification_id ?? 0),
  );

  return { summary: server.summary, cards: [...blockers, ...proposals, ...sortedUpdates] };
}

export function FeedProvider({ children }: FeedProviderProps): JSX.Element {
  const [feed, setFeed] = useState<FeedSnapshot | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');

  // Session-scoped, render-stable state (mirrors chat-store's ref pattern): the
  // held/seen update cards keyed by `notification_id`, and the latest server
  // snapshot (so a `visibilitychange` can re-run the seen check).
  const heldRef = useRef<Map<number, FeedCard>>(new Map());
  const serverFeedRef = useRef<FeedSnapshot | null>(null);

  const applySnapshot = useCallback((snapshot: FeedSnapshot): void => {
    serverFeedRef.current = snapshot;

    // Mark unseen update/preview cards seen — but only on a visible screen
    // (08 §3). Held cards are the ones we've already acked this session.
    const unseen = snapshot.cards.filter(
      (card) =>
        isUpdateCard(card) &&
        typeof card.notification_id === 'number' &&
        !heldRef.current.has(card.notification_id),
    );
    if (unseen.length > 0 && document.visibilityState === 'visible') {
      let maxId = 0;
      for (const card of unseen) {
        const id = card.notification_id ?? 0;
        if (id > maxId) {
          maxId = id;
        }
        heldRef.current.set(id, card);
      }
      void postFeedSeen(maxId);
    }

    setFeed(mergeFeed(snapshot, heldRef.current));
  }, []);

  // First paint: fetch the current snapshot directly (08 §3). Unlike the board
  // (pushed on every stream connect) and chat (refetched on reconnect), the feed
  // has no server-side connect-time push — `feed` SSE events fire only when the
  // feed actually mutates. So this one-shot fetch is the ONLY guaranteed initial
  // delivery: if it fails, the view sits blank until an unrelated `feed.updated`
  // happens to land. Retry with bounded backoff so a transient failure/timeout
  // doesn't strand the client on the empty state.
  useEffect(() => {
    let cancelled = false;

    async function loadInitialFeed(): Promise<void> {
      const backoffMs = [250, 500, 1000, 2000, 4000];
      for (let attempt = 0; ; attempt += 1) {
        try {
          const initialFeed = await fetchFeed();
          if (cancelled) {
            return;
          }
          applySnapshot(initialFeed);
          return;
        } catch {
          if (cancelled) {
            return;
          }
          // Retry until we land a snapshot; the reconnect-refetch below is the
          // steady-state safety net once the stream is up.
          const delay = backoffMs[Math.min(attempt, backoffMs.length - 1)];
          await new Promise((resolve) => setTimeout(resolve, delay));
        }
      }
    }

    void loadInitialFeed();
    return () => {
      cancelled = true;
    };
  }, [applySnapshot]);

  // Re-run the seen check when the screen becomes visible again: cards rendered
  // while hidden were deliberately not acked (08 §3 "only when visible").
  useEffect(() => {
    function handleVisibility(): void {
      if (document.visibilityState === 'visible' && serverFeedRef.current !== null) {
        applySnapshot(serverFeedRef.current);
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [applySnapshot]);

  useEffect(() => {
    // Reconnect-refetch (mirrors chat-store, 07 §5/§8): a `feed` SSE event only
    // fires when the feed mutates, and nothing is pushed on connect — so a
    // stream drop/reopen would otherwise leave the feed stale until the next
    // unrelated `feed.updated`. Refetch once on every reconnecting -> connected
    // transition to close that gap. The initial connect is already covered by
    // the mount fetch above, so it doesn't double-fetch.
    let previousState: ConnectionState = 'connecting';

    async function refetchFeed(): Promise<void> {
      try {
        applySnapshot(await fetchFeed());
      } catch {
        // Leave the existing (stale-but-visible) feed in place.
      }
    }

    return subscribeStream({
      onBoard: () => {
        // The feed store doesn't care about raw board snapshots.
      },
      onSay: () => {
        // The feed store doesn't care about chat replies.
      },
      onFeed: applySnapshot,
      onConnectionStateChange: (state) => {
        if (state === 'connected' && previousState === 'reconnecting') {
          void refetchFeed();
        }
        previousState = state;
        setConnectionState(state);
      },
    });
  }, [applySnapshot]);

  const value = useMemo<FeedStoreValue>(() => ({ feed, connectionState }), [feed, connectionState]);

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
