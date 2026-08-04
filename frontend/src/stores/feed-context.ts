// Split from feed-store.tsx so that file exports only the `FeedProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the consumer hook. Mirrors board-context.ts.
import { createContext, useContext } from 'react';
import type { ConnectionState, FeedSnapshot } from '@/transport/transport';

export type { FeedSnapshot };

export interface FeedStoreValue {
  /**
   * The current feed, or `null` before the first `feed` event/fetch arrives.
   * Blocker/proposal cards mirror the server snapshot wholesale; update/preview
   * cards are retained history (08 D2′): the store accumulates them across
   * snapshots and paged history so nothing vanishes when the user returns.
   */
  feed: FeedSnapshot | null;
  /** Stream state for the connection chip (07 §8, 08 §F feed region gate). */
  connectionState: ConnectionState;
  /**
   * The last-seen divider boundary (08 D2′): update/preview cards with a greater
   * `notification_id` are new since the last visit (above the divider); those at
   * or below it are older history (below it). Frozen at the first snapshot of the
   * session so marking-seen-on-view doesn't move the divider mid-session. `null`
   * when nothing has ever been seen (no divider).
   */
  lastSeenId: number | null;
  /** True when older retained update history remains to page in (08 D2′). */
  hasMoreHistory: boolean;
  /** True while a `loadMoreHistory()` page fetch is in flight. */
  loadingMoreHistory: boolean;
  /** Fetch and append the next older page of update history (08 D2′). No-op when
   * `hasMoreHistory` is false or a fetch is already in flight. */
  loadMoreHistory: () => void;
  /**
   * Re-fetch the current feed snapshot on demand — the pull-to-refresh gesture
   * (this change). Mirrors the reconnect refetch: applies the fresh snapshot on
   * success and leaves the existing (stale-but-visible) feed in place on failure.
   * Returns a promise that resolves once the fetch has settled, so the caller can
   * hold its loading indicator up for the whole round-trip.
   */
  refreshFeed: () => Promise<void>;
  /**
   * Whether seen cards past their linger window are currently revealed (08 D2″).
   * False by default — a notification the user has already seen drops out of the
   * feed ~10 minutes after being seen, so the default view stays a to-attend
   * list rather than a log. Unseen cards are never affected.
   */
  showSeen: boolean;
  /**
   * How many accumulated cards the linger window has expired, whether or not
   * they are currently revealed. Zero means there is nothing behind the "Show
   * seen notifications" affordance, so it isn't offered.
   */
  expiredSeenCount: number;
  /**
   * Flip the "Show seen notifications" reveal (08 D2″). Purely local view state:
   * the expired cards are still held (and still retained server-side), so this
   * only changes what the merged feed renders — nothing is fetched, retracted, or
   * persisted, and the reveal resets to off on reopen.
   */
  toggleShowSeen: () => void;
  /**
   * Optimistically hide an accepted proposal card by ticket id: the card drops
   * from the feed immediately, ahead of the server confirming the move. The hide
   * is in-memory and time-boxed (~5 min, or until app reopen) — if the accept
   * never lands, the proposal reappears so nothing is silently lost.
   */
  acceptProposal: (ticketId: string) => void;
  /**
   * Optimistically hide a deleted ticket's board-derived card (proposal or
   * blocker) by ticket id: the card drops from the feed immediately, ahead of the
   * server confirming the archive. Same time-boxed, self-healing hide as
   * `acceptProposal` — deleting a proposal or a blocked ticket both make the card
   * disappear, so both suppress it the same way.
   */
  deleteTicketCard: (ticketId: string) => void;
  /**
   * Clear (dismiss) a single update/preview card by its notification id — the
   * swipe-left gesture (08 §3). The card drops from the feed immediately
   * (optimistic) and is retracted server-side so it does not return on the next
   * snapshot or reload; if the request fails the card springs back so nothing is
   * silently lost. No-op for board-derived cards, which have no notification id.
   */
  dismissCard: (notificationId: number) => void;
  /**
   * Clear ALL notification-backed cards at once — the header trash affordance
   * (08 §3). Every currently-known update/preview/poke/done card drops from the
   * feed immediately (optimistic) and all are retracted server-side so none
   * return on the next snapshot or reload; if the request fails the cards spring
   * back so nothing is silently lost. Board-derived cards (blockers/proposals)
   * are untouched — they are board state, not notifications.
   */
  dismissAll: () => void;
}

export const FeedStoreContext = createContext<FeedStoreValue | undefined>(undefined);

export function useFeedStore(): FeedStoreValue {
  const context = useContext(FeedStoreContext);
  if (context === undefined) {
    throw new Error('useFeedStore must be used within a FeedProvider');
  }
  return context;
}
