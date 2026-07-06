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
  it('shows only the title and description by default — no internal metadata (main app view)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    const dialog = screen.getByRole('dialog', { name: 'Ticket: Build the widget' });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Build the widget' })).toBeInTheDocument();
    expect(screen.getByText('The complete body text the card only previews.')).toBeInTheDocument();
    // Internal bookkeeping (priority, id, state, timestamps) is hidden here.
    expect(screen.queryByText('t-42')).toBeNull();
    expect(screen.queryByText('Priority')).toBeNull();
    expect(screen.queryByText('ID')).toBeNull();
  });

  it('shows the full internal record when showInternalMeta is set (/debug inspection, D5)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} showInternalMeta />);

    expect(screen.getByText('t-42')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('working')).toBeInTheDocument();
  });

  it('renders the description as Markdown', () => {
    const markdown = makeTicket({
      id: 't-md',
      title: 'Markdown ticket',
      body: 'Some **bold** text\n\n- first\n- second\n\nInline `code` here.',
      state: 'working',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });

    const { container } = render(<TicketDetail ticket={markdown} onClose={vi.fn()} />);

    expect(container.querySelector('strong')?.textContent).toBe('bold');
    expect(container.querySelectorAll('li')).toHaveLength(2);
    expect(container.querySelector('code')?.textContent).toBe('code');
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
