// What the working strip says is being worked on (13 §8.2). The rules asserted
// here are the ones that decide whether the strip can be trusted at a glance:
// it reads the board rather than the curated feed, it reports the session's real
// state instead of assuming the column, and it never reorders under the eye.
import { describe, it, expect } from 'vitest';
import { workingStatusNote, workingTickets } from '@/components/desktop/working-now';
import { makeAgentStatus, makeBoard, makeTicket } from '@/test/fixtures';

const ticket = (id: string, title: string, statusChangedAt: string) =>
  makeTicket({
    id,
    title,
    body: 'body',
    state: 'working',
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T09:00:00Z',
    statusChangedAt,
  });

describe('workingTickets', () => {
  it('names every ticket in the board Working bucket', () => {
    const board = makeBoard({
      working: [
        ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
        ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
      ],
    });
    expect(workingTickets(board).map((entry) => entry.title)).toEqual(['auth refresh', 'poller']);
  });

  it('orders oldest-started first, so a ticket starting never moves the rows above it', () => {
    const board = makeBoard({
      working: [
        ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
        ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
      ],
    });
    expect(workingTickets(board).map((entry) => entry.id)).toEqual(['t1', 't2']);
  });

  it('does not mutate the board it was handed', () => {
    const working = [
      ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
      ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
    ];
    const board = makeBoard({ working });
    workingTickets(board);
    expect(working.map((entry) => entry.id)).toEqual(['t2', 't1']);
  });

  it("reports the worker's REAL session state, not the column's implication", () => {
    // A ticket can sit in Working with a dead session behind it. Reporting that
    // as "working" is the one lie this strip must not tell.
    const board = makeBoard({
      working: [ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z')],
      agents: [makeAgentStatus('t1', 'errored')],
    });
    expect(workingTickets(board)[0]?.status).toBe('errored');
  });

  it('falls back to building for a ticket whose status join has not landed yet', () => {
    const board = makeBoard({ working: [ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] });
    expect(workingTickets(board)[0]?.status).toBe('building');
  });

  it('clocks time-in-status from state_changed_at, which a same-state nudge does not bump', () => {
    const nudged = makeTicket({
      id: 't1',
      title: 'auth refresh',
      body: 'body',
      state: 'working',
      priority: 1,
      createdAt: '2026-08-04T09:00:00Z',
      updatedAt: '2026-08-04T11:59:00Z',
      statusChangedAt: '2026-08-04T10:00:00Z',
    });
    expect(workingTickets(makeBoard({ working: [nudged] }))[0]?.since).toBe('2026-08-04T10:00:00Z');
  });

  it('is empty for a board that has not arrived, rather than claiming nothing runs', () => {
    expect(workingTickets(null)).toEqual([]);
  });

  it('ignores every other bucket — blocked and ready are not being worked', () => {
    const board = makeBoard({
      blocked: [
        makeTicket({
          id: 'b1',
          title: 'blocked one',
          body: 'body',
          state: 'blocked',
          priority: 1,
          createdAt: '2026-08-04T09:00:00Z',
          updatedAt: '2026-08-04T09:00:00Z',
        }),
      ],
      ready: [
        makeTicket({
          id: 'r1',
          title: 'ready one',
          body: 'body',
          state: 'ready',
          priority: 1,
          createdAt: '2026-08-04T09:00:00Z',
          updatedAt: '2026-08-04T09:00:00Z',
        }),
      ],
    });
    expect(workingTickets(board)).toEqual([]);
  });
});

describe('workingStatusNote', () => {
  it('says nothing for the expected case — a row reading "working · working" is noise', () => {
    expect(workingStatusNote('building')).toBe('');
  });

  it('names every state that is NOT plainly building', () => {
    expect(workingStatusNote('errored')).toBe('failing');
    expect(workingStatusNote('stopped')).toBe('stopped');
    expect(workingStatusNote('idle')).toBe('idle');
    expect(workingStatusNote('starting')).toBe('starting up');
  });
});
