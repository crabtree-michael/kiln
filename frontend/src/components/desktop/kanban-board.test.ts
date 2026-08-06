// The board-as-columns derivation. The DOM cases live next door; these are the
// decisions that have to hold whatever the markup does — the column set, their
// order, where a card's session status comes from, and the one thing this module
// must NOT do (re-order a column).
import { describe, it, expect } from 'vitest';
import { KANBAN_COLUMNS, kanbanColumns } from '@/components/desktop/kanban-board';
import { makeAgentStatus, makeBoard, makeTicket } from '@/test/fixtures';

function ticket(id: string, state: 'shaping' | 'ready' | 'working' | 'blocked' | 'done') {
  return makeTicket({
    id,
    title: `ticket ${id}`,
    body: 'the full record',
    state,
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T11:00:00Z',
  });
}

describe('kanbanColumns', () => {
  it('lays the five board states out left to right as a pipeline', () => {
    expect(KANBAN_COLUMNS.map((column) => column.key)).toEqual([
      'shaping',
      'ready',
      'working',
      'blocked',
      'done',
    ]);
    expect(KANBAN_COLUMNS.map((column) => column.label)).toEqual([
      'Shaping',
      'Ready',
      'Working',
      'Blocked',
      'Done',
    ]);
  });

  it('puts each bucket of the snapshot in its own column', () => {
    const columns = kanbanColumns(
      makeBoard({
        shaping: [ticket('s1', 'shaping')],
        ready: [ticket('r1', 'ready'), ticket('r2', 'ready')],
        working: [ticket('w1', 'working')],
        blocked: [ticket('b1', 'blocked')],
        done: [ticket('d1', 'done')],
      }),
    );
    expect(columns.map((column) => column.cards.map((card) => card.ticket.id))).toEqual([
      ['s1'],
      ['r1', 'r2'],
      ['w1'],
      ['b1'],
      ['d1'],
    ]);
  });

  it('keeps the server order inside a column — `ready` is the pull order', () => {
    // 03 §4/§5: `ready` arrives in exact pull order, top to bottom, so the user
    // can see what gets pulled next. Sorting it here by age or priority would
    // destroy the one column whose order carries information — hence a fixture
    // whose ids are deliberately NOT alphabetical or chronological.
    const columns = kanbanColumns(
      makeBoard({ ready: [ticket('r3', 'ready'), ticket('r1', 'ready'), ticket('r2', 'ready')] }),
    );
    expect(columns[1]?.cards.map((card) => card.ticket.id)).toEqual(['r3', 'r1', 'r2']);
  });

  it("takes a card's status from the agents join, not from its column", () => {
    // A ticket can sit in Working with a stopped session behind it; the card has
    // to report the session, which is the thing the user is actually asking about.
    const columns = kanbanColumns(
      makeBoard({
        working: [ticket('w1', 'working'), ticket('w2', 'working')],
        agents: [makeAgentStatus('w1', 'stopped'), makeAgentStatus('w2', 'building')],
      }),
    );
    expect(columns[2]?.cards.map((card) => card.status)).toEqual(['stopped', 'building']);
  });

  it('leaves a ticket with no worker bound to it status-less', () => {
    // Null, not a defaulted 'building': a Ready ticket has no session at all, and
    // a card that painted a session mark on it would be inventing one.
    const columns = kanbanColumns(makeBoard({ ready: [ticket('r1', 'ready')] }));
    expect(columns[1]?.cards[0]?.status).toBeNull();
  });

  it('renders five empty columns for a null board rather than nothing', () => {
    // Before the first snapshot the board's furniture is already up, so it fills
    // in rather than assembling itself around the user.
    const columns = kanbanColumns(null);
    expect(columns).toHaveLength(5);
    expect(columns.every((column) => column.cards.length === 0)).toBe(true);
  });
});
