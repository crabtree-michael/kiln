// TicketDetail overlay: shows a ticket's full record and is dismissable via the
// close button, backdrop click, and Escape — never a trap (07 §7–§8). Clicks
// inside the panel must not fall through to the backdrop close.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { TicketDetail } from '@/components/TicketDetail';
import { makeTicket, LONG_BLOCKED_REASON } from '@/test/fixtures';

const working = makeTicket({
  id: 't-42',
  title: 'Build the widget',
  body: 'The complete body text the card only previews.',
  state: 'working',
  priority: 3,
  createdAt: '2026-07-01T00:00:00Z',
  updatedAt: '2026-07-02T00:00:00Z',
});

describe('TicketDetail', () => {
  it('renders the full ticket record the card elides', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    const dialog = screen.getByRole('dialog', { name: 'Ticket: Build the widget' });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText('The complete body text the card only previews.')).toBeInTheDocument();
    expect(screen.getByText('t-42')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('working')).toBeInTheDocument();
  });

  it('shows the full blocked reason for a blocked ticket', () => {
    const blocked = makeTicket({
      id: 't-9',
      title: 'Blocked ticket',
      body: 'body',
      state: 'blocked',
      priority: 0,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
      blockedReason: LONG_BLOCKED_REASON,
    });

    render(<TicketDetail ticket={blocked} onClose={vi.fn()} />);

    expect(screen.getByText(LONG_BLOCKED_REASON)).toBeInTheDocument();
  });

  it('calls onClose from the close button', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onClose when the backdrop is clicked, but not the panel', () => {
    const onClose = vi.fn();
    const { container } = render(<TicketDetail ticket={working} onClose={onClose} />);

    // A click inside the panel must not close it.
    fireEvent.click(screen.getByRole('dialog'));
    expect(onClose).not.toHaveBeenCalled();

    const backdrop = container.querySelector('[data-role="ticket-detail-backdrop"]');
    if (backdrop === null) {
      throw new Error('backdrop not found');
    }
    fireEvent.click(backdrop);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onClose when Escape is pressed', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} />);

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('is read-only by default — no Accept action (D5 board inspection)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('shows an Accept action when onAccept is provided (proposal click-through, 08 §5)', () => {
    const onAccept = vi.fn();
    render(<TicketDetail ticket={working} onClose={vi.fn()} onAccept={onAccept} />);

    fireEvent.click(screen.getByRole('button', { name: 'Accept' }));

    expect(onAccept).toHaveBeenCalledWith('t-42');
  });
});
