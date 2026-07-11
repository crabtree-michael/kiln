// Tests for the Web Push opt-in hook (02 §10). The browser push APIs
// (navigator.serviceWorker, PushManager, Notification) don't exist in jsdom, so
// each test installs just the slice it needs and the transport module is mocked
// at its boundary.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { JSX } from 'react';
import { useWebPush } from '@/stores/use-web-push';
import * as transport from '@/transport/transport';
// The static worker's source, read verbatim (Vite `?raw`) so we can run it
// against a stub `self` — it isn't an importable module (see the SW suite below).
import swSource from '../../public/push-sw.js?raw';

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
    expect(vi.mocked(transport.deletePushSubscription)).toHaveBeenCalledWith(
      'https://push.example/abc',
    );
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

// The static service worker (public/push-sw.js) is served verbatim and imports
// nothing, so we exercise its `push` handler by evaluating the file against a
// stub `self`. The behaviour under test is the iOS revocation guard: WebKit
// permanently drops a push subscription after ~3 pushes that don't show a
// notification, so the foreground-suppression shortcut must NOT run on iOS.
describe('push service worker', () => {
  interface FakeNavigator {
    userAgent?: string;
    platform?: string;
    maxTouchPoints?: number;
  }
  interface PushLikeEvent {
    data: { json: () => unknown };
    waitUntil: (p: Promise<unknown>) => void;
  }
  type PushHandler = (event: PushLikeEvent) => void;

  const PAYLOAD = { title: 'Blocked', body: 'A ticket needs you', url: '/app?ticket=1' };
  const IPHONE_UA = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15';
  const CHROME_UA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0';

  // Load push-sw.js against a stubbed worker global and return its `push`
  // handler plus the showNotification spy. `foreground` controls whether a
  // visible Kiln tab is reported to matchAll().
  function loadServiceWorker(
    navigator: FakeNavigator,
    foreground: boolean,
  ): { push: PushHandler; showNotification: ReturnType<typeof vi.fn> } {
    const handlers = new Map<string, PushHandler>();
    const showNotification = vi.fn(() => Promise.resolve());
    const windows = foreground ? [{ focused: true, visibilityState: 'visible' }] : [];
    const fakeSelf = {
      navigator,
      addEventListener: (type: string, handler: PushHandler) => {
        handlers.set(type, handler);
      },
      skipWaiting: vi.fn(),
      clients: {
        claim: vi.fn(() => Promise.resolve()),
        matchAll: vi.fn(() => Promise.resolve(windows)),
      },
      registration: { showNotification },
    };
    const fakeCaches = { keys: () => Promise.resolve([]), delete: () => Promise.resolve() };
    // The worker is plain JS with free `self`/`caches` and registers its handlers
    // at top level, so it can't be imported — bind the globals as parameters and
    // run it once. eslint-disable: this eval is over trusted first-party source.
    // eslint-disable-next-line @typescript-eslint/no-implied-eval, @typescript-eslint/no-unsafe-call
    new Function('self', 'caches', swSource)(fakeSelf, fakeCaches);
    const push = handlers.get('push');
    if (push === undefined) throw new Error('worker registered no push handler');
    return { push, showNotification };
  }

  // Fire the push handler and await the promise it hands to waitUntil.
  async function dispatchPush(handler: PushHandler): Promise<void> {
    let pending: Promise<unknown> = Promise.resolve();
    handler({
      data: { json: () => PAYLOAD },
      waitUntil: (p) => {
        pending = p;
      },
    });
    await pending;
  }

  it('shows a notification when the app is backgrounded, on any engine', async () => {
    const { push, showNotification } = loadServiceWorker({ userAgent: CHROME_UA }, false);
    await dispatchPush(push);
    expect(showNotification).toHaveBeenCalledOnce();
  });

  it('suppresses a foreground push on non-iOS engines (they grant a silent budget)', async () => {
    const { push, showNotification } = loadServiceWorker({ userAgent: CHROME_UA }, true);
    await dispatchPush(push);
    expect(showNotification).not.toHaveBeenCalled();
  });

  it('still shows a foreground push on iPhone, so iOS never revokes the subscription', async () => {
    const { push, showNotification } = loadServiceWorker({ userAgent: IPHONE_UA }, true);
    await dispatchPush(push);
    expect(showNotification).toHaveBeenCalledOnce();
  });

  it('still shows a foreground push on iPadOS (desktop UA masquerade + touch points)', async () => {
    const { push, showNotification } = loadServiceWorker(
      {
        userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X) AppleWebKit/605.1.15',
        platform: 'MacIntel',
        maxTouchPoints: 5,
      },
      true,
    );
    await dispatchPush(push);
    expect(showNotification).toHaveBeenCalledOnce();
  });
});
