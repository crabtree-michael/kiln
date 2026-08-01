// Deep-link → open-ticket bridge tests (02 §10 tap-to-open). Covers the three
// arrival paths the hook wires: a cold open at `/app?ticket=<id>`, a live
// service-worker `kiln:navigate` message to an already-open tab, and a ticket
// stashed by a cross-project switch that remounted the screen (12 §6.3). The
// URL parsing itself is tested in `stores/deep-link.test.ts`.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { stashDeepLinkTicket, takeDeepLinkTicket } from '@/stores/deep-link';

describe('useDeepLinkTicket', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/');
    takeDeepLinkTicket();
  });

  it('opens the deep-linked ticket on mount and strips the query param', () => {
    // A manual reload afterwards must not reopen a ticket the user dismissed.
    window.history.replaceState(null, '', '/?ticket=t-login');
    const onOpen = vi.fn();
    renderHook(() => {
      useDeepLinkTicket(onOpen);
    });
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith('t-login');
    expect(window.location.search).toBe('');
  });

  it('does nothing on a plain visit with no ticket param', () => {
    const onOpen = vi.fn();
    renderHook(() => {
      useDeepLinkTicket(onOpen);
    });
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('opens a ticket stashed by a cross-project switch, over the stale URL', () => {
    // This screen mounted *because* a tap switched projects: the URL still
    // describes whatever the tab was showing before, so the stash wins.
    window.history.replaceState(null, '', '/?ticket=t-stale');
    stashDeepLinkTicket('t-tapped');
    const onOpen = vi.fn();
    renderHook(() => {
      useDeepLinkTicket(onOpen);
    });
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith('t-tapped');
    // Consumed: a later remount (e.g. a manual project switch) must not reopen it.
    expect(takeDeepLinkTicket()).toBeNull();
  });

  it('opens the ticket from a live service-worker navigate message', () => {
    // Simulate a tap forwarded to an already-open tab: the worker postMessages
    // the deep link rather than reloading (which would drop the voice channel).
    const swTarget = new EventTarget();
    const orig = Object.getOwnPropertyDescriptor(navigator, 'serviceWorker');
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: swTarget });
    try {
      const onOpen = vi.fn();
      renderHook(() => {
        useDeepLinkTicket(onOpen);
      });

      swTarget.dispatchEvent(
        new MessageEvent('message', { data: { type: 'kiln:navigate', url: '/?ticket=t-x' } }),
      );
      expect(onOpen).toHaveBeenCalledTimes(1);
      expect(onOpen).toHaveBeenCalledWith('t-x');

      // Unrelated messages (other SW chatter, no ticket) are ignored.
      swTarget.dispatchEvent(new MessageEvent('message', { data: { type: 'other' } }));
      swTarget.dispatchEvent(
        new MessageEvent('message', { data: { type: 'kiln:navigate', url: '/' } }),
      );
      expect(onOpen).toHaveBeenCalledTimes(1);
    } finally {
      if (orig) {
        Object.defineProperty(navigator, 'serviceWorker', orig);
      } else {
        Reflect.deleteProperty(navigator, 'serviceWorker');
      }
    }
  });
});
