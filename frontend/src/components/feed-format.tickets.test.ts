// Unit tests for the ticket-list derivation behind the header dropdown (amended
// 2026-07-06: only working, blocked, and ready tickets — done and shaping are
// excluded entirely). Active tickets (working/blocked) come first, then the
// ready backlog, each in decreasing-activity order. Active rows still join their
// worker's real session status from board.agents by ticket id, falling back to
// the column default before a status has arrived.
import { describe, expect, it } from 'vitest';
import { ticketStatuses } from '@/components/feed-format';
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
    const at = (id: string, state: ReturnType<typeof makeTicket>['state'], updatedAt: string) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state, priority: 0, updatedAt });
    const board = makeBoard({
      shaping: [at('sh', 'shaping', '2026-07-05T00:00:00Z')],
      ready: [at('rd', 'ready', '2026-07-04T00:00:00Z')],
      working: [at('wk', 'working', '2026-07-01T00:00:00Z')],
      blocked: [at('bl', 'blocked', '2026-07-02T00:00:00Z')],
      done: [at('dn', 'done', '2026-07-03T00:00:00Z')],
    });
    // working, blocked (active), then ready — done and shaping are dropped
    // entirely, regardless of raw updated_at recency (which only breaks ties).
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

  it('orders same-rank tickets by decreasing activity (most-recently-updated first)', () => {
    const ready = (id: string, updatedAt: string) =>
      makeTicket({
        ...baseFields,
        id,
        title: id,
        body: '',
        state: 'ready',
        priority: 0,
        updatedAt,
      });
    const board = makeBoard({
      ready: [
        ready('old', '2026-07-01T00:00:00Z'),
        ready('new', '2026-07-05T00:00:00Z'),
        ready('mid', '2026-07-03T00:00:00Z'),
      ],
    });
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['new', 'mid', 'old']);
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
