// The feed's card-kind taxonomy — the ONE place that knows what the six kinds
// are and what each one means (08 §3, §7). Every other file asks a question of
// this module instead of comparing `card.kind` to a string.
//
// ---------------------------------------------------------------------------
// WHY THIS EXISTS
// ---------------------------------------------------------------------------
// The kinds were previously re-expressed as inline comparisons in eight files:
// the wire guard in `transport.ts`, the store's board-card filters, the tag
// switch in `feed-format.ts`, the two predicates in `feed-model.ts`, and seven
// separate `card.kind === '…'` reads inside `FeedCardItem`. The union permits
// every kind everywhere, so adding a seventh kind type-checked cleanly and then
// did nothing at each site that had been missed: no tag, no body, no tap
// target, dropped on the floor by the transport guard. The compiler could not
// help, because "I forgot to mention this kind" is not a type error.
//
// So the taxonomy is stated once, as a MATRIX (`FEED_KIND_TRAITS`) with one row
// per kind and one column per decision the app makes about a card. Because the
// table is a `Record<FeedCardKind, …>`, adding a kind to the wire schema breaks
// this file — and only this file — with "property 'x' is missing", listing every
// decision that now needs an answer. The named predicates below read columns out
// of it, and `matchKind` gives a call site its own exhaustive table for the
// decisions that are one view's presentation rather than a shared fact.
//
// ---------------------------------------------------------------------------
// THE ONE EDGE THAT LOOKS LIKE A CYCLE, AND ISN'T
// ---------------------------------------------------------------------------
// `transport.ts` imports `isFeedCardKind` from here (a value) while this module
// imports `FeedCard` from there (a type). The type import is erased at compile
// time, so there is no runtime cycle — and pointing the wire guard at the same
// table the UI reads is the whole point: a kind the server can send is a kind
// every screen has had to decide about.
import type { FeedCard } from '@/transport/transport';

/** The wire's card-kind union, named once so call sites don't spell out
 * `FeedCard['kind']`. The wire schema stays the source of truth for WHICH kinds
 * exist (`/schema`, regenerated — never hand-written); this module is the source
 * of truth for what they MEAN. */
export type FeedCardKind = FeedCard['kind'];

/** Everything the app decides about a card purely from its kind — one column per
 * question, so a new kind cannot answer some of them and silently skip the rest.
 *
 * What is deliberately NOT here: anything that depends on a card's *data* rather
 * than its kind (a done card with no work summary, a preview with no image, a
 * proposal with no ticket id — all still guarded at the call site), and anything
 * that is copy or markup (the 👉/✅ glyphs, the tag words). Presentation belongs
 * to the view; see `matchKind` for how a view gets exhaustiveness of its own. */
export interface FeedKindTraits {
  /** Where the card comes from, which decides how the store treats it.
   *
   * `board` — state the brain owns (blocker/proposal). Carries no
   * notification_id, is replaced wholesale by every snapshot, and cannot be
   * cleared: it stays until the underlying ticket moves.
   *
   * `notification` — a row in the `notifications` table. Accumulates across
   * snapshots, drives the history cursor, and CAN be cleared — any stray notice
   * may be waved off once read (that is what the swipe and "clear all" retract). */
  source: 'board' | 'notification';
  /** Whether Kiln SAID this — i.e. the brain authored the notice, as opposed to
   * the mechanical `poke`/`done` notices nobody wrote.
   *
   * This is the last-seen divider's taxonomy and it is deliberately NARROWER
   * than `source === 'notification'`: widening it slides the "Earlier" divider
   * and the seen de-emphasis onto the poke and done cards. `feed-model.ts`'s
   * header explains the trap at length; `feed-model.test.ts` opens with the
   * cases that fail if the two sets are ever made to agree.
   *
   * Note it is also not "did the brain write these words": a blocker's body is
   * brain-authored prose too, but a blocker is board state rather than something
   * the user catches up on reading. */
  authoredNotice: boolean;
  /** Whether the card renders its `body` field as the main body block. False for
   * the two body-less mechanical notices — a poke is a 👉 and a ticket title, a
   * done card leads with its GitHub link and carries a work summary instead. */
  body: boolean;
  /** Where a tap opens the full ticket detail overlay (08 §5), if anywhere.
   *
   * `body` — the clamped body is a click-through into the ticket (proposal: the
   * digest is a shortcut into the ticket's context).
   * `head` — the card has no body to carry the gesture, so the head row itself
   * becomes the button (poke/done, when tagged to a ticket).
   * `null` — no click-through. An update is a self-contained note that expands
   * in place even when it carries a linked ticket; a blocker likewise. */
  detail: 'body' | 'head' | null;
  /** Whether the card offers the inline Accept button — the one card action the
   * feed itself carries (08 §5). Still gated on the card having a ticket id. */
  accept: boolean;
  /** Whether the card wears its kind as a tag. Update, blocker and proposal drop
   * it because the title colour carries the kind; poke and done are carried by
   * their glyph. Only preview keeps it, since the colour scheme doesn't cover it. */
  tag: boolean;
  /** Whether the kind may carry a preview image (08 §3). */
  image: boolean;
  /** Whether the kind carries the landed-work fields — `github_url`,
   * `github_label`, `work_summary` (08 §7). Only the completion notice does. */
  landedWork: boolean;
}

/** The taxonomy itself. One row per kind; adding a kind to the wire schema fails
 * to compile here until every column has an answer.
 *
 * Ordered as the feed orders them (08 §3): the board-derived cards first, then
 * the notification-backed ones. `FEED_CARD_KINDS` is derived from this order. */
export const FEED_KIND_TRAITS: Record<FeedCardKind, FeedKindTraits> = {
  blocker: {
    source: 'board',
    authoredNotice: false,
    body: true,
    detail: null,
    accept: false,
    tag: false,
    image: false,
    landedWork: false,
  },
  proposal: {
    source: 'board',
    authoredNotice: false,
    body: true,
    detail: 'body',
    accept: true,
    tag: false,
    image: false,
    landedWork: false,
  },
  update: {
    source: 'notification',
    authoredNotice: true,
    body: true,
    detail: null,
    accept: false,
    tag: false,
    image: false,
    landedWork: false,
  },
  preview: {
    source: 'notification',
    authoredNotice: true,
    body: true,
    detail: null,
    accept: false,
    tag: true,
    image: true,
    landedWork: false,
  },
  poke: {
    source: 'notification',
    authoredNotice: false,
    body: false,
    detail: 'head',
    accept: false,
    tag: false,
    image: false,
    landedWork: false,
  },
  done: {
    source: 'notification',
    authoredNotice: false,
    body: false,
    detail: 'head',
    accept: false,
    tag: false,
    image: false,
    landedWork: true,
  },
};

/** Whether an unknown value off the wire is a card kind we know (`transport.ts`'s
 * shallow guard). Asks the matrix, so a kind the schema gains is accepted by the
 * transport the moment it has been given a row here — and, until then, is
 * rejected loudly at the one place that can see it rather than rendering as a
 * blank card. */
export function isFeedCardKind(value: unknown): value is FeedCardKind {
  return typeof value === 'string' && Object.hasOwn(FEED_KIND_TRAITS, value);
}

/** Every card kind, in feed order. DERIVED from the matrix rather than written
 * out again — a second hand-maintained list is exactly the thing this module
 * exists to remove, and it would go stale silently (a list is never wrong, only
 * short). */
export const FEED_CARD_KINDS: readonly FeedCardKind[] =
  Object.keys(FEED_KIND_TRAITS).filter(isFeedCardKind);

/** The exhaustive switch. Pick a value per kind at a call site that owns the
 * decision — copy, markup, an ordering rank — and get a compile error listing
 * the kinds you haven't answered for when a seventh is added:
 *
 * ```ts
 * const tag = matchKind(card.kind, {
 *   blocker: 'Blocker', proposal: 'Proposal', preview: 'Preview',
 *   poke: 'Poke', done: 'Done', update: 'Update',
 * });
 * ```
 *
 * Prefer this to a `switch` with a `default`, which is what `cardTag` used to be:
 * a default arm turns "a kind nobody thought about" into a plausible-looking
 * answer, which is the failure mode this module is about. Prefer a TRAIT to
 * either when the answer is a fact about the kind that more than one view needs
 * — a table nobody shares drifts the same way the string comparisons did. */
export function matchKind<T>(kind: FeedCardKind, arms: Record<FeedCardKind, T>): T {
  return arms[kind];
}

/** A card, or anything else carrying a kind. Narrow enough that the predicates
 * work on a bare `{ kind }` in a test without building a whole card. */
type Kinded = Pick<FeedCard, 'kind'>;

/** Whether the card is board state the brain owns — a blocker or a proposal,
 * carrying no notification_id and replaced wholesale by every snapshot. It is
 * NOT clearable: it stays until the underlying ticket moves. */
export function isBoardCard(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].source === 'board';
}

/** Whether the card is notification-backed — a row in the `notifications` table
 * (update/preview/poke/done). This is the taxonomy the store ACCUMULATES by, the
 * history cursor pages by, and the swipe/"clear all" gesture CLEARS by: every
 * such card is a stray notice the user may wave off once read.
 *
 * Deliberately one predicate rather than one per caller. "Is it clearable?" and
 * "does the store keep it?" are the same question about the same set, and a
 * second name for an identical set is how two answers drift apart. */
export function isNotificationCard(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].source === 'notification';
}

/** Whether the card is one Kiln AUTHORED — an `update` or a `preview`. A strict
 * subset of `isNotificationCard`: poke and done are notification-backed but
 * nobody wrote them, so they take no part in "what the user has caught up on
 * reading" (the last-seen divider and the seen de-emphasis, 08 D2′).
 *
 * Read `feed-model.ts`'s header before widening this. */
export function isAuthoredUpdate(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].authoredNotice;
}

/** Whether the card renders its `body` field as the main body block, or is one
 * of the two body-less mechanical notices (poke/done). */
export function rendersBody(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].body;
}

/** Whether a tap on the card's BODY opens the full ticket detail overlay — the
 * proposal's digest, which is a shortcut into the ticket's context (08 §5).
 * Every other body expands in place instead. Still gated on a ticket id and a
 * wired callback at the call site. */
export function opensDetailFromBody(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].detail === 'body';
}

/** Whether a tap on the card's HEAD opens the full ticket detail overlay — the
 * body-less poke and done notices, whose head row is the only surface they have
 * to carry the gesture. Still gated on a ticket id and a wired callback. */
export function opensDetailFromHead(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].detail === 'head';
}

/** Whether the card offers the inline Accept button (08 §5) — the proposal, and
 * only when it also carries a ticket id. */
export function isAcceptable(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].accept;
}

/** Whether the card wears its kind as a tag — preview alone; every other kind is
 * carried by its title colour or its glyph. */
export function showsKindTag(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].tag;
}

/** Whether the kind may carry a preview image (08 §3). Still gated on the card
 * actually having an `image_url`. */
export function carriesPreviewImage(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].image;
}

/** Whether the kind carries the landed-work fields — the GitHub link and the
 * commit message / PR description (08 §7). Still gated on the fields being set. */
export function carriesLandedWork(card: Kinded): boolean {
  return FEED_KIND_TRAITS[card.kind].landedWork;
}

/** Whether the card is a blocker. One of the two single-kind questions worth a
 * name: the feed's order is blockers, then proposals, then the notification
 * cards newest-first (08 §3), so the merge has to tell the two board kinds apart
 * rather than just recognise them as board state. */
export function isBlockerCard(card: Kinded): boolean {
  return card.kind === 'blocker';
}

/** Whether the card is a proposal — the other half of the feed's board-card
 * ordering; see `isBlockerCard`. */
export function isProposalCard(card: Kinded): boolean {
  return card.kind === 'proposal';
}
