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
  /** Injected "now" for deterministic relative-age rendering (defaults to real time). */
  now?: number;
}

function isUpdate(card: FeedCard): boolean {
  return card.kind === 'update' || card.kind === 'preview';
}

/** The index of the first update/preview card, but only when a blocker or
 * proposal precedes it — that's when the "While you were away" divider belongs
 * (08 §2 / design 4a). Returns -1 when no divider should show. */
function dividerIndex(cards: FeedCard[]): number {
  const firstUpdate = cards.findIndex(isUpdate);
  if (firstUpdate <= 0) {
    return -1;
  }
  const precededByLead = cards
    .slice(0, firstUpdate)
    .some((card) => card.kind === 'blocker' || card.kind === 'proposal');
  return precededByLead ? firstUpdate : -1;
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
  now = Date.now(),
}: PrimaryScreenViewProps): JSX.Element {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  const isEmpty = cards.length === 0;
  const divider = dividerIndex(cards);

  // Which proposal's full ticket is open in the click-through detail overlay
  // (08 §5). View-only state held here, mirroring how Board owns its selected
  // ticket. The id is resolved against the live board each render, so the
  // overlay drains on its own if the ticket leaves the board (e.g. after Accept).
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
            cards.map((card, index) => (
              <div key={card.id} data-role="backlog-slot">
                {index === divider && <div data-role="feed-divider">While you were away</div>}
                <FeedCardItem
                  card={card}
                  now={now}
                  onAccept={onAccept}
                  onOpenDetail={setOpenTicketId}
                />
              </div>
            ))
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
