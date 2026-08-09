// Unit tests for the shared feed reading model (shell-architecture plan, T1 for
// the predicates, T2 for `readFeed`).
//
// The first describe block is the point of this file. `notificationId` and
// `authoredUpdateId` are near-identical functions over the same argument, and
// they were both called `updateId` before this module existed — one in
// `feed-store.tsx` (all four notification-backed kinds) and one in each feed
// shell (the two brain-authored kinds). Merging them by name is the obvious
// move when you see them side by side, it type-checks, and it silently slides
// the "Earlier" divider and the seen de-emphasis onto the mechanical poke and
// done cards. Nothing else in the gate would catch it, because until these
// functions shared a module there was no place to write this assertion.
//
// So: these tests exist to FAIL if the two sets are ever made to agree.
import { describe, expect, it } from 'vitest';
import {
  authoredUpdateId,
  dividerIndex,
  findTicket,
  isSeen,
  notificationId,
  readFeed,
} from '@/components/feed-model';
// The two SETS live in the taxonomy module now (`feed-kinds.ts`); the two ID
// readers below still live in the reading model. The cases in this file are
// about the pair AGREEING or DISAGREEING, so they exercise both together and
// stay here rather than moving with the predicates.
import { isAuthoredUpdate, isNotificationCard } from '@/components/feed-kinds';
import {
  makeBoard,
  makeFeedCard,
  makeFeedSnapshot,
  makeFeedSummary,
  makeTicket,
} from '@/test/fixtures';
import type { FeedCard } from '@/transport/transport';

const ALL_KINDS: FeedCard['kind'][] = ['blocker', 'proposal', 'update', 'preview', 'poke', 'done'];

/** A card of the given kind, carrying a notification id unless told otherwise —
 * so a `null` in these tests always means "the taxonomy rejected the kind",
 * never "the fixture forgot the id". */
function card(kind: FeedCard['kind'], notification: number | null = 1): FeedCard {
  const base = {
    kind,
    id: `c-${kind}-${String(notification)}`,
    label: kind,
    body: 'body',
    createdAt: '2026-08-08T00:00:00Z',
  };
  return makeFeedCard(notification === null ? base : { ...base, notificationId: notification });
}

describe('the two card taxonomies disagree, on purpose', () => {
  it('counts all four notification-backed kinds as notification-backed', () => {
    const backed = ALL_KINDS.filter((kind) => isNotificationCard(card(kind)));
    expect(backed).toEqual(['update', 'preview', 'poke', 'done']);
  });

  it('counts only the two brain-authored kinds as authored updates', () => {
    const authored = ALL_KINDS.filter((kind) => isAuthoredUpdate(card(kind)));
    expect(authored).toEqual(['update', 'preview']);
  });

  it('gives poke and done a notification id but NOT an authored-update id', () => {
    // The whole disagreement, in one assertion. `poke` (the steward's stall
    // nudge) and `done` (the runtime's completion notice) are real rows in the
    // notifications table — so they accumulate in the store and they can be
    // swiped away — but Kiln did not SAY them, so they take no part in the
    // last-seen divider or the seen de-emphasis.
    for (const kind of ['poke', 'done'] as const) {
      expect(notificationId(card(kind, 7))).toBe(7);
      expect(authoredUpdateId(card(kind, 7))).toBeNull();
    }
  });

  it('agrees on update/preview, and on the board-derived cards', () => {
    for (const kind of ['update', 'preview'] as const) {
      expect(notificationId(card(kind, 7))).toBe(7);
      expect(authoredUpdateId(card(kind, 7))).toBe(7);
    }
    for (const kind of ['blocker', 'proposal'] as const) {
      expect(notificationId(card(kind, 7))).toBeNull();
      expect(authoredUpdateId(card(kind, 7))).toBeNull();
    }
  });

  it('authored updates are a strict subset of notification-backed cards', () => {
    // Stated as a property rather than a list, so a NEW card kind added to the
    // wire schema cannot land in the authored set without also being
    // notification-backed — which would mean a card that drives the divider but
    // that the store never accumulates, so it would vanish from the feed.
    for (const kind of ALL_KINDS) {
      if (isAuthoredUpdate(card(kind))) {
        expect(isNotificationCard(card(kind))).toBe(true);
      }
    }
    expect(ALL_KINDS.filter((k) => isNotificationCard(card(k)))).not.toEqual(
      ALL_KINDS.filter((k) => isAuthoredUpdate(card(k))),
    );
  });

  it('returns null when a notification-backed kind carries no id', () => {
    expect(notificationId(card('update', null))).toBeNull();
    expect(authoredUpdateId(card('update', null))).toBeNull();
  });
});

describe('dividerIndex', () => {
  it('is -1 when no boundary is known', () => {
    expect(dividerIndex([card('update', 3), card('update', 1)], null)).toBe(-1);
  });

  it('is -1 when every card is newer than the boundary', () => {
    expect(dividerIndex([card('update', 5), card('update', 4)], 3)).toBe(-1);
  });

  it('is -1 when every card is older than the boundary (nothing new above it)', () => {
    // A divider with nothing above it is a line at the top of the feed labelling
    // the whole feed "Earlier", which says nothing.
    expect(dividerIndex([card('update', 2), card('update', 1)], 3)).toBe(-1);
  });

  it('marks the first card at/below the boundary', () => {
    const cards = [card('update', 5), card('preview', 4), card('update', 3), card('update', 2)];
    expect(dividerIndex(cards, 3)).toBe(2);
  });

  it('skips poke and done cards when locating the boundary', () => {
    // The regression this module's naming exists to prevent: a `done` card older
    // than the boundary sitting above the first authored one must NOT pull the
    // divider up onto it.
    const cards = [card('update', 9), card('done', 2), card('update', 1)];
    expect(dividerIndex(cards, 3)).toBe(2);
  });

  it('ignores board cards, which carry no notification id at all', () => {
    const cards = [card('blocker', null), card('update', 9), card('update', 1)];
    expect(dividerIndex(cards, 3)).toBe(2);
  });
});

describe('isSeen', () => {
  it('is false with no boundary, at any age', () => {
    expect(isSeen(card('update', 1), null)).toBe(false);
  });

  it('is true at or below the boundary and false above it', () => {
    expect(isSeen(card('update', 3), 3)).toBe(true);
    expect(isSeen(card('update', 2), 3)).toBe(true);
    expect(isSeen(card('update', 4), 3)).toBe(false);
  });

  it('never de-emphasizes a card the user still has to act on', () => {
    // Blockers and proposals are board state awaiting a decision; poke and done
    // are not things Kiln said, so "already read" is not a claim we make of them.
    for (const kind of ['blocker', 'proposal', 'poke', 'done'] as const) {
      expect(isSeen(card(kind, 1), 3)).toBe(false);
    }
  });
});

describe('findTicket', () => {
  const ticket = (id: string, state: 'shaping' | 'ready' | 'blocked' | 'working' | 'done') =>
    makeTicket({
      id,
      title: id,
      body: '',
      state,
      priority: 0,
      createdAt: '2026-08-08T00:00:00Z',
      updatedAt: '2026-08-08T00:00:00Z',
    });

  it('is null before the first board snapshot, and for a null id', () => {
    expect(findTicket(null, 't1')).toBeNull();
    expect(findTicket(makeBoard(), null)).toBeNull();
  });

  it('scans every bucket, so a ticket that moved state still resolves', () => {
    const board = makeBoard({
      shaping: [ticket('t1', 'shaping')],
      ready: [ticket('t2', 'ready')],
      blocked: [ticket('t3', 'blocked')],
      working: [ticket('t4', 'working')],
      done: [ticket('t5', 'done')],
    });
    for (const id of ['t1', 't2', 't3', 't4', 't5']) {
      expect(findTicket(board, id)?.id).toBe(id);
    }
  });

  it('is null for an id that has left the board', () => {
    expect(findTicket(makeBoard({ shaping: [ticket('t1', 'shaping')] }), 'gone')).toBeNull();
  });
});

describe('readFeed', () => {
  const NOW = Date.parse('2026-08-08T12:00:00Z');

  it('reads a null feed as empty, with the shared empty summary', () => {
    // What a shell shows before the first snapshot lands. `isEmpty` being a fact
    // here is what lets the desk withhold "All quiet." while it is still loading
    // without the phone's all-clear block changing at all.
    const reading = readFeed(null, null, NOW);
    expect(reading.isEmpty).toBe(true);
    expect(reading.rows).toEqual([]);
    expect(reading.summary).toEqual(makeFeedSummary());
    expect(reading.lastWord).toBeNull();
  });

  it('keeps the snapshot’s card order untouched', () => {
    // Order is the server's (blockers, proposals, then updates newest-first —
    // the feed store's merge). This layer decides ABOUT each card; it never
    // re-sorts them.
    const cards = [card('blocker', null), card('proposal', null), card('update', 9)];
    const reading = readFeed(makeFeedSnapshot({ cards }), null, NOW);
    expect(reading.rows.map((row) => row.card.id)).toEqual(cards.map((c) => c.id));
  });

  it('decides seen-ness, dismissability and the divider for every row at once', () => {
    const cards = [
      card('update', 9), // new since the visit
      card('done', 8), // notification-backed, but not something Kiln said
      card('update', 2), // older history — the divider falls here
      card('blocker', null), // board state: never seen, never dismissable
    ];
    const reading = readFeed(makeFeedSnapshot({ cards }), 3, NOW);
    expect(
      reading.rows.map((row) => ({
        seen: row.seen,
        dismissId: row.dismissId,
        dividerBefore: row.dividerBefore,
      })),
    ).toEqual([
      { seen: false, dismissId: 9, dividerBefore: false },
      { seen: false, dismissId: 8, dividerBefore: false },
      { seen: true, dismissId: 2, dividerBefore: true },
      { seen: false, dismissId: null, dividerBefore: false },
    ]);
  });

  it('marks at most one row with the divider, and none when there is none to draw', () => {
    const cards = [card('update', 9), card('update', 2), card('update', 1)];
    expect(
      readFeed(makeFeedSnapshot({ cards }), 3, NOW).rows.filter((r) => r.dividerBefore),
    ).toHaveLength(1);
    expect(readFeed(makeFeedSnapshot({ cards }), null, NOW).rows.some((r) => r.dividerBefore)).toBe(
      false,
    );
  });

  it('takes `now` as an argument, so the resting subtext is deterministic', () => {
    // Both shells default `now` to Date.now(); the snapshot tests pin it, and
    // that only works while this stays injected rather than read here.
    const feed = makeFeedSnapshot({ summary: { last_word_at: '2026-08-08T11:54:00Z' } });
    expect(readFeed(feed, null, NOW).lastWord).toBe('last word 6m ago');
    expect(readFeed(feed, null, NOW + 60 * 60 * 1000).lastWord).toBe('last word 1h ago');
  });

  it('reports a feed of only board state as non-empty, with nothing dismissable', () => {
    const feed = makeFeedSnapshot({ cards: [card('blocker', null), card('proposal', null)] });
    const reading = readFeed(feed, null, NOW);
    expect(reading.isEmpty).toBe(false);
    expect(reading.rows.every((row) => row.dismissId === null)).toBe(true);
  });
});
