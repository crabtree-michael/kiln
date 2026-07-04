// The primary screen, presentational (08 §2–§5). Pure props in → the whole
// selector surface out, so the DOM-snapshot tests render it directly with
// fixture data and never touch the live stores. `PrimaryScreen` (the composing
// wrapper) bridges the feed + activity stores into these props, mirroring how
// `App` bridges its stores into `Board`/`ChatPanel`.
import type { JSX } from 'react';
import type { ConnectionState, FeedCard, FeedSnapshot, FeedSummary } from '@/transport/transport';
import type { ActivityToast } from '@/stores/activity-context';
import { FeedCardItem } from '@/components/FeedCardItem';
import { ActivityRow } from '@/components/ActivityRow';
import { Dock } from '@/components/Dock';
import { feedStatus, streamDetail } from '@/components/feed-format';
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
  connectionState: ConnectionState;
  thinking: boolean;
  toasts: ActivityToast[];
  onDismiss: (id: number) => void;
  onAccept: (ticketId: string) => void;
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

export function PrimaryScreenView({
  feed,
  connectionState,
  thinking,
  toasts,
  onDismiss,
  onAccept,
  now = Date.now(),
}: PrimaryScreenViewProps): JSX.Element {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  const isEmpty = cards.length === 0;
  const divider = dividerIndex(cards);

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
          <span data-role="feed-status">{feedStatus(summary)}</span>
        </header>

        <div data-role="backlog">
          {isEmpty ? (
            <div data-role="feed-empty">
              <span data-role="feed-empty-mark" aria-hidden="true" />
              <span data-role="feed-empty-title">All clear</span>
              <p data-role="feed-empty-body">
                Nothing needs you right now. I&rsquo;m keeping your streams moving and I&rsquo;ll
                speak up the moment something needs a decision.
              </p>
              <div data-role="feed-empty-status">
                <span data-role="feed-empty-pulse" aria-hidden="true" />
                <span>{streamDetail(summary, now)}</span>
              </div>
            </div>
          ) : (
            cards.map((card, index) => (
              <div key={card.id} data-role="backlog-slot">
                {index === divider && <div data-role="feed-divider">While you were away</div>}
                <FeedCardItem card={card} now={now} onAccept={onAccept} />
              </div>
            ))
          )}
        </div>
      </section>

      <ActivityRow thinking={thinking} toasts={toasts} onDismiss={onDismiss} />
      <Dock />
    </div>
  );
}
