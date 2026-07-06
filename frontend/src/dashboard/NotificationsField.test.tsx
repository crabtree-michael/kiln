// NotificationsField renders the push opt-in control off the useWebPush hook
// (02 §10). The hook itself is unit-tested separately; here it is mocked so we
// assert the field's per-state presentation.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { NotificationsField } from '@/dashboard/NotificationsField';
import type { WebPush, WebPushStatus } from '@/stores/use-web-push';

const enable = vi.fn(() => Promise.resolve());
let hookValue: WebPush;

vi.mock('@/stores/use-web-push', () => ({
  useWebPush: (): WebPush => hookValue,
}));

function setStatus(status: WebPushStatus, error: string | null = null): void {
  hookValue = { status, error, enable };
}

describe('NotificationsField', () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it('offers an enable button in the default state and drives enable()', () => {
    setStatus('default');
    render(<NotificationsField />);
    const button = screen.getByRole('button', { name: 'Enable notifications' });
    fireEvent.click(button);
    expect(enable).toHaveBeenCalledTimes(1);
  });

  it('shows a disabled Enabling… button while the flow runs', () => {
    setStatus('enabling');
    render(<NotificationsField />);
    expect(screen.getByRole('button', { name: 'Enabling…' })).toBeDisabled();
  });

  it('shows the on-state without a button when enabled', () => {
    setStatus('enabled');
    render(<NotificationsField />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.getByText(/notifications are on/i)).toBeInTheDocument();
  });

  it('explains unavailability without a button when unsupported', () => {
    setStatus('unsupported');
    render(<NotificationsField />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText(/doesn’t support push notifications/i)).toBeInTheDocument();
  });

  it('explains unavailability without a button when unconfigured', () => {
    setStatus('unconfigured');
    render(<NotificationsField />);
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText(/aren’t configured on the server/i)).toBeInTheDocument();
  });

  it('surfaces an error and offers a retry button', () => {
    setStatus('error', 'subscribe failed');
    render(<NotificationsField />);
    expect(screen.getByRole('button', { name: 'Enable notifications' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('subscribe failed');
  });
});
