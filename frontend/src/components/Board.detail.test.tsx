// Board ⇄ TicketDetail wiring (07 §7): clicking a card opens the read-only
// detail for that ticket; closing it removes the overlay. Board holds the
// selection as local view state only — no authoritative state (02 §11) — and
// re-derives the open ticket from the live board snapshot each render.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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

describe('Board ticket detail', () => {
  it('opens the detail for the clicked ticket and closes it again', () => {
    mockStore({
      board: makeBoard({
        working: [
          makeTicket({
            ...baseFields,
            id: 'w1',
            title: 'Build the widget',
            body: 'The full widget body.',
            state: 'working',
            priority: 2,
          }),
        ],
      }),
      connectionState: 'connected',
    });

    render(<Board />);

    // No dialog until a card is clicked.
    expect(screen.queryByRole('dialog')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Build the widget' }));

    const dialog = screen.getByRole('dialog', { name: 'Ticket: Build the widget' });
    expect(dialog.querySelector('[data-role="ticket-detail-body"]')?.textContent).toBe(
      'The full widget body.',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('closes the detail if the selected ticket leaves the board snapshot', () => {
    const ticket = makeTicket({
      ...baseFields,
      id: 'w1',
      title: 'Build the widget',
      body: 'body',
      state: 'working',
      priority: 2,
    });
    mockStore({ board: makeBoard({ working: [ticket] }), connectionState: 'connected' });

    const { rerender } = render(<Board />);
    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Build the widget' }));
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    // Next snapshot no longer contains the ticket → detail derives to null.
    mockStore({ board: makeBoard(), connectionState: 'connected' });
    rerender(<Board />);

    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
