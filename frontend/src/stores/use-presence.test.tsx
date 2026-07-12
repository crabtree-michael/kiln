// Tests for the foreground-presence heartbeat hook (02 §10 push dedup). jsdom
// has no push APIs and no real visibility transitions, so each test installs the
// slice it needs (serviceWorker.getSubscription, document.visibilityState,
// navigator.sendBeacon) and mocks the transport module at its boundary. Timers
// are faked so the 15s heartbeat cadence is deterministic.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render } from '@testing-library/react';
import type { JSX } from 'react';
import { usePresence } from '@/stores/use-presence';
import * as transport from '@/transport/transport';

vi.mock('@/transport/transport', () => ({
  postPresence: vi.fn(() => Promise.resolve()),
  beaconPresenceHidden: vi.fn(() => true),
}));

const ENDPOINT = 'https://push.example/device';

function Probe(): JSX.Element {
  usePresence();
  return <div />;
}

// visibilityState is read-only; redefine it per test and dispatch the event.
function setVisibility(state: DocumentVisibilityState): void {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
}

function fireVisibilityChange(state: DocumentVisibilityState): void {
  setVisibility(state);
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'));
  });
}

// Install a service worker whose registration yields (or doesn't) a push
// subscription with ENDPOINT — the gate the hook resolves lazily.
function installServiceWorker(hasSubscription: boolean): void {
  const registration = {
    pushManager: {
      getSubscription: vi.fn(() =>
        Promise.resolve(hasSubscription ? { endpoint: ENDPOINT } : null),
      ),
    },
  };
  Object.defineProperty(navigator, 'serviceWorker', {
    value: { getRegistration: vi.fn(() => Promise.resolve(registration)) },
    configurable: true,
  });
}

// Let the hook's lazily-awaited endpoint resolution settle (getRegistration →
// getSubscription are two microtask hops) before asserting on the transport.
async function flushMicrotasks(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe('usePresence', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.mocked(transport.postPresence).mockResolvedValue(undefined);
    vi.mocked(transport.beaconPresenceHidden).mockReturnValue(true);
    setVisibility('visible');
    Object.defineProperty(navigator, 'sendBeacon', {
      value: vi.fn(() => true),
      configurable: true,
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    if ('serviceWorker' in navigator) {
      Reflect.deleteProperty(navigator, 'serviceWorker');
    }
  });

  it('beats immediately on mount when visible, then on the 15s cadence', async () => {
    installServiceWorker(true);
    render(<Probe />);
    await flushMicrotasks();

    expect(transport.postPresence).toHaveBeenCalledTimes(1);
    expect(transport.postPresence).toHaveBeenLastCalledWith(ENDPOINT, true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(transport.postPresence).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(transport.postPresence).toHaveBeenCalledTimes(3);
  });

  it('sends nothing when there is no push subscription', async () => {
    installServiceWorker(false);
    render(<Probe />);
    await flushMicrotasks();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(45_000);
    });
    expect(transport.postPresence).not.toHaveBeenCalled();
    expect(transport.beaconPresenceHidden).not.toHaveBeenCalled();
  });

  it('stops heartbeating and fires one leave beacon on hide', async () => {
    installServiceWorker(true);
    render(<Probe />);
    await flushMicrotasks(); // first beat resolves + caches the endpoint

    expect(transport.postPresence).toHaveBeenCalledTimes(1);

    fireVisibilityChange('hidden');
    expect(transport.beaconPresenceHidden).toHaveBeenCalledTimes(1);
    expect(transport.beaconPresenceHidden).toHaveBeenCalledWith(ENDPOINT);

    // No further heartbeats while hidden.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(transport.postPresence).toHaveBeenCalledTimes(1);
  });

  it('resumes heartbeating when the tab becomes visible again', async () => {
    installServiceWorker(true);
    render(<Probe />);
    await flushMicrotasks();
    expect(transport.postPresence).toHaveBeenCalledTimes(1);

    fireVisibilityChange('hidden');
    await flushMicrotasks();
    fireVisibilityChange('visible');
    await flushMicrotasks();

    // The immediate beat on re-becoming visible.
    expect(transport.postPresence).toHaveBeenCalledTimes(2);
  });

  it('does not beat on mount when the tab starts hidden', async () => {
    installServiceWorker(true);
    setVisibility('hidden');
    render(<Probe />);
    await flushMicrotasks();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(transport.postPresence).not.toHaveBeenCalled();
  });
});
