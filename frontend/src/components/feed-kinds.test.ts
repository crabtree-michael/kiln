// Unit tests for the card-kind taxonomy (dev-velocity review D9).
//
// The point of this file is that every predicate is pinned to the EXPLICIT LIST
// of kinds it holds for, rather than to a couple of representative examples. The
// taxonomy's failure mode is not "this predicate is wrong" — it is "a kind
// quietly changed sides", which a spot-check passes straight through. Written
// this way, moving `poke` from one column of the matrix to another fails here
// with the two lists side by side.
//
// The other half of the guarantee is the compiler, and it can't be tested from
// in here: `FEED_KIND_TRAITS` is a `Record<FeedCardKind, …>`, so a seventh kind
// on the wire breaks `feed-kinds.ts` until it has been given a row, and every
// `matchKind` call site breaks until it has an arm.
import { describe, expect, it } from 'vitest';
import {
  FEED_CARD_KINDS,
  FEED_KIND_TRAITS,
  type FeedCardKind,
  carriesLandedWork,
  carriesPreviewImage,
  isAcceptable,
  isAuthoredUpdate,
  isBlockerCard,
  isBoardCard,
  isFeedCardKind,
  isNotificationCard,
  isProposalCard,
  matchKind,
  opensDetailFromBody,
  opensDetailFromHead,
  rendersBody,
  showsKindTag,
} from '@/components/feed-kinds';

/** The kinds a predicate holds for, in feed order — the shape every case below
 * asserts against. */
function kindsWhere(predicate: (card: { kind: FeedCardKind }) => boolean): FeedCardKind[] {
  return FEED_CARD_KINDS.filter((kind) => predicate({ kind }));
}

describe('the kind list', () => {
  it('is the six wire kinds, in feed order', () => {
    // Board-derived first, then the notification-backed ones — the order the
    // feed itself renders them in (08 §3).
    expect(FEED_CARD_KINDS).toEqual(['blocker', 'proposal', 'update', 'preview', 'poke', 'done']);
  });

  it('accepts every kind it lists, off the wire', () => {
    for (const kind of FEED_CARD_KINDS) {
      expect(isFeedCardKind(kind)).toBe(true);
    }
  });

  it('rejects anything else, including near misses and non-strings', () => {
    // A kind the schema gains but this module has not been told about is
    // rejected HERE, at the transport guard, rather than reaching a screen that
    // would render it as a blank card.
    for (const value of ['digest', 'Update', '', 'toString', null, undefined, 7, {}]) {
      expect(isFeedCardKind(value)).toBe(false);
    }
  });
});

describe('the taxonomy: which kinds each question holds for', () => {
  it('splits board state from notification-backed cards, exhaustively', () => {
    expect(kindsWhere(isBoardCard)).toEqual(['blocker', 'proposal']);
    expect(kindsWhere(isNotificationCard)).toEqual(['update', 'preview', 'poke', 'done']);
    // Every kind is exactly one of the two — there is no third source, and a
    // kind belonging to neither would be dropped by the store without a word.
    expect(kindsWhere(isBoardCard).length + kindsWhere(isNotificationCard).length).toBe(
      FEED_CARD_KINDS.length,
    );
  });

  it('counts only the two brain-authored kinds as authored updates', () => {
    // The divider's taxonomy, deliberately narrower than notification-backed:
    // the mechanical poke and done notices are things that HAPPENED, not things
    // Kiln said, so they take no part in what the user has caught up on
    // reading. See feed-model.ts's header and its first describe block.
    expect(kindsWhere(isAuthoredUpdate)).toEqual(['update', 'preview']);
  });

  it('keeps authored updates a strict subset of the notification-backed cards', () => {
    for (const kind of kindsWhere(isAuthoredUpdate)) {
      expect(isNotificationCard({ kind })).toBe(true);
    }
    expect(kindsWhere(isAuthoredUpdate)).not.toEqual(kindsWhere(isNotificationCard));
  });

  it('gives every kind but the two mechanical notices a body', () => {
    expect(kindsWhere(rendersBody)).toEqual(['blocker', 'proposal', 'update', 'preview']);
  });

  it('opens the ticket from the proposal body, and from the notices’ head', () => {
    // Two different gestures, because a body-less card has nowhere else to put
    // one: the proposal's clamped digest is a click-through, the poke/done head
    // row IS the button. Every other kind expands in place instead.
    expect(kindsWhere(opensDetailFromBody)).toEqual(['proposal']);
    expect(kindsWhere(opensDetailFromHead)).toEqual(['poke', 'done']);
  });

  it('never asks one kind to open the detail from two places', () => {
    for (const kind of FEED_CARD_KINDS) {
      expect(opensDetailFromBody({ kind }) && opensDetailFromHead({ kind })).toBe(false);
    }
  });

  it('offers Accept on the proposal alone', () => {
    expect(kindsWhere(isAcceptable)).toEqual(['proposal']);
  });

  it('tags and illustrates the preview alone', () => {
    expect(kindsWhere(showsKindTag)).toEqual(['preview']);
    expect(kindsWhere(carriesPreviewImage)).toEqual(['preview']);
  });

  it('carries landed work on the completion notice alone', () => {
    expect(kindsWhere(carriesLandedWork)).toEqual(['done']);
  });

  it('names the two board kinds apart, for the feed’s order', () => {
    expect(kindsWhere(isBlockerCard)).toEqual(['blocker']);
    expect(kindsWhere(isProposalCard)).toEqual(['proposal']);
  });
});

describe('internal consistency of the matrix', () => {
  it('gives a body-less kind no body click-through', () => {
    // A card with no body cannot open anything from one. This is the pairing
    // that made poke and done need the head gesture in the first place.
    for (const kind of FEED_CARD_KINDS) {
      if (!rendersBody({ kind })) {
        expect(opensDetailFromBody({ kind })).toBe(false);
      }
    }
  });

  it('gives a kind with a body no head click-through', () => {
    // The mirror: the head is the fallback surface, so a kind that has a body to
    // carry the gesture must not also claim the head — that would make the whole
    // card one tap target with two meanings.
    for (const kind of FEED_CARD_KINDS) {
      if (rendersBody({ kind })) {
        expect(opensDetailFromHead({ kind })).toBe(false);
      }
    }
  });

  it('leaves board state out of the notification-shaped columns', () => {
    for (const kind of kindsWhere(isBoardCard)) {
      // Board cards carry no notification_id, so nothing about a notification —
      // being authored, being cleared, being paged through history — can be
      // true of them.
      expect(isAuthoredUpdate({ kind })).toBe(false);
    }
  });
});

describe('matchKind', () => {
  it('picks each kind’s own arm, for every kind', () => {
    // The runtime half of the exhaustiveness guarantee. The compile-time half —
    // that a table with an arm MISSING doesn't build, which is the half that
    // actually catches a seventh kind — is tsc's, not assertable from in here.
    const arms = {
      blocker: 'b',
      proposal: 'p',
      update: 'u',
      preview: 'v',
      poke: 'k',
      done: 'd',
    };
    expect(FEED_CARD_KINDS.map((kind) => matchKind(kind, arms))).toEqual([
      'b',
      'p',
      'u',
      'v',
      'k',
      'd',
    ]);
  });

  it('reads a column out of the matrix like any other table', () => {
    expect(matchKind('done', FEED_KIND_TRAITS).landedWork).toBe(true);
    expect(matchKind('update', FEED_KIND_TRAITS).landedWork).toBe(false);
  });
});
