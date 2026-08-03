// NotificationsField renders the push frequency dropdown off the useWebPush and
// useNotificationMode hooks (02 §10). Both hooks are unit-tested separately;
// here they are mocked so we assert the row's per-state presentation and what a
// selection drives.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { NotificationsField } from '@/dashboard/NotificationsField';
import type { WebPush, WebPushStatus } from '@/stores/use-web-push';
import type { NotificationModeControl } from '@/stores/use-notification-mode';
import type { NotificationModeValue } from '@/transport/transport';

const enable = vi.fn(() => Promise.resolve());
const disable = vi.fn(() => Promise.resolve());
const setMode = vi.fn();
let hookValue: WebPush;
let modeValue: NotificationModeControl;

vi.mock('@/stores/use-web-push', () => ({
  useWebPush: (): WebPush => hookValue,
}));

vi.mock('@/stores/use-notification-mode', () => ({
  useNotificationMode: (): NotificationModeControl => modeValue,
}));

function setStatus(status: WebPushStatus, error: string | null = null): void {
  hookValue = { status, error, enable, disable };
}

function setModeState(mode: NotificationModeValue, ready = true): void {
  modeValue = { mode, ready, setMode };
}

/** The single dropdown the section renders, labelled by the row's title. */
function dropdown(): HTMLSelectElement {
  const element = screen.getByRole('combobox', { name: 'Push notifications' });
  if (!(element instanceof HTMLSelectElement)) {
    throw new Error('the notifications control is not a select');
  }
  return element;
}

describe('NotificationsField', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('offers Off then the three frequencies least-to-most noisy, current one selected', () => {
    setStatus('enabled');
    setModeState('blocked');
    render(<NotificationsField />);
    const options = within(dropdown()).getAllByRole('option');
    expect(options.map((option) => option.textContent)).toEqual([
      'Off',
      'Default',
      'Blocked',
      'All',
    ]);
    expect(dropdown()).toHaveValue('blocked');
  });

  // The row shows what this browser will actually do, not what the account has
  // stored: a stored frequency on an unsubscribed browser produces no pushes.
  it('reads Off on a browser that has not subscribed, whatever mode is stored', () => {
    setStatus('default');
    setModeState('all');
    render(<NotificationsField />);
    expect(dropdown()).toHaveValue('off');
  });

  it('unsubscribes this browser when Off is chosen, leaving the stored mode alone', () => {
    setStatus('enabled');
    setModeState('all');
    render(<NotificationsField />);
    fireEvent.change(dropdown(), { target: { value: 'off' } });
    expect(disable).toHaveBeenCalledTimes(1);
    // The frequency is the account's, shared with every other browser — turning
    // this one off must not rewrite it.
    expect(setMode).not.toHaveBeenCalled();
    expect(enable).not.toHaveBeenCalled();
  });

  it('re-subscribes when a frequency is picked again after Off', () => {
    setStatus('default');
    setModeState('all');
    render(<NotificationsField />);
    fireEvent.change(dropdown(), { target: { value: 'default' } });
    expect(setMode).toHaveBeenCalledWith('default');
    expect(enable).toHaveBeenCalledTimes(1);
    expect(disable).not.toHaveBeenCalled();
  });

  it('persists a new frequency without re-running the opt-in when already enabled', () => {
    setStatus('enabled');
    setModeState('default');
    render(<NotificationsField />);
    fireEvent.change(dropdown(), { target: { value: 'all' } });
    expect(setMode).toHaveBeenCalledWith('all');
    expect(enable).not.toHaveBeenCalled();
  });

  // A frequency means nothing without a subscription, so the same gesture that
  // picks one opts this browser in.
  it('runs the opt-in alongside the write when push is not enabled yet', () => {
    setStatus('default');
    setModeState('default');
    render(<NotificationsField />);
    fireEvent.change(dropdown(), { target: { value: 'blocked' } });
    expect(setMode).toHaveBeenCalledWith('blocked');
    expect(enable).toHaveBeenCalledTimes(1);
  });

  it('disables the dropdown while the opt-in flow runs, holding the chosen frequency', () => {
    setStatus('enabling');
    setModeState('blocked');
    render(<NotificationsField />);
    expect(dropdown()).toBeDisabled();
    expect(dropdown()).toHaveAttribute('aria-busy', 'true');
    // Not "Off" for the length of the flow — the mode write is optimistic, so
    // the row shows the choice being made.
    expect(dropdown()).toHaveValue('blocked');
  });

  it('disables the dropdown until the initial mode read resolves', () => {
    setStatus('enabled');
    setModeState('default', false);
    render(<NotificationsField />);
    expect(dropdown()).toBeDisabled();
  });

  it('carries no explanatory copy in the on state — just the dropdown', () => {
    setStatus('enabled');
    setModeState('default');
    render(<NotificationsField />);
    expect(screen.queryByRole('alert')).toBeNull();
    // The row is the label and the dropdown, nothing else.
    expect(screen.getByText('Push notifications')).toBeInTheDocument();
    expect(screen.queryByText(/notifications are on/i)).toBeNull();
  });

  it('renders an inert dropdown with a one-word reason when unsupported', () => {
    setStatus('unsupported');
    setModeState('default');
    render(<NotificationsField />);
    expect(dropdown()).toBeDisabled();
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
  });

  it('renders an inert dropdown with a one-word reason when unconfigured', () => {
    setStatus('unconfigured');
    setModeState('default');
    render(<NotificationsField />);
    expect(dropdown()).toBeDisabled();
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
  });

  // "Denied", not "Blocked" — "Blocked" is one of the frequencies in the same
  // row, and a browser-level refusal is a different thing entirely.
  it('renders an inert dropdown reading Denied when permission was refused', () => {
    setStatus('denied');
    setModeState('default');
    render(<NotificationsField />);
    expect(dropdown()).toBeDisabled();
    expect(document.querySelector('[data-role="notifications-reason"]')).toHaveTextContent(
      'Denied',
    );
  });

  it('surfaces an error and keeps the dropdown usable for a retry', () => {
    setStatus('error', 'subscribe failed');
    setModeState('default');
    render(<NotificationsField />);
    expect(dropdown()).toBeEnabled();
    expect(screen.getByRole('alert')).toHaveTextContent('subscribe failed');
    fireEvent.change(dropdown(), { target: { value: 'all' } });
    expect(enable).toHaveBeenCalledTimes(1);
  });
});
