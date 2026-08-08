import { describe, expect, it } from 'vitest';
import { ticketDependencies } from '@/components/ticket-dependencies';
import { makeBoard, makeTicket } from '@/test/fixtures';

const at = '2026-08-08T10:00:00Z';

function ticket(id: string, title: string, state: Parameters<typeof makeTicket>[0]['state']) {
  return makeTicket({ id, title, body: '', state, priority: 0, createdAt: at, updatedAt: at });
}

describe('ticketDependencies', () => {
  it('resolves ids to the titles the user actually recognises', () => {
    const waiter = makeTicket({
      id: 'w',
      title: 'Use the column',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
      dependsOn: ['a', 'b'],
    });
    const board = makeBoard({
      ready: [waiter],
      done: [ticket('a', 'Land the migration', 'done')],
      working: [ticket('b', 'Backfill the rows', 'working')],
    });

    expect(ticketDependencies(board, waiter)).toEqual([
      { id: 'a', title: 'Land the migration', done: true },
      { id: 'b', title: 'Backfill the rows', done: false },
    ]);
  });

  it('keeps the server order, which is the order they were added', () => {
    const waiter = makeTicket({
      id: 'w',
      title: 'Waiter',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
      dependsOn: ['b', 'a'],
    });
    const board = makeBoard({
      ready: [waiter, ticket('a', 'A', 'ready'), ticket('b', 'B', 'ready')],
    });

    expect(ticketDependencies(board, waiter).map((d) => d.title)).toEqual(['B', 'A']);
  });

  // A bare uuid is not an answer to "waiting on what?", so an unresolvable
  // dependency is dropped rather than rendered raw.
  it('drops a dependency the snapshot does not carry rather than showing its id', () => {
    const waiter = makeTicket({
      id: 'w',
      title: 'Waiter',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
      dependsOn: ['ghost', 'a'],
    });
    const board = makeBoard({ ready: [waiter, ticket('a', 'A', 'ready')] });

    expect(ticketDependencies(board, waiter)).toEqual([{ id: 'a', title: 'A', done: false }]);
  });

  it('is empty before the first snapshot, so the sheet renders no section', () => {
    const waiter = makeTicket({
      id: 'w',
      title: 'Waiter',
      body: '',
      state: 'ready',
      priority: 0,
      createdAt: at,
      updatedAt: at,
      dependsOn: ['a'],
    });
    expect(ticketDependencies(null, waiter)).toEqual([]);
  });

  it('is empty for a ticket that waits for nothing', () => {
    const plain = ticket('p', 'Plain', 'ready');
    expect(ticketDependencies(makeBoard({ ready: [plain] }), plain)).toEqual([]);
  });
});
