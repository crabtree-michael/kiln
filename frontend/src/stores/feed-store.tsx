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
  getActiveProjectId,
  postFeedSeen,
} from '@/transport/transport';
import type { ConnectionState, FeedCard, FeedSnapshot } from '@/transport/transport';
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

// How long a notification-backed card lingers in the default feed after the
// server stamped it seen (08 D2″). Once the window lapses the card drops out of
// the default view and is reachable again behind "Show seen notifications" —
// visibility only, never deletion: the row is still retained history and still
// pages in. Measured from the server's `seen_at`, not from a local timer, so the
// countdown survives a reload and a card seen two hours ago in another tab is
// already hidden when this one opens. UNSEEN cards have no `seen_at` and are
// therefore never auto-hidden, whatever their age.
const SEEN_LINGER_MS = 10 * 60 * 1000;

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

/** When the server stamped this card seen, in epoch ms — or null when it is
 * unseen (or carries no parseable stamp). Board-derived blocker/proposal cards
 * are never stamped, so they always read null and never expire. */
function seenAtMs(card: FeedCard): number | null {
  if (card.seen_at == null) {
    return null;
  }
  const ms = Date.parse(card.seen_at);
  return Number.isNaN(ms) ? null : ms;
}

/** The accumulated cards whose linger window has lapsed (08 D2″): seen at least
 * `SEEN_LINGER_MS` ago, so they drop out of the default feed until the user asks
 * for them back. Already-dismissed ids are left out — they are suppressed on
 * their own account, and counting them would inflate the "show seen" affordance
 * with cards the reveal would not bring back. */
function expiredSeenIds(
  updates: Map<number, FeedCard>,
  dismissedIds: Set<number>,
  now: number,
): Set<number> {
  const expired = new Set<number>();
  for (const [id, card] of updates) {
    const seen = seenAtMs(card);
    if (seen !== null && seen + SEEN_LINGER_MS <= now && !dismissedIds.has(id)) {
      expired.add(id);
    }
  }
  return expired;
}

/** When the next still-lingering seen card is due to expire, in epoch ms — or
 * null when none is pending. Drives the one timer that re-merges the feed at the
 * moment a card lapses, so a card the user has finished reading disappears on
 * its own rather than waiting for the next unrelated snapshot. */
function nextSeenExpiryAt(updates: Map<number, FeedCard>, now: number): number | null {
  let soonest: number | null = null;
  for (const card of updates.values()) {
    const seen = seenAtMs(card);
    if (seen === null) {
      continue;
    }
    const due = seen + SEEN_LINGER_MS;
    if (due > now && (soonest === null || due < soonest)) {
      soonest = due;
    }
  }
  return soonest;
}

/** Merge the wholesale board-derived cards from the server snapshot with the
 * accumulated (retained) update cards. Order (08 §3): blockers, then proposals,
 * then updates newest-first by `notification_id`. A board-derived card (proposal
 * OR blocker) whose ticket is in `hiddenTicketIds` is optimistically dropped —
 * the user tapped Accept on a proposal, or Delete on a proposal/blocked ticket,
 * and the card is hidden ahead of the server confirming the move. Update/preview
 * cards whose notification id is in `dismissedIds` are likewise dropped — the
 * user swiped them away and the retract may not have round-tripped yet — and so
 * are those in `hiddenSeenIds`, the seen cards whose linger window lapsed (08
 * D2″; empty while the user has "Show seen notifications" switched on). */
function mergeFeed(
  server: FeedSnapshot,
  updates: Map<number, FeedCard>,
  hiddenTicketIds: Set<string>,
  dismissedIds: Set<number>,
  hiddenSeenIds: Set<number>,
): FeedSnapshot {
  const hidden = (card: FeedCard): boolean =>
    card.ticket_id != null && hiddenTicketIds.has(card.ticket_id);
  const blockers = server.cards.filter((card) => card.kind === 'blocker' && !hidden(card));
  const proposals = server.cards.filter((card) => card.kind === 'proposal' && !hidden(card));
  const sortedUpdates = [...updates.values()]
    .filter(
      (card) =>
        card.notification_id == null ||
        !(dismissedIds.has(card.notification_id) || hiddenSeenIds.has(card.notification_id)),
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
  expiredCount: number;
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
  // "Show seen notifications" (08 D2″): false (the default) hides seen cards
  // once their linger window lapses; true reveals them again. View state only —
  // it changes nothing server-side and resets on reopen, so the feed always
  // opens decluttered.
  const [showSeen, setShowSeen] = useState(false);

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
  // Whether seen cards past their linger window are revealed (08 D2″). Mirrors
  // the `showSeen` state as a ref because every merge reads it: keeping the
  // merge callbacks off `showSeen` keeps them render-stable, so toggling the
  // reveal doesn't tear down and re-establish the SSE subscription below.
  const showSeenRef = useRef(false);
  // The single pending timer that re-merges the feed when the next seen card's
  // linger window lapses, so a read card leaves on its own.
  const seenSweepRef = useRef<ReturnType<typeof setTimeout> | null>(null);
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
      return { feed: null, lastSeen: null, expiredCount: 0 };
    }
    for (const card of cached.updates) {
      const id = updateId(card);
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
    const expired = expiredSeenIds(updatesRef.current, dismissedRef.current, now);
    return {
      feed: mergeFeed(
        cached.server,
        updatesRef.current,
        new Set(acceptedRef.current.keys()),
        dismissedRef.current,
        expired,
      ),
      lastSeen: cached.lastSeen,
      expiredCount: expired.size,
    };
  });
  const [feed, setFeed] = useState<FeedSnapshot | null>(seed.feed);
  const [lastSeenId, setLastSeenId] = useState<number | null>(seed.lastSeen);
  // How many accumulated cards the linger window has expired, whether or not
  // they are currently revealed — the gate on showing the affordance at all.
  const [expiredSeenCount, setExpiredSeenCount] = useState(seed.expiredCount);

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
  // the lapsed seen cards of 08 D2″). Every mutation path funnels through here
  // so those three suppressions can never fall out of step, and so the expiry
  // sweep is re-armed on each pass. No-op before the first snapshot lands.
  // Held in a ref as well as a callback because the sweep timer it arms calls
  // back into it.
  const remergeRef = useRef<() => void>(() => {
    // Replaced on mount; a sweep can only be armed from inside remerge itself.
  });
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
      dismissed: [...dismissedRef.current],
      hiddenTickets: [...acceptedRef.current],
    });
    const now = Date.now();
    const expired = expiredSeenIds(updatesRef.current, dismissedRef.current, now);
    setExpiredSeenCount(expired.size);
    setFeed(
      mergeFeed(
        server,
        updatesRef.current,
        liveAccepted(),
        dismissedRef.current,
        // Revealed ⇒ suppress nothing; the cards are still there, just hidden.
        showSeenRef.current ? new Set<number>() : expired,
      ),
    );

    // Re-arm the one sweep timer for the next card due to lapse. Rescheduling
    // from scratch each pass keeps it correct as cards arrive, get seen, or are
    // dismissed; a floor of 1s keeps a due-now card from busy-looping.
    if (seenSweepRef.current !== null) {
      clearTimeout(seenSweepRef.current);
      seenSweepRef.current = null;
    }
    const due = nextSeenExpiryAt(updatesRef.current, now);
    if (due !== null) {
      seenSweepRef.current = setTimeout(
        () => {
          seenSweepRef.current = null;
          remergeRef.current();
        },
        Math.max(due - now, 1000),
      );
    }
  }, [liveAccepted, projectId]);
  useEffect(() => {
    remergeRef.current = remerge;
  }, [remerge]);

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

      // An optimistically-hidden ticket the server no longer lists as a
      // proposal/blocker has resolved (the accept/delete took, or the brain
      // withdrew it): drop its marker so it neither lingers nor wrongly suppresses
      // a future re-proposal/re-block of the same ticket. These board-derived
      // cards are always sent in full, so absence here is authoritative regardless
      // of `has_more_history` (which is about update history only).
      const liveCardTicketIds = new Set<string>();
      for (const card of snapshot.cards) {
        if ((card.kind === 'proposal' || card.kind === 'blocker') && card.ticket_id != null) {
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
    // Paging deliberately reveals seen cards (08 D2″): an older page is almost
    // entirely long-seen updates, so leaving the linger filter on would make
    // "Show earlier updates" fetch a page and appear to do nothing. Asking for
    // history IS asking to see what was already read.
    showSeenRef.current = true;
    setShowSeen(true);
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

  // Toggle the "Show seen notifications" reveal (08 D2″). Pure view state: the
  // hidden cards were never dropped from the accumulated set, so revealing them
  // is a re-merge, not a fetch.
  const toggleShowSeen = useCallback((): void => {
    showSeenRef.current = !showSeenRef.current;
    setShowSeen(showSeenRef.current);
    remerge();
  }, [remerge]);

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

  // Clear any pending reappear timers — and the seen-linger sweep — on unmount
  // so they don't fire into an unmounted store.
  useEffect(() => {
    const timers = reappearTimersRef.current;
    return () => {
      for (const timer of timers) {
        clearTimeout(timer);
      }
      timers.clear();
      if (seenSweepRef.current !== null) {
        clearTimeout(seenSweepRef.current);
        seenSweepRef.current = null;
      }
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
      hasMoreHistory,
      loadingMoreHistory,
      loadMoreHistory,
      refreshFeed,
      showSeen,
      expiredSeenCount,
      toggleShowSeen,
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
      hasMoreHistory,
      loadingMoreHistory,
      loadMoreHistory,
      refreshFeed,
      showSeen,
      expiredSeenCount,
      toggleShowSeen,
      hideTicketCard,
      dismissCard,
      dismissAll,
    ],
  );

  return <FeedStoreContext.Provider value={value}>{children}</FeedStoreContext.Provider>;
}
