// Unit tests for the ticket-list derivation behind the header dropdown (amended
// 2026-07-06: every ticket, not just the active ones). Active tickets
// (working/blocked) come first, then the rest in decreasing-activity order with
// the backlog-type states (ready/shaping) at the bottom. Active rows still join
// their worker's real session status from board.agents by ticket id, falling
// back to the column default before a status has arrived.
import { describe, expect, it } from 'vitest';
import { ticketStatuses, ticketStatusLabel, type TicketRowStatus } from '@/components/feed-format';
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

  it('includes every ticket, active first then done then backlog (ready/shaping) last', () => {
    const at = (id: string, state: ReturnType<typeof makeTicket>['state'], updatedAt: string) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state, priority: 0, updatedAt });
    const board = makeBoard({
      shaping: [at('sh', 'shaping', '2026-07-05T00:00:00Z')],
      ready: [at('rd', 'ready', '2026-07-04T00:00:00Z')],
      working: [at('wk', 'working', '2026-07-01T00:00:00Z')],
      blocked: [at('bl', 'blocked', '2026-07-02T00:00:00Z')],
      done: [at('dn', 'done', '2026-07-03T00:00:00Z')],
    });
    // working, blocked (active), then done, then ready, then shaping — regardless
    // of the raw updated_at recency, which only breaks ties within a rank.
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['wk', 'bl', 'dn', 'rd', 'sh']);
  });

  it('orders same-rank tickets by decreasing activity (most-recently-updated first)', () => {
    const done = (id: string, updatedAt: string) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state: 'done', priority: 0, updatedAt });
    const board = makeBoard({
      done: [
        done('old', '2026-07-01T00:00:00Z'),
        done('new', '2026-07-05T00:00:00Z'),
        done('mid', '2026-07-03T00:00:00Z'),
      ],
    });
    expect(ticketStatuses(board).map((t) => t.id)).toEqual(['new', 'mid', 'old']);
  });

  it('shows the lifecycle state for tickets with no live worker', () => {
    const at = (id: string, state: ReturnType<typeof makeTicket>['state']) =>
      makeTicket({ ...baseFields, id, title: id, body: '', state, priority: 0 });
    const board = makeBoard({
      done: [at('dn', 'done')],
      ready: [at('rd', 'ready')],
      shaping: [at('sh', 'shaping')],
    });
    const byId = new Map(ticketStatuses(board).map((t) => [t.id, t.status]));
    expect(byId.get('dn')).toBe('done');
    expect(byId.get('rd')).toBe('ready');
    expect(byId.get('sh')).toBe('shaping');
  });
});

describe('ticketStatusLabel', () => {
  it('renders a human label for every row status', () => {
    const labels: [TicketRowStatus, string][] = [
      ['building', 'Building'],
      ['idle', 'Idle'],
      ['stopped', 'Stopped'],
      ['errored', 'Errored'],
      ['starting', 'Starting'],
      ['ready', 'Ready'],
      ['shaping', 'Shaping'],
      ['done', 'Done'],
    ];
    for (const [status, label] of labels) {
      expect(ticketStatusLabel(status)).toBe(label);
    }
  });
});
