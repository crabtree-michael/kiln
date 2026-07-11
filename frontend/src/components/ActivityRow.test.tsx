// Activity-row tests (08 §4 / §F): the activity surfaces — the thinking spinner
// (6a, only when the stack is empty), the action toast (6b, carrying data-verb),
// and the persistent say pill — each rendered from the stack the store hands
// down. Multiple live toasts stack. DOM-structure snapshots stand in for pixel
// snapshots (07 §9 D4).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ActivityRow } from '@/components/ActivityRow';
import { KILN_WORDS } from '@/components/kiln-words';
import type { ActivityToast } from '@/stores/activity-context';

const noop = (): void => {
  /* inert dismiss for render tests */
};

function toast(id: number, pill: ActivityToast['pill']): ActivityToast {
  return { id, pill };
}

/**
 * jsdom performs no layout, so `scrollHeight`/`clientHeight` are both 0 and the
 * clamp never registers as truncated. Fake a clamped box (content taller than
 * the 2-line window) so the expand affordance can be exercised; restore after
 * each test so the layout-free default holds for the snapshot cases.
 */
function fakeClampedOverflow(): void {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(100);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(40);
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

  it('renders the say pill with a dismiss affordance and calls onDismiss with its id', () => {
    const onDismiss = vi.fn();
    const toasts = [toast(7, { kind: 'say', text: 'Trust the session cookie.' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={onDismiss} />);
    expect(screen.getByText('Trust the session cookie.')).toHaveAttribute('data-role', 'say-text');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(onDismiss).toHaveBeenCalledWith(7);
  });

  it('leaves a say pill inert when its text fits (no clamp, no expand affordance)', () => {
    const toasts = [toast(1, { kind: 'say', text: 'On it.' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    const text = screen.getByText('On it.');
    expect(text).not.toHaveAttribute('data-expandable');
    expect(text).not.toHaveAttribute('data-expanded');
    expect(text).not.toHaveAttribute('role', 'button');
  });

  it('expands a truncated say pill in place on tap and collapses it again', () => {
    fakeClampedOverflow();
    const toasts = [toast(1, { kind: 'say', text: 'A very long agent utterance that overflows.' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    const text = screen.getByText(/A very long agent utterance/);

    // Truncated: tappable, collapsed to start.
    expect(text).toHaveAttribute('data-expandable', 'true');
    expect(text).toHaveAttribute('role', 'button');
    expect(text).toHaveAttribute('aria-expanded', 'false');
    expect(text).not.toHaveAttribute('data-expanded');

    fireEvent.click(text);
    expect(text).toHaveAttribute('data-expanded', 'true');
    expect(text).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(text);
    expect(text).not.toHaveAttribute('data-expanded');
    expect(text).toHaveAttribute('aria-expanded', 'false');
  });

  it('reports expand/collapse for a truncated toast so its timer can pause and resume', () => {
    fakeClampedOverflow();
    const onToastExpandedChange = vi.fn();
    const toasts = [toast(4, { kind: 'say', text: 'A very long say that clamps and reveals.' })];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={noop}
        onToastExpandedChange={onToastExpandedChange}
      />,
    );
    const text = screen.getByText(/A very long say/);

    fireEvent.click(text);
    expect(onToastExpandedChange).toHaveBeenLastCalledWith(4, true);
    fireEvent.click(text);
    expect(onToastExpandedChange).toHaveBeenLastCalledWith(4, false);
  });

  it('opens the ticket detail when a ticket-activity toast is tapped (not expand-in-place)', () => {
    const onOpenTicket = vi.fn();
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={noop}
        onOpenTicket={onOpenTicket}
      />,
    );
    // The whole icon+title region is one button labelled by the ticket, and the
    // title never becomes an expand toggle — the tap opens the ticket instead.
    const open = screen.getByRole('button', { name: 'Open ticket: Login Redesign' });
    expect(screen.getByText('Login Redesign')).not.toHaveAttribute('data-expandable');
    fireEvent.click(open);
    expect(onOpenTicket).toHaveBeenCalledTimes(1);
    expect(onOpenTicket).toHaveBeenCalledWith('t-1');
  });

  it('tapping the toast dismiss × closes it without opening the ticket', () => {
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
    // The dismiss button is a sibling of the open button, so its click never
    // bubbles into an open.
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledWith(5);
    expect(onOpenTicket).not.toHaveBeenCalled();
  });

  it('renders a ticket-activity toast inert when no open handler is wired', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    // Without onOpenTicket the pill is a static row — no open button, just the
    // (still-present) dismiss affordance.
    expect(screen.queryByRole('button', { name: /Open ticket/ })).toBeNull();
    expect(screen.getByText('Login Redesign')).toBeInTheDocument();
  });

  it('leaves a toast with no linked ticket id inert even when an open handler is wired', () => {
    const onOpenTicket = vi.fn();
    const toasts = [toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Orphan', ticketId: '' })];
    render(
      <ActivityRow
        thinking={false}
        toasts={toasts}
        onDismiss={noop}
        onOpenTicket={onOpenTicket}
      />,
    );
    expect(screen.queryByRole('button', { name: /Open ticket/ })).toBeNull();
  });

  it('dismisses a say pill without toggling expansion when the × is tapped', () => {
    fakeClampedOverflow();
    const onDismiss = vi.fn();
    const toasts = [
      toast(9, { kind: 'say', text: 'A very long say that is clamped and dismissable.' }),
    ];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={onDismiss} />);
    const text = screen.getByText(/A very long say/);
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledWith(9);
    // The dismiss button is a sibling of the text, so it never toggles expansion.
    expect(text).not.toHaveAttribute('data-expanded');
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

  it('matches the DOM-structure snapshot: action toast (6b)', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign', ticketId: 't-1' }),
    ];
    const { container } = render(
      <ActivityRow thinking={false} toasts={toasts} onDismiss={noop} onOpenTicket={noop} />,
    );
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: say pill', () => {
    const toasts = [toast(1, { kind: 'say', text: 'Trust the session cookie.' })];
    const { container } = render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });
});
