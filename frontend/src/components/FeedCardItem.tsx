// One backlog card (08 §3 / design 4a–4c). Renders the selector surface the
// E2E asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`,
// the preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and callbacks, never
// touching the transport or stores directly.
//
// Proposal cards are a compact digest: the head + a truncated one-line summary
// wrapped in a click-through button (`feed-card-open`) that opens the full ticket
// detail (08 §5). The inline Accept stays a *sibling* of that button — never
// nested — so tapping Accept accepts without also opening the detail, and the
// full shaped body lives behind the click-through rather than dumped in the feed.
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardSummary, cardTag, relativeAge } from '@/components/feed-format';

export interface FeedCardItemProps {
  card: FeedCard;
  /** Fixed "now" so the relative age stays deterministic under test. */
  now: number;
  /** Called with the proposal's ticket id when Accept is tapped (08 §5). */
  onAccept: (ticketId: string) => void;
  /** Called with the proposal's ticket id when the card is opened for the full
   * ticket detail. Omitted → the card renders its full body inline (no
   * click-through), preserving the pre-08-§5 behaviour for non-proposal kinds. */
  onOpenDetail?: (ticketId: string) => void;
}

export function FeedCardItem({
  card,
  now,
  onAccept,
  onOpenDetail,
}: FeedCardItemProps): JSX.Element {
  const isBlocker = card.kind === 'blocker';
  const ticketId = card.ticket_id;
  const canAccept = card.kind === 'proposal' && ticketId != null;
  // Narrow on the callback directly (not a derived boolean) so TypeScript knows
  // both it and the id are defined inside the handler — no optional chain, which
  // the lint gate rejects as unnecessary (mirrors TicketCard's onSelect).
  const openDetail =
    card.kind === 'proposal' && ticketId != null && onOpenDetail !== undefined
      ? () => {
          onOpenDetail(ticketId);
        }
      : null;

  return (
    <article data-role="feed-card" data-kind={card.kind}>
      <div data-role="feed-card-head">
        {isBlocker && <span data-role="feed-card-dot" aria-hidden="true" />}
        <span data-role="feed-card-label">{card.label}</span>
        <span data-role="feed-card-tag">{cardTag(card.kind)}</span>
        <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
      </div>
      {openDetail !== null ? (
        <button
          type="button"
          data-role="feed-card-open"
          aria-label={`Open ticket: ${card.label}`}
          onClick={openDetail}
        >
          <span data-role="feed-card-body">{cardSummary(card.body)}</span>
          <span data-role="feed-card-open-hint" aria-hidden="true">
            Read full ticket
          </span>
        </button>
      ) : (
        <p data-role="feed-card-body">{card.body}</p>
      )}
      {card.kind === 'preview' && card.image_url != null && (
        <img data-role="feed-card-image" src={card.image_url} alt={card.label} />
      )}
      {canAccept && (
        <div data-role="feed-card-actions">
          <button
            type="button"
            data-role="proposal-accept"
            onClick={() => {
              onAccept(ticketId);
            }}
          >
            Accept
          </button>
        </div>
      )}
    </article>
  );
}
