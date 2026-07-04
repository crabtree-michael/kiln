// One backlog card (08 §3 / design 4a–4c). Renders the selector surface the
// E2E asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`,
// the preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and an `onAccept`
// callback, never touching the transport or stores directly.
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

export interface FeedCardItemProps {
  card: FeedCard;
  /** Fixed "now" so the relative age stays deterministic under test. */
  now: number;
  /** Called with the proposal's ticket id when Accept is tapped (08 §5). */
  onAccept: (ticketId: string) => void;
}

export function FeedCardItem({ card, now, onAccept }: FeedCardItemProps): JSX.Element {
  const isBlocker = card.kind === 'blocker';
  const canAccept = card.kind === 'proposal' && card.ticket_id != null;
  const acceptId = card.ticket_id;

  return (
    <article data-role="feed-card" data-kind={card.kind}>
      <div data-role="feed-card-head">
        {isBlocker && <span data-role="feed-card-dot" aria-hidden="true" />}
        <span data-role="feed-card-label">{card.label}</span>
        <span data-role="feed-card-tag">{cardTag(card.kind)}</span>
        <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
      </div>
      <p data-role="feed-card-body">{card.body}</p>
      {card.kind === 'preview' && card.image_url != null && (
        <img data-role="feed-card-image" src={card.image_url} alt={card.label} />
      )}
      {canAccept && acceptId != null && (
        <div data-role="feed-card-actions">
          <button
            type="button"
            data-role="proposal-accept"
            onClick={() => {
              onAccept(acceptId);
            }}
          >
            Accept
          </button>
        </div>
      )}
    </article>
  );
}
