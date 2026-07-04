// TicketCard opt-in interactivity (debug view): with `onSelect` the card is a
// keyboard-operable button that selects the ticket on click/Enter/Space;
// without it the card is inert and unchanged (existing snapshots hold).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { TicketCard } from '@/components/TicketCard';
import { makeTicket } from '@/test/fixtures';

const ticket = makeTicket({
  id: 't-1',
  title: 'Build the widget',
  body: 'body',
  state: 'ready',
  priority: 0,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-01T00:00:00Z',
});

describe('TicketCard interactivity', () => {
  it('is inert when no onSelect is given', () => {
    render(<TicketCard ticket={ticket} />);

    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText('Build the widget')).toBeInTheDocument();
  });

  it('exposes a labelled button and selects on click when onSelect is given', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);

    const card = screen.getByRole('button', { name: 'Open ticket: Build the widget' });
    fireEvent.click(card);

    expect(onSelect).toHaveBeenCalledWith(ticket);
  });

  it('selects on Enter and Space', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);

    const card = screen.getByRole('button');
    fireEvent.keyDown(card, { key: 'Enter' });
    fireEvent.keyDown(card, { key: ' ' });

    expect(onSelect).toHaveBeenCalledTimes(2);
    expect(onSelect).toHaveBeenNthCalledWith(1, ticket);
    expect(onSelect).toHaveBeenNthCalledWith(2, ticket);
  });

  it('does not select on other keys', () => {
    const onSelect = vi.fn();
    render(<TicketCard ticket={ticket} onSelect={onSelect} />);

    fireEvent.keyDown(screen.getByRole('button'), { key: 'a' });

    expect(onSelect).not.toHaveBeenCalled();
  });
});
