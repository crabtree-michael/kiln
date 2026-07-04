// Activity-row tests (08 §4 / §F): the three activity surfaces — the thinking
// spinner (6a), the action toast (6b, carrying data-verb), and the persistent
// say pill — each rendered from the state the store hands down. DOM-structure
// snapshots stand in for pixel snapshots (07 §9 D4).
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ActivityRow } from '@/components/ActivityRow';
import type { ActivityPill } from '@/stores/activity-context';

const noop = (): void => {
  /* inert dismiss for render tests */
};

describe('ActivityRow', () => {
  it('renders the thinking indicator only when the pill is clear (6a)', () => {
    render(<ActivityRow thinking={true} pill={null} onDismiss={noop} />);
    expect(screen.getByText('Kiln is thinking…')).toBeInTheDocument();
    const indicator = document.querySelector('[data-role="thinking-indicator"]');
    expect(indicator).not.toBeNull();
  });

  it('does not render the thinking indicator while a pill is present (08 §4 contention)', () => {
    const pill: ActivityPill = { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' };
    render(<ActivityRow thinking={true} pill={pill} onDismiss={noop} />);
    expect(document.querySelector('[data-role="thinking-indicator"]')).toBeNull();
  });

  it('renders the toast pill with its verb (6b)', () => {
    const pill: ActivityPill = { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' };
    render(<ActivityRow thinking={false} pill={pill} onDismiss={noop} />);
    const toast = document.querySelector('[data-role="toast-pill"]');
    expect(toast).not.toBeNull();
    expect(toast).toHaveAttribute('data-verb', 'started');
    expect(screen.getByText('Login Redesign')).toBeInTheDocument();
    expect(screen.getByText(/Started/)).toBeInTheDocument();
  });

  it('renders the say pill with a dismiss affordance and calls onDismiss', () => {
    const onDismiss = vi.fn();
    const pill: ActivityPill = { kind: 'say', text: 'Trust the session cookie.' };
    render(<ActivityRow thinking={false} pill={pill} onDismiss={onDismiss} />);
    expect(screen.getByText('Trust the session cookie.')).toHaveAttribute('data-role', 'say-text');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('renders an empty row when idle (nothing needs the activity surface)', () => {
    render(<ActivityRow thinking={false} pill={null} onDismiss={noop} />);
    const row = document.querySelector('[data-role="activity-row"]');
    expect(row).not.toBeNull();
    expect(row?.children).toHaveLength(0);
  });

  it('matches the DOM-structure snapshot: thinking indicator (6a)', () => {
    const { container } = render(<ActivityRow thinking={true} pill={null} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: action toast (6b)', () => {
    const pill: ActivityPill = { kind: 'toast', verb: 'started', ticketTitle: 'Login Redesign' };
    const { container } = render(<ActivityRow thinking={false} pill={pill} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });

  it('matches the DOM-structure snapshot: say pill', () => {
    const pill: ActivityPill = { kind: 'say', text: 'Trust the session cookie.' };
    const { container } = render(<ActivityRow thinking={false} pill={pill} onDismiss={noop} />);
    expect(container).toMatchSnapshot();
  });
});
