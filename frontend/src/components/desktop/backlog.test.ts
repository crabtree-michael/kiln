// What the left panel's backlog section says is waiting (13 §8.2, extending the
// in-progress column). The rules asserted here are the ones that decide whether
// the list can be trusted at a glance: it reads the board rather than the
// curated feed, it covers both waiting states, and it keeps the server's order —
// which for Ready is the actual pull sequence and is the only information the
// order carries.
import { describe, it, expect } from 'vitest';
import { backlogStateNote, backlogTickets } from '@/components/desktop/backlog';
import { makeBoard, makeTicket } from '@/test/fixtures';
import type { Ticket } from '@/components/TicketCard';

const waiting = (id: string, title: string, state: Ticket['state'], statusChangedAt: string) =>
  makeTicket({
    id,
    title,
    body: 'body',
    state,
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T09:00:00Z',
    statusChangedAt,
  });

describe('backlogTickets', () => {
  it('names the ready queue and the shaping proposals — the whole backlog', () => {
    const board = makeBoard({
      ready: [waiting('r1', 'poller', 'ready', '2026-08-04T11:00:00Z')],
      shaping: [waiting('s1', 'auth refresh', 'shaping', '2026-08-04T11:30:00Z')],
    });
    expect(backlogTickets(board).map((entry) => entry.title)).toEqual(['poller', 'auth refresh']);
  });

  it('leads with ready, because those tickets are the ones actually next', () => {
    // A ready ticket is decided and queued behind a free worker; a shaping one
    // is still a proposal. "What happens next" is answered at the top.
    const board = makeBoard({
      shaping: [waiting('s1', 'auth refresh', 'shaping', '2026-08-04T11:30:00Z')],
      ready: [waiting('r1', 'poller', 'ready', '2026-08-04T11:00:00Z')],
    });
    expect(backlogTickets(board).map((entry) => entry.state)).toEqual(['ready', 'shaping']);
  });

  it("keeps the server's order within ready — it IS the pull order (03 §5/D9)", () => {
    // The board sends Ready sorted priority DESC, ready_at ASC, id ASC: the
    // exact sequence a freed worker will take them in. Re-sorting locally (by
    // created_at, the way the phone's dropdown approximates it) would throw away
    // the one thing this list's order actually carries.
    const board = makeBoard({
      ready: [
        waiting('r-urgent', 'urgent', 'ready', '2026-08-04T11:59:00Z'),
        waiting('r-older', 'older', 'ready', '2026-08-04T09:00:00Z'),
      ],
    });
    expect(backlogTickets(board).map((entry) => entry.id)).toEqual(['r-urgent', 'r-older']);
  });

  it('does not mutate the board it was handed', () => {
    const ready = [
      waiting('r2', 'second', 'ready', '2026-08-04T11:30:00Z'),
      waiting('r1', 'first', 'ready', '2026-08-04T11:00:00Z'),
    ];
    const board = makeBoard({ ready });
    backlogTickets(board);
    expect(ready.map((entry) => entry.id)).toEqual(['r2', 'r1']);
  });

  it('clocks the wait from state_changed_at, which a same-state nudge does not bump', () => {
    const nudged = makeTicket({
      id: 'r1',
      title: 'poller',
      body: 'body',
      state: 'ready',
      priority: 1,
      createdAt: '2026-08-04T09:00:00Z',
      updatedAt: '2026-08-04T11:59:00Z',
      statusChangedAt: '2026-08-04T10:00:00Z',
    });
    expect(backlogTickets(makeBoard({ ready: [nudged] }))[0]?.since).toBe('2026-08-04T10:00:00Z');
  });

  it('is empty for a board that has not arrived, rather than claiming an empty queue', () => {
    expect(backlogTickets(null)).toEqual([]);
  });

  it('ignores every other bucket — working, blocked and done are not waiting', () => {
    const board = makeBoard({
      working: [waiting('w1', 'in flight', 'working', '2026-08-04T11:00:00Z')],
      blocked: [waiting('b1', 'stuck', 'blocked', '2026-08-04T11:00:00Z')],
      done: [waiting('d1', 'landed', 'done', '2026-08-04T11:00:00Z')],
    });
    expect(backlogTickets(board)).toEqual([]);
  });
});

describe('backlogStateNote', () => {
  const note = (
    state: 'ready' | 'shaping',
    waitingOnDependencies = false,
    unmetDependencies = 0,
  ): string => backlogStateNote({ state, waitingOnDependencies, unmetDependencies });

  it('says nothing for the expected case — a row reading "ready · ready" is noise', () => {
    expect(note('ready')).toBe('');
  });

  it('names a shaping ticket, because it is waiting on a person rather than a worker', () => {
    expect(note('shaping')).toBe('proposal');
  });

  it('names the unlanded dependencies holding a queued ticket, which otherwise reads as a stuck queue', () => {
    expect(note('ready', true, 2)).toBe('waiting on 2');
  });

  it('lets the dependency note outrank the state word — why it is waiting beats what it is', () => {
    expect(note('shaping', true, 1)).toBe('waiting on 1');
  });
});
