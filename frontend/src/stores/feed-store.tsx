// Feed store (08 §3, D2′): holds the current feed with RETAINED update history.
// Every `feed` SSE event — and the initial `GET /api/feed` — replaces the
// board-derived cards (blocker/proposal) wholesale. Update/preview cards are
// notification-backed and now KEPT: the store accumulates them across snapshots
// and paged history (`GET /api/feed/history`), so returning after being away no
// longer erases what happened. A frozen "last seen" boundary
// (`summary.last_seen_notification_id`, captured once per session) drives the
// divider between new-since-last-visit and older history — the client keeps
// marking updates seen on view (advancing the server mark for NEXT time) without
// the divider jumping mid-session. Live updates ride the single app-wide stream
// connection (`@/stores/stream-connection`), shared with the board/chat stores.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import { fetchFeed, fetchFeedHistory, postFeedSeen } from '@/transport/transport';
import type { ConnectionState, FeedCard, FeedSnapshot } from '@/transport/transport';
import { FeedStoreContext, type FeedStoreValue } from '@/stores/feed-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface FeedProviderProps {
  children: ReactNode;
}

// Older history pages this many cards at a time (matches the backend default at
// GET /api/feed/history).
const HISTORY_PAGE_SIZE = 30;

function isUpdateCard(card: FeedCard): boolean {
  return card.kind === 'update' || card.kind === 'preview';
}

/** An update/preview card with a usable numeric notification_id, narrowed. */
function updateId(card: FeedCard): number | null {
  return isUpdateCard(card) && typeof card.notification_id === 'number'
    ? card.notification_id
    : null;
}

/** The smallest notification_id currently accumulated — the keyset cursor for
 * the next older history page. `undefined` when no updates are held yet. */
function oldestUpdateId(updates: Map<number, FeedCard>): number | undefined {
  let min: number | undefined;
  for (const id of updates.keys()) {
    if (min === undefined || id < min) {
      min = id;
    }
  }
  return min;
}

/** The greatest notification_id currently accumulated — the seen high-water to
 * ack. `0` when no updates are held. */
function newestUpdateId(updates: Map<number, FeedCard>): number {
  let max = 0;
  for (const id of updates.keys()) {
    if (id > max) {
      max = id;
    }
  }
  return max;
}

/** Merge the wholesale board-derived cards from the server snapshot with the
 * accumulated (retained) update cards. Order (08 §3): blockers, then proposals,
 * then updates newest-first by `notification_id`. */
function mergeFeed(server: FeedSnapshot, updates: Map<number, FeedCard>): FeedSnapshot {
  const blockers = server.cards.filter((card) => card.kind === 'blocker');
  const proposals = server.cards.filter((card) => card.kind === 'proposal');
  const sortedUpdates = [...updates.values()].sort(
    (a, b) => (b.notification_id ?? 0) - (a.notification_id ?? 0),
  );
  return { ...server, cards: [...blockers, ...proposals, ...sortedUpdates] };
}

export function FeedProvider({ children }: FeedProviderProps): JSX.Element {
  const [feed, setFeed] = useState<FeedSnapshot | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const [lastSeenId, setLastSeenId] = useState<number | null>(null);
  const [hasMoreHistory, setHasMoreHistory] = useState(false);
  const [loadingMoreHistory, setLoadingMoreHistory] = useState(false);

  // Session-scoped, render-stable state (mirrors chat-store's ref pattern):
  const updatesRef = useRef<Map<number, FeedCard>>(new Map()); // accumulated update cards by id
  const serverFeedRef = useRef<FeedSnapshot | null>(null); // latest server snapshot (for re-merge / visibility)
  const seededRef = useRef(false); // has the session last-seen boundary been frozen?
  const sessionLastSeenRef = useRef<number | null>(null); // the frozen divider boundary
  const ackedRef = useRef(0); // highest notification_id already POSTed to /feed/seen this session
  const pagedBelowWindowRef = useRef(false); // has the user paged older than the snapshot window?

  // Mark unseen update cards seen — but only on a visible screen (08 §3). Seen
  // updates are RETAINED now (they stay as history); the ack just advances the
  // persistent last-seen mark so NEXT session's divider is right. Deduped by a
  // session high-water so we don't re-POST a mark we've already sent.
  const ackVisibleSeen = useCallback((): void => {
    if (document.visibilityState !== 'visible') {
      return;
    }
    const maxId = newestUpdateId(updatesRef.current);
    if (maxId > ackedRef.current) {
      ackedRef.current = maxId;
      void postFeedSeen(maxId);
    }
  }, []);

  const applySnapshot = useCallback(
    (snapshot: FeedSnapshot): void => {
      serverFeedRef.current = snapshot;

      // Freeze the last-seen divider boundary once per session, before the first
      // ack advances the server mark (08 D2′).
      if (!seededRef.current) {
        seededRef.current = true;
        const ls = snapshot.summary.last_seen_notification_id;
        sessionLastSeenRef.current = typeof ls === 'number' ? ls : null;
        setLastSeenId(sessionLastSeenRef.current);
      }

      // Reconcile the retained update set against the snapshot's update cards.
      // `has_more_history === false` means the snapshot carries the COMPLETE
      // unretracted set, so anything accumulated but absent was retracted — drop
      // it. When there IS older history the snapshot is only the newest page, so
      // an absent id is authoritatively retracted only if it falls at/above the
      // page's floor; older loaded history below the floor is left untouched
      // (a deep retraction reconciles on the next full snapshot or reload).
      const serverIds = new Set<number>();
      let windowFloor = Infinity;
      for (const card of snapshot.cards) {
        const id = updateId(card);
        if (id === null) {
          continue;
        }
        serverIds.add(id);
        if (id < windowFloor) {
          windowFloor = id;
        }
      }
      const snapshotIsComplete = !snapshot.has_more_history;
      for (const id of [...updatesRef.current.keys()]) {
        if (!serverIds.has(id) && (snapshotIsComplete || id >= windowFloor)) {
          updatesRef.current.delete(id);
        }
      }
      for (const card of snapshot.cards) {
        const id = updateId(card);
        if (id !== null) {
          updatesRef.current.set(id, card);
        }
      }

      // has-more only tracks the snapshot while we haven't paged below its
      // window; once the user loads older history, pagination owns the flag
      // (a fresh snapshot's has_more_history is about the newest page, not about
      // what's older than everything we've now loaded).
      if (!pagedBelowWindowRef.current) {
        setHasMoreHistory(snapshot.has_more_history);
      }

      ackVisibleSeen();
      setFeed(mergeFeed(snapshot, updatesRef.current));
    },
    [ackVisibleSeen],
  );

  const loadMoreHistory = useCallback((): void => {
    if (!seededRef.current) {
      return;
    }
    setLoadingMoreHistory((inFlight) => {
      if (inFlight) {
        return inFlight; // already fetching — ignore repeat taps
      }
      const before = oldestUpdateId(updatesRef.current);
      void (async () => {
        try {
          const page = await fetchFeedHistory(before, HISTORY_PAGE_SIZE);
          for (const card of page.cards) {
            const id = updateId(card);
            if (id !== null) {
              updatesRef.current.set(id, card);
            }
          }
          pagedBelowWindowRef.current = true;
          setHasMoreHistory(page.has_more);
          if (serverFeedRef.current !== null) {
            setFeed(mergeFeed(serverFeedRef.current, updatesRef.current));
          }
        } catch {
          // Leave the existing feed in place; the button stays available to retry.
        } finally {
          setLoadingMoreHistory(false);
        }
      })();
      return true;
    });
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
        ackVisibleSeen();
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [ackVisibleSeen]);

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

  const value = useMemo<FeedStoreValue>(
    () => ({
      feed,
      connectionState,
      lastSeenId,
      hasMoreHistory,
      loadingMoreHistory,
      loadMoreHistory,
    }),
    [feed, connectionState, lastSeenId, hasMoreHistory, loadingMoreHistory, loadMoreHistory],
  );

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
