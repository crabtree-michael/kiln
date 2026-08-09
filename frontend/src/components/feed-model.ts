// The feed's reading model — the decisions every feed shell has to make about a
// card, in one place (shell-architecture plan, layer L2). A plain `.ts` module
// with no components, like its neighbour `feed-format.ts`, so the presentational
// `.tsx` files stay clean of react-refresh/only-export-components warnings and
// this logic is unit-testable on its own.
//
// Before this module the six functions below were hand-copied between
// `PrimaryScreenView` and `DesktopScreenView` (and a third of them into
// `KanbanScreenView`), which is how the same conceptual bug got fixed once per
// shell. Membership, order, the divider position, seen-ness and dismissability
// are now decided exactly once.
//
// ---------------------------------------------------------------------------
// THE TWO TAXONOMIES, AND WHY THEY MUST KEEP DIFFERENT NAMES
// ---------------------------------------------------------------------------
// A feed card is either BOARD-DERIVED (blocker/proposal — state the brain owns,
// taken fresh from every snapshot) or NOTIFICATION-BACKED (a row in the
// `notifications` table, accumulated across snapshots). But the shells split the
// notification-backed cards a second time, and the two splits are NOT the same:
//
//   notificationId()   update | preview | poke | done   — all four
//   authoredUpdateId() update | preview                 — brain-authored only
//
// Both used to be spelled `updateId` — in `feed-store.tsx` (all four) and in
// both feed shells (two). They are different questions:
//
//   * `notificationId` asks "is there a notification behind this card?" It drives
//     the store's accumulation and history cursor, and the swipe-to-clear
//     gesture: any stray notice can be waved off once read.
//   * `authoredUpdateId` asks "did Kiln SAY this?" It drives the last-seen
//     divider and the seen de-emphasis, which are claims about what the user has
//     caught up on *reading*. The mechanical poke and done notices are
//     deliberately excluded — they are not things Kiln said.
//
// Unifying them under one name — the obvious move when two functions look
// identical — silently slides the "Earlier" divider onto the poke and done
// cards. It type-checks, and nothing else in the gate would notice. Hence two
// names, and the first describe block of `feed-model.test.ts`, which exists
// solely to fail if the two sets are ever made to agree.
//
// The two SETS themselves now live in `feed-kinds.ts` with the rest of the
// taxonomy (`isNotificationCard` / `isAuthoredUpdate`, two columns of one
// matrix); what stays here is the pair of id readers built on them, because
// they are about the nullable `notification_id` field as much as the kind.
import type { Board, FeedCard, FeedSnapshot, FeedSummary, Ticket } from '@/transport/transport';
import { lastWordDetail } from '@/components/feed-format';
import { isAuthoredUpdate, isNotificationCard } from '@/components/feed-kinds';

/** The summary a shell renders before any feed snapshot has landed. Shared so
 * the two shells cannot drift on what "nothing yet" counts as. */
export const EMPTY_SUMMARY: FeedSummary = {
  blocker_count: 0,
  update_count: 0,
  stream_count: 0,
  building: 0,
  idle: 0,
};

/** A notification-backed card's numeric notification_id, or null. Narrows both
 * the kind and the nullable wire field in one step, so callers get a plain
 * `number | null` to branch on.
 *
 * Used for: the store's accumulation map and history cursor, the seen
 * high-water ack, and the swipe-to-clear retract id. Every notification-backed
 * card is a stray notice the user may wave off once read; only blockers and
 * proposals stay put, because they are board state awaiting a decision. */
export function notificationId(card: FeedCard): number | null {
  return isNotificationCard(card) && typeof card.notification_id === 'number'
    ? card.notification_id
    : null;
}

/** A brain-authored card's numeric notification_id, or null — the last-seen
 * divider boundary (08 D2′). Cards with a greater id are new since the last
 * visit; those at or below it are older history.
 *
 * Deliberately narrower than `notificationId`. Widening it to the four
 * notification-backed kinds would drag the "Earlier" divider and the seen
 * de-emphasis onto the poke and done cards. */
export function authoredUpdateId(card: FeedCard): number | null {
  return isAuthoredUpdate(card) && typeof card.notification_id === 'number'
    ? card.notification_id
    : null;
}

/** The index of the first authored card at/below the last-seen boundary — the
 * "last seen" divider position (08 D2′), separating new-since-last-visit
 * updates above from older history below. Shown only when there is at least one
 * newer update above the boundary AND `lastSeenId` is known. Returns -1
 * otherwise.
 *
 * The index is shared; the element each shell wraps it in is not (mobile puts it
 * inside the `backlog-slot`, desktop inside the `<li>` before the row). */
export function dividerIndex(cards: FeedCard[], lastSeenId: number | null): number {
  if (lastSeenId === null) {
    return -1;
  }
  const firstOld = cards.findIndex((card) => {
    const id = authoredUpdateId(card);
    return id !== null && id <= lastSeenId;
  });
  if (firstOld === -1) {
    return -1;
  }
  const hasNewerAbove = cards.slice(0, firstOld).some((card) => {
    const id = authoredUpdateId(card);
    return id !== null && id > lastSeenId;
  });
  return hasNewerAbove ? firstOld : -1;
}

/** Whether a card sits at/below the last-seen boundary — already-seen history
 * that renders de-emphasized (unbolded title, body collapsed tighter) so the
 * new-since-last-visit cards above stay the feed's focus (08 D2′). Board cards
 * (blocker/proposal, no `notification_id`) never recede — they still need the
 * user. Returns false when no boundary is known (fresh visit / nothing seen). */
export function isSeen(card: FeedCard, lastSeenId: number | null): boolean {
  if (lastSeenId === null) {
    return false;
  }
  const id = authoredUpdateId(card);
  return id !== null && id <= lastSeenId;
}

/** The full ticket a card points at, looked up in the board snapshot by id
 * (08 §5). Proposals are Shaping tickets, but every bucket is scanned so a
 * ticket that moves state between the click and the render still resolves.
 * Returns null before the first board snapshot lands or if the id is gone. */
export function findTicket(board: Board | null, id: string | null): Ticket | null {
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

/** One card, with every decision about it already made. A shell's card loop
 * chooses the element type and nothing else. */
export interface FeedRow {
  card: FeedCard;
  /** Already-seen history, rendered de-emphasized (08 D2′). */
  seen: boolean;
  /** The notification id to retract if the user clears this card, or null when
   * it is board state that cannot be cleared. A shell that offers no clear
   * gesture (the desk — 13 §6, and §13 Q3 is open) simply ignores this; having
   * the field does not grow the affordance. */
  dismissId: number | null;
  /** Whether the "Earlier" divider falls immediately before this row. WHERE it
   * falls is shared; the element it is wrapped in is not — mobile puts it inside
   * the `backlog-slot`, desktop inside the `<li>` above the row. */
  dividerBefore: boolean;
}

/** The whole feed, already decided. Membership, order, the divider position,
 * seen-ness and dismissability are settled here so they cannot be settled
 * differently in two shells — which is the "same conceptual bug fixed five
 * times, alternating shells" failure this layer exists to make impossible. */
export interface FeedReading {
  summary: FeedSummary;
  rows: FeedRow[];
  /** Nothing to show. A FACT, not an instruction: each shell decides what to do
   * with it. The phone always renders its all-clear block; the desk withholds
   * "All quiet." while it is still loading, because that line asserts we asked
   * and there was nothing (§4.2 of the plan). Deliberately not gated here. */
  isEmpty: boolean;
  /** The resting-state subtext, null when the brain has never spoken. */
  lastWord: string | null;
}

/** Read a feed snapshot into the shape a shell renders.
 *
 * `now` stays injectable — both shells default it to `Date.now()` and the
 * deterministic snapshot tests depend on being able to pin it.
 *
 * What this deliberately does NOT decide: anything a shell should. There is no
 * `loading` here (it is desktop-only), no resting-state copy (the two shells
 * word it differently, on purpose), and no opinion about where "Show earlier"
 * goes — that control must stay last INSIDE each shell's own scroll wrapper,
 * because its `position: sticky` hangs off that containing block. Hoisting it
 * to a shared parent is the regression this whole plan is most exposed to; the
 * browser-measured layout gate (`tests/layout/`) is what catches it. */
export function readFeed(
  feed: FeedSnapshot | null,
  lastSeenId: number | null,
  now: number,
): FeedReading {
  const summary = feed?.summary ?? EMPTY_SUMMARY;
  const cards = feed?.cards ?? [];
  const divider = dividerIndex(cards, lastSeenId);
  return {
    summary,
    rows: cards.map((card, index) => ({
      card,
      seen: isSeen(card, lastSeenId),
      dismissId: notificationId(card),
      dividerBefore: index === divider,
    })),
    isEmpty: cards.length === 0,
    lastWord: lastWordDetail(summary, now),
  };
}
