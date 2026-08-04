// The ticket detail sheet's direct text edit: pressing the rendered body turns
// the title and body into fields in place, and Save writes the typed text to the
// board without a brain pass. These cover the gating (which states offer it, and
// that an unwired sheet is unchanged), the entry point itself (the body, the
// keyboard stand-in, and the two presses inside a body that must NOT open the
// editor), the patch the sheet actually emits (only what changed), the
// blank-title guard, and the optimistic hold that keeps the saved wording on
// screen until the board snapshot catches up.
//
// Like the rest of the sheet's tests, content portals to document.body — query
// via `screen`/`document`, not the render container.
import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { TicketDetail, type TicketTextEdit } from '@/components/TicketDetail';
import type { Ticket } from '@/components/TicketCard';
import { makeTicket } from '@/test/fixtures';

function ticketIn(state: Ticket['state']): Ticket {
  return makeTicket({
    id: 't-42',
    title: 'Add teh login redirect',
    body: 'Send the user to /app after sign-in.',
    state,
    priority: 1,
    createdAt: '2026-07-01T00:00:00Z',
    updatedAt: '2026-07-01T00:00:00Z',
    ...(state === 'blocked' ? { blockedReason: 'Which redirect?' } : {}),
  });
}

/** The pressable body region — the sheet's only pointer route into edit mode.
 * Null on a ticket the board would refuse the write for, which is exactly what
 * the gating tests assert. */
function bodyTarget(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[data-role="detail-body-edit-target"]');
}

/** Presses the body and returns the two fields it swapped itself for. */
function startEditing(): { title: HTMLElement; body: HTMLElement } {
  const target = bodyTarget();
  if (target === null) {
    throw new Error('the body is not pressable — no [data-role="detail-body-edit-target"]');
  }
  fireEvent.click(target);
  return { title: screen.getByLabelText('Title'), body: screen.getByLabelText('Description') };
}

describe('TicketDetail — direct text edit', () => {
  // There is no pencil anywhere on the sheet any more; the body is the whole
  // affordance. Asserted by name so a reintroduced icon fails here.
  it('shows no separate edit control', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(document.querySelector('[data-role="ticket-detail-edit"]')).toBeNull();
  });

  it('leaves the body inert when the caller did not wire the write', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} />);

    expect(bodyTarget()).toBeNull();
    expect(screen.queryByRole('button', { name: 'Edit description' })).toBeNull();
  });

  // Shaping is where wording gets refined before work starts; ready is queued
  // but not yet briefed to anyone. Both are still editable board-side.
  it.each<Ticket['state']>(['shaping', 'ready'])(
    'makes the body pressable on a %s ticket',
    (state) => {
      render(<TicketDetail ticket={ticketIn(state)} onClose={vi.fn()} onEditText={vi.fn()} />);

      expect(bodyTarget()).not.toBeNull();
    },
  );

  // Past the backlog the text is what an agent was actually briefed with, and
  // the board refuses the write — so the sheet must not offer it either. The
  // body still renders; it just does nothing when pressed.
  it.each<Ticket['state']>(['working', 'blocked', 'done'])(
    'leaves the body inert on a %s ticket',
    (state) => {
      render(<TicketDetail ticket={ticketIn(state)} onClose={vi.fn()} onEditText={vi.fn()} />);

      expect(bodyTarget()).toBeNull();
      expect(screen.queryByRole('button', { name: 'Edit description' })).toBeNull();
      expect(screen.getByText('Send the user to /app after sign-in.')).toBeInTheDocument();
      fireEvent.click(screen.getByText('Send the user to /app after sign-in.'));
      expect(screen.queryByLabelText('Description')).toBeNull();
    },
  );

  // The pointer route is a press on the words themselves — not on a wrapper the
  // user can't see, so pressing a paragraph *inside* the body must work too.
  it('enters edit mode when the rendered Markdown itself is pressed', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    fireEvent.click(screen.getByText('Send the user to /app after sign-in.'));

    expect(screen.getByLabelText('Description')).toHaveValue(
      'Send the user to /app after sign-in.',
    );
  });

  // With the pencil gone, a keyboard user needs a real control — the off-screen
  // stand-in that a tab brings into view.
  it('offers a keyboard route into edit mode', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit description' }));

    expect(screen.getByLabelText('Title')).toHaveValue('Add teh login redirect');
    // It is the way *in*: once the fields are up, Cancel/Save are the way out
    // and a second copy of it would just be noise behind the textarea.
    expect(screen.queryByRole('button', { name: 'Edit description' })).toBeNull();
  });

  // A reference in the body is still a reference. Following one is not a
  // request to rewrite the sentence it sits in.
  it('does not enter edit mode when a link in the body is followed', () => {
    const ticket = makeTicket({
      id: 't-42',
      title: 'Add teh login redirect',
      // A hash target, so jsdom treats the click as a navigation it implements
      // (an external href logs a "not implemented" error). What the guard reads
      // is the <a> itself, not where it points.
      body: 'See [the RFC](#rfc) first.',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={ticket} onClose={vi.fn()} onEditText={vi.fn()} />);

    fireEvent.click(screen.getByRole('link', { name: 'the RFC' }));

    expect(screen.queryByLabelText('Description')).toBeNull();
  });

  // Selecting words to quote ends in a click on the body; swapping the
  // selection out for a textarea the instant the user lets go would make the
  // ticket impossible to copy from.
  it('does not enter edit mode when the press ends a text selection', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);
    const paragraph = screen.getByText('Send the user to /app after sign-in.');
    const selection = window.getSelection();
    expect(selection).not.toBeNull();
    selection?.selectAllChildren(paragraph);

    fireEvent.click(paragraph);

    expect(screen.queryByLabelText('Description')).toBeNull();
    selection?.removeAllRanges();
  });

  // An empty body renders nothing at all — with no pencil that would leave an
  // editable ticket with nothing to press.
  it('offers a prompt to press when the body is empty', () => {
    const ticket = makeTicket({
      id: 't-42',
      title: 'Add teh login redirect',
      body: '',
      state: 'shaping',
      priority: 1,
      createdAt: '2026-07-01T00:00:00Z',
      updatedAt: '2026-07-01T00:00:00Z',
    });
    render(<TicketDetail ticket={ticket} onClose={vi.fn()} onEditText={vi.fn()} />);

    fireEvent.click(screen.getByText('Add a description'));

    expect(screen.getByLabelText('Description')).toHaveValue('');
  });

  // The body is what the user pressed, so it is where the caret belongs — at
  // the end, so adding a line doesn't land in front of what is already there.
  it('puts the caret at the end of the body', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    const { body } = startEditing();

    expect(body).toHaveFocus();
    expect(body).toBeInstanceOf(HTMLTextAreaElement);
    if (body instanceof HTMLTextAreaElement) {
      expect(body.selectionStart).toBe(body.value.length);
    }
  });

  it('seeds the fields from the ticket and swaps the rendered body for them', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    const { title, body } = startEditing();

    expect(title).toHaveValue('Add teh login redirect');
    expect(body).toHaveValue('Send the user to /app after sign-in.');
    // The rendered Markdown is what the textarea replaced — the body region now
    // holds the field alone, not the paragraphs react-markdown produced.
    const region = screen.getByRole('dialog').querySelector('[data-role="ticket-detail-body"]');
    expect(region?.querySelector('p')).toBeNull();
  });

  // Radix names the dialog by the <Drawer.Title>, so the title element has to
  // stay in the tree while the field stands in for it visually.
  it('keeps the dialog named by its title while editing', () => {
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={vi.fn()} />);

    startEditing();

    expect(screen.getByRole('dialog', { name: 'Add teh login redirect' })).toBeInTheDocument();
  });

  it('saves the edited title and body, and leaves the sheet open', () => {
    const onEditText = vi.fn<(id: string, patch: TicketTextEdit) => void>();
    const onClose = vi.fn();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={onClose} onEditText={onEditText} />);

    const { title, body } = startEditing();
    fireEvent.change(title, { target: { value: 'Add the login redirect' } });
    fireEvent.change(body, { target: { value: 'Land on /app, not /.' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onEditText).toHaveBeenCalledTimes(1);
    expect(onEditText).toHaveBeenCalledWith('t-42', {
      title: 'Add the login redirect',
      body: 'Land on /app, not /.',
    });
    // A correction should leave the user looking at the corrected ticket, with
    // the body pressable again for the next one.
    expect(onClose).not.toHaveBeenCalled();
    expect(bodyTarget()).not.toBeNull();
  });

  // The patch is a patch: an untouched field must not be sent, or an edit to one
  // could overwrite the other with the text the sheet happened to open with.
  it('sends only the field that changed', () => {
    const onEditText = vi.fn<(id: string, patch: TicketTextEdit) => void>();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={onEditText} />);

    const { title } = startEditing();
    fireEvent.change(title, { target: { value: 'Add the login redirect' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onEditText).toHaveBeenCalledWith('t-42', { title: 'Add the login redirect' });
  });

  // Closing the editor is the whole effect of an untouched save — a write here
  // would fan a board.updated out to every open client for nothing.
  it('writes nothing when the text is unchanged', () => {
    const onEditText = vi.fn();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={onEditText} />);

    startEditing();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onEditText).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('Title')).toBeNull();
  });

  it('discards the draft on Cancel', () => {
    const onEditText = vi.fn();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={onEditText} />);

    const { title } = startEditing();
    fireEvent.change(title, { target: { value: 'Something else entirely' } });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onEditText).not.toHaveBeenCalled();
    expect(screen.getByRole('heading', { name: 'Add teh login redirect' })).toBeInTheDocument();
    // Re-entering starts from the ticket again, not the abandoned draft.
    expect(startEditing().title).toHaveValue('Add teh login redirect');
  });

  // A ticket must keep a name — the server refuses a blank title, so the button
  // is dead rather than inviting a round-trip that 400s.
  it('disables Save while the title is blank', () => {
    const onEditText = vi.fn();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={onEditText} />);

    const { title } = startEditing();
    fireEvent.change(title, { target: { value: '   ' } });

    const save = screen.getByRole('button', { name: 'Save' });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(onEditText).not.toHaveBeenCalled();
  });

  // An empty description is a real choice, unlike an empty title.
  it('allows clearing the body', () => {
    const onEditText = vi.fn<(id: string, patch: TicketTextEdit) => void>();
    render(<TicketDetail ticket={ticketIn('shaping')} onClose={vi.fn()} onEditText={onEditText} />);

    const { body } = startEditing();
    fireEvent.change(body, { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(onEditText).toHaveBeenCalledWith('t-42', { body: '' });
  });

  // Mid-edit is no moment to be offered Accept, Delete or a mic — the two ways
  // out of the mode are the only controls that should be reachable.
  it('replaces the state actions with Cancel/Save while editing', () => {
    render(
      <TicketDetail
        ticket={ticketIn('shaping')}
        onClose={vi.fn()}
        onEditText={vi.fn()}
        onAccept={vi.fn()}
        onDelete={vi.fn()}
        voiceControl={<button type="button">Mic</button>}
      />,
    );

    expect(screen.getByRole('button', { name: 'Accept' })).toBeInTheDocument();
    startEditing();

    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Mic' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save' })).toBeInTheDocument();
  });

  describe('optimistic hold', () => {
    it('keeps the saved text on screen while the board snapshot still has the old', () => {
      const stale = ticketIn('shaping');
      const { rerender } = render(
        <TicketDetail ticket={stale} onClose={vi.fn()} onEditText={vi.fn()} />,
      );

      const { title } = startEditing();
      fireEvent.change(title, { target: { value: 'Add the login redirect' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      // The board has not caught up yet — the sheet must not flicker back.
      rerender(<TicketDetail ticket={stale} onClose={vi.fn()} onEditText={vi.fn()} />);
      expect(screen.getByRole('heading', { name: 'Add the login redirect' })).toBeInTheDocument();
    });

    // A write that never lands must not leave the sheet showing text the board
    // does not have; the time-box is what self-heals it.
    it('reverts to the board snapshot when the write never lands', () => {
      vi.useFakeTimers();
      try {
        const stale = ticketIn('shaping');
        render(<TicketDetail ticket={stale} onClose={vi.fn()} onEditText={vi.fn()} />);

        const { title } = startEditing();
        fireEvent.change(title, { target: { value: 'Add the login redirect' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));

        act(() => {
          vi.advanceTimersByTime(5000);
        });

        expect(screen.getByRole('heading', { name: 'Add teh login redirect' })).toBeInTheDocument();
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
