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
import {
  dismissAllFeedCards,
  dismissFeedCard,
  fetchFeed,
  fetchFeedHistory,
  postFeedSeen,
} from '@/transport/transport';
import type { ConnectionState, FeedCard, FeedSnapshot } from '@/transport/transport';
import { FeedStoreContext, type FeedStoreValue } from '@/stores/feed-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface FeedProviderProps {
  children: ReactNode;
}

// Older history pages this many cards at a time (matches the backend default at
// GET /api/feed/history).
const HISTORY_PAGE_SIZE = 30;

// How long an optimistically-accepted proposal stays hidden before it is allowed
// to reappear if the server never confirmed the acceptance (08 tap-accept, this
// change). Held only in memory, so it also clears on app reopen — whichever comes
// first. Long enough to cover the round-trip + brain transition, short enough that
// a genuinely-failed accept resurfaces the proposal so nothing is silently lost.
const OPTIMISTIC_ACCEPT_TTL_MS = 5 * 60 * 1000;

// Notification-backed card kinds (08 §3, §7): update/preview are brain-authored,
// poke is the steward's mechanical stall nudge, done is the runtime's mechanical
// completion card. All four are rows in the `notifications` table — the runtime
// returns them in the feed's retained, paginated update stream — so the store
// accumulates them across snapshots the same way. (Blocker/proposal cards are
// board-derived and taken fresh from every snapshot instead.) A card that isn't
// accumulated here never reaches the merged feed, so it silently vanishes even
// though FeedCardItem renders it.
function isUpdateCard(card: FeedCard): boolean {
  return (
    card.kind === 'update' ||
    card.kind === 'preview' ||
    card.kind === 'poke' ||
    card.kind === 'done'
  );
}

/** A notification-backed card (update/preview/poke/done) with a usable numeric
 * notification_id, narrowed. */
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
 * then updates newest-first by `notification_id`. Proposals whose ticket is in
 * `acceptedTicketIds` are optimistically dropped — the user tapped Accept and the
 * card is hidden ahead of the server confirming the move. Update/preview cards
 * whose notification id is in `dismissedIds` are likewise dropped — the user
 * swiped them away and the retract may not have round-tripped yet (this change). */
function mergeFeed(
  server: FeedSnapshot,
  updates: Map<number, FeedCard>,
  acceptedTicketIds: Set<string>,
  dismissedIds: Set<number>,
): FeedSnapshot {
  const blockers = server.cards.filter((card) => card.kind === 'blocker');
  const proposals = server.cards.filter(
    (card) =>
      card.kind === 'proposal' &&
      !(card.ticket_id != null && acceptedTicketIds.has(card.ticket_id)),
  );
  const sortedUpdates = [...updates.values()]
    .filter((card) => !(card.notification_id != null && dismissedIds.has(card.notification_id)))
    .sort((a, b) => (b.notification_id ?? 0) - (a.notification_id ?? 0));
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
  // Optimistically-accepted proposal tickets: ticket_id -> expiry timestamp (ms).
  // Purely in-memory, so it also clears on app reopen (this change).
  const acceptedRef = useRef<Map<string, number>>(new Map());
  // Notification ids the user has swiped away (08 §3 swipe-to-dismiss). Suppresses
  // the card in every merge until the server-side retract lands and the snapshot
  // stops listing it (pruned in applySnapshot). Purely in-memory — a failed
  // dismiss removes the id here so the card springs back.
  const dismissedRef = useRef<Set<number>>(new Set());
  // Live timers that force the proposal back into view when its TTL lapses.
  const reappearTimersRef = useRef<Set<ReturnType<typeof setTimeout>>>(new Set());

  // Prune expired optimistic acceptances and return the still-live ticket ids —
  // the set `mergeFeed` filters proposals against. Called on every merge, so a
  // lapsed acceptance stops hiding its proposal the next time the feed re-renders.
  const liveAccepted = useCallback((): Set<string> => {
    const now = Date.now();
    for (const [ticketId, expiry] of acceptedRef.current) {
      if (expiry <= now) {
        acceptedRef.current.delete(ticketId);
      }
    }
    return new Set(acceptedRef.current.keys());
  }, []);

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

      // An optimistically-accepted proposal the server no longer lists has
      // resolved (the accept took, or the brain withdrew it): drop its marker so
      // it neither lingers nor wrongly suppresses a future re-proposal of the same
      // ticket. Proposals are board-derived and always sent in full, so absence
      // here is authoritative regardless of `has_more_history` (which is about
      // update history only).
      const proposalIds = new Set<string>();
      for (const card of snapshot.cards) {
        if (card.kind === 'proposal' && card.ticket_id != null) {
          proposalIds.add(card.ticket_id);
        }
      }
      for (const ticketId of [...acceptedRef.current.keys()]) {
        if (!proposalIds.has(ticketId)) {
          acceptedRef.current.delete(ticketId);
        }
      }

      // Prune dismissals the server has now confirmed gone: once reconciliation
      // above drops a swiped-away id from the retained set, its suppression here
      // is spent (a fresh notification would reuse neither the id nor the intent).
      for (const id of [...dismissedRef.current]) {
        if (!updatesRef.current.has(id)) {
          dismissedRef.current.delete(id);
        }
      }

      ackVisibleSeen();
      setFeed(mergeFeed(snapshot, updatesRef.current, liveAccepted(), dismissedRef.current));
    },
    [ackVisibleSeen, liveAccepted],
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
            setFeed(
              mergeFeed(
                serverFeedRef.current,
                updatesRef.current,
                liveAccepted(),
                dismissedRef.current,
              ),
            );
          }
        } catch {
          // Leave the existing feed in place; the button stays available to retry.
        } finally {
          setLoadingMoreHistory(false);
        }
      })();
      return true;
    });
  }, [liveAccepted]);

  // Optimistically drop an accepted proposal card ahead of the server confirming
  // the move (08 tap-accept, this change): mark the ticket, re-merge to hide it
  // now, and arm a timer to restore it once the TTL lapses if the accept never
  // lands (a resolved accept clears the marker earlier, in `applySnapshot`).
  const acceptProposal = useCallback(
    (ticketId: string): void => {
      acceptedRef.current.set(ticketId, Date.now() + OPTIMISTIC_ACCEPT_TTL_MS);
      const timer = setTimeout(() => {
        reappearTimersRef.current.delete(timer);
        if (serverFeedRef.current !== null) {
          setFeed(
            mergeFeed(
              serverFeedRef.current,
              updatesRef.current,
              liveAccepted(),
              dismissedRef.current,
            ),
          );
        }
      }, OPTIMISTIC_ACCEPT_TTL_MS);
      reappearTimersRef.current.add(timer);
      if (serverFeedRef.current !== null) {
        setFeed(
          mergeFeed(
            serverFeedRef.current,
            updatesRef.current,
            liveAccepted(),
            dismissedRef.current,
          ),
        );
      }
    },
    [liveAccepted],
  );

  // Clear (dismiss) a single update/preview card by its notification id — the
  // swipe-left gesture (08 §3). Suppress it locally at once so the swipe feels
  // instant, then retract it server-side; the resulting feed.updated snapshot
  // drops it for good (and prunes the suppression). A failed request removes the
  // local suppression so the card springs back — nothing is silently lost.
  const dismissCard = useCallback(
    (notificationId: number): void => {
      dismissedRef.current.add(notificationId);
      if (serverFeedRef.current !== null) {
        setFeed(
          mergeFeed(
            serverFeedRef.current,
            updatesRef.current,
            liveAccepted(),
            dismissedRef.current,
          ),
        );
      }
      void dismissFeedCard(notificationId).catch(() => {
        dismissedRef.current.delete(notificationId);
        if (serverFeedRef.current !== null) {
          setFeed(
            mergeFeed(
              serverFeedRef.current,
              updatesRef.current,
              liveAccepted(),
              dismissedRef.current,
            ),
          );
        }
      });
    },
    [liveAccepted],
  );

  // Clear ALL notification-backed cards at once — the header trash affordance
  // (08 §3, this change). Suppress every currently-known update card locally so
  // the feed empties instantly, then retract them all server-side; the resulting
  // feed.updated snapshot drops them for good. A failed request removes only the
  // suppressions we just added (a card already swiped away stays hidden) so the
  // cleared cards spring back — nothing is silently lost. No-op on the empty feed.
  const dismissAll = useCallback((): void => {
    // Only the ids not already suppressed, so a rollback can't un-hide a card the
    // user had individually swiped before clearing all.
    const cleared = [...updatesRef.current.keys()].filter((id) => !dismissedRef.current.has(id));
    if (cleared.length === 0 && dismissedRef.current.size === 0) {
      return; // nothing notification-backed to clear
    }
    for (const id of cleared) {
      dismissedRef.current.add(id);
    }
    if (serverFeedRef.current !== null) {
      setFeed(
        mergeFeed(serverFeedRef.current, updatesRef.current, liveAccepted(), dismissedRef.current),
      );
    }
    void dismissAllFeedCards().catch(() => {
      for (const id of cleared) {
        dismissedRef.current.delete(id);
      }
      if (serverFeedRef.current !== null) {
        setFeed(
          mergeFeed(
            serverFeedRef.current,
            updatesRef.current,
            liveAccepted(),
            dismissedRef.current,
          ),
        );
      }
    });
  }, [liveAccepted]);

  // Clear any pending reappear timers on unmount so they don't fire into an
  // unmounted store.
  useEffect(() => {
    const timers = reappearTimersRef.current;
    return () => {
      for (const timer of timers) {
        clearTimeout(timer);
      }
      timers.clear();
    };
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
      acceptProposal,
      dismissCard,
      dismissAll,
    }),
    [
      feed,
      connectionState,
      lastSeenId,
      hasMoreHistory,
      loadingMoreHistory,
      loadMoreHistory,
      acceptProposal,
      dismissCard,
      dismissAll,
    ],
  );

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
