// TicketDetail sheet: shows a ticket's full record and is dismissable — never a
// trap (07 §7–§8). It renders as a `vaul` bottom sheet, so its content and scrim
// portal to document.body (query via `screen`/`document`, not the render
// container) and dismissal — Escape, scrim, drag — is Vaul's concern, routed to
// onClose via onOpenChange. We test our own surface here (the close button, the
// content, the Escape wiring reaching onClose); the drag physics are the
// library's and are not re-tested.
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
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

    // The dialog is named by its visible title (Radix wires aria-labelledby to
    // the <Drawer.Title>).
    const dialog = screen.getByRole('dialog', { name: 'Build the widget' });
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

    // Content portals to document.body, so scope the query to the dialog itself
    // rather than the render container (which is now empty of the sheet).
    render(<TicketDetail ticket={markdown} onClose={vi.fn()} />);
    const dialog = screen.getByRole('dialog');

    expect(dialog.querySelector('strong')?.textContent).toBe('bold');
    expect(dialog.querySelectorAll('li')).toHaveLength(2);
    expect(dialog.querySelector('code')?.textContent).toBe('code');
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

  it('renders the scrim so the sheet reads as a modal surface', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    // The scrim is Vaul's overlay, portaled alongside the panel.
    expect(document.querySelector('[data-role="ticket-detail-backdrop"]')).not.toBeNull();
  });

  it('calls onClose from the close button', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} />);

    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('calls onClose when Escape is pressed (Vaul dismiss → onOpenChange → onClose)', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} />);

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('is read-only by default — no Accept action (D5 board inspection)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('shows an Accept action for a shaping ticket when onAccept is provided (proposal click-through, 08 §5)', () => {
    const onAccept = vi.fn();
    const shaping = makeTicket({
      id: 't-shape',
      title: 'A shaped proposal',
      body: 'body',
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={shaping} onClose={vi.fn()} onAccept={onAccept} />);

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' }));

    expect(onAccept).toHaveBeenCalledWith('t-shape');
  });

  it('never offers Accept once past shaping — a working ticket has already been accepted', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} onAccept={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('shows an "in progress" status indicator for a working ticket', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);
    const status = within(screen.getByRole('dialog'))
      .getByText('In progress')
      .closest('[data-role="ticket-detail-status"]');
    expect(status).not.toBeNull();
    expect(status).toHaveAttribute('data-state', 'working');
    expect(status?.querySelector('[data-role="ticket-detail-status-dot"]')).not.toBeNull();
  });

  describe('done ticket', () => {
    const done = makeTicket({
      id: 't-done',
      title: 'Shipped thing',
      body: 'body',
      state: 'done',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-02T00:00:00Z',
    });

    it('shows a "done" status indicator in the header', () => {
      render(<TicketDetail ticket={done} onClose={vi.fn()} />);
      const dialog = screen.getByRole('dialog');
      const status = within(dialog).getByText('Done').closest('[data-role="ticket-detail-status"]');
      expect(status).not.toBeNull();
      expect(status).toHaveAttribute('data-state', 'done');
      // The clear dot is part of the indicator.
      expect(status?.querySelector('[data-role="ticket-detail-status-dot"]')).not.toBeNull();
    });

    it('never offers Accept — completed work has nothing to accept, even if onAccept is wired', () => {
      render(<TicketDetail ticket={done} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    });
  });

  describe('blocked ticket', () => {
    const blocked = makeTicket({
      id: 't-blocked',
      title: 'Stuck thing',
      body: 'body',
      state: 'blocked',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-02T00:00:00Z',
      blockedReason: 'Needs a decision on the auth scheme.',
    });

    it('offers Talk (not Accept) when onTalk is wired, and fires it on tap', () => {
      const onTalk = vi.fn();
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onTalk={onTalk} />);
      const dialog = screen.getByRole('dialog');
      expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Talk to unblock' }));
      expect(onTalk).toHaveBeenCalledTimes(1);
    });

    it('never offers Accept even when onAccept is wired — a block is discussed, not accepted', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    it('shows no action when neither onTalk nor onAccept is wired (read-only inspection)', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Talk to unblock' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    it('shows a "blocked" status indicator (with the reason below), not a "done" one', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onTalk={vi.fn()} />);
      const dialog = screen.getByRole('dialog');
      const status = within(dialog)
        .getByText('Blocked')
        .closest('[data-role="ticket-detail-status"]');
      expect(status).not.toBeNull();
      expect(status).toHaveAttribute('data-state', 'blocked');
      expect(within(dialog).queryByText('Done')).toBeNull();
      // The at-a-glance badge and the full reason both render.
      expect(within(dialog).getByText('Needs a decision on the auth scheme.')).toBeInTheDocument();
    });
  });
});
