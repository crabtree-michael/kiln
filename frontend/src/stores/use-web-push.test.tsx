// Tests for the Web Push opt-in hook (02 §10). The browser push APIs
// (navigator.serviceWorker, PushManager, Notification) don't exist in jsdom, so
// each test installs just the slice it needs and the transport module is mocked
// at its boundary.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { useWebPush } from '@/stores/use-web-push';
import * as transport from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  fetchPushKey: vi.fn(),
  postPushSubscription: vi.fn(),
  deletePushSubscription: vi.fn(),
}));

function Probe(): JSX.Element {
  const { status, error, enable, disable } = useWebPush();
  return (
    <div>
      <span data-testid="status">{status}</span>
      <span data-testid="error">{error ?? ''}</span>
      <button
        type="button"
        onClick={() => {
          void enable();
        }}
      >
        enable
      </button>
      <button
        type="button"
        onClick={() => {
          void disable();
        }}
      >
        disable
      </button>
    </div>
  );
}

interface PushEnv {
  permission?: NotificationPermission;
  existingSubscription?: boolean;
  requestPermissionResult?: NotificationPermission;
}

// The fake subscription returned by pushManager.subscribe().
const FAKE_SUBSCRIPTION = {
  endpoint: 'https://push.example/abc',
  toJSON: () => ({ endpoint: 'https://push.example/abc', keys: { p256dh: 'pub', auth: 'auth' } }),
};

let subscribeMock: ReturnType<typeof vi.fn>;
let getSubscriptionMock: ReturnType<typeof vi.fn>;
let registerMock: ReturnType<typeof vi.fn>;

// Install the browser push APIs so browserSupportsPush() is satisfied.
function installPushEnv(env: PushEnv): void {
  getSubscriptionMock = vi.fn(() =>
    Promise.resolve(env.existingSubscription ? FAKE_SUBSCRIPTION : null),
  );
  subscribeMock = vi.fn(() => Promise.resolve(FAKE_SUBSCRIPTION));
  const registration = {
    pushManager: { getSubscription: getSubscriptionMock, subscribe: subscribeMock },
  };
  registerMock = vi.fn(() => Promise.resolve(registration));
  const serviceWorker = {
    register: registerMock,
    ready: Promise.resolve(registration),
    getRegistration: vi.fn(() => Promise.resolve(undefined)),
  };
  Object.defineProperty(navigator, 'serviceWorker', { value: serviceWorker, configurable: true });
  // Only its presence on window matters (browserSupportsPush checks `in`).
  vi.stubGlobal('PushManager', {});
  vi.stubGlobal('Notification', {
    permission: env.permission ?? 'default',
    requestPermission: vi.fn(() => Promise.resolve(env.requestPermissionResult ?? 'granted')),
  });
}

describe('useWebPush', () => {
  beforeEach(() => {
    vi.mocked(transport.fetchPushKey).mockResolvedValue('BPUBLIC');
    vi.mocked(transport.postPushSubscription).mockResolvedValue(undefined);
    vi.mocked(transport.deletePushSubscription).mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    if ('serviceWorker' in navigator) {
      Reflect.deleteProperty(navigator, 'serviceWorker');
    }
  });

  it('reports unsupported when the browser lacks the push APIs', async () => {
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('unsupported');
    });
  });

  it('reports unconfigured when the backend has no VAPID key', async () => {
    installPushEnv({});
    vi.mocked(transport.fetchPushKey).mockResolvedValue(null);
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('unconfigured');
    });
  });

  it('reports default when supported, configured, and not yet subscribed', async () => {
    installPushEnv({});
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });
  });

  it('reports enabled on mount when a subscription already exists', async () => {
    installPushEnv({ existingSubscription: true });
    // getRegistration must return a registration for the mount probe to find the sub.
    const registration = {
      pushManager: { getSubscription: () => Promise.resolve(FAKE_SUBSCRIPTION) },
    };
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { getRegistration: () => Promise.resolve(registration) },
      configurable: true,
    });
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('enabled');
    });
  });

  it('subscribes and posts to the backend when enabled', async () => {
    installPushEnv({});
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });

    fireEvent.click(screen.getByText('enable'));

    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('enabled');
    });
    expect(subscribeMock).toHaveBeenCalledWith(expect.objectContaining({ userVisibleOnly: true }));
    expect(vi.mocked(transport.postPushSubscription)).toHaveBeenCalledWith({
      endpoint: 'https://push.example/abc',
      keys: { p256dh: 'pub', auth: 'auth' },
    });
  });

  it('unsubscribes and drops back to default when disabled', async () => {
    installPushEnv({ existingSubscription: true });
    const unsubscribe = vi.fn(() => Promise.resolve(true));
    const subscription = { ...FAKE_SUBSCRIPTION, unsubscribe };
    const registration = {
      pushManager: { getSubscription: () => Promise.resolve(subscription) },
    };
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { getRegistration: () => Promise.resolve(registration) },
      configurable: true,
    });
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('enabled');
    });

    fireEvent.click(screen.getByText('disable'));

    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });
    expect(unsubscribe).toHaveBeenCalledOnce();
    // The server row is dropped immediately (not left for send-time pruning),
    // keyed by the endpoint captured before unsubscribe() invalidated it.
    expect(vi.mocked(transport.deletePushSubscription)).toHaveBeenCalledWith('https://push.example/abc');
  });

  it('still disables locally when the server-side delete fails', async () => {
    installPushEnv({ existingSubscription: true });
    vi.mocked(transport.deletePushSubscription).mockRejectedValue(new Error('offline'));
    const unsubscribe = vi.fn(() => Promise.resolve(true));
    const subscription = { ...FAKE_SUBSCRIPTION, unsubscribe };
    const registration = {
      pushManager: { getSubscription: () => Promise.resolve(subscription) },
    };
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { getRegistration: () => Promise.resolve(registration) },
      configurable: true,
    });
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('enabled');
    });

    fireEvent.click(screen.getByText('disable'));

    // Best-effort: a failed server delete must not strand the user in 'error' —
    // the browser is already unsubscribed and send-time pruning is the fallback.
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });
    expect(unsubscribe).toHaveBeenCalledOnce();
    expect(screen.getByTestId('error').textContent).toBe('');
  });

  // Regression guard for the cross-device sync ticket. Push subscriptions are
  // per-device browser state: opening the app must only *reflect* the current
  // subscription, never mutate it. If a future change made the mount probe
  // subscribe/unsubscribe/POST, one device opening the app could disturb another
  // device's enablement — the reported "Phone A reverts to disabled" symptom.
  it('does not touch the subscription on mount when already enabled', async () => {
    installPushEnv({ existingSubscription: true });
    const unsubscribe = vi.fn(() => Promise.resolve(true));
    const subscribe = vi.fn(() => Promise.resolve(FAKE_SUBSCRIPTION));
    const subscription = { ...FAKE_SUBSCRIPTION, unsubscribe };
    const registration = {
      pushManager: { getSubscription: () => Promise.resolve(subscription), subscribe },
    };
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { getRegistration: () => Promise.resolve(registration) },
      configurable: true,
    });
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('enabled');
    });
    // Reflected, not re-subscribed, not unsubscribed, not re-POSTed, not deleted.
    expect(subscribe).not.toHaveBeenCalled();
    expect(unsubscribe).not.toHaveBeenCalled();
    expect(vi.mocked(transport.postPushSubscription)).not.toHaveBeenCalled();
    expect(vi.mocked(transport.deletePushSubscription)).not.toHaveBeenCalled();
  });

  it('does not subscribe or POST on mount when not yet subscribed', async () => {
    installPushEnv({});
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });
    expect(subscribeMock).not.toHaveBeenCalled();
    expect(vi.mocked(transport.postPushSubscription)).not.toHaveBeenCalled();
  });

  it('reports denied when the user blocks the permission prompt', async () => {
    installPushEnv({ requestPermissionResult: 'denied' });
    render(<Probe />);
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('default');
    });

    fireEvent.click(screen.getByText('enable'));

    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('denied');
    });
    expect(vi.mocked(transport.postPushSubscription)).not.toHaveBeenCalled();
  });
});
