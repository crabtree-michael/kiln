// Board composition tests (07 §7): two column groups — Backlog [Ready],
// Developing [Blocked above Working]. Shaping and Done are hidden from the
// list; the active work states (Ready, Blocked, Working) stay visible. Ready
// rendered in the exact order the store hands it (no client-side re-sort,
// 03 §5/07 §7), the capacity chip fed from the snapshot, the connection
// state exposed for the "dim while reconnecting" treatment (07 §8), and no
// drag-and-drop mutation path (D5 — board is read-only).
//
// `useBoardStore` is mocked directly so this file targets Board.tsx's own
// derivation/composition contract independent of the store's transport
// wiring (covered separately in board-store.test.tsx).
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Board } from '@/components/Board';
import * as boardContext from '@/stores/board-context';
import type { BoardStoreValue } from '@/stores/board-context';
import { makeBoard, makeTicket } from '@/test/fixtures';

vi.mock('@/stores/board-context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/board-context')>();
  return { ...actual, useBoardStore: vi.fn() };
});

const baseFields = { createdAt: '2026-07-01T00:00:00Z', updatedAt: '2026-07-01T00:00:00Z' };

function mockStore(value: Pick<BoardStoreValue, 'board' | 'connectionState'>): void {
  // Board.tsx reads only board + connectionState; the refresh affordance is the
  // header dropdown's concern, so default it here to keep call sites focused.
  vi.mocked(boardContext.useBoardStore).mockReturnValue({
    refreshBoard: () => undefined,
    refreshing: false,
    ...value,
  });
}

describe('Board', () => {
  it('surfaces Backlog[Ready] and Developing[Blocked,Working], hiding Shaping/Done (07 §7)', () => {
    const board = makeBoard({
      shaping: [
        makeTicket({
          ...baseFields,
          id: 's1',
          title: 'shaping',
          body: '',
          state: 'shaping',
          priority: 0,
        }),
      ],
      ready: [
        makeTicket({
          ...baseFields,
          id: 'r1',
          title: 'ready',
          body: '',
          state: 'ready',
          priority: 0,
        }),
      ],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'blocked',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
      working: [
        makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'working',
          body: '',
          state: 'working',
          priority: 0,
        }),
      ],
      done: [
        makeTicket({
          ...baseFields,
          id: 'd1',
          title: 'done',
          body: '',
          state: 'done',
          priority: 0,
        }),
      ],
    });
    mockStore({ board, connectionState: 'connected' });

    render(<Board />);

    // `getAllByRole('region')` also matches the outer `<section
    // aria-label="Board">` landmark, so scope to the three
    // `data-role="board-column"` elements specifically.
    const columns = Array.from(
      screen.getByRole('region', { name: 'Board' }).querySelectorAll('[data-role="board-column"]'),
    ).map((column) => column.getAttribute('aria-label'));
    expect(columns).toEqual(['Backlog', 'Developing']);

    // Scope to the zone-label headings (`[data-role="board-zone"] > h3`),
    // not the ticket cards' own `<h3>` titles nested underneath.
    const backlog = screen.getByRole('region', { name: 'Backlog' });
    expect(
      Array.from(backlog.querySelectorAll('[data-role="board-zone"] > h3')).map(
        (h) => h.textContent,
      ),
    ).toEqual(['Ready']);

    const developing = screen.getByRole('region', { name: 'Developing' });
    expect(
      Array.from(developing.querySelectorAll('[data-role="board-zone"] > h3')).map(
        (h) => h.textContent,
      ),
    ).toEqual(['Blocked', 'Working']);

    // Hidden states never render a zone or a Done column.
    expect(screen.queryByRole('region', { name: 'Done' })).toBeNull();
    const zoneLabels = Array.from(
      screen.getByRole('region', { name: 'Board' }).querySelectorAll('[data-role="board-zone"] > h3'),
    ).map((h) => h.textContent);
    expect(zoneLabels).not.toContain('Shaping');
    expect(zoneLabels).not.toContain('Done');

    // The hidden states' tickets are not rendered anywhere on the board.
    expect(screen.queryByText('shaping')).toBeNull();
    expect(screen.queryByText('done')).toBeNull();
    // Ready/Blocked/Working tickets are present.
    expect(screen.getByText('ready')).toBeTruthy();
    expect(screen.getByText('blocked')).toBeTruthy();
    expect(screen.getByText('working')).toBeTruthy();
  });

  it('stacks Blocked (loud) above Working in the Developing column (01 §5, 07 §7)', () => {
    const board = makeBoard({
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'blocked',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
      working: [
        makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'working',
          body: '',
          state: 'working',
          priority: 0,
        }),
      ],
    });
    mockStore({ board, connectionState: 'connected' });

    render(<Board />);

    const developing = screen.getByRole('region', { name: 'Developing' });
    const zones = developing.querySelectorAll('[data-role="board-zone"]');
    expect(zones[0]?.querySelector('h3')?.textContent).toBe('Blocked');
    expect(zones[1]?.querySelector('h3')?.textContent).toBe('Working');
    expect(zones[0]?.getAttribute('data-emphasis')).toBe('loud');
  });

  it('renders Ready tickets in the exact order the board snapshot gives (no client re-sort) (03 §5)', () => {
    const ready = [
      makeTicket({
        ...baseFields,
        id: 'r-high',
        title: 'highest priority',
        body: '',
        state: 'ready',
        priority: 10,
      }),
      makeTicket({
        ...baseFields,
        id: 'r-mid',
        title: 'mid priority',
        body: '',
        state: 'ready',
        priority: 5,
      }),
      makeTicket({
        ...baseFields,
        id: 'r-low',
        title: 'lowest priority',
        body: '',
        state: 'ready',
        priority: 1,
      }),
    ];
    // Deliberately not sorted ascending by id/priority in the snapshot array
    // — Board must render exactly this order, since the backend already pulls
    // in priority DESC, ready_at ASC, id ASC order (03 §5).
    mockStore({ board: makeBoard({ ready }), connectionState: 'connected' });

    render(<Board />);

    const backlog = screen.getByRole('region', { name: 'Backlog' });
    const titles = Array.from(backlog.querySelectorAll('article h3')).map((h) => h.textContent);
    expect(titles).toEqual(['highest priority', 'mid priority', 'lowest priority']);
  });

  it('feeds the capacity chip from board.worker_free/worker_total (07 §7)', () => {
    mockStore({
      board: makeBoard({ worker_free: 1, worker_total: 3 }),
      connectionState: 'connected',
    });

    render(<Board />);

    expect(screen.getByLabelText('Worker capacity')).toHaveTextContent('1/3');
  });

  it('exposes connectionState for the dim-while-reconnecting treatment, never blank (07 §8)', () => {
    mockStore({ board: makeBoard(), connectionState: 'reconnecting' });

    render(<Board />);

    expect(screen.getByRole('region', { name: 'Board' })).toHaveAttribute(
      'data-connection-state',
      'reconnecting',
    );
    // Stale-but-visible: the board content must still be rendered, not blank.
    expect(screen.getAllByRole('region')).toHaveLength(3); // Board + 2 columns
  });

  it('is read-only: no drag-and-drop affordances or handlers anywhere on the board (D5)', () => {
    const board = makeBoard({
      ready: [
        makeTicket({
          ...baseFields,
          id: 'r1',
          title: 'ready',
          body: '',
          state: 'ready',
          priority: 0,
        }),
      ],
      blocked: [
        makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'blocked',
          body: '',
          state: 'blocked',
          priority: 0,
        }),
      ],
    });
    mockStore({ board, connectionState: 'connected' });

    const { container } = render(<Board />);

    const draggableEls = container.querySelectorAll('[draggable="true"]');
    expect(draggableEls).toHaveLength(0);
    // No element should declare a drop target.
    const dropTargets = container.querySelectorAll('[data-role="board-drop-target"], [ondrop]');
    expect(dropTargets).toHaveLength(0);
  });
});
