// One feed card (08 §3 / design 4a–4c). Renders the selector surface the E2E
// asserts: `feed-card` + `data-kind`, `feed-card-label`, `feed-card-body`, the
// preview `feed-card-image`, and — for proposals — the real Accept button
// (`proposal-accept`). Presentational only: it takes a card and callbacks, never
// touching the transport or stores directly.
//
// Every kind shares one scannable layout: a left-aligned head (bolded ticket
// name · age) over a normal-weight body clamped to three lines. Update, blocker
// and proposal cards drop the kind tag — the title colour carries the kind
// (muted for updates, fire for blockers — the latter also flagged by the pulse
// dot — and fire for proposals too); only preview keeps the tag since the colour
// scheme doesn't cover it.
// Every kind clamps its body to three lines, and when the body actually
// overflows the last line carries the same small, light "tap to see more" cue
// (right-aligned, with a tiny chevron) so the truncation reads as more, not as
// text that just stops. The cue is decoration inside the body, not its own tap
// target, and only appears while the body is actually clamped.
// The *action* behind the tap differs by kind: update/blocker/preview cards
// make the whole clamped body the tap target that expands it in place (tap
// again to collapse), or — when the body doesn't overflow — leave it inert so
// the tap is a no-op. Update cards are always this expand-in-place kind, even
// when they carry a linked ticket: a brain update is a self-contained note, not
// a shortcut into a ticket, so it never opens the detail overlay. Proposal cards
// instead make the clamped body a click-through button (`feed-card-open`) that
// opens the full ticket detail overlay (08 §5) — the whole ticket (title, full
// body, actions) is one tap away rather than dumped in the feed. Either way the
// cue is the same; only where the tap lands changes. The inline Accept stays a
// *sibling* of that button — never nested — so tapping Accept accepts without
// also opening the detail.
// Done and poke cards have no body to carry that click-through (they are just a
// ✅/👉 + ticket title, 08 §7/§3), so when they are tagged to a ticket the *head*
// row itself becomes the button that opens the same ticket detail overlay — the
// only surface a body-less card has.
//
// Already-seen cards (below the last-seen divider, 08 D2′) render de-emphasized
// via `seen`: an unbolded ticket name and a body collapsed tighter than the
// three-line preview, so the new-since-last-visit cards above stay the focus.
// The expand affordance is unchanged — a seen card just starts more collapsed.
import { useLayoutEffect, useRef, useState } from 'react';
import type { JSX, RefObject } from 'react';
import type { FeedCard } from '@/transport/transport';
import { cardTag, relativeAge } from '@/components/feed-format';

/**
 * Measures whether the clamped body actually overflows its clamp — the single
 * signal both card-body variants share to decide whether to show the "tap to see
 * more" cue. Returns a ref to attach to the clamped element and the `truncated`
 * flag (`scrollHeight` overflows the clamped `clientHeight`, `+1` absorbing
 * sub-pixel rounding). Measured only while `active` (the clamp is applied): the
 * expand-in-place body passes `active = !expanded` so the flag freezes once the
 * clamp is gone; the open-detail body always clamps, so it passes `true`.
 * Mirrors ActivityRow's `ClampedText`; jsdom performs no layout, so the flag
 * stays false under test unless the heights are faked. Re-runs when `body`
 * changes (the text) or `active` flips.
 */
function useClampOverflow<T extends HTMLElement>(
  body: string,
  active: boolean,
): { ref: RefObject<T>; truncated: boolean } {
  const ref = useRef<T>(null);
  const [truncated, setTruncated] = useState(false);

  useLayoutEffect(() => {
    if (!active) return;
    const el = ref.current;
    if (el === null) return;
    const measure = (): void => {
      setTruncated(el.scrollHeight > el.clientHeight + 1);
    };
    measure();
    window.addEventListener('resize', measure);
    return () => {
      window.removeEventListener('resize', measure);
    };
  }, [body, active]);

  return { ref, truncated };
}

/**
 * The small, light "tap to see more" cue rendered on the clamped body's last
 * line (`feed-card-more`) — a right-aligned label with a tiny chevron that fades
 * over the clipped text. It's `aria-hidden` decoration with pointer-events off,
 * so it's never a separate tap target: taps fall through to the body/button
 * underneath. Shared by both card-body variants so the truncation reads
 * identically whether the tap expands in place or opens the detail overlay.
 */
function SeeMoreCue(): JSX.Element {
  return (
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
  );
}

/**
 * The GitHub mark (invertocat), a single path so it inherits currentColor and
 * follows the accent the SHA link is drawn in. Rendered inside the done card's
 * GitHub link so the commit SHA (or "#<number>") reads unmistakably as a link
 * out to GitHub, matching the mark used on the landing page's connect step.
 */
function GitHubMark(): JSX.Element {
  return (
    <svg
      data-role="feed-card-github-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      aria-hidden="true"
      focusable="false"
    >
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.65 7.65 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"
      />
    </svg>
  );
}

/**
 * The card body for kinds that expand in place (update/blocker/preview). Unseen
 * cards clamp to three lines; already-seen cards (`seen`) clamp tighter (a skim
 * of the top) via the `data-seen` hook, both driven from CSS. When the clamp
 * actually bites, the paragraph turns into a button (cursor + `data-clickable`)
 * that reveals the full body on tap and collapses it again on the next, with the
 * shared "tap to see more" cue on the last line while clamped. A body that fits
 * stays inert plain copy with no cue.
 */
function FeedCardBody({ body, seen }: { body: string; seen: boolean }): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  const { ref, truncated } = useClampOverflow<HTMLParagraphElement>(body, !expanded);

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
      {showMore && <SeeMoreCue />}
    </p>
  );
}

/**
 * The click-through card body for the proposal kind — the one body kind that
 * opens the full ticket detail overlay (08 §5) instead of expanding in place. A
 * button (`feed-card-open`) whose body stays permanently clamped to three lines
 * (two when `seen`) — the full record lives in the overlay, not the feed — so it
 * wears the same "tap to see more" cue as every other kind whenever it overflows
 * (measured here, `active` always true since it never expands). For a proposal
 * the Accept button is a sibling of this one, never nested (see FeedCardItem), so
 * accepting doesn't also open the detail.
 */
function OpenDetailCardBody({
  body,
  label,
  seen,
  onOpen,
}: {
  body: string;
  label: string;
  seen: boolean;
  onOpen: () => void;
}): JSX.Element {
  const { ref, truncated } = useClampOverflow<HTMLSpanElement>(body, true);
  return (
    <button
      type="button"
      data-role="feed-card-open"
      aria-label={`Open ticket: ${label}`}
      onClick={onOpen}
    >
      <span ref={ref} data-role="feed-card-body" data-seen={seen ? 'true' : undefined}>
        {body}
        {truncated && <SeeMoreCue />}
      </span>
    </button>
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
  /** Called with the card's linked ticket id when it is tapped to open the full
   * ticket detail (08 §5): from the body for proposals and ticket-linked activity
   * updates, or from the head for body-less done/poke cards tagged to a ticket.
   * Omitted → no click-through (updates with no linked ticket, other kinds, or
   * presentational tests with no board to resolve the ticket against). */
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
  // with a 👉 pointing at it, no body (08 §3 poke kind). The emoji is the whole
  // signal.
  const isPoke = card.kind === 'poke';
  // A done card is the mechanical completion notice (08 §7): just the ticket
  // title with a ✅ in front of it, no body. Styled like a poke — the emoji is
  // the whole signal.
  const isDone = card.kind === 'done';
  // Update, blocker and proposal cards drop the kind tag — their title colour
  // carries the kind (muted, fire and fire respectively). Only preview keeps it,
  // since the colour scheme doesn't cover it. Poke and done carry no tag either.
  const showTag = card.kind === 'preview';
  const ticketId = card.ticket_id;
  const canAccept = card.kind === 'proposal' && ticketId != null;
  // Only a proposal card is a digest that opens the full ticket detail on tap
  // (08 §5): its clamped body is a shortcut into the ticket's context. Update
  // cards — brain-authored notes — always fall through to the expand-in-place
  // body below, whether or not they carry a linked ticket: a brain update
  // expands its own description in place (or is an inert no-op when it doesn't
  // overflow), it never opens the ticket. Narrow on the callback and id directly
  // (not a derived boolean) so TypeScript knows both are defined inside the
  // handler — no optional chain, which the lint gate rejects as unnecessary
  // (mirrors TicketCard's onSelect).
  const openDetail =
    card.kind === 'proposal' && ticketId != null && onOpenDetail !== undefined
      ? () => {
          onOpenDetail(ticketId);
        }
      : null;
  // Poke cards, and done cards without a work summary, are body-less notices —
  // just the ✅/👉 + ticket title (08 §7/§3). A done card's optional work-summary
  // body is an expand-in-place note, never a click-through, so it too leaves the
  // head as the ticket tap target. When one is tagged to a ticket, its *head*
  // becomes the link into the same ticket detail overlay a proposal/update body
  // opens (08 §5), so a completion or a stall nudge is a shortcut into its ticket
  // rather than a dead-end note. Narrow on the id + callback directly, same as
  // openDetail above.
  const openHeadDetail =
    (isDone || isPoke) && ticketId != null && onOpenDetail !== undefined
      ? () => {
          onOpenDetail(ticketId);
        }
      : null;
  // The head row's children are shared by both renderings — the plain div and the
  // button that opens the ticket for a tagged done/poke card — so they live here
  // once and slot into whichever wrapper the head takes below.
  const head = (
    <>
      {isBlocker && <span data-role="feed-card-dot" aria-hidden="true" />}
      {isPoke && (
        <span data-role="feed-card-poke" aria-label="poke">
          👉
        </span>
      )}
      {isDone && (
        <span data-role="feed-card-done" aria-label="done">
          ✅
        </span>
      )}
      {showTag && <span data-role="feed-card-tag">{cardTag(card.kind)}</span>}
      <span data-role="feed-card-label">{card.label}</span>
      <span data-role="feed-card-age">{relativeAge(card.created_at, now)}</span>
    </>
  );

  return (
    <article data-role="feed-card" data-kind={card.kind} data-seen={seen ? 'true' : undefined}>
      {openHeadDetail !== null ? (
        <button
          type="button"
          data-role="feed-card-head"
          data-clickable="true"
          aria-label={`Open ticket: ${card.label}`}
          onClick={openHeadDetail}
        >
          {head}
        </button>
      ) : (
        <div data-role="feed-card-head">{head}</div>
      )}
      {isDone && card.work_summary != null && card.work_summary !== '' && (
        <FeedCardBody body={card.work_summary} seen={seen} />
      )}
      {isDone && card.github_url != null && card.github_label != null && (
        <a
          data-role="feed-card-github"
          href={card.github_url}
          target="_blank"
          rel="noreferrer noopener"
        >
          <GitHubMark />
          {card.github_label}
        </a>
      )}
      {!isPoke &&
        !isDone &&
        (openDetail !== null ? (
          <OpenDetailCardBody body={card.body} label={card.label} seen={seen} onOpen={openDetail} />
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
