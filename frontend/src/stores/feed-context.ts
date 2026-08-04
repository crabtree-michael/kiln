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
   * True while a full-snapshot fetch is in flight: the mount/project-switch
   * load, the reconnect refetch, and pull-to-refresh. It stays true through the
   * refresh that runs behind a cache-seeded feed (12 §4.1), so a consumer can
   * distinguish "still catching up with this project" from "this project really
   * has nothing to show" — two states that otherwise render identically.
   */
  loading: boolean;
  /**
   * The last-seen divider boundary (08 D2′): update/preview cards with a greater
   * `notification_id` are new since the last visit (above the divider); those at
   * or below it are older history (below it). Frozen at the first snapshot of the
   * session so marking-seen-on-view doesn't move the divider mid-session. `null`
   * when nothing has ever been seen (no divider).
   */
  lastSeenId: number | null;
  /**
   * True when there is anything further back to show (08 D2‴): cards that have
   * collapsed out of the default feed for having been seen already, or older
   * retained history still on the server. It is the whole gate on the single
   * "Show earlier" control — false means that control has nothing to do, so it
   * isn't offered.
   */
  hasEarlier: boolean;
  /** True while `showEarlier()` has a history page fetch in flight. */
  loadingEarlier: boolean;
  /**
   * Re-fetch the current feed snapshot on demand — the pull-to-refresh gesture
   * (this change). Mirrors the reconnect refetch: applies the fresh snapshot on
   * success and leaves the existing (stale-but-visible) feed in place on failure.
   * Returns a promise that resolves once the fetch has settled, so the caller can
   * hold its loading indicator up for the whole round-trip.
   */
  refreshFeed: () => Promise<void>;
  /**
   * The one "Show earlier" action (08 D2‴), and the only way back to what the
   * feed has tucked away. It means *further back* and does whatever that takes
   * next: first it reveals the collapsed already-seen cards (purely local — they
   * are still held here and still retained server-side, so nothing is fetched,
   * retracted, or persisted), then, once nothing is collapsed, it pages the next
   * older window of history. There is no counterpart that puts them back: the
   * reveal is view state and resets on reopen, so the feed still always opens
   * decluttered.
   */
  showEarlier: () => void;
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
