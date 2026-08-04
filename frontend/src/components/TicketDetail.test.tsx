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

  // The bottom-left voice control (the mic). TicketDetail is voice-store-agnostic
  // — it renders whatever node the caller passes — so a plain stand-in stands in
  // for the real MicButton here. It is the unified communication surface: shown on
  // every ticket state whenever the caller wires it (only its presence is gated,
  // not the ticket's lifecycle state).
  describe('voice control', () => {
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

    it('renders the voice control on a non-shaping ticket too (the unified surface)', () => {
      // The mic is no longer shaping-only: it is the one communication surface
      // shared across every ticket state, so a working ticket shows it as well.
      render(<TicketDetail ticket={working} onClose={vi.fn()} voiceControl={mic} />);
      const lead = within(screen.getByRole('dialog'))
        .getByText('mic')
        .closest('[data-role="ticket-detail-lead-actions"]');
      expect(lead).not.toBeNull();
    });

    it('renders no lead cluster when the caller wires no voice control (read-only inspection)', () => {
      render(<TicketDetail ticket={proposal} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(within(screen.getByRole('dialog')).queryByText('mic')).toBeNull();
      expect(document.querySelector('[data-role="ticket-detail-lead-actions"]')).toBeNull();
    });

    // The live transcript slot. TicketDetail is voice-store-agnostic, so a plain
    // stand-in stands in for the real TicketDetailTranscript. It rides the same
    // gate as the mic (shown on any state when wired) and lands inside the dock,
    // above the controls.
    const transcript = <div data-role="mock-transcript">move the button</div>;

    it('renders the transcript inside the dock, above the action controls, on a shaping proposal', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          voiceControl={mic}
          transcript={transcript}
        />,
      );
      const dialog = screen.getByRole('dialog');
      const slot = within(dialog).getByText('move the button');
      // The transcript lives inside the sheet's dock (the unified controls +
      // transcript region), not in a separate area.
      expect(slot.closest('[data-role="ticket-detail-dock"]')).not.toBeNull();
      // …and it stacks ABOVE the controls: it precedes the Accept button (which
      // lives in the controls row) in document order. slot and Accept sit in
      // sibling subtrees, so compareDocumentPosition is exactly FOLLOWING.
      const accept = within(dialog).getByRole('button', { name: 'Accept' });
      expect(slot.compareDocumentPosition(accept)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    });

    it('renders the transcript on a non-shaping ticket too (rides the mic gate)', () => {
      // The transcript is the mic's on-screen feedback, so it follows the same
      // unified gate — present on a working ticket, not just a shaping proposal.
      // It renders alongside the wired mic (`showVoice`), so pass both.
      render(
        <TicketDetail
          ticket={working}
          onClose={vi.fn()}
          voiceControl={mic}
          transcript={transcript}
        />,
      );
      const slot = within(screen.getByRole('dialog')).getByText('move the button');
      expect(slot.closest('[data-role="ticket-detail-dock"]')).not.toBeNull();
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
      // Icon-only: the 👉 is the poke's whole visible signal (matching the feed poke
      // card), with no text label — the accessible name comes from aria-label="Poke".
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

    it('offers Poke on a blocked ticket and fires it with the id', () => {
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
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onPoke={onPoke} />);
      const dialog = screen.getByRole('dialog');

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

    // The mic (voiceControl) is the unified communication surface, replacing the
    // old blocked-only "Talk to unblock" button — a blocked ticket now shows the
    // same mic every other ticket type does. A plain stand-in stands in for the
    // real MicButton (TicketDetail is voice-store-agnostic).
    const mic = <button data-role="mock-mic">mic</button>;

    it('shows the mic (not the old Talk button, and never Accept) when voiceControl is wired', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} voiceControl={mic} />);
      const dialog = screen.getByRole('dialog');
      expect(within(dialog).getByText('mic')).toBeInTheDocument();
      expect(within(dialog).queryByRole('button', { name: 'Talk to unblock' })).toBeNull();
      expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    it('never offers Accept even when onAccept is wired — a block is discussed, not accepted', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    it('shows no action when nothing is wired (read-only inspection)', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} />);
      expect(screen.queryByText('mic')).toBeNull();
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    it('shows a "blocked" status indicator (with the reason below), not a "done" one', () => {
      render(<TicketDetail ticket={blocked} onClose={vi.fn()} />);
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

// The per-ticket sandbox option: the switch that replaced the project form's
// "save a dev box as a snapshot" section. Saving a ticket's sandbox stops the
// board recycling its worker, so an agent can keep working in the same workspace
// across turns — the whole point of moving the choice onto the ticket.
describe('TicketDetail — sandbox option', () => {
  /** The sandbox switch inside the open sheet. */
  function sandboxSwitch(): HTMLInputElement {
    const el = within(screen.getByRole('dialog')).getByRole('checkbox', {
      name: /save this ticket.s sandbox/i,
    });
    if (!(el instanceof HTMLInputElement)) {
      throw new Error('sandbox switch is not an input');
    }
    return el;
  }

  it('is absent by default — read-only inspection offers no sandbox switch', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);
    expect(screen.queryByRole('checkbox', { name: /sandbox/i })).toBeNull();
  });

  it('reflects the ticket\u2019s own keep_sandbox and reports a turn-on with the id', () => {
    const onSetKeepSandbox = vi.fn();
    render(<TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />);

    expect(sandboxSwitch()).not.toBeChecked();
    fireEvent.click(sandboxSwitch());

    expect(onSetKeepSandbox).toHaveBeenCalledWith('t-42', true);
    // Optimistic: the switch shows the choice at once, without waiting for the
    // board snapshot to come back over the stream.
    expect(sandboxSwitch()).toBeChecked();
  });

  it('starts checked for a ticket whose sandbox is already saved, and reports a turn-off', () => {
    const saved = makeTicket({
      id: 't-keep',
      title: 'Long-running work',
      body: 'body',
      state: 'working',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
      keepSandbox: true,
    });
    const onSetKeepSandbox = vi.fn();
    render(<TicketDetail ticket={saved} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />);

    expect(sandboxSwitch()).toBeChecked();
    fireEvent.click(sandboxSwitch());

    expect(onSetKeepSandbox).toHaveBeenCalledWith('t-keep', false);
    expect(sandboxSwitch()).not.toBeChecked();
  });

  it('offers the switch on a shaping proposal too — the choice predates the sandbox', () => {
    const proposal = makeTicket({
      id: 't-prop',
      title: 'Proposed work',
      body: 'body',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={proposal} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);
    expect(sandboxSwitch()).toBeInTheDocument();
  });

  it('defers to the board snapshot once it catches up', () => {
    const onSetKeepSandbox = vi.fn();
    const { rerender } = render(
      <TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />,
    );
    fireEvent.click(sandboxSwitch());
    expect(sandboxSwitch()).toBeChecked();

    // The write landed and the next board snapshot carries it: the optimistic
    // overlay drops and the switch is driven by the ticket again.
    const confirmed = { ...working, keep_sandbox: true };
    rerender(
      <TicketDetail ticket={confirmed} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />,
    );
    expect(sandboxSwitch()).toBeChecked();
  });
});

// The manual sandbox overrides: Kill (destroy this ticket's workspace, leave the
// ticket where it is) and Move to a new sandbox (rebind and start the work over
// somewhere clean). Both are irreversible, so both are two-tap; both are scoped
// to a ticket that actually has a sandbox.
describe('TicketDetail sandbox controls', () => {
  const blocked = makeTicket({
    id: 't-blocked',
    title: 'Stuck work',
    body: 'body',
    state: 'blocked',
    priority: 1,
    createdAt: '2026-07-01T00:00:00Z',
    updatedAt: '2026-07-01T00:00:00Z',
    blockedReason: 'the working tree is corrupted',
  });

  function killButton(): HTMLElement {
    return screen.getByRole('button', { name: /kill sandbox|destroy it/i });
  }
  function moveButton(): HTMLElement {
    return screen.getByRole('button', { name: /move to a new sandbox|start over there/i });
  }

  it('is absent by default — a read-only sheet offers no override', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: /kill sandbox/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /move to a new sandbox/i })).toBeNull();
  });

  it('is absent on a ticket with no sandbox, even when wired', () => {
    const proposal = makeTicket({
      id: 't-prop',
      title: 'Proposed work',
      body: 'body',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(
      <TicketDetail
        ticket={proposal}
        onClose={vi.fn()}
        onKillSandbox={vi.fn()}
        onReassignSandbox={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: /kill sandbox/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /move to a new sandbox/i })).toBeNull();
  });

  it('takes two taps to kill: the first arms, the second fires with the ticket id', () => {
    const onKillSandbox = vi.fn();
    render(<TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={onKillSandbox} />);

    fireEvent.click(killButton());
    expect(onKillSandbox).not.toHaveBeenCalled();
    // Armed: the label names the consequence rather than the action.
    expect(killButton()).toHaveTextContent(/tap to confirm/i);

    fireEvent.click(killButton());
    expect(onKillSandbox).toHaveBeenCalledWith('t-42');
    // Disarmed again, so a third stray tap can't kill the replacement sandbox.
    expect(killButton()).toHaveTextContent(/kill sandbox/i);
  });

  it('takes two taps to move, and reports the ticket id', () => {
    const onReassignSandbox = vi.fn();
    render(
      <TicketDetail ticket={blocked} onClose={vi.fn()} onReassignSandbox={onReassignSandbox} />,
    );

    fireEvent.click(moveButton());
    expect(onReassignSandbox).not.toHaveBeenCalled();
    fireEvent.click(moveButton());
    expect(onReassignSandbox).toHaveBeenCalledWith('t-blocked');
  });

  it('arming one override disarms the other — only one tap is ever live', () => {
    const onKillSandbox = vi.fn();
    const onReassignSandbox = vi.fn();
    render(
      <TicketDetail
        ticket={working}
        onClose={vi.fn()}
        onKillSandbox={onKillSandbox}
        onReassignSandbox={onReassignSandbox}
      />,
    );

    fireEvent.click(killButton());
    fireEvent.click(moveButton()); // arms Move, disarms Kill
    expect(onKillSandbox).not.toHaveBeenCalled();
    expect(onReassignSandbox).not.toHaveBeenCalled();

    // A tap on Kill now only re-arms it — it does not fire the stale arming.
    fireEvent.click(killButton());
    expect(onKillSandbox).not.toHaveBeenCalled();
  });

  it('disarms when the sheet moves to another ticket, so a tap can’t hit the wrong sandbox', () => {
    const onKillSandbox = vi.fn();
    const { rerender } = render(
      <TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={onKillSandbox} />,
    );
    fireEvent.click(killButton());
    expect(killButton()).toHaveTextContent(/tap to confirm/i);

    rerender(<TicketDetail ticket={blocked} onClose={vi.fn()} onKillSandbox={onKillSandbox} />);
    expect(killButton()).toHaveTextContent(/kill sandbox/i);
    fireEvent.click(killButton());
    expect(onKillSandbox).not.toHaveBeenCalled();
  });

  it('names the sandbox’s live session status, so the kill is a considered one', () => {
    render(
      <TicketDetail
        ticket={working}
        onClose={vi.fn()}
        onKillSandbox={vi.fn()}
        sandboxStatus="errored"
      />,
    );
    expect(screen.getByText(/sandbox is failing/i)).toBeInTheDocument();
  });

  it('says so when the sandbox reports nothing at all', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={vi.fn()} />);
    expect(screen.getByText(/sandbox is not reporting/i)).toBeInTheDocument();
  });

  it('disables Move — and says why — when there is no free sandbox to move to', () => {
    const onReassignSandbox = vi.fn();
    render(
      <TicketDetail
        ticket={working}
        onClose={vi.fn()}
        onKillSandbox={vi.fn()}
        onReassignSandbox={onReassignSandbox}
        canReassign={false}
      />,
    );

    expect(moveButton()).toBeDisabled();
    fireEvent.click(moveButton());
    expect(onReassignSandbox).not.toHaveBeenCalled();
    expect(screen.getByText(/none free to move this ticket to/i)).toBeInTheDocument();
    // Kill is still offered: with nowhere to move, recycling in place is the
    // remaining escape from a corrupted workspace.
    expect(killButton()).toBeEnabled();
  });
});
