// What the panel says is open (13 §8.2). The rules asserted here are the ones
// that decide whether it can be trusted at a glance: it reads the board rather
// than the curated feed, it names the tickets that need a person as well as the
// ones that need nothing, it reports the session's real state instead of
// assuming the column, and it never reorders under the eye.
import { describe, it, expect } from 'vitest';
import {
  activeStatusNote,
  activeTickets,
  workingPanelLabel,
  workingPanelState,
  workingStatusNote,
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

const blocked = (id: string, title: string, statusChangedAt: string) =>
  makeTicket({
    id,
    title,
    body: 'body',
    state: 'blocked',
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T09:00:00Z',
    statusChangedAt,
    blockedReason: 'Which region should this deploy to?',
  });

/** The panel's rows as `[id, state]`, which is what most of these cases are
 * about: which tickets are listed, and in what order. */
const rows = (board: Parameters<typeof activeTickets>[0]) =>
  activeTickets(board).map((entry) => [entry.id, entry.state]);

describe('activeTickets', () => {
  it('names every ticket in the board Working bucket', () => {
    const board = makeBoard({
      working: [
        ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
        ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
      ],
    });
    expect(activeTickets(board).map((entry) => entry.title)).toEqual(['auth refresh', 'poller']);
  });

  it('names the BLOCKED tickets too — the state that wants a person is not a count', () => {
    // The panel used to take the blocked bucket as a number: enough to colour
    // the head, never enough to say which ticket. On the one surface a user
    // keeps open all day, the only thing waiting on them was the only thing this
    // column would not point at.
    const board = makeBoard({
      blocked: [blocked('b1', 'which region?', '2026-08-04T11:00:00Z')],
    });
    expect(activeTickets(board).map((entry) => entry.title)).toEqual(['which region?']);
  });

  it('puts blocked above working — a row that wants a decision does not sit under rows that want nothing', () => {
    const board = makeBoard({
      working: [ticket('t1', 'auth refresh', '2026-08-04T10:00:00Z')],
      blocked: [blocked('b1', 'which region?', '2026-08-04T11:00:00Z')],
    });
    // Blocked leads even though it entered its state LAST — the ordering within
    // a group is by age, the ordering between the groups is by what it costs to
    // miss one.
    expect(rows(board)).toEqual([
      ['b1', 'blocked'],
      ['t1', 'working'],
    ]);
  });

  it('orders each group oldest-first, so a ticket arriving never moves the rows above it', () => {
    const board = makeBoard({
      working: [
        ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
        ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
      ],
      blocked: [
        blocked('b2', 'which region?', '2026-08-04T11:40:00Z'),
        blocked('b1', 'drop the column?', '2026-08-04T11:10:00Z'),
      ],
    });
    expect(activeTickets(board).map((entry) => entry.id)).toEqual(['b1', 'b2', 't1', 't2']);
  });

  it('does not mutate the board it was handed', () => {
    const working = [
      ticket('t2', 'poller', '2026-08-04T11:30:00Z'),
      ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z'),
    ];
    const stuck = [
      blocked('b2', 'which region?', '2026-08-04T11:40:00Z'),
      blocked('b1', 'drop the column?', '2026-08-04T11:10:00Z'),
    ];
    const board = makeBoard({ working, blocked: stuck });
    activeTickets(board);
    expect(working.map((entry) => entry.id)).toEqual(['t2', 't1']);
    expect(stuck.map((entry) => entry.id)).toEqual(['b2', 'b1']);
  });

  it("reports the worker's REAL session state, not the column's implication", () => {
    // A ticket can sit in Working with a dead session behind it. Reporting that
    // as "working" is the one lie this strip must not tell.
    const board = makeBoard({
      working: [ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z')],
      agents: [makeAgentStatus('t1', 'errored')],
    });
    expect(activeTickets(board)[0]?.status).toBe('errored');
  });

  it('falls back to building for a ticket whose status join has not landed yet', () => {
    const board = makeBoard({ working: [ticket('t1', 'auth refresh', '2026-08-04T11:00:00Z')] });
    expect(activeTickets(board)[0]?.status).toBe('building');
  });

  it('carries NO session status on a blocked row, even when a worker is still bound', () => {
    // A session state is how the work is faring, and a blocked ticket's work has
    // stopped by definition. Carrying one through would texture the mark with a
    // session nobody is watching — and a `building` reading would set the alarm
    // ink breathing, which is more than "one ticket needs a decision" is worth.
    const board = makeBoard({
      blocked: [blocked('b1', 'which region?', '2026-08-04T11:00:00Z')],
      agents: [makeAgentStatus('b1', 'building')],
    });
    expect(activeTickets(board)[0]?.status).toBeNull();
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
    expect(activeTickets(makeBoard({ working: [nudged] }))[0]?.since).toBe('2026-08-04T10:00:00Z');
  });

  it('is empty for a board that has not arrived, rather than claiming nothing runs', () => {
    expect(activeTickets(null)).toEqual([]);
  });

  it('ignores the queued and finished buckets — this section is what has STARTED', () => {
    // Ready and shaping have their own section under this one (Backlog); done
    // has none, and neither belongs in a list of tickets that are open.
    const board = makeBoard({
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
      shaping: [
        makeTicket({
          id: 's1',
          title: 'shaping one',
          body: 'body',
          state: 'shaping',
          priority: 1,
          createdAt: '2026-08-04T09:00:00Z',
          updatedAt: '2026-08-04T09:00:00Z',
        }),
      ],
      done: [
        makeTicket({
          id: 'd1',
          title: 'done one',
          body: 'body',
          state: 'done',
          priority: 1,
          createdAt: '2026-08-04T09:00:00Z',
          updatedAt: '2026-08-04T09:00:00Z',
        }),
      ],
    });
    expect(activeTickets(board)).toEqual([]);
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

describe('activeStatusNote', () => {
  it('says "needs you" on a blocked row — fire alone is not a reading', () => {
    // The mark carries the alarm ink, and ink is not a reading: it fails anyone
    // who cannot pick the hue out, and on a two-state list it fails everyone at
    // a glance. The phrase is the rail's, verbatim, so the two surfaces say the
    // same thing about the same board.
    expect(activeStatusNote({ state: 'blocked', status: null })).toBe('needs you');
  });

  it('says it even when a session is still bound — the worker is not why the row is stuck', () => {
    expect(activeStatusNote({ state: 'blocked', status: 'idle' })).toBe('needs you');
  });

  it('leaves a plainly building ticket wordless, and names every other session', () => {
    expect(activeStatusNote({ state: 'working', status: 'building' })).toBe('');
    expect(activeStatusNote({ state: 'working', status: 'errored' })).toBe('failing');
  });
});

describe('workingPanelState', () => {
  const working = {
    id: 't1',
    title: 'auth refresh',
    state: 'working',
    status: 'building',
  } as const;
  const stuck = { id: 'b1', title: 'which region?', state: 'blocked', status: null } as const;
  const since = '2026-08-04T11:00:00Z';

  it('says working while any ticket is being worked', () => {
    expect(workingPanelState([{ ...working, since }])).toBe('working');
  });

  it('says blocked when nothing is running and a ticket waits on the user', () => {
    // The distinction the fixed head could not draw: an idle project has nothing
    // to do, a blocked one has nothing it CAN do until the user decides. Reading
    // both as the same rest is how a blocker goes unnoticed on a screen the user
    // is looking at.
    expect(workingPanelState([{ ...stuck, since }])).toBe('blocked');
  });

  it('says idle when the board holds neither', () => {
    expect(workingPanelState([])).toBe('idle');
  });

  it('lets working outrank blocked — the head names the project, the row names itself', () => {
    // Both are true at once often enough (one agent building, another ticket
    // waiting). The head is the project's reading and each row states its own
    // state, in its own ink, with its own word — the same division the rows'
    // session statuses already live under. Letting one stuck ticket retitle a
    // column of live work would be the inversion of it.
    expect(
      workingPanelState([
        { ...stuck, since },
        { ...working, since },
      ]),
    ).toBe('working');
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
