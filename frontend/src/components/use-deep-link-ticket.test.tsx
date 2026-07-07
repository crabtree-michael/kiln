// Deep-link → open-ticket bridge tests (02 §10 tap-to-open). Covers the URL
// parser and both arrival paths the hook wires: a cold open at `/?ticket=<id>`
// and a live service-worker `kiln:navigate` message to an already-open tab.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import { ticketIdFromUrl, useDeepLinkTicket } from '@/components/use-deep-link-ticket';

describe('ticketIdFromUrl', () => {
  it('pulls the ticket id out of a deep link (full URL or bare query)', () => {
    expect(ticketIdFromUrl('/?ticket=t-login')).toBe('t-login');
    expect(ticketIdFromUrl('?ticket=a+b%2Fc')).toBe('a b/c');
    expect(ticketIdFromUrl('/?other=1&ticket=t-x')).toBe('t-x');
  });

  it('returns null when the ticket param is absent or empty', () => {
    expect(ticketIdFromUrl('/')).toBeNull();
    expect(ticketIdFromUrl('/?other=1')).toBeNull();
    expect(ticketIdFromUrl('/?ticket=')).toBeNull();
  });
});

describe('useDeepLinkTicket', () => {
  afterEach(() => {
    window.history.replaceState(null, '', '/');
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
