// NotificationsField renders the push opt-in switch off the useWebPush hook
// (02 §10). The hook itself is unit-tested separately; here it is mocked so we
// assert the row's per-state presentation.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { NotificationsField } from '@/dashboard/NotificationsField';
import type { WebPush, WebPushStatus } from '@/stores/use-web-push';

const enable = vi.fn(() => Promise.resolve());
const disable = vi.fn(() => Promise.resolve());
let hookValue: WebPush;

vi.mock('@/stores/use-web-push', () => ({
  useWebPush: (): WebPush => hookValue,
}));

function setStatus(status: WebPushStatus, error: string | null = null): void {
  hookValue = { status, error, enable, disable };
}

/** The single switch the section renders, labelled by the row's title. */
function toggle(): HTMLElement {
  return screen.getByRole('switch', { name: 'Push notifications' });
}

describe('NotificationsField', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('renders an off switch in the default state and drives enable()', () => {
    setStatus('default');
    render(<NotificationsField />);
    expect(toggle()).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(toggle());
    expect(enable).toHaveBeenCalledTimes(1);
    expect(disable).not.toHaveBeenCalled();
  });

  it('renders an on switch when enabled and drives disable()', () => {
    setStatus('enabled');
    render(<NotificationsField />);
    expect(toggle()).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(toggle());
    expect(disable).toHaveBeenCalledTimes(1);
    expect(enable).not.toHaveBeenCalled();
  });

  it('disables the switch while the flow runs', () => {
    setStatus('enabling');
    render(<NotificationsField />);
    expect(toggle()).toBeDisabled();
    expect(toggle()).toHaveAttribute('aria-busy', 'true');
  });

  it('carries no explanatory copy in the on state — just the switch', () => {
    setStatus('enabled');
    render(<NotificationsField />);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByRole('switch')).toBeInTheDocument();
    // The row is the label and the switch, nothing else.
    expect(screen.getByText('Push notifications')).toBeInTheDocument();
    expect(screen.queryByText(/notifications are on/i)).toBeNull();
  });

  it('renders an inert switch with a one-word reason when unsupported', () => {
    setStatus('unsupported');
    render(<NotificationsField />);
    expect(toggle()).toBeDisabled();
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
  });

  it('renders an inert switch with a one-word reason when unconfigured', () => {
    setStatus('unconfigured');
    render(<NotificationsField />);
    expect(toggle()).toBeDisabled();
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
  });

  it('renders an inert switch with a one-word reason when denied', () => {
    setStatus('denied');
    render(<NotificationsField />);
    expect(toggle()).toBeDisabled();
    expect(screen.getByText('Blocked')).toBeInTheDocument();
  });

  it('surfaces an error and keeps the switch flippable for a retry', () => {
    setStatus('error', 'subscribe failed');
    render(<NotificationsField />);
    expect(toggle()).toBeEnabled();
    expect(screen.getByRole('alert')).toHaveTextContent('subscribe failed');
    fireEvent.click(toggle());
    expect(enable).toHaveBeenCalledTimes(1);
  });
});
