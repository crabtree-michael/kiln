// One feed card (08 §3 / design 4a–4c). Renders the selector surface the E2E
// asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`, the
// preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and callbacks, never
// touching the transport or stores directly.
//
// Every kind shares one scannable layout: a left-aligned head (type · bolded
// ticket name · age) over a normal-weight body clamped to five lines. Long
// bodies get a quiet "tap to see more" affordance that expands the text in place
// — uniformly across update, proposal, and blocker cards, so nothing hides
// behind a click-through to another surface (08 goal: readable without leaving
// the feed). Proposals keep their inline Accept as a sibling of the body.
import { useLayoutEffect, useRef, useState } from 'react';
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

/**
 * The card body, clamped to five lines and expandable in place. When the clamp
 * actually bites we surface a "tap to see more" control (the "read full ticket"
 * text-link style, but a quiet gray rather than red) that reveals the full body
 * and toggles back to "Show less".
 *
 * Truncation is measured (`scrollHeight` overflows the clamped `clientHeight`)
 * only while collapsed — once expanded the clamp is gone and the two heights
 * agree, so the flag is frozen rather than re-measured. Mirrors ActivityRow's
 * `ClampedText`; jsdom performs no layout, so the flag stays false under test
 * unless the heights are faked. `body` re-runs the check when the text changes.
 */
function FeedCardBody({ body }: { body: string }): JSX.Element {
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
      <p ref={ref} data-role="feed-card-body" data-expanded={expanded ? 'true' : undefined}>
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
}

export function FeedCardItem({ card, now, onAccept }: FeedCardItemProps): JSX.Element {
  const isBlocker = card.kind === 'blocker';
  const ticketId = card.ticket_id;
  const canAccept = card.kind === 'proposal' && ticketId != null;

  return (
    <article data-role="feed-card" data-kind={card.kind}>
      <div data-role="feed-card-head">
        {isBlocker && <span data-role="feed-card-dot" aria-hidden="true" />}
        <span data-role="feed-card-tag">{cardTag(card.kind)}</span>
        <span data-role="feed-card-label">{card.label}</span>
        <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
      </div>
      <FeedCardBody body={card.body} />
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
