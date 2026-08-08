// Unit tests for the ticket-list derivation behind the header dropdown (amended
// 2026-07-11: only working, blocked, and ready tickets — done and shaping are
// excluded entirely). Active tickets (working/blocked) come first, newest-ticket
// first; then the ready backlog, oldest-ticket first so the next item to pick up
// is at the top (both by created_at). Active rows still join their worker's real
// session status from board.agents by ticket id, falling back to the column
// default before a status has arrived.
import { describe, expect, it } from 'vitest';
import { rowSubtitle, ticketStatuses, waitingOnLabel } from '@/components/feed-format';
import { makeAgentStatus, makeBoard, makeTicket } from '@/test/fixtures';

const baseFields = { createdAt: '2026-07-01T00:00:00Z', updatedAt: '2026-07-01T00:00:00Z' };

const working = (id: string, title: string): ReturnType<typeof makeTicket> =>
  makeTicket({ ...baseFields, id, title, body: '', state: 'working', priority: 0 });

describe('ticketStatuses', () => {
  it('returns [] before the first board snapshot', () => {
    expect(ticketStatuses(null)).toEqual([]);
  });

  it('uses the real session status from board.agents, keyed by ticket id', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth'), working('t2', 'Search')],
      agents: [makeAgentStatus('t1', 'stopped'), makeAgentStatus('t2', 'errored')],
    });
    const tickets = ticketStatuses(board);
    expect(tickets.map((t) => [t.id, t.status])).toEqual([
      ['t1', 'stopped'],
      ['t2', 'errored'],
    ]);
  });

  it('falls back to the column default when no agent entry has arrived yet', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth')],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
          blockedReason: 'which gateway?',
        }),
      ],
      agents: [], // none reported yet
    });
    const tickets = ticketStatuses(board);
    expect(tickets[0]).toMatchObject({ id: 't1', status: 'building', reason: null });
    expect(tickets[1]).toMatchObject({ id: 'b1', status: 'idle', reason: 'which gateway?' });
    // The lifecycle state rides along beside the session status: the row's mark
    // takes its colour from this (the detail sheet's colour) and its texture
    // from the status, so the two readings of one ticket cannot disagree.
    expect(tickets.map((ticket) => ticket.state)).toEqual(['working', 'blocked']);
  });

  it('lists working tickets before blocked ones', () => {
    const board = makeBoard({
      working: [working('t1', 'Auth')],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Billing',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
    });
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['t1', 'b1']);
  });

  it('lists working then blocked (active) then the ready backlog, excluding done and shaping', () => {
    const at = (id: string, state: ReturnType<typeof makeTicket>['state'], createdAt: string) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state, priority: 0, createdAt });
    const board = makeBoard({
      shaping: [at('sh', 'shaping', '2026-07-05T00:00:00Z')],
      ready: [at('rd', 'ready', '2026-07-04T00:00:00Z')],
      working: [at('wk', 'working', '2026-07-01T00:00:00Z')],
      blocked: [at('bl', 'blocked', '2026-07-02T00:00:00Z')],
      done: [at('dn', 'done', '2026-07-03T00:00:00Z')],
    });
    // working, blocked (active), then ready — done and shaping are dropped
    // entirely, regardless of raw created_at recency (which only breaks ties).
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['wk', 'bl', 'rd']);
  });

  it('excludes done and shaping tickets even when nothing else is on the board', () => {
    const at = (id: string, state: ReturnType<typeof makeTicket>['state']) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state, priority: 0 });
    const board = makeBoard({
      done: [at('dn', 'done')],
      shaping: [at('sh', 'shaping')],
    });
    expect(ticketStatuses(board)).toEqual([]);
  });

  it('orders the ready backlog oldest-ticket first (by created_at)', () => {
    const ready = (id: string, createdAt: string) =>
      makeTicket({
        ...baseFields,
        id,
        title: id,
        body: '',
        state: 'ready',
        priority: 0,
        createdAt,
      });
    const board = makeBoard({
      ready: [
        ready('old', '2026-07-01T00:00:00Z'),
        ready('new', '2026-07-05T00:00:00Z'),
        ready('mid', '2026-07-03T00:00:00Z'),
      ],
    });
    // The next item to pick up sits at the top of the backlog.
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['old', 'mid', 'new']);
  });

  it('orders same-state active tickets newest-ticket first (by created_at)', () => {
    const at = (id: string, createdAt: string) =>
      makeTicket({
        ...baseFields,
        id,
        title: id,
        body: '',
        state: 'working',
        priority: 0,
        createdAt,
      });
    const board = makeBoard({
      working: [
        at('old', '2026-07-01T00:00:00Z'),
        at('new', '2026-07-05T00:00:00Z'),
        at('mid', '2026-07-03T00:00:00Z'),
      ],
    });
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['new', 'mid', 'old']);
  });

  it('sources the time-in-status subtext from state_changed_at, not updated_at', () => {
    // A nudged ticket: updated_at moved to the nudge time, but state_changed_at
    // still points at when it entered Working. The subtext must follow the
    // latter so the nudge does not reset the displayed timer.
    const board = makeBoard({
      working: [
        makeTicket({
          ...baseFields,
          id: 't1',
          title: 'Auth',
          body: '',
          state: 'working',
          priority: 0,
          updatedAt: '2026-07-06T09:00:00Z',
          statusChangedAt: '2026-07-05T12:00:00Z',
        }),
      ],
    });
    expect(ticketStatuses(board)[0]).toMatchObject({
      id: 't1',
      statusSince: '2026-07-05T12:00:00Z',
    });
  });

  it('shows the lifecycle state for a ready ticket with no live worker', () => {
    const board = makeBoard({
      ready: [
        makeTicket({ ...baseFields, id: 'rd', title: 'rd', body: '', state: 'ready', priority: 0 }),
      ],
    });
    const byId = new Map(ticketStatuses(board).map((t) => [t.id, t.status]));
    expect(byId.get('rd')).toBe('ready');
  });
});

// A queued ticket held by unlanded dependencies (0013). The row carries the
// count so the dropdown can say why a ready ticket at the top of the pull order
// is not starting — without it, a deliberately-ordered queue reads as a stuck
// one.
describe('ticketStatuses — waiting on dependencies', () => {
  const queued = (id: string, title: string, dependsOn: string[]): ReturnType<typeof makeTicket> =>
    makeTicket({ ...baseFields, id, title, body: '', state: 'ready', priority: 0, dependsOn });

  it('carries the unmet count for a ready ticket held by its dependencies', () => {
    const board = makeBoard({ ready: [queued('r1', 'Use the column', ['b1', 'b2'])] });
    expect(ticketStatuses(board)[0]?.waitingOn).toBe(2);
  });

  it('reports 0 for a ready ticket that waits on nothing', () => {
    const board = makeBoard({ ready: [queued('r1', 'Free to start', [])] });
    expect(ticketStatuses(board)[0]?.waitingOn).toBe(0);
  });

  // Only a queued ticket can be waiting: one already working is past the point
  // its dependencies could hold it, so the row must not claim otherwise.
  it('reports 0 for a working ticket even if it has dependencies', () => {
    const board = makeBoard({
      working: [
        makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'Already running',
          body: '',
          state: 'working',
          priority: 0,
          dependsOn: ['b1'],
        }),
      ],
    });
    expect(ticketStatuses(board)[0]?.waitingOn).toBe(0);
  });
});

// The wording and the shape of the subtitle line both shells render. These are
// asserted here, once, rather than in each shell's own tests: the whole point of
// hoisting them out of the two components was that the phone and the desk cannot
// state this fact two different ways.
describe('waitingOnLabel', () => {
  it('names the count and the noun, so a bare number cannot read as a second duration', () => {
    expect(waitingOnLabel(2)).toBe('Waiting on 2 tickets');
  });

  it('says "1 ticket", not "1 tickets"', () => {
    expect(waitingOnLabel(1)).toBe('Waiting on 1 ticket');
  });
});

describe('rowSubtitle', () => {
  it('leads with the note, then the dot, then the age — the news before the constant', () => {
    expect(rowSubtitle(waitingOnLabel(2), '3h')).toBe('Waiting on 2 tickets · 3h');
  });

  it('is the bare age when there is no note, leaving an ordinary row untouched', () => {
    expect(rowSubtitle('', '3h')).toBe('3h');
  });

  it('joins any note, not just a dependency one — the desk backlog passes its state word', () => {
    expect(rowSubtitle('proposal', 'now')).toBe('proposal · now');
  });
});
