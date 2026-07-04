// TicketCard interactivity (07 §7): with an `onSelect` handler the card is a
// keyboard-operable button that opens the ticket's read-only detail; without
// one it is inert and DOM-identical (so the existing snapshots in
// TicketCard.test.tsx are unaffected). Board stays read-only — this is
// inspection, not mutation (D5).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { TicketCard } from '@/components/TicketCard';
import { makeTicket } from '@/test/fixtures';

const ticket = makeTicket({
  id: 't1',
  title: 'Add login page',
  body: 'Users need a way to sign in.',
  state: 'shaping',
  priority: 0,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-01T00:00:00Z',
});

describe('TicketCard interactivity', () => {
  it('opens on click, passing the ticket to onSelect', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);

    fireEvent.click(screen.getByRole('button', { name: 'Open ticket: Add login page' }));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(ticket);
  });

  it('opens on Enter and Space', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);
    const card = screen.getByRole('button');

    fireEvent.keyDown(card, { key: 'Enter' });
    fireEvent.keyDown(card, { key: ' ' });

    expect(onSelect).toHaveBeenCalledTimes(2);
  });

  it('ignores other keys', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);

    fireEvent.keyDown(screen.getByRole('button'), { key: 'a' });

    expect(onSelect).not.toHaveBeenCalled();
  });

  it('is inert without onSelect: no button role, no tabstop, click does nothing', () => {
    render(<TicketCard ticket={ticket} />);

    expect(screen.queryByRole('button')).toBeNull();
    const card = screen.getByRole('article');
    expect(card).not.toHaveAttribute('tabindex');
    // Clicking an inert card must not throw (no handler attached).
    fireEvent.click(card);
  });
});
