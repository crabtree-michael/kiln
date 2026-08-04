// The rail's state vocabulary (13 §5). These are the rules that decide what a
// project looks like out of the corner of your eye, so they are asserted
// directly rather than through the rail's DOM.
import { describe, it, expect } from 'vitest';
import { deriveProjectState } from '@/stores/project-status';
import { makeBoard, makeTicket } from '@/test/fixtures';

const ticket = (id: string, state: Parameters<typeof makeTicket>[0]['state']) =>
  makeTicket({
    id,
    title: `ticket ${id}`,
    body: 'body',
    state,
    priority: 1,
    createdAt: '2026-08-04T09:00:00Z',
    updatedAt: '2026-08-04T09:00:00Z',
  });

describe('deriveProjectState', () => {
  it('an empty board is quiet — the resting state, stated plainly', () => {
    expect(deriveProjectState(makeBoard())).toBe('quiet');
  });

  it('a blocked ticket needs you', () => {
    expect(deriveProjectState(makeBoard({ blocked: [ticket('t1', 'blocked')] }))).toBe('needs-you');
  });

  it('a shaping ticket (a pending proposal) needs you', () => {
    expect(deriveProjectState(makeBoard({ shaping: [ticket('t1', 'shaping')] }))).toBe('needs-you');
  });

  it('a working ticket is working, not needs-you', () => {
    expect(deriveProjectState(makeBoard({ working: [ticket('t1', 'working')] }))).toBe('working');
  });

  it('needs-you beats working — the question is what the rail exists to surface', () => {
    const board = makeBoard({
      blocked: [ticket('t1', 'blocked')],
      working: [ticket('t2', 'working')],
    });
    expect(deriveProjectState(board)).toBe('needs-you');
  });

  it('ready and done tickets alone are quiet — neither wants a person', () => {
    const board = makeBoard({ ready: [ticket('t1', 'ready')], done: [ticket('t2', 'done')] });
    expect(deriveProjectState(board)).toBe('quiet');
  });

  it('never heard from is unknown, NOT quiet — the rail must not claim an all-clear it has not verified', () => {
    expect(deriveProjectState(null)).toBe('unknown');
  });
});
