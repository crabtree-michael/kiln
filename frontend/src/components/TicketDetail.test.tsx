// TicketDetail sheet: shows a ticket's full record and is dismissable — never a
// trap (07 §7–§8). It renders as a `vaul` bottom sheet, so its content and scrim
// portal to document.body (query via `screen`/`document`, not the render
// container) and dismissal — Escape, scrim, drag — is Vaul's concern, routed to
// onClose via onOpenChange. The header carries no × of its own, so those three
// paths are the whole of it: we test the content and the Escape wiring reaching
// onClose; the drag physics are the library's and are not re-tested.
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

  it('rises from the bottom edge by default — the phone sheet is what it is (07 §7)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    // Vaul records the direction it was handed on the panel, and derives the
    // entrance, the closed transform and the drag axis from it. Omitting
    // `placement` must leave the mobile sheet exactly as it was.
    expect(screen.getByRole('dialog').getAttribute('data-vaul-drawer-direction')).toBe('bottom');
  });

  it('slides in from the right when placed there (the desk’s side panel, 13 D7a)', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} placement="right" />);

    expect(screen.getByRole('dialog').getAttribute('data-vaul-drawer-direction')).toBe('right');
  });

  it('is dismissable from the right-hand placement too — the edge changes, nothing else does', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} placement="right" />);

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('carries no × in the header — dismissal is the scrim, Escape and the drag', () => {
    // The header is the heading and nothing else: a × was a fourth way out on a
    // sheet that already had three, and the column it held is what forced the
    // title to render small. Dismissal is unchanged (the Escape test below, and
    // the scrim/drag that are Vaul's own).
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
    expect(screen.getByRole('dialog').textContent).not.toContain('×');
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

  it('draws Accept as a glyph and keeps the word only as its name', () => {
    // Accept is an icon button now — the check, on the mic's disc (see
    // TicketDetail.action-icons.test.ts), with nothing spelled out beside it. The
    // aria-label is therefore the ONLY place the word survives: lose it and the
    // one headline decision on the sheet goes unnamed to a screen reader, and
    // every `getByRole('button', { name: 'Accept' })` in this file stops matching.
    const shaping = makeTicket({
      id: 't-shape',
      title: 'A shaped proposal',
      body: 'body',
      state: 'shaping',
      priority: 2,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={shaping} onClose={vi.fn()} onAccept={vi.fn()} />);

    const accept = within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' });
    expect(accept.getAttribute('aria-label')).toBe('Accept');
    expect(accept.textContent).toBe('');
    expect(accept.querySelector('svg')).not.toBeNull();
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
      const cluster = within(screen.getByRole('dialog'))
        .getByText('mic')
        .closest('[data-role="ticket-detail-voice-actions"]');
      expect(cluster).not.toBeNull();
      // At rest it is the row's lead: the auto margin that pins it left.
      expect(cluster).toHaveAttribute('data-position', 'lead');
      // It shares the footer with the trailing Accept action.
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' }),
      ).toBeInTheDocument();
    });

    it('renders the voice control on a non-shaping ticket too (the unified surface)', () => {
      // The mic is no longer shaping-only: it is the one communication surface
      // shared across every ticket state, so a working ticket shows it as well.
      render(<TicketDetail ticket={working} onClose={vi.fn()} voiceControl={mic} />);
      const cluster = within(screen.getByRole('dialog'))
        .getByText('mic')
        .closest('[data-role="ticket-detail-voice-actions"]');
      expect(cluster).not.toBeNull();
    });

    it('renders no voice cluster when the caller wires no voice control (read-only inspection)', () => {
      render(<TicketDetail ticket={proposal} onClose={vi.fn()} onAccept={vi.fn()} />);
      expect(within(screen.getByRole('dialog')).queryByText('mic')).toBeNull();
      expect(document.querySelector('[data-role="ticket-detail-voice-actions"]')).toBeNull();
    });

    // A live voice session rearranges the footer: the cluster crosses to the
    // trailing group (so the mic sits beside the Send and × its own node renders)
    // and Accept — whose slot Send takes — stands down until the session ends.
    // TicketDetail is voice-store-agnostic, so the reading arrives as a prop.
    it('moves the voice cluster to the trailing group and withholds Accept while speaking', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          voiceControl={mic}
          voiceActive
        />,
      );
      const dialog = screen.getByRole('dialog');
      const cluster = within(dialog)
        .getByText('mic')
        .closest('[data-role="ticket-detail-voice-actions"]');
      expect(cluster).toHaveAttribute('data-position', 'trail');
      expect(within(dialog).queryByRole('button', { name: 'Accept' })).toBeNull();
    });

    // Delete is the other half of the proposal's accept-or-discard pair, so it
    // stands down with Accept: a trash can has no business appearing the moment
    // someone starts speaking to a proposal they are reshaping.
    it('withholds Delete on a proposal while speaking', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          onDelete={vi.fn()}
          voiceControl={mic}
          voiceActive
        />,
      );
      expect(
        within(screen.getByRole('dialog')).queryByRole('button', { name: 'Delete' }),
      ).toBeNull();
    });

    it('brings Delete back on a proposal the moment the session ends', () => {
      const { rerender } = render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          onDelete={vi.fn()}
          voiceControl={mic}
          voiceActive
        />,
      );
      expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
      rerender(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          onDelete={vi.fn()}
          voiceControl={mic}
          voiceActive={false}
        />,
      );
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }),
      ).toBeInTheDocument();
    });

    it('keeps Delete on a proposal when no voice control is wired (a read-only sheet)', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          onDelete={vi.fn()}
          voiceActive
        />, // no mic
      );
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' }),
      ).toBeInTheDocument();
    });

    it('brings Accept back in its normal place the moment the session ends', () => {
      const { rerender } = render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          voiceControl={mic}
          voiceActive
        />,
      );
      expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
      rerender(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onAccept={vi.fn()}
          voiceControl={mic}
          voiceActive={false}
        />,
      );
      const dialog = screen.getByRole('dialog');
      expect(within(dialog).getByRole('button', { name: 'Accept' })).toBeInTheDocument();
      expect(
        within(dialog).getByText('mic').closest('[data-role="ticket-detail-voice-actions"]'),
      ).toHaveAttribute('data-position', 'lead');
    });

    // A blocked ticket is not a proposal: speaking there is how the user unblocks
    // the work, so neither of its secondaries moves.
    it('leaves a blocked ticket’s Delete and Poke alone while speaking', () => {
      render(
        <TicketDetail
          ticket={makeTicket({
            id: 't-blocked',
            title: 'Stuck',
            body: 'body',
            state: 'blocked',
            priority: 2,
            createdAt: '2026-07-01T00:00:00Z',
            updatedAt: '2026-07-01T00:00:00Z',
          })}
          onClose={vi.fn()}
          onDelete={vi.fn()}
          onPoke={vi.fn()}
          voiceControl={mic}
          voiceActive
        />,
      );
      const dialog = screen.getByRole('dialog');
      expect(within(dialog).getByRole('button', { name: 'Delete' })).toBeInTheDocument();
      expect(within(dialog).getByRole('button', { name: 'Poke' })).toBeInTheDocument();
    });

    it('ignores voiceActive when no voice control is wired (a read-only sheet keeps Accept)', () => {
      render(
        <TicketDetail ticket={proposal} onClose={vi.fn()} onAccept={vi.fn()} voiceActive />, // no mic
      );
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Accept' }),
      ).toBeInTheDocument();
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

    it('offers Poke on a working ticket, and fires it with the id', () => {
      const onPoke = vi.fn();
      render(<TicketDetail ticket={working} onClose={vi.fn()} onPoke={onPoke} />);

      const poke = within(screen.getByRole('dialog')).getByRole('button', { name: 'Poke' });
      // Icon-only: the 👉 is the poke's whole visible signal (matching the feed poke
      // card), with no text label — the accessible name comes from aria-label="Poke".
      expect(poke).toHaveTextContent('👉');
      fireEvent.click(poke);

      expect(onPoke).toHaveBeenCalledWith('t-42');
    });

    it('keeps Poke on a working ticket whose agent is mid-turn', () => {
      // The old `agentIdle` gate hid the button while the session read `building`,
      // which is most of an in-progress ticket's life — and "the agent looks busy"
      // is not the same as "the agent needs nothing". A streaming session is now
      // just as pokeable; the brain decides what to do with the intent.
      render(
        <TicketDetail
          ticket={working}
          onClose={vi.fn()}
          onPoke={vi.fn()}
          sandboxStatus="building"
        />,
      );
      expect(
        within(screen.getByRole('dialog')).getByRole('button', { name: 'Poke' }),
      ).toBeInTheDocument();
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

// Every sandbox decision for a ticket now lives behind one gear beside the
// lifecycle badge: the save toggle (formerly a checkbox at the foot of the
// body), Re-create (formerly "Kill sandbox") and Move (formerly "Move to a new
// sandbox"), all of which used to be a row of buttons pushing the ticket's own
// text down the screen. The menu is closed until the gear is tapped, so every
// test here opens it first — and closed means `aria-hidden`, which is why a
// closed menu's items are absent from these role queries rather than merely
// invisible.
describe('TicketDetail — sandbox menu', () => {
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
  const proposal = makeTicket({
    id: 't-prop',
    title: 'Proposed work',
    body: 'body',
    state: 'shaping',
    priority: 1,
    createdAt: '2026-07-01T00:00:00Z',
    updatedAt: '2026-07-01T00:00:00Z',
  });
  const done = makeTicket({
    id: 't-done',
    title: 'Shipped work',
    body: 'body',
    state: 'done',
    priority: 1,
    createdAt: '2026-07-01T00:00:00Z',
    updatedAt: '2026-07-02T00:00:00Z',
  });

  /** The gear itself, inside the open sheet. */
  function gear(): HTMLElement {
    return within(screen.getByRole('dialog')).getByRole('button', { name: 'Sandbox options' });
  }
  function openMenu(): void {
    fireEvent.click(gear());
  }
  function keepToggle(): HTMLElement {
    return screen.getByRole('menuitemcheckbox', { name: /save sandbox when done/i });
  }
  function recreateItem(): HTMLElement {
    return screen.getByRole('menuitem', { name: /re-create sandbox/i });
  }
  function moveItem(): HTMLElement {
    return screen.getByRole('menuitem', { name: /move to free sandbox/i });
  }

  it('is absent by default — read-only inspection offers no sandbox controls', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Sandbox options' })).toBeNull();
  });

  it('keeps its items out of the page until the gear is tapped', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);

    expect(gear()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('menuitemcheckbox')).toBeNull();

    openMenu();

    expect(gear()).toHaveAttribute('aria-expanded', 'true');
    expect(keepToggle()).toBeInTheDocument();
  });

  it('leads the status row, on the title’s left edge, ahead of the lifecycle badge', () => {
    render(<TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);
    const row = gear().closest('[data-role="ticket-detail-status-row"]');
    expect(row).not.toBeNull();
    expect(row?.querySelector('[data-role="ticket-detail-status"]')?.textContent).toContain(
      'In progress',
    );
    // The gear comes first in the row — that (plus the row starting at the
    // heading's left edge) is what left-aligns it with the title. jsdom does no
    // layout, so DOM order is what there is to assert; the geometry rides on the
    // CSS assertion in TicketDetail.header-layout.test.ts.
    expect(row?.firstElementChild?.getAttribute('data-role')).toBe('detail-sandbox-menu');
  });

  // The work is over on a done ticket, so every item behind the gear is spent:
  // the two overrides have no sandbox to act on, and the save toggle decides what
  // happens to a sandbox when the ticket finishes — which it already has. The
  // gear goes with them rather than opening onto a choice that changes nothing.
  it('is absent on a done ticket, however much is wired', () => {
    render(
      <TicketDetail
        ticket={done}
        onClose={vi.fn()}
        onSetKeepSandbox={vi.fn()}
        onKillSandbox={vi.fn()}
        onReassignSandbox={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: 'Sandbox options' })).toBeNull();
  });

  it('leaves the rest of a done ticket’s header alone — the badge still reads Done', () => {
    render(<TicketDetail ticket={done} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);
    const dialog = screen.getByRole('dialog');
    const status = within(dialog).getByText('Done').closest('[data-role="ticket-detail-status"]');
    expect(status).not.toBeNull();
    // The status row survives the gear's departure, with the badge as its only
    // child — a done ticket's header is otherwise unchanged.
    const row = status?.closest('[data-role="ticket-detail-status-row"]');
    expect(row?.children).toHaveLength(1);
  });

  describe('save sandbox when done', () => {
    it('reflects the ticket’s own keep_sandbox and reports a turn-on with the id', () => {
      const onSetKeepSandbox = vi.fn();
      render(
        <TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />,
      );
      openMenu();

      expect(keepToggle()).toHaveAttribute('aria-checked', 'false');
      fireEvent.click(keepToggle());

      expect(onSetKeepSandbox).toHaveBeenCalledWith('t-42', true);
      // Optimistic: the checkmark lands at once, without waiting for the board
      // snapshot to come back over the stream.
      expect(keepToggle()).toHaveAttribute('aria-checked', 'true');
      // And the menu stays open, so the checkmark is something the user sees.
      expect(gear()).toHaveAttribute('aria-expanded', 'true');
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
      openMenu();

      expect(keepToggle()).toHaveAttribute('aria-checked', 'true');
      fireEvent.click(keepToggle());

      expect(onSetKeepSandbox).toHaveBeenCalledWith('t-keep', false);
      expect(keepToggle()).toHaveAttribute('aria-checked', 'false');
    });

    it('is offered on a shaping proposal too — the choice predates the sandbox', () => {
      render(<TicketDetail ticket={proposal} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);
      openMenu();
      expect(keepToggle()).toBeInTheDocument();
    });

    it('defers to the board snapshot once it catches up', () => {
      const onSetKeepSandbox = vi.fn();
      const { rerender } = render(
        <TicketDetail ticket={working} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />,
      );
      openMenu();
      fireEvent.click(keepToggle());
      expect(keepToggle()).toHaveAttribute('aria-checked', 'true');

      // The write landed and the next board snapshot carries it: the optimistic
      // overlay drops and the toggle is driven by the ticket again.
      const confirmed = { ...working, keep_sandbox: true };
      rerender(
        <TicketDetail ticket={confirmed} onClose={vi.fn()} onSetKeepSandbox={onSetKeepSandbox} />,
      );
      // Same ticket, so the menu is still open — only where the value comes from
      // has changed.
      expect(keepToggle()).toHaveAttribute('aria-checked', 'true');
    });
  });

  // The manual overrides: Re-create (destroy this ticket's workspace and bring a
  // fresh one up on the same slot) and Move to free sandbox (rebind and start the
  // work over somewhere clean). Both destroy in-progress work irreversibly, so
  // both are gated behind a confirm that says so; both are scoped to a ticket
  // that actually has a sandbox.
  describe('the destructive overrides', () => {
    it('are absent on a ticket with no sandbox, even when wired', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onKillSandbox={vi.fn()}
          onReassignSandbox={vi.fn()}
        />,
      );
      // Nothing else is wired either, so there is no gear at all to open.
      expect(screen.queryByRole('button', { name: 'Sandbox options' })).toBeNull();
    });

    it('leaves the ticket’s own controls alone: a proposal’s gear holds only the toggle', () => {
      render(
        <TicketDetail
          ticket={proposal}
          onClose={vi.fn()}
          onSetKeepSandbox={vi.fn()}
          onKillSandbox={vi.fn()}
          onReassignSandbox={vi.fn()}
        />,
      );
      openMenu();
      expect(keepToggle()).toBeInTheDocument();
      expect(screen.queryByRole('menuitem', { name: /re-create sandbox/i })).toBeNull();
      expect(screen.queryByRole('menuitem', { name: /move to free sandbox/i })).toBeNull();
    });

    it('re-creates the sandbox once the confirm is accepted, and closes the menu', () => {
      const onKillSandbox = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
      render(<TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={onKillSandbox} />);
      openMenu();

      fireEvent.click(recreateItem());

      // The confirm names what is lost — an ongoing turn, not just a workspace.
      expect(confirm.mock.calls[0]?.[0]).toMatch(/ongoing work is killed/i);
      expect(onKillSandbox).toHaveBeenCalledWith('t-42');
      expect(gear()).toHaveAttribute('aria-expanded', 'false');
      confirm.mockRestore();
    });

    it('does not re-create the sandbox when the confirm is dismissed', () => {
      const onKillSandbox = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
      render(<TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={onKillSandbox} />);
      openMenu();

      fireEvent.click(recreateItem());

      expect(confirm).toHaveBeenCalledTimes(1);
      expect(onKillSandbox).not.toHaveBeenCalled();
      confirm.mockRestore();
    });

    it('moves the ticket to a free sandbox once the confirm is accepted', () => {
      const onReassignSandbox = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
      render(
        <TicketDetail ticket={blocked} onClose={vi.fn()} onReassignSandbox={onReassignSandbox} />,
      );
      openMenu();

      fireEvent.click(moveItem());

      expect(confirm.mock.calls[0]?.[0]).toMatch(/ongoing work in the current sandbox is lost/i);
      expect(onReassignSandbox).toHaveBeenCalledWith('t-blocked');
      confirm.mockRestore();
    });

    it('does not move the ticket when the confirm is dismissed', () => {
      const onReassignSandbox = vi.fn();
      const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
      render(
        <TicketDetail ticket={blocked} onClose={vi.fn()} onReassignSandbox={onReassignSandbox} />,
      );
      openMenu();

      fireEvent.click(moveItem());

      expect(onReassignSandbox).not.toHaveBeenCalled();
      confirm.mockRestore();
    });

    it('drops Move entirely when there is no free sandbox to move to', () => {
      render(
        <TicketDetail
          ticket={working}
          onClose={vi.fn()}
          onKillSandbox={vi.fn()}
          onReassignSandbox={vi.fn()}
          canReassign={false}
        />,
      );
      openMenu();

      expect(screen.queryByRole('menuitem', { name: /move to free sandbox/i })).toBeNull();
      // Re-create is still offered: with nowhere to move, recycling in place is
      // the remaining escape from a corrupted workspace.
      expect(recreateItem()).toBeInTheDocument();
    });
  });

  describe('the sandbox’s live status', () => {
    it('heads the menu, so the re-create is a considered one', () => {
      render(
        <TicketDetail
          ticket={working}
          onClose={vi.fn()}
          onKillSandbox={vi.fn()}
          sandboxStatus="errored"
        />,
      );
      openMenu();
      expect(screen.getByText(/sandbox is failing/i)).toBeInTheDocument();
    });

    it('says so when the sandbox reports nothing at all', () => {
      render(<TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={vi.fn()} />);
      openMenu();
      expect(screen.getByText(/sandbox is not reporting/i)).toBeInTheDocument();
    });

    it('is absent on a ticket with no sandbox behind it', () => {
      render(<TicketDetail ticket={proposal} onClose={vi.fn()} onSetKeepSandbox={vi.fn()} />);
      openMenu();
      expect(screen.queryByText(/sandbox is/i)).toBeNull();
    });
  });

  it('closes when the sheet moves to another ticket, so it can’t act on the wrong sandbox', () => {
    const onKillSandbox = vi.fn();
    const { rerender } = render(
      <TicketDetail ticket={working} onClose={vi.fn()} onKillSandbox={onKillSandbox} />,
    );
    openMenu();
    expect(recreateItem()).toBeInTheDocument();

    rerender(<TicketDetail ticket={blocked} onClose={vi.fn()} onKillSandbox={onKillSandbox} />);

    expect(gear()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('menuitem', { name: /re-create sandbox/i })).toBeNull();
  });

  // Escape belongs to the topmost layer. The sheet is a Radix dialog listening
  // for the key in the capture phase on `document`, so the menu has to get there
  // first (capture on `window`) or one press would take the whole sheet away
  // instead of closing the dropdown over it.
  it('closes on Escape without dismissing the sheet under it', () => {
    const onClose = vi.fn();
    render(<TicketDetail ticket={working} onClose={onClose} onSetKeepSandbox={vi.fn()} />);
    openMenu();

    fireEvent.keyDown(document, { key: 'Escape' });

    expect(gear()).toHaveAttribute('aria-expanded', 'false');
    expect(onClose).not.toHaveBeenCalled();

    // …and with the menu closed the key means what it always did.
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('is folded away while the ticket’s text is being edited', () => {
    render(
      <TicketDetail
        ticket={proposal}
        onClose={vi.fn()}
        onSetKeepSandbox={vi.fn()}
        onEditText={vi.fn()}
      />,
    );
    expect(gear()).toBeInTheDocument();

    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Edit description' }),
    );

    expect(screen.queryByRole('button', { name: 'Sandbox options' })).toBeNull();
  });
});
