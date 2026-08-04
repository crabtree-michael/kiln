// Per-project snapshot cache for the data stores (12 §4.1).
//
// **Why this exists.** `CurrentProjectProvider` keys its subtree by the current
// `project_id` precisely so that switching tears the data providers down and
// re-opens the single EventSource against the new project's stream. That is the
// right mechanism — one stream, one board, one feed, all unambiguously scoped —
// but it also means every switch remounts `BoardProvider`/`FeedProvider` with
// empty state and a round-trip to wait out. Coming BACK to a project the app
// already loaded therefore looked exactly like opening it for the first time:
// a blank feed and an "All quiet." that was not yet known to be true.
//
// So each store writes its latest snapshot here, keyed by project, and seeds
// itself from it on mount. The switch paints the last thing we held for that
// project immediately; the fetch the store was going to make anyway then
// refreshes it. Nothing here is ever shown *instead* of fetching.
//
// **This is not client-owned state** (02 §11). Every value in here is a copy of
// a snapshot the server owns, held only to bridge a remount — the same job, and
// the same justification, as `use-projects-status`'s `lastKnownStates`. It is
// module scope rather than a context because the remount is the whole point: a
// provider above the keyed subtree would have to be told about each store's
// internals, and one below it dies with the switch. It is per-JS-context, so two
// tabs stay independent (12 §7.2), and it is memory-only — a reload starts cold.
import type { Board, FeedCard, FeedSnapshot } from '@/transport/transport';

/**
 * How many projects' snapshots are held at once. The rail is scoped to "a
 * handful of projects" (13 §5) and a board/feed pair is small, but the cache is
 * still bounded rather than unbounded-by-assumption: the LEAST recently written
 * project is evicted, so a user with many projects keeps the ones they actually
 * move between and simply pays the old cold-open cost for the rest.
 */
const MAX_CACHED_PROJECTS = 8;

/**
 * Everything the feed store needs to come back up mid-session (08 §3, D2′).
 *
 * The three optimistic suppressions ride along deliberately. They are the user's
 * own just-taken actions — a swiped card, an accepted proposal, a deleted
 * blocker — and they are only *pending* server confirmation, so dropping them
 * across a switch would flash a card the user has already dealt with back onto
 * the screen for one round-trip. Restoring them keeps the switch honest.
 */
export interface CachedFeed {
  /** The last server snapshot: board-derived cards plus the summary. */
  server: FeedSnapshot;
  /** The accumulated retained update/preview/poke/done cards (08 D2′). */
  updates: FeedCard[];
  /** The session's frozen last-seen divider boundary, or null for no divider. */
  lastSeen: number | null;
  /** The highest notification id already POSTed to `/feed/seen` this session. */
  acked: number;
  /**
   * The visit's seen floor (08 D2‴) — notification ids at or below it have been
   * caught up on and are collapsed out of the default feed. Cached for the same
   * reason the suppressions above are: a switch back should paint exactly what
   * was last on screen for this project. Recomputing it from the server's mark
   * instead would collapse everything the user had been reading a moment ago,
   * which is the blank screen this cache exists to prevent. The floor advances
   * on its own terms — a reload, or coming back to a backgrounded app.
   */
  seenFloor: number;
  /** Notification ids swiped away and not yet reconciled by a snapshot. */
  dismissed: number[];
  /** Optimistically hidden ticket ids → their expiry timestamp in epoch ms. */
  hiddenTickets: [string, number][];
}

const boards = new Map<string, Board>();
const feeds = new Map<string, CachedFeed>();

function read<T>(store: Map<string, T>, projectId: string | null): T | null {
  // A null id means no project is resolved yet (the session has none, or one was
  // deleted). There is nothing to key on, so there is nothing to serve.
  return projectId === null ? null : (store.get(projectId) ?? null);
}

function write<T>(store: Map<string, T>, projectId: string | null, value: T): void {
  if (projectId === null) {
    return;
  }
  // Delete-then-set so the Map's insertion order tracks recency of WRITE, which
  // is what the eviction below reads. A project being written to is a project
  // whose stream is live, so write-recency and use-recency are the same thing.
  store.delete(projectId);
  store.set(projectId, value);
  while (store.size > MAX_CACHED_PROJECTS) {
    const oldest = store.keys().next().value;
    if (oldest === undefined) {
      break;
    }
    store.delete(oldest);
  }
}

/** The last board snapshot held for `projectId`, or null if none. */
export function readCachedBoard(projectId: string | null): Board | null {
  return read(boards, projectId);
}

/** Records the latest board snapshot for `projectId`. */
export function cacheBoard(projectId: string | null, board: Board): void {
  write(boards, projectId, board);
}

/** The last feed state held for `projectId`, or null if none. */
export function readCachedFeed(projectId: string | null): CachedFeed | null {
  return read(feeds, projectId);
}

/** Records the latest feed state for `projectId`. */
export function cacheFeed(projectId: string | null, feed: CachedFeed): void {
  write(feeds, projectId, feed);
}

/**
 * Empties the cache. Test-only, for the same reason `resetProjectStatesCache`
 * exists: a vitest module registry is shared across the cases in a file, so
 * without this one case's snapshots seed the next one's first paint.
 */
export function resetProjectCache(): void {
  boards.clear();
  feeds.clear();
}
