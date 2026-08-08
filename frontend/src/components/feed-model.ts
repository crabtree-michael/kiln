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
import type { Board, FeedCard, FeedSummary, Ticket } from '@/transport/transport';

/** The summary a shell renders before any feed snapshot has landed. Shared so
 * the two shells cannot drift on what "nothing yet" counts as. */
export const EMPTY_SUMMARY: FeedSummary = {
  blocker_count: 0,
  update_count: 0,
  stream_count: 0,
  building: 0,
  idle: 0,
};

/** Whether a card is notification-backed — one of the four kinds that is a row
 * in the `notifications` table (08 §3, §7): the brain-authored update/preview,
 * the steward's mechanical `poke` stall nudge, and the runtime's mechanical
 * `done` completion notice. The other two kinds (blocker/proposal) are
 * board-derived, carry no notification_id, and are replaced wholesale by every
 * snapshot.
 *
 * This is the taxonomy the STORE accumulates by and the swipe gesture clears by.
 * It is NOT the divider's taxonomy — see the header, and `isAuthoredUpdate`. */
export function isNotificationCard(card: FeedCard): boolean {
  return (
    card.kind === 'update' ||
    card.kind === 'preview' ||
    card.kind === 'poke' ||
    card.kind === 'done'
  );
}

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

/** Whether a card is one Kiln AUTHORED — an `update` or a `preview`, written by
 * the brain. A strict subset of `isNotificationCard`: the mechanical `poke` and
 * `done` notices are notification-backed but nobody wrote them, so they are not
 * part of "what the user has caught up on reading". */
export function isAuthoredUpdate(card: FeedCard): boolean {
  return card.kind === 'update' || card.kind === 'preview';
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

/** Whether the feed holds anything the "clear all" trash would actually clear —
 * i.e. any notification-backed card. Blockers and proposals are board state the
 * brain owns and are untouched by a clear, so a feed of nothing but those leaves
 * the affordance disabled rather than silently doing nothing.
 *
 * Asks the taxonomy rather than spelling out "not a blocker and not a proposal",
 * which was a third hand-written copy of the same question. */
export function hasClearableCards(cards: FeedCard[]): boolean {
  return cards.some(isNotificationCard);
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
