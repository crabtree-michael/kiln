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

  it('shows a Delete action for a shaping proposal when onDelete is provided', () => {
    const onDelete = vi.fn();
    const shaping = makeTicket({
      id: 't-shape',
      title: 'A shaped proposal',
      body: 'body',
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={shaping} onClose={vi.fn()} onDelete={onDelete} />);

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    expect(onDelete).toHaveBeenCalledWith('t-shape');
  });

  it('is read-only by default — no Delete action (D5 board inspection)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('never offers Delete on a working ticket — a live agent is mid-turn', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} onDelete={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });

  it('shows Delete on a blocked ticket and calls onDelete once the confirm is accepted', () => {
    const onDelete = vi.fn();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const blocked = makeTicket({
      id: 't-blocked',
      title: 'A duplicate stuck in dev',
      body: 'body',
      state: 'blocked',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
      blockedReason: 'Duplicate of t-1.',
    });
    render(<TicketDetail ticket={blocked} onClose={vi.fn()} onDelete={onDelete} />);

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    expect(confirm).toHaveBeenCalledTimes(1);
    expect(onDelete).toHaveBeenCalledWith('t-blocked');
    confirm.mockRestore();
  });

  it('does not delete a blocked ticket when the confirm is dismissed', () => {
    const onDelete = vi.fn();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const blocked = makeTicket({
      id: 't-blocked',
      title: 'A duplicate stuck in dev',
      body: 'body',
      state: 'blocked',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
      blockedReason: 'Duplicate of t-1.',
    });
    render(<TicketDetail ticket={blocked} onClose={vi.fn()} onDelete={onDelete} />);

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    expect(confirm).toHaveBeenCalledTimes(1);
    expect(onDelete).not.toHaveBeenCalled();
    confirm.mockRestore();
  });

  it('deletes a shaping proposal without a confirm — cheap and re-proposable', () => {
    const onDelete = vi.fn();
    const confirm = vi.spyOn(window, 'confirm');
    const shaping = makeTicket({
      id: 't-shape2',
      title: 'A shaped proposal',
      body: 'body',
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={shaping} onClose={vi.fn()} onDelete={onDelete} />);

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }));

    expect(confirm).not.toHaveBeenCalled();
    expect(onDelete).toHaveBeenCalledWith('t-shape2');
    confirm.mockRestore();
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

  // The bottom-left voice control (the mic) on a proposal sheet. TicketDetail is
  // voice-store-agnostic — it renders whatever node the caller passes — so a plain
  // stand-in stands in for the real MicButton here. It rides the same shaping-only
  // gate as Accept: a proposal is only ever a shaping ticket.
  describe('proposal voice control', () => {
    const proposal = makeTicket({
      id: 't-prop',
      title: 'A shaped proposal',
      body: 'body',
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    const mic = <button data-role="mock-mic">mic</button>;

    it('renders the voice control at the footer bottom-left on a shaping proposal', () => {
      render(
        <TicketDetail ticket={proposal} onClose={vi.fn()} onAccept={vi.fn()} voiceControl={mic} />,
      );
      const lead = within(screen.getByRole('dialog'))
        .getByText('mic')
        .closest('[data-role="ticket-detail-lead-actions"]');
      expect(lead).not.toBeNull();
      // It shares the footer with the trailing Accept action.
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' }),
      ).toBeInTheDocument();
    });

    it('never renders the voice control past shaping — not a proposal anymore', () => {
      render(<TicketDetail ticket={working} onClose={vi.fn()} voiceControl={mic} />);
      expect(screen.queryByText('mic')).toBeNull();
    });

    it('renders no lead cluster when the caller wires no voice control (/debug inspection)', () => {
      render(<TicketDetail ticket={proposal} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(within(screen.getByRole('dialog')).queryByText('mic')).toBeNull();
      expect(document.querySelector('[data-role="ticket-detail-lead-actions"]')).toBeNull();
    });
  });

  describe('Poke action', () => {
    it('is absent by default — read-only inspection has no Poke button', () => {
      render(<TicketDetail ticket={working} onClose={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Poke' })).toBeNull();
    });

    it('offers Poke on a working ticket with an idle agent, and fires it with the id', () => {
      const onPoke = vi.fn();
      render(<TicketDetail ticket={working} onClose={vi.fn()} onPoke={onPoke} agentIdle />);

      const poke = within(screen.getByRole('dialog')).getByRole('button', { name: 'Poke' });
      // The 👉 is the poke's whole signal (matching the feed poke card), rendered
      // decoratively alongside the "Poke" label.
      expect(poke).toHaveTextContent('👉');
      fireEvent.click(poke);

      expect(onPoke).toHaveBeenCalledWith('t-42');
    });

    it('hides Poke on a working ticket while the agent is mid-turn (not idle)', () => {
      // agentIdle defaults false — the agent is `building`, streaming progress, so
      // there is nothing to nudge and the button must not appear.
      render(<TicketDetail ticket={working} onClose={vi.fn()} onPoke={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Poke' })).toBeNull();
    });

    it('offers Poke alongside Talk on a blocked ticket (both actions coexist)', () => {
      const blocked = makeTicket({
        id: 't-b',
        title: 'Stuck',
        body: 'body',
        state: 'blocked',
        priority: 1,
        createdAt: '2026-07-01T00:00:00Z',
        updatedAt: '2026-07-01T00:00:00Z',
        blockedReason: 'Needs a decision.',
      });
      const onPoke = vi.fn();
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onTalk={vi.fn()} onPoke={onPoke} />);
      const dialog = screen.getByRole('dialog');

      expect(within(dialog).getByRole('button', { name: 'Talk to unblock' })).toBeInTheDocument();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Poke' }));

      expect(onPoke).toHaveBeenCalledWith('t-b');
    });

    it('never offers Poke on a done ticket, even when onPoke is wired', () => {
      const done = makeTicket({
        id: 't-d',
        title: 'Shipped',
        body: 'body',
        state: 'done',
        priority: 1,
        createdAt: '2026-07-01T00:00:00Z',
        updatedAt: '2026-07-02T00:00:00Z',
      });
      render(<TicketDetail ticket={done} onClose={vi.fn()} onPoke={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Poke' })).toBeNull();
    });

    it('never offers Poke on a shaping/ready ticket — no agent to nudge yet', () => {
      const shaping = makeTicket({
        id: 't-s',
        title: 'Idea',
        body: 'body',
        state: 'shaping',
        priority: 1,
        createdAt: '2026-07-01T00:00:00Z',
        updatedAt: '2026-07-01T00:00:00Z',
      });
      render(<TicketDetail ticket={shaping} onClose={vi.fn()} onPoke={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Poke' })).toBeNull();
    });
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
