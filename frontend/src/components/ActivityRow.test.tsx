// Activity-row tests (08 §4 / §F): the activity surfaces — the thinking spinner
// (6a, only when the stack is empty), the action toast (6b, carrying data-verb),
// and the persistent say pill — each rendered from the stack the store hands
// down. Multiple live toasts stack. DOM-structure snapshots stand in for pixel
// snapshots (07 §9 D4).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ActivityRow } from '@/components/ActivityRow';
import type { ActivityToast } from '@/stores/activity-context';

const noop = (): void => {
  /* inert dismiss for render tests */
};

function toast(id: number, pill: ActivityToast['pill']): ActivityToast {
  return { id, pill };
}

describe('ActivityRow', () => {
  it('renders the thinking indicator only when the stack is empty (6a)', () => {
    render(<ActivityRow thinking={true} toasts={[]} onDismiss={noop} />);
    expect(screen.getByText('Kiln is thinking…')).toBeInTheDocument();
    const indicator = document.querySelector('[data-role="thinking-indicator"]');
    expect(indicator).not.toBeNull();
  });

  it('does not render the thinking indicator while a toast is present (08 §4 contention)', () => {
    const toasts = [toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' })];
    render(<ActivityRow thinking={true} toasts={toasts} onDismiss={noop} />);
    expect(document.querySelector('[data-role="thinking-indicator"]')).toBeNull();
  });

  it('renders the toast pill with its verb (6b)', () => {
    const toasts = [toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' })];
    render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    const pill = document.querySelector('[data-role="toast-pill"]');
    expect(pill).not.toBeNull();
    expect(pill).toHaveAttribute('data-verb', 'started');
    expect(screen.getByText('Login Redesign')).toBeInTheDocument();
    expect(screen.getByText(/Started/)).toBeInTheDocument();
  });

  it('stacks multiple live toasts into a list', () => {
    const toasts = [
      toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'A' }),
      toast(2, { kind: 'say', text: 'On it.' }),
      toast(3, { kind: 'toast', verb: 'finished', ticketTitle: 'C' }),
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

  it('renders an empty row when idle (nothing needs the activity surface)', () => {
    render(<ActivityRow thinking={false} toasts={[]} onDismiss={noop} />);
    const row = document.querySelector('[data-role="activity-row"]');
    expect(row).not.toBeNull();
    expect(row?.children).toHaveLength(0);
  });

  it('matches the DOM-structure snapshot: thinking indicator (6a)', () => {
    const { container } = render(<ActivityRow thinking={true} toasts={[]} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: action toast (6b)', () => {
    const toasts = [toast(1, { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' })];
    const { container } = render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: say pill', () => {
    const toasts = [toast(1, { kind: 'say', text: 'Trust the session cookie.' })];
    const { container } = render(<ActivityRow thinking={false} toasts={toasts} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });
});
