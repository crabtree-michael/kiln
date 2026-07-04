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

  // First paint: fetch the current snapshot directly (08 §3). The stream's
  // first `feed` event carries the client through any reconnect after that.
  useEffect(() => {
    let cancelled = false;

    async function loadInitialFeed(): Promise<void> {
      try {
        const initialFeed = await fetchFeed();
        if (!cancelled) {
          applySnapshot(initialFeed);
        }
      } catch {
        // Swallowed: the stream's first `feed` event resyncs once the
        // connection opens (07 §8) — a failed first paint isn't fatal.
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

  useEffect(
    () =>
      subscribeStream({
        onBoard: () => {
          // The feed store doesn't care about raw board snapshots.
        },
        onSay: () => {
          // The feed store doesn't care about chat replies.
        },
        onFeed: applySnapshot,
        onConnectionStateChange: setConnectionState,
      }),
    [applySnapshot],
  );

  const value = useMemo<FeedStoreValue>(() => ({ feed, connectionState }), [feed, connectionState]);

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
