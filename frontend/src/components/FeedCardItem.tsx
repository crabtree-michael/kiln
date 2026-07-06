// One feed card (08 §3 / design 4a–4c). Renders the selector surface the E2E
// asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`, the
// preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and callbacks, never
// touching the transport or stores directly.
//
// Every kind shares one scannable layout: a left-aligned head (bolded ticket
// name · age) over a normal-weight body clamped to three lines. Update and
// blocker cards drop the kind tag — the title colour carries the kind (muted
// for updates, fire for blockers, the latter also flagged by the pulse dot);
// proposal and preview keep the tag since the colour scheme doesn't cover them.
// For update/blocker/preview cards, when the body actually overflows, the whole
// clamped body stays the tap target that expands it in place (tap again to
// collapse) — but the third line now carries a small, light "tap to see more"
// cue (right-aligned, with a tiny chevron) so the truncation reads as more, not
// as text that just stops. The cue is decoration inside the body, not its own
// tap target, and only appears while the body is actually clamped.
// Proposal cards instead make
// the clamped body a click-through button (`feed-card-open`) that opens the full
// ticket detail overlay (08 §5) — the whole shaped ticket (title, full body,
// actions) is one tap away rather than dumped in the feed. The inline Accept
// stays a *sibling* of that button — never nested — so tapping Accept accepts
// without also opening the detail.
//
// Already-seen cards (below the last-seen divider, 08 D2′) render de-emphasized
// via `seen`: an unbolded ticket name and a body collapsed tighter than the
// three-line preview, so the new-since-last-visit cards above stay the focus.
// The expand affordance is unchanged — a seen card just starts more collapsed.
import { useLayoutEffect, useRef, useState } from 'react';
import type { JSX } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

/**
 * The card body, clamped and expandable in place. Unseen cards clamp to three
 * lines; already-seen cards (`seen`) clamp tighter (a skim of the top) via the
 * `data-seen` hook, both driven from CSS. When the clamp actually bites, the
 * paragraph turns into a button (cursor + `data-clickable`) that reveals the
 * full body on tap and collapses it again on the next. The clamped state also
 * renders a small "tap to see more" cue on the last line (`feed-card-more`) —
 * a right-aligned label with a tiny chevron that fades over the clipped text.
 * It's `aria-hidden` decoration with pointer-events off, so it's not a separate
 * tap target: taps land on the body button underneath. It shows only while
 * clamped-and-overflowing, and disappears once expanded. A body that fits stays
 * inert plain copy with no cue.
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

  // The clamp is the cue: only make the body a toggle once it actually overflows
  // (or is already expanded). A body that fits stays plain, non-interactive copy.
  const interactive = truncated || expanded;
  // Show the "tap to see more" cue only while the clamp is actually biting —
  // i.e. overflowing and still collapsed. Once expanded the full body is visible
  // and the cue would be a lie, so it drops.
  const showMore = truncated && !expanded;
  const toggle = (): void => {
    setExpanded((value) => !value);
  };

  return (
    <p
      ref={ref}
      data-role="feed-card-body"
      data-seen={seen ? 'true' : undefined}
      data-expanded={expanded ? 'true' : undefined}
      data-clickable={interactive ? 'true' : undefined}
      role={interactive ? 'button' : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-expanded={interactive ? expanded : undefined}
      onClick={interactive ? toggle : undefined}
      onKeyDown={
        interactive
          ? (event) => {
              // Enter/Space toggle, matching native button semantics for the
              // role we've taken on; preventDefault stops Space from scrolling.
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                toggle();
              }
            }
          : undefined
      }
    >
      {body}
      {showMore && (
        <span data-role="feed-card-more" aria-hidden="true">
          tap to see more
          <svg viewBox="0 0 24 24" width="11" height="11" aria-hidden="true">
            <path
              d="M9 6l6 6-6 6"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.4"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </span>
      )}
    </p>
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
  // A poke card is the steward's mechanical stall nudge: just the ticket title
  // with a 👉 pointing at it, no body (08 §3 poke kind). It carries no tag and
  // no body — the emoji is the whole signal.
  const isPoke = card.kind === 'poke';
  // Update and blocker cards drop the kind tag — their title colour carries the
  // kind. Proposal and preview keep it (the colour scheme doesn't cover them).
  const showTag = card.kind === 'proposal' || card.kind === 'preview';
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
        {isPoke && (
          <span data-role="feed-card-poke" aria-label="poke">
            👉
          </span>
        )}
        {showTag && <span data-role="feed-card-tag">{cardTag(card.kind)}</span>}
        <span data-role="feed-card-label">{card.label}</span>
        <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
      </div>
      {!isPoke &&
        (openDetail !== null ? (
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
        ))}
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
