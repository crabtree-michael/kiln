// One feed card (08 §3 / design 4a–4c). Renders the selector surface the E2E
// asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`, the
// preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and callbacks, never
// touching the transport or stores directly.
//
// Every kind shares one scannable layout: a left-aligned head (type · bolded
// ticket name · age) over a normal-weight body clamped to five lines. For
// update/blocker/preview cards a long body gets a quiet "tap to see more"
// affordance that expands the text in place. Proposal cards instead make the
// clamped body a click-through button (`feed-card-open`) that opens the full
// ticket detail overlay (08 §5) — the whole shaped ticket (title, full body,
// actions) is one tap away rather than dumped in the feed. The inline Accept
// stays a *sibling* of that button — never nested — so tapping Accept accepts
// without also opening the detail.
//
// Already-seen cards (below the last-seen divider, 08 D2′) render de-emphasized
// via `seen`: an unbolded ticket name and a body collapsed tighter than the
// five-line preview, so the new-since-last-visit cards above stay the focus.
// The expand affordance is unchanged — a seen card just starts more collapsed.
import { useLayoutEffect, useRef, useState } from 'react';
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

/**
 * The card body, clamped and expandable in place. Unseen cards clamp to five
 * lines; already-seen cards (`seen`) clamp tighter (a skim of the top) via the
 * `data-seen` hook, both driven from CSS. When the clamp actually bites we
 * surface a "tap to see more" control (the "read full ticket" text-link style,
 * but a quiet gray rather than red) that reveals the full body and toggles back
 * to "Show less".
 *
 * Truncation is measured (`scrollHeight` overflows the clamped `clientHeight`)
 * only while collapsed — once expanded the clamp is gone and the two heights
 * agree, so the flag is frozen rather than re-measured. Mirrors ActivityRow's
 * `ClampedText`; jsdom performs no layout, so the flag stays false under test
 * unless the heights are faked. `body` re-runs the check when the text changes.
 */
function FeedCardBody({ body, seen }: { body: string; seen: boolean }): JSX.Element {
  const ref = useRef<HTMLParagraphElement>(null);
  const [truncated, setTruncated] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useLayoutEffect(() => {
    if (expanded) return;
    const el = ref.current;
    if (el === null) return;
    // `+1` absorbs sub-pixel rounding between scroll/client height.
    const measure = (): void => {
      setTruncated(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    window.addEventListener('resize', measure);
    return () => {
      window.removeEventListener('resize', measure);
    };
  }, [body, expanded]);

  const toggle = (): void => {
    setExpanded((value) => !value);
  };

  return (
    <>
      <p
        ref={ref}
        data-role="feed-card-body"
        data-seen={seen ? 'true' : undefined}
        data-expanded={expanded ? 'true' : undefined}
      >
        {body}
      </p>
      {(truncated || expanded) && (
        <button
          type="button"
          data-role="feed-card-more"
          data-expanded={expanded ? 'true' : undefined}
          aria-expanded={expanded}
          onClick={toggle}
        >
          {expanded ? 'Show less' : 'Tap to see more'}
        </button>
      )}
    </>
  );
}

export interface FeedCardItemProps {
  card: FeedCard;
  /** Fixed "now" so the relative age stays deterministic under test. */
  now: number;
  /** Called with the proposal's ticket id when Accept is tapped (08 §5). */
  onAccept: (ticketId: string) => void;
  /** True for already-seen history below the last-seen divider (08 D2′): renders
   * the card de-emphasized — unbolded title, body collapsed tighter by default.
   * Defaults to false (the unseen/new treatment). */
  seen?: boolean;
  /** Called with the proposal's ticket id when the card body is tapped to open
   * the full ticket detail (08 §5). Omitted → the body renders inline/collapsible
   * with no click-through (non-proposal kinds, or presentational tests with no
   * board to resolve the ticket against). */
  onOpenDetail?: (ticketId: string) => void;
}

export function FeedCardItem({
  card,
  now,
  onAccept,
  seen = false,
  onOpenDetail,
}: FeedCardItemProps): JSX.Element {
  const isBlocker = card.kind === 'blocker';
  const ticketId = card.ticket_id;
  const canAccept = card.kind === 'proposal' && ticketId != null;
  // A proposal card is a digest that opens the full ticket detail on tap (08 §5).
  // Narrow on the callback and id directly (not a derived boolean) so TypeScript
  // knows both are defined inside the handler — no optional chain, which the lint
  // gate rejects as unnecessary (mirrors TicketCard's onSelect).
  const openDetail =
    card.kind === 'proposal' && ticketId != null && onOpenDetail !== undefined
      ? () => {
          onOpenDetail(ticketId);
        }
      : null;

  return (
    <article data-role="feed-card" data-kind={card.kind} data-seen={seen ? 'true' : undefined}>
      <div data-role="feed-card-head">
        {isBlocker && <span data-role="feed-card-dot" aria-hidden="true" />}
        <span data-role="feed-card-tag">{cardTag(card.kind)}</span>
        <span data-role="feed-card-label">{card.label}</span>
        <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
      </div>
      {openDetail !== null ? (
        <button
          type="button"
          data-role="feed-card-open"
          aria-label={`Open ticket: ${card.label}`}
          onClick={openDetail}
        >
          <span data-role="feed-card-body">{card.body}</span>
          <span data-role="feed-card-open-hint" aria-hidden="true">
            Read full ticket
          </span>
        </button>
      ) : (
        <FeedCardBody body={card.body} seen={seen} />
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
