// Activity-row tests (08 §4 / §F): the activity surfaces — the thinking spinner
// (6a, only when the stack is empty), the action toast (6b, carrying data-verb),
// and the persistent say pill — each rendered from the stack the store hands
// down. Multiple live toasts stack. DOM-structure snapshots stand in for pixel
// snapshots (07 §9 D4).
//
// Interaction (08 §4): no pill carries a close control in any state. A board
// `toast` taps straight through to its linked ticket's detail view and dismisses
// itself. A `say` pill — and an orphan toast with no ticket to route to — instead
// expands in place on the first tap when its clamp is actually hiding something
// (pausing its auto-dismiss timer), and dismisses on the next; with nothing more
// to reveal, the first tap dismisses outright.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ActivityRow } from '@/components/ActivityRow';
import { KILN_WORDS } from '@/components/kiln-words';
import type { ActivityToast } from '@/stores/activity-context';

const noop = (): void => {
  /* inert dismiss for render tests */
};

/**
 * jsdom performs no layout, so a pill's `scrollHeight`/`clientHeight` are both 0
 * and its 2-line clamp never registers as overflowing — every pill would read as
 * "nothing more to show". Fake a clamped box (content taller than its clamp) for
 * the cases that exercise tap-to-expand; `restoreAllMocks` puts the layout-free
 * default back, which is exactly the "fits, so tapping dismisses" case (mirrors
 * PrimaryScreenView.test's helper of the same name).
 */
function fakeClampedOverflow(): void {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(200);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(80);
}

function toast(id: number, pill: ActivityToast['pill']): ActivityToast {
  return { id, pill };
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ActivityRow', () => {
  it('renders the thinking indicator when thinking with an empty stack (6a)', () => {
    // The pill shows a random clay-work verb (kiln-words) in place of a static
    // "thinking"; pin the RNG to the first word so the exact text is stable.
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const firstWord = KILN_WORDS[0] ?? '';
    render(<ActivityRow thinking={true} toasts={[]} onDismiss={noop} />);
    expect(screen.getByText(`${firstWord}…`)).toBeInTheDocument();
    const indicator = document.querySelector('[data-role="thinking-indicator"]');
    expect(indicator).not.toBeNull();
  });

  it('renders the thinking indicator above the toast stack when both are present (08 §4)', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    render(<ActivityRow thinking={true} toasts={toasts} onDismiss={noop} />);
    const row = document.querySelector('[data-role="activity-row"]');
    const stack = document.querySelector('[data-role="toast-stack"]');
    const indicator = document.querySelector('[data-role="thinking-indicator"]');
    // Both share the one activity row (a single stacking layer)...
    expect(stack).not.toBeNull();
    expect(indicator).not.toBeNull();
    // ...with the thinking indicator ordered first (floating above), the toast
    // stack below it, nearest the dock.
    expect(row?.children[0]).toBe(indicator);
    expect(row?.children[1]).toBe(stack);
  });

  it('renders the toast pill with its verb (6b)', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    const pill = document.querySelector('[data-role="toast-pill"]');
    expect(pill).not.toBeNull();
    expect(pill).toHaveAttribute('data-verb', 'started');
    expect(screen.getByText('Login Redesign')).toBeInTheDocument();
    // The verb is conveyed by the status emoji; its label carries the status for
    // assistive tech rather than repeating it as a visible text prefix.
    expect(screen.getByRole('img', { name: 'Started' })).toBeInTheDocument();
  });

  it('stacks multiple live toasts into a list', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'A', ticketId: 't-a' }),
      toast(2, { kind: 'say', text: 'On it.' }),
      toast(3, { kind: 'toast', verb: 'finished', ticketTitle: 'C', ticketId: 't-c' }),
    ];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    const stack = document.querySelector('[data-role="toast-stack"]');
    expect(stack).not.toBeNull();
    expect(document.querySelectorAll('[data-role="toast-pill"]')).toHaveLength(2);
    expect(document.querySelectorAll('[data-role="say-pill"]')).toHaveLength(1);
  });

  it('expands a clamped say pill in place on tap, revealing the full text with no close control', () => {
    fakeClampedOverflow();
    const onDismiss = vi.fn();
    const toasts = [toast(1, { kind: 'say', text: 'A very long agent utterance that overflows.' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={onDismiss} />);

    // Collapsed: the whole pill is one Open button, the text clamped (no expand flag).
    const openButton = screen.getByRole('button', { name: 'Open message' });
    expect(openButton).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText(/A very long agent utterance/)).not.toHaveAttribute('data-expanded');

    fireEvent.click(openButton);

    // Expanded: the clamp drops, the pill stays a single tap target — no × and no
    // Close button appears beside it — and the first tap did not dismiss.
    expect(screen.getByText(/A very long agent utterance/)).toHaveAttribute(
      'data-expanded',
      'true',
    );
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
    expect(document.querySelectorAll('[data-role="say-pill"] button')).toHaveLength(1);
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it('tapping an already-expanded say pill dismisses it entirely with its id', () => {
    fakeClampedOverflow();
    const onDismiss = vi.fn();
    const toasts = [toast(7, { kind: 'say', text: 'Trust the session cookie.' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={onDismiss} />);
    fireEvent.click(screen.getByRole('button', { name: 'Open message' }));
    // The expanded pill is now a dismiss target — same surface, next meaning.
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss message' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(onDismiss).toHaveBeenCalledWith(7);
  });

  it('dismisses a say pill on the first tap when it has nothing more to reveal', () => {
    // No faked overflow: the text fits inside the clamp, so there is no expanded
    // state to go to and the first tap must dismiss rather than "open" the pill
    // onto an identical copy of itself.
    const onDismiss = vi.fn();
    const onToastExpandedChange = vi.fn();
    const toasts = [toast(9, { kind: 'say', text: 'On it.' })];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={onDismiss}
        onToastExpandedChange={onToastExpandedChange}
      />,
    );

    const pill = screen.getByRole('button', { name: 'Dismiss message' });
    // Nothing to disclose, so it doesn't claim a collapsed disclosure state.
    expect(pill).not.toHaveAttribute('aria-expanded');

    fireEvent.click(pill);
    expect(onDismiss).toHaveBeenCalledWith(9);
    expect(onToastExpandedChange).not.toHaveBeenCalled();
  });

  it("pauses a say pill's auto-dismiss timer when it is expanded so it can't vanish mid-read", () => {
    fakeClampedOverflow();
    const onToastExpandedChange = vi.fn();
    const toasts = [toast(4, { kind: 'say', text: 'A say that clamps and reveals.' })];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={noop}
        onToastExpandedChange={onToastExpandedChange}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Open message' }));
    // Expanding pauses the timer; the next tap dismisses outright, so it is never
    // resumed.
    expect(onToastExpandedChange).toHaveBeenCalledTimes(1);
    expect(onToastExpandedChange).toHaveBeenCalledWith(4, true);
  });

  it("opens a ticket toast's linked ticket on tap and dismisses the toast in one move", () => {
    const onOpenTicket = vi.fn();
    const onDismiss = vi.fn();
    const toasts = [
      toast(5, { kind: 'toast', verb: 'finished', ticketTitle: 'Auth', ticketId: 't-5' }),
    ];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={onDismiss}
        onOpenTicket={onOpenTicket}
      />,
    );

    // The whole pill is one tap target — no inline-expand flag, no separate Close.
    const openButton = screen.getByRole('button', { name: 'Open update: Auth' });
    expect(openButton).not.toHaveAttribute('aria-expanded');
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();

    fireEvent.click(openButton);

    // Tapping routes to the ticket AND clears the toast so it doesn't linger over
    // the detail view it just opened.
    expect(onOpenTicket).toHaveBeenCalledTimes(1);
    expect(onOpenTicket).toHaveBeenCalledWith('t-5');
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(onDismiss).toHaveBeenCalledWith(5);
  });

  it('falls back to expanding in place when a toast has no ticket to route to', () => {
    // An orphan toast (no ticket id) has nowhere to jump, so tapping expands it in
    // place — revealing the full title and pausing its auto-dismiss timer — and
    // the next tap dismisses it by id, just like a say pill.
    fakeClampedOverflow();
    const onOpenTicket = vi.fn();
    const onDismiss = vi.fn();
    const onToastExpandedChange = vi.fn();
    const toasts = [
      toast(3, { kind: 'toast', verb: 'started', ticketTitle: 'Orphan', ticketId: '' }),
    ];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={onDismiss}
        onOpenTicket={onOpenTicket}
        onToastExpandedChange={onToastExpandedChange}
      />,
    );

    const openButton = screen.getByRole('button', { name: 'Open update: Orphan' });
    expect(openButton).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(openButton);

    // No ticket routing — it expanded in place and paused its timer.
    expect(onOpenTicket).not.toHaveBeenCalled();
    expect(document.querySelector('[data-role="toast-text"]')).toHaveAttribute(
      'data-expanded',
      'true',
    );
    expect(onToastExpandedChange).toHaveBeenCalledWith(3, true);
    expect(onDismiss).not.toHaveBeenCalled();

    // The next tap on the same pill dismisses it entirely by id — no Close control.
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss update: Orphan' }));
    expect(onDismiss).toHaveBeenCalledWith(3);
  });

  it('dismisses an unexpandable orphan toast on the first tap', () => {
    // Nothing faked, so the title fits its clamp: there is nothing to expand into
    // and the single tap dismisses.
    const onDismiss = vi.fn();
    const onToastExpandedChange = vi.fn();
    const toasts = [
      toast(6, { kind: 'toast', verb: 'queued', ticketTitle: 'Short', ticketId: '' }),
    ];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={onDismiss}
        onToastExpandedChange={onToastExpandedChange}
      />,
    );

    const pill = screen.getByRole('button', { name: 'Dismiss update: Short' });
    expect(pill).not.toHaveAttribute('aria-expanded');
    fireEvent.click(pill);
    expect(onDismiss).toHaveBeenCalledWith(6);
    expect(onToastExpandedChange).not.toHaveBeenCalled();
  });

  it('renders an empty row when idle (nothing needs the activity surface)', () => {
    render(<ActivityRow thinking={false} toasts={[]} onDismiss={noop} />);
    const row = document.querySelector('[data-role="activity-row"]');
    expect(row).not.toBeNull();
    expect(row?.children).toHaveLength(0);
  });

  it('matches the DOM-structure snapshot: thinking indicator (6a)', () => {
    // Pin the RNG so the random clay-work verb (kiln-words) is deterministic.
    vi.spyOn(Math, 'random').mockReturnValue(0);
    const { container } = render(<ActivityRow thinking={true} toasts={[]} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  // Both pill snapshots render layout-free (no faked overflow) and without an
  // `onOpenTicket` handler, so each pill is its simplest shape: one tap target,
  // no close control, labelled "Dismiss…" because nothing is clamped away.
  it('matches the DOM-structure snapshot: action toast (6b)', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    const { container } = render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: say pill', () => {
    const toasts = [toast(1, { kind: 'say', text: 'Trust the session cookie.' })];
    const { container } = render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });
});
