// The primary screen, presentational (08 §2–§5). Pure props in → the whole
// selector surface out, so the DOM-snapshot tests render it directly with
// fixture data and never touch the live stores. `PrimaryScreen` (the composing
// wrapper) bridges the feed + activity stores into these props, mirroring how
// `App` bridges its stores into `Board`/`ChatPanel`.
import { useState, type JSX } from 'react';
import type {
  Board,
  ConnectionState,
  FeedCard,
  FeedSnapshot,
  FeedSummary,
} from '@/transport/transport';
import type { ActivityToast } from '@/stores/activity-context';
import type { Ticket } from '@/components/TicketCard';
import { FeedCardItem } from '@/components/FeedCardItem';
import { TicketDetail } from '@/components/TicketDetail';
import { ActivityRow } from '@/components/ActivityRow';
import { Dock } from '@/components/Dock';
import { HeaderStatusMenu } from '@/components/HeaderStatusMenu';
import { streamDetail } from '@/components/feed-format';
import '@/components/PrimaryScreen.css';

const EMPTY_SUMMARY: FeedSummary = {
  blocker_count: 0,
  update_count: 0,
  stream_count: 0,
  building: 0,
  idle: 0,
};

export interface PrimaryScreenViewProps {
  feed: FeedSnapshot | null;
  /** The latest board snapshot, broken out per-stream in the header dropdown.
   * Optional so presentational tests can omit it (the menu then shows no
   * active streams). */
  board?: Board | null;
  connectionState: ConnectionState;
  thinking: boolean;
  toasts: ActivityToast[];
  onDismiss: (id: number) => void;
  onAccept: (ticketId: string) => void;
  /** Fired when the streams dropdown opens — triggers an independent board
   * refresh so the streams view isn't stale until the next agent push.
   * Optional so presentational tests can omit it. */
  onOpenStreams?: (() => void) | undefined;
  /** True while that refresh is in flight, so the dropdown can show a loading
   * indicator instead of a blank/empty state. */
  streamsRefreshing?: boolean;
  /** The last-seen divider boundary (08 D2′): update/preview cards with a greater
   * `notification_id` are new since the last visit; those at or below it are
   * older history. `null` (default) shows no divider. */
  lastSeenId?: number | null;
  /** True when older retained update history remains to page in (08 D2′) — shows
   * the "Show earlier updates" affordance at the foot of the feed. */
  hasMoreHistory?: boolean;
  /** True while a history page fetch is in flight (button shows a loading label). */
  loadingMoreHistory?: boolean;
  /** Fetch and append the next older page of update history (08 D2′). */
  onLoadMoreHistory?: (() => void) | undefined;
  /** Injected "now" for deterministic relative-age rendering (defaults to real time). */
  now?: number;
}

/** An update/preview card's numeric notification_id, or null for board cards. */
function updateId(card: FeedCard): number | null {
  const isUpdate = card.kind === 'update' || card.kind === 'preview';
  return isUpdate && typeof card.notification_id === 'number' ? card.notification_id : null;
}

/** The index of the first update card at/below the last-seen boundary — the
 * "last seen" divider position (08 D2′), separating new-since-last-visit updates
 * above from older history below. Shown only when there is at least one newer
 * update above the boundary AND `lastSeenId` is known. Returns -1 otherwise. */
function dividerIndex(cards: FeedCard[], lastSeenId: number | null): number {
  if (lastSeenId === null) {
    return -1;
  }
  const firstOld = cards.findIndex((card) => {
    const id = updateId(card);
    return id !== null && id <= lastSeenId;
  });
  if (firstOld === -1) {
    return -1;
  }
  const hasNewerAbove = cards.slice(0, firstOld).some((card) => {
    const id = updateId(card);
    return id !== null && id > lastSeenId;
  });
  return hasNewerAbove ? firstOld : -1;
}

/** The full ticket a proposal card points at, looked up in the board snapshot by
 * id (08 §5). Proposals are Shaping tickets, but every bucket is scanned so a
 * ticket that moves state between the click and the render still resolves.
 * Returns null before the first board snapshot lands or if the id is gone. */
function findTicket(board: Board | null, id: string | null): Ticket | null {
  if (board === null || id === null) {
    return null;
  }
  const all: Ticket[] = [
    ...board.shaping,
    ...board.ready,
    ...board.blocked,
    ...board.working,
    ...board.done,
  ];
  return all.find((ticket) => ticket.id === id) ?? null;
}

export function PrimaryScreenView({
  feed,
  board = null,
  connectionState,
  thinking,
  toasts,
  onDismiss,
  onAccept,
  onOpenStreams,
  streamsRefreshing = false,
  lastSeenId = null,
  hasMoreHistory = false,
  loadingMoreHistory = false,
  onLoadMoreHistory,
  now = Date.now(),
}: PrimaryScreenViewProps): JSX.Element {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  const isEmpty = cards.length === 0;
  const divider = dividerIndex(cards, lastSeenId);

  // Which proposal's full ticket is open in the click-through detail overlay
  // (08 §5). View-only state held here, mirroring how Board owns its selected
  // ticket. The id is resolved against the live board each render, so the overlay
  // drains on its own if the ticket leaves the board (e.g. after Accept).
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const openTicket = findTicket(board, openTicketId);

  return (
    <div data-role="primary-screen" data-connection-state={connectionState}>
      <section
        role="region"
        aria-label="Feed"
        data-role="feed"
        data-connection-state={connectionState}
      >
        <header data-role="feed-header">
          <div data-role="kiln-mark">
            <span data-role="kiln-glyph" aria-hidden="true" />
            <span data-role="kiln-wordmark">Kiln</span>
          </div>
          <HeaderStatusMenu
            summary={summary}
            board={board}
            onOpen={onOpenStreams}
            refreshing={streamsRefreshing}
          />
        </header>

        <div data-role="backlog">
          {isEmpty ? (
            <div data-role="feed-empty">
              <span data-role="feed-empty-mark" aria-hidden="true" />
              <span data-role="feed-empty-title">Nothing needs you right now.</span>
              <div data-role="feed-empty-status">
                <span
                  data-role="feed-empty-pulse"
                  data-active={summary.building > 0}
                  aria-hidden="true"
                />
                <span>{streamDetail(summary, now)}</span>
              </div>
            </div>
          ) : (
            <>
              {cards.map((card, index) => (
                <div key={card.id} data-role="backlog-slot">
                  {index === divider && (
                    <div data-role="feed-divider" data-variant="last-seen">
                      Earlier
                    </div>
                  )}
                  <FeedCardItem
                    card={card}
                    now={now}
                    onAccept={onAccept}
                    onOpenDetail={setOpenTicketId}
                  />
                </div>
              ))}
              {hasMoreHistory && onLoadMoreHistory !== undefined && (
                <button
                  type="button"
                  data-role="feed-load-more"
                  onClick={onLoadMoreHistory}
                  disabled={loadingMoreHistory}
                >
                  {loadingMoreHistory ? 'Loading…' : 'Show earlier updates'}
                </button>
              )}
            </>
          )}
        </div>
      </section>

      {/* The dock region is the in-flow bottom anchor; its height is exactly the
          dock's, because the activity row (toasts) and the live transcript are
          both lifted out of flow as overlays that grow UPWARD over the feed (see
          PrimaryScreen.css). That keeps a multi-line toast or a long transcript
          from shrinking the flex:1 feed and reflowing the empty state / backlog. */}
      <div data-role="dock-region">
        <ActivityRow thinking={thinking} toasts={toasts} onDismiss={onDismiss} />
        <Dock />
      </div>

      {openTicket !== null && (
        <TicketDetail
          ticket={openTicket}
          onClose={() => {
            setOpenTicketId(null);
          }}
          onAccept={(ticketId) => {
            onAccept(ticketId);
            setOpenTicketId(null);
          }}
        />
      )}
    </div>
  );
}
