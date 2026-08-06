// What the working strip says is being worked on (13 §8.2). The rules asserted
// here are the ones that decide whether the strip can be trusted at a glance:
// it reads the board rather than the curated feed, it reports the session's real
// state instead of assuming the column, and it never reorders under the eye.
import { describe, it, expect } from 'vitest';
import {
  blockedCount,
  workingPanelLabel,
  workingPanelState,
  workingStatusNote,
  workingTickets,
} from '@/components/desktop/working-now';
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

describe('workingPanelState', () => {
  it('says working while any ticket is being worked', () => {
    expect(workingPanelState(2, 0)).toBe('working');
  });

  it('says blocked when nothing is running and a ticket waits on the user', () => {
    // The distinction the fixed head could not draw: an idle project has nothing
    // to do, a blocked one has nothing it CAN do until the user decides. Reading
    // both as the same rest is how a blocker goes unnoticed on a screen the user
    // is looking at.
    expect(workingPanelState(0, 1)).toBe('blocked');
  });

  it('says idle when the board holds neither', () => {
    expect(workingPanelState(0, 0)).toBe('idle');
  });

  it('lets working outrank blocked — the panel names the work it can show', () => {
    // Both are true at once often enough (one agent building, another ticket
    // waiting). This panel's subject is the work in motion, and it lists those
    // tickets underneath; the blocker is stated in the feed, pinned, with its
    // reason. A head that said "blocked" over a list of live rows would
    // contradict the rows it heads.
    expect(workingPanelState(1, 3)).toBe('working');
  });
});

describe('workingPanelLabel', () => {
  it('gives each state its own word', () => {
    expect(workingPanelLabel('working')).toBe('working now');
    expect(workingPanelLabel('blocked')).toBe('blocked');
    expect(workingPanelLabel('idle')).toBe('idle');
  });

  it('is lower case — the head is set in small caps by CSS, not by the string', () => {
    // Upper case here would double-shout it anywhere the text is read rather
    // than rendered, the accessible name included.
    for (const state of ['working', 'blocked', 'idle'] as const) {
      const label = workingPanelLabel(state);
      expect(label).toBe(label.toLowerCase());
    }
  });
});

describe('blockedCount', () => {
  it('counts the board blocked bucket', () => {
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
    });
    expect(blockedCount(board)).toBe(1);
  });

  it('is zero before the first snapshot, rather than raising an alarm on an unknown board', () => {
    expect(blockedCount(null)).toBe(0);
  });
});
