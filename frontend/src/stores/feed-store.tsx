// Feed store (08 §3, D2′): holds the current feed with RETAINED update history.
// Every `feed` SSE event — and the initial `GET /api/feed` — replaces the
// board-derived cards (blocker/proposal) wholesale. Update/preview cards are
// notification-backed and now KEPT: the store accumulates them across snapshots
// and paged history (`GET /api/feed/history`), so returning after being away no
// longer erases what happened. A frozen "last seen" boundary
// (`summary.last_seen_notification_id`, captured once per session) drives the
// divider between new-since-last-visit and older history — the client keeps
// marking updates seen on view (advancing the server mark for NEXT time) without
// the divider jumping mid-session. The same boundary is what the DEFAULT view
// shows (D2‴): everything already caught up on collapses out of it with no
// timer, and one control, "Show earlier", brings it back and then keeps paging.
// Live updates ride the single app-wide stream connection
// (`@/stores/stream-connection`), shared with the board/chat stores.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import {
  dismissAllFeedCards,
  dismissFeedCard,
  fetchFeed,
  fetchFeedHistory,
  getActiveProjectId,
  postFeedSeen,
} from '@/transport/transport';
import type { ConnectionState, FeedCard, FeedSnapshot } from '@/transport/transport';
import { notificationId } from '@/components/feed-model';
import { isBlockerCard, isBoardCard, isProposalCard } from '@/components/feed-kinds';
import { FeedStoreContext, type FeedStoreValue } from '@/stores/feed-context';
import { cacheFeed, readCachedFeed } from '@/stores/project-cache';
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

// The notification-backed taxonomy this store accumulates by — update/preview
// (brain-authored), poke (the steward's mechanical stall nudge) and done (the
// runtime's mechanical completion card) — now lives in `@/components/feed-model`
// as `notificationId`, shared with the feed shells that ask the same question
// for the swipe-to-clear gesture. It used to be spelled `updateId` here and
// `dismissableId` in `PrimaryScreenView`, which is the duplication review D9
// named.
//
// It is NOT the same set as the shells' divider boundary (`authoredUpdateId`,
// update/preview only), which was ALSO called `updateId` before the split. Read
// feed-model.ts's header before touching either: collapsing them is a silent,
// type-checking way to slide the "Earlier" divider onto the poke and done cards.
//
// Both sets, and every other question this store asks about a card's kind
// (`isBoardCard` for the reconciliation below, `isBlockerCard`/`isProposalCard`
// for the merge order), come from `@/components/feed-kinds` — the one matrix a
// new kind has to be added to before anything here can see it.
//
// A card that isn't accumulated here never reaches the merged feed, so it
// silently vanishes even though FeedCardItem renders it.

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

/** The accumulated cards that have collapsed out of the default feed (08 D2‴):
 * everything at or below the visit's seen floor — i.e. every notification the
 * user had already caught up on when they last looked at this screen. There is
 * no timer and no age: a card collapses the moment "seen" is a fact from a
 * previous look, and "Show earlier" brings it back.
 *
 * The floor only ever holds a mark the server stamped seen (its persistent
 * high-water, or one this client acked), so an UNSEEN card can never fall at or
 * below it — that invariant is what makes this safe, and it is why the filter
 * reads notification ids rather than `created_at`. Already-dismissed ids are
 * left out: they are suppressed on their own account, and counting them would
 * offer "Show earlier" for cards the reveal would not bring back. */
function collapsedSeenIds(
  updates: Map<number, FeedCard>,
  dismissedIds: Set<number>,
  seenFloor: number,
): Set<number> {
  const collapsed = new Set<number>();
  for (const id of updates.keys()) {
    if (id <= seenFloor && !dismissedIds.has(id)) {
      collapsed.add(id);
    }
  }
  return collapsed;
}

/** Merge the wholesale board-derived cards from the server snapshot with the
 * accumulated (retained) update cards. Order (08 §3): blockers, then proposals,
 * then updates newest-first by `notification_id`. A board-derived card (proposal
 * OR blocker) whose ticket is in `hiddenTicketIds` is optimistically dropped —
 * the user tapped Accept on a proposal, or Delete on a proposal/blocked ticket,
 * and the card is hidden ahead of the server confirming the move. Update/preview
 * cards whose notification id is in `dismissedIds` are likewise dropped — the
 * user swiped them away and the retract may not have round-tripped yet — and so
 * are those in `collapsedIds`, the already-seen cards tucked away behind "Show
 * earlier" (08 D2‴; empty once the user has asked for them back). */
function mergeFeed(
  server: FeedSnapshot,
  updates: Map<number, FeedCard>,
  hiddenTicketIds: Set<string>,
  dismissedIds: Set<number>,
  collapsedIds: Set<number>,
): FeedSnapshot {
  const hidden = (card: FeedCard): boolean =>
    card.ticket_id != null && hiddenTicketIds.has(card.ticket_id);
  const blockers = server.cards.filter((card) => isBlockerCard(card) && !hidden(card));
  const proposals = server.cards.filter((card) => isProposalCard(card) && !hidden(card));
  const sortedUpdates = [...updates.values()]
    .filter(
      (card) =>
        card.notification_id == null ||
        !(dismissedIds.has(card.notification_id) || collapsedIds.has(card.notification_id)),
    )
    .sort((a, b) => (b.notification_id ?? 0) - (a.notification_id ?? 0));
  return { ...server, cards: [...blockers, ...proposals, ...sortedUpdates] };
}

/** What the per-project cache seeds this store's first render with — see the
 * restore in `FeedProvider`. All three fields are `null`/`0` on a project the
 * app hasn't loaded this session. */
interface FeedSeed {
  feed: FeedSnapshot | null;
  lastSeen: number | null;
  collapsedCount: number;
}

export function FeedProvider({ children }: FeedProviderProps): JSX.Element {
  // The project this instance is scoped to, captured once at mount. A switch
  // remounts the whole subtree (12 §4.1), so it cannot change under us — which
  // is what makes it safe to key the cache on for this store's whole lifetime.
  const [projectId] = useState(getActiveProjectId);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const [hasMoreHistory, setHasMoreHistory] = useState(false);
  const [loadingMoreHistory, setLoadingMoreHistory] = useState(false);
  // True while a full-snapshot fetch is in flight (mount/switch, reconnect
  // refetch, pull-to-refresh) — including the refresh that runs behind a
  // cache-seeded feed, so "catching up" never renders as "all quiet".
  const [loading, setLoading] = useState(true);

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
  // Whether the collapsed already-seen cards have been revealed (08 D2‴). View
  // state only — it changes nothing server-side and resets on reopen, so the
  // feed always opens decluttered. Held as a ref rather than state because every
  // merge reads it: keeping the merge callbacks off it keeps them render-stable,
  // so asking for earlier cards doesn't tear down and re-establish the SSE
  // subscription below. Flipping it is always followed by a `remerge()`, which
  // is what re-renders.
  const showEarlierRef = useRef(false);
  // The visit's seen floor (08 D2‴): notification ids at or below this have been
  // caught up on and collapse out of the default feed. It only ever advances to
  // a mark the server has stamped seen — its persistent high-water at the first
  // snapshot of the visit, or what this client acked during the previous one —
  // so an unseen card can never fall below it. It is deliberately NOT advanced
  // by the ack we fire while the user is looking at the screen: cards must not
  // evaporate mid-read, so what is seen *now* collapses at the next visit.
  const seenFloorRef = useRef(0);
  // How many full-snapshot fetches are in flight, so overlapping ones (a
  // reconnect refetch landing on top of the mount fetch) can't clear `loading`
  // while the other is still running.
  const loadsRef = useRef(0);

  // Seed from the per-project cache (12 §4.1). This runs in a lazy state
  // initializer rather than a mount effect on purpose: an effect fires after
  // paint, so a switch back to a loaded project would still flash one empty
  // frame — which is the exact thing the cache exists to remove. Writing to the
  // refs from here is a deliberate one-shot restore of THIS instance's own
  // session state, and it is idempotent, so a StrictMode double-invoke lands on
  // the same values. Everything restored is still refreshed by the mount fetch
  // below; nothing here is shown instead of asking the server.
  const [seed] = useState<FeedSeed>(() => {
    const cached = readCachedFeed(projectId);
    if (cached === null) {
      return { feed: null, lastSeen: null, collapsedCount: 0 };
    }
    for (const card of cached.updates) {
      const id = notificationId(card);
      if (id !== null) {
        updatesRef.current.set(id, card);
      }
    }
    serverFeedRef.current = cached.server;
    // The divider boundary was frozen when this project was first opened this
    // session (08 D2′) and it stays frozen across the switch: re-freezing it
    // against a mark our own acks have since advanced would move the "Earlier"
    // line under the user for no reason they could see.
    seededRef.current = true;
    sessionLastSeenRef.current = cached.lastSeen;
    ackedRef.current = cached.acked;
    // The collapse floor is restored, not recomputed, for the same reason as the
    // suppressions below: a switch back paints what was last on screen here. A
    // switch is not a new visit — the user never left the app — so re-reading
    // the server's (by now ack-advanced) mark would empty a feed they were
    // reading a moment ago.
    seenFloorRef.current = cached.seenFloor;
    for (const id of cached.dismissed) {
      dismissedRef.current.add(id);
    }
    const now = Date.now();
    for (const [ticketId, expiry] of cached.hiddenTickets) {
      // Restore only hides that are still within their time box; a lapsed one
      // is exactly a card that should have been allowed back by now.
      if (expiry > now) {
        acceptedRef.current.set(ticketId, expiry);
      }
    }
    // No reappear timer is re-armed for the restored hides: `liveAccepted()`
    // prunes on every merge, and the mount fetch below re-merges within a
    // round-trip, so a lapsed hide is released then rather than on its own timer.
    const collapsed = collapsedSeenIds(
      updatesRef.current,
      dismissedRef.current,
      seenFloorRef.current,
    );
    return {
      feed: mergeFeed(
        cached.server,
        updatesRef.current,
        new Set(acceptedRef.current.keys()),
        dismissedRef.current,
        collapsed,
      ),
      lastSeen: cached.lastSeen,
      collapsedCount: collapsed.size,
    };
  });
  const [feed, setFeed] = useState<FeedSnapshot | null>(seed.feed);
  const [lastSeenId, setLastSeenId] = useState<number | null>(seed.lastSeen);
  // How many accumulated cards are currently collapsed out of the feed — half of
  // what puts the one "Show earlier" control on screen (older history on the
  // server is the other half).
  const [collapsedCount, setCollapsedCount] = useState(seed.collapsedCount);

  // Bracket a full-snapshot fetch, so `loading` is true for exactly as long as
  // at least one is outstanding.
  const beginLoad = useCallback((): void => {
    loadsRef.current += 1;
    setLoading(true);
  }, []);
  const endLoad = useCallback((): void => {
    loadsRef.current = Math.max(0, loadsRef.current - 1);
    setLoading(loadsRef.current > 0);
  }, []);

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

  // Re-derive the visible feed from the latest server snapshot plus everything
  // the client suppresses on top of it (optimistic hides, swipe dismissals, and
  // the collapsed already-seen cards of 08 D2‴). Every mutation path funnels
  // through here so those three suppressions can never fall out of step. No-op
  // before the first snapshot lands.
  const remerge = useCallback((): void => {
    const server = serverFeedRef.current;
    if (server === null) {
      return;
    }
    // Write through to the per-project cache from the one funnel every mutation
    // path already goes through, so what a later switch back restores is exactly
    // what was last on screen — suppressions included (see `CachedFeed`).
    cacheFeed(projectId, {
      server,
      updates: [...updatesRef.current.values()],
      lastSeen: sessionLastSeenRef.current,
      acked: ackedRef.current,
      seenFloor: seenFloorRef.current,
      dismissed: [...dismissedRef.current],
      hiddenTickets: [...acceptedRef.current],
    });
    // Revealed ⇒ collapse nothing; the cards were never dropped, only hidden.
    const collapsed = showEarlierRef.current
      ? new Set<number>()
      : collapsedSeenIds(updatesRef.current, dismissedRef.current, seenFloorRef.current);
    setCollapsedCount(collapsed.size);
    setFeed(mergeFeed(server, updatesRef.current, liveAccepted(), dismissedRef.current, collapsed));
  }, [liveAccepted, projectId]);

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
        // The same mark opens this visit's seen floor (08 D2‴): everything the
        // user had caught up on before now collapses out of the default feed.
        // It has to be read here, BEFORE `ackVisibleSeen` below advances the
        // server's mark past every card in this very snapshot — that ack is
        // about the *next* visit, not this one.
        seenFloorRef.current = Math.max(seenFloorRef.current, ls ?? 0);
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
        const id = notificationId(card);
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
        const id = notificationId(card);
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

      // An optimistically-hidden ticket the server no longer lists as a
      // proposal/blocker has resolved (the accept/delete took, or the brain
      // withdrew it): drop its marker so it neither lingers nor wrongly suppresses
      // a future re-proposal/re-block of the same ticket. These board-derived
      // cards are always sent in full, so absence here is authoritative regardless
      // of `has_more_history` (which is about update history only).
      const liveCardTicketIds = new Set<string>();
      for (const card of snapshot.cards) {
        if (isBoardCard(card) && card.ticket_id != null) {
          liveCardTicketIds.add(card.ticket_id);
        }
      }
      for (const ticketId of [...acceptedRef.current.keys()]) {
        if (!liveCardTicketIds.has(ticketId)) {
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
      remerge();
    },
    [ackVisibleSeen, remerge],
  );

  const loadMoreHistory = useCallback((): void => {
    if (!seededRef.current) {
      return;
    }
    // Paging deliberately reveals the collapsed cards (08 D2‴): an older page is
    // almost entirely long-seen updates, so leaving the collapse on would make
    // "Show earlier" fetch a page and appear to do nothing. Asking for history
    // IS asking to see what was already read.
    showEarlierRef.current = true;
    setLoadingMoreHistory((inFlight) => {
      if (inFlight) {
        return inFlight; // already fetching — ignore repeat taps
      }
      const before = oldestUpdateId(updatesRef.current);
      void (async () => {
        try {
          const page = await fetchFeedHistory(before, HISTORY_PAGE_SIZE);
          for (const card of page.cards) {
            const id = notificationId(card);
            if (id !== null) {
              updatesRef.current.set(id, card);
            }
          }
          pagedBelowWindowRef.current = true;
          setHasMoreHistory(page.has_more);
          remerge();
        } catch {
          // Leave the existing feed in place; the button stays available to retry.
        } finally {
          setLoadingMoreHistory(false);
        }
      })();
      return true;
    });
  }, [remerge]);

  // The one "Show earlier" action (08 D2‴). It has a single meaning — *further
  // back* — and answers it with whatever is nearest to hand: the collapsed
  // already-seen cards first (a re-merge, not a fetch: they were never dropped
  // from the accumulated set), and once nothing is collapsed, the next older
  // page from the server. That is why there is one control and one label rather
  // than a reveal toggle beside a pager: the user is asking the same thing both
  // times. There is no way back — the reveal is view state and resets on reopen,
  // so the feed still always opens decluttered.
  const showEarlier = useCallback((): void => {
    if (!showEarlierRef.current && collapsedCount > 0) {
      showEarlierRef.current = true;
      remerge();
      return;
    }
    loadMoreHistory();
  }, [collapsedCount, loadMoreHistory, remerge]);

  // Re-fetch the current snapshot on demand — the pull-to-refresh gesture (this
  // change). Same shape as the reconnect refetch below: apply a fresh snapshot on
  // success, keep the stale-but-visible feed on failure. Returns the promise so
  // the gesture can keep its spinner up until the round-trip settles.
  const refreshFeed = useCallback(async (): Promise<void> => {
    beginLoad();
    try {
      applySnapshot(await fetchFeed());
    } catch {
      // Leave the existing (stale-but-visible) feed in place.
    } finally {
      endLoad();
    }
  }, [applySnapshot, beginLoad, endLoad]);

  // Optimistically drop a ticket's board-derived card (proposal or blocker)
  // ahead of the server confirming its removal: tap-Accept (the proposal becomes
  // ready), tap-Delete on a proposal (archived), and tap-Delete on a blocked
  // ticket (archived, its worker released) all make the card disappear, so all
  // hide it the same way — mark the ticket, re-merge to hide it now, and arm a
  // timer to restore it once the TTL lapses if the server transition never lands
  // (a resolved accept/delete clears the marker earlier, in `applySnapshot`, when
  // the card drops out of the board snapshot).
  const hideTicketCard = useCallback(
    (ticketId: string): void => {
      acceptedRef.current.set(ticketId, Date.now() + OPTIMISTIC_ACCEPT_TTL_MS);
      const timer = setTimeout(() => {
        reappearTimersRef.current.delete(timer);
        remerge();
      }, OPTIMISTIC_ACCEPT_TTL_MS);
      reappearTimersRef.current.add(timer);
      remerge();
    },
    [remerge],
  );

  // Clear (dismiss) a single update/preview card by its notification id — the
  // swipe-left gesture (08 §3). Suppress it locally at once so the swipe feels
  // instant, then retract it server-side; the resulting feed.updated snapshot
  // drops it for good (and prunes the suppression). A failed request removes the
  // local suppression so the card springs back — nothing is silently lost.
  const dismissCard = useCallback(
    (notificationId: number): void => {
      dismissedRef.current.add(notificationId);
      remerge();
      void dismissFeedCard(notificationId).catch(() => {
        dismissedRef.current.delete(notificationId);
        remerge();
      });
    },
    [remerge],
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
    remerge();
    void dismissAllFeedCards().catch(() => {
      for (const id of cleared) {
        dismissedRef.current.delete(id);
      }
      remerge();
    });
  }, [remerge]);

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
      beginLoad();
      try {
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
      } finally {
        // The retry loop only exits on success or on unmount, so this genuinely
        // brackets the whole wait — a client stuck on backoff keeps saying so
        // rather than silently presenting a cache-seeded feed as current.
        endLoad();
      }
    }

    void loadInitialFeed();
    return () => {
      cancelled = true;
    };
  }, [applySnapshot, beginLoad, endLoad]);

  // Re-run the seen check when the screen becomes visible again: cards rendered
  // while hidden were deliberately not acked (08 §3 "only when visible").
  //
  // Coming back to the screen also STARTS A NEW VISIT (08 D2‴), and that is what
  // keeps the feed near-empty on a surface that is never reloaded — the desktop
  // window left open all day, the phone's app resumed from the background.
  // Everything acked while the user was last looking has now been looked at, so
  // the floor advances to it and those cards collapse. The floor is read from
  // our own ack high-water rather than re-fetched, so this costs nothing and
  // can never collapse a card the server hasn't stamped seen.
  useEffect(() => {
    function handleVisibility(): void {
      if (document.visibilityState !== 'visible' || serverFeedRef.current === null) {
        return;
      }
      const opensNewVisit = seenFloorRef.current < ackedRef.current;
      seenFloorRef.current = Math.max(seenFloorRef.current, ackedRef.current);
      ackVisibleSeen();
      if (opensNewVisit) {
        remerge();
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [ackVisibleSeen, remerge]);

  useEffect(() => {
    // Reconnect-refetch (mirrors chat-store, 07 §5/§8): a `feed` SSE event only
    // fires when the feed mutates, and nothing is pushed on connect — so a
    // stream drop/reopen would otherwise leave the feed stale until the next
    // unrelated `feed.updated`. Refetch once on every reconnecting -> connected
    // transition to close that gap. The initial connect is already covered by
    // the mount fetch above, so it doesn't double-fetch.
    let previousState: ConnectionState = 'connecting';

    async function refetchFeed(): Promise<void> {
      beginLoad();
      try {
        applySnapshot(await fetchFeed());
      } catch {
        // Leave the existing (stale-but-visible) feed in place.
      } finally {
        endLoad();
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
  }, [applySnapshot, beginLoad, endLoad]);

  const value = useMemo<FeedStoreValue>(
    () => ({
      feed,
      connectionState,
      loading,
      lastSeenId,
      // Anything further back to show: cards collapsed for being already seen,
      // or older history still on the server. Either one puts the single
      // "Show earlier" control on screen; neither leaves it there dead.
      hasEarlier: collapsedCount > 0 || hasMoreHistory,
      loadingEarlier: loadingMoreHistory,
      showEarlier,
      refreshFeed,
      // Accept and delete are the same optimistic board-card hide (see
      // `hideTicketCard`); the two names keep the caller's intent legible.
      // `deleteTicketCard` covers deleting a proposal or a blocked ticket — both
      // hide the ticket's board-derived card the same way.
      acceptProposal: hideTicketCard,
      deleteTicketCard: hideTicketCard,
      dismissCard,
      dismissAll,
    }),
    [
      feed,
      connectionState,
      loading,
      lastSeenId,
      collapsedCount,
      hasMoreHistory,
      loadingMoreHistory,
      showEarlier,
      refreshFeed,
      hideTicketCard,
      dismissCard,
      dismissAll,
    ],
  );

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
