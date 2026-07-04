// TicketDetail overlay (07 §7): read-only inspection of a board ticket's full
// record. Verifies the full body/blocked-reason are shown (never truncated,
// mirroring the Blocked card, 07 §7), and that the overlay never traps the
// user — dismiss via close button, backdrop, or Escape — while a click on the
// dialog body itself does not dismiss it.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { TicketDetail } from '@/components/TicketDetail';
import { makeTicket, LONG_BLOCKED_REASON } from '@/test/fixtures';

const baseFields = { createdAt: '2026-07-01T09:00:00Z', updatedAt: '2026-07-03T12:30:00Z' };

const LONG_BODY =
  'Users need a way to sign in. This body is intentionally long so the detail ' +
  'view can show it in full, unlike the two-line clamped card preview on the board.';

describe('TicketDetail', () => {
  it('renders the full record: title, full body, state, priority, id, timestamps', () => {
    render(
      <TicketDetail
        ticket={makeTicket({
          ...baseFields,
          id: 't-42',
          title: 'Add login page',
          body: LONG_BODY,
          state: 'working',
          priority: 7,
        })}
        onClose={vi.fn()}
      />,
    );

    const dialog = screen.getByRole('dialog', { name: 'Ticket: Add login page' });
    expect(dialog).toHaveAttribute('data-state', 'working');
    expect(screen.getByRole('heading', { name: 'Add login page' })).toBeInTheDocument();
    expect(dialog.querySelector('[data-role="ticket-detail-body"]')?.textContent).toBe(LONG_BODY);
    expect(dialog.querySelector('[data-role="detail-state"]')?.textContent).toBe('working');
    expect(dialog.querySelector('[data-role="detail-priority"]')?.textContent).toBe('7');
    expect(dialog.querySelector('[data-role="detail-id"]')?.textContent).toBe('t-42');
    expect(dialog.querySelector('[data-role="detail-created"]')?.textContent).toBe(
      '2026-07-01T09:00:00Z',
    );
    expect(dialog.querySelector('[data-role="detail-updated"]')?.textContent).toBe(
      '2026-07-03T12:30:00Z',
    );
  });

  it('shows the full blocked_reason for a blocked ticket, not truncated (07 §7)', () => {
    render(
      <TicketDetail
        ticket={makeTicket({
          ...baseFields,
          id: 'b1',
          title: 'Blocked ticket',
          body: 'Waiting on input.',
          state: 'blocked',
          priority: 4,
          blockedReason: LONG_BLOCKED_REASON,
        })}
        onClose={vi.fn()}
      />,
    );

    expect(screen.getByText(LONG_BLOCKED_REASON)).toBeInTheDocument();
  });

  it('does not render a blocked-reason node for non-blocked tickets', () => {
    const { container } = render(
      <TicketDetail
        ticket={makeTicket({
          ...baseFields,
          id: 'w1',
          title: 'Working ticket',
          body: 'In progress.',
          state: 'working',
          priority: 0,
        })}
        onClose={vi.fn()}
      />,
    );

    expect(container.querySelector('[data-role="detail-blocked-reason"]')).toBeNull();
  });

  it('closes via the close button, the backdrop, and Escape', () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <TicketDetail
        ticket={makeTicket({
          ...baseFields,
          id: 't1',
          title: 'T',
          body: '',
          state: 'ready',
          priority: 0,
        })}
        onClose={onClose}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('dialog').parentElement!);
    expect(onClose).toHaveBeenCalledTimes(2);

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(3);

    // The Escape listener is cleaned up on unmount (no leak / no post-unmount fire).
    rerender(<></>);
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(3);
  });

  it('does not close when the dialog body itself is clicked', () => {
    const onClose = vi.fn();
    render(
      <TicketDetail
        ticket={makeTicket({
          ...baseFields,
          id: 't1',
          title: 'T',
          body: 'body',
          state: 'ready',
          priority: 0,
        })}
        onClose={onClose}
      />,
    );

    fireEvent.click(screen.getByRole('dialog'));

    expect(onClose).not.toHaveBeenCalled();
  });
});
