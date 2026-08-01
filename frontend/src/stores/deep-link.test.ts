// Deep-link plumbing tests (02 §10 tap-to-open, 12 §6.3 tap→project): parsing
// the `/app?project=<id>&ticket=<id>` link both halves of a tap read, the
// service-worker subscription, and the ticket hand-off that survives the
// project-switch remount.
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  parseDeepLink,
  stashDeepLinkTicket,
  subscribeDeepLink,
  takeDeepLinkTicket,
} from '@/stores/deep-link';

describe('parseDeepLink', () => {
  it('pulls both ids out of a deep link (full URL or bare query)', () => {
    expect(parseDeepLink('/app?project=p-2&ticket=t-login')).toEqual({
      projectId: 'p-2',
      ticketId: 't-login',
    });
    expect(parseDeepLink('?project=p+1&ticket=a+b%2Fc')).toEqual({
      projectId: 'p 1',
      ticketId: 'a b/c',
    });
    expect(parseDeepLink('/app?other=1&ticket=t-x')).toEqual({
      projectId: null,
      ticketId: 't-x',
    });
  });

  it('returns nulls for absent or empty params', () => {
    expect(parseDeepLink('/app')).toEqual({ projectId: null, ticketId: null });
    expect(parseDeepLink('/app?other=1')).toEqual({ projectId: null, ticketId: null });
    expect(parseDeepLink('/app?project=&ticket=')).toEqual({ projectId: null, ticketId: null });
    // A ticketless notify still lands the app on its project.
    expect(parseDeepLink('/app?project=p-2')).toEqual({ projectId: 'p-2', ticketId: null });
  });
});

describe('subscribeDeepLink', () => {
  afterEach(() => {
    takeDeepLinkTicket();
  });

  it('delivers parsed links from the service worker and stops on unsubscribe', () => {
    const swTarget = new EventTarget();
    const orig = Object.getOwnPropertyDescriptor(navigator, 'serviceWorker');
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: swTarget });
    try {
      const onLink = vi.fn();
      const unsubscribe = subscribeDeepLink(onLink);

      swTarget.dispatchEvent(
        new MessageEvent('message', {
          data: { type: 'kiln:navigate', url: '/app?project=p-2&ticket=t-x' },
        }),
      );
      expect(onLink).toHaveBeenCalledTimes(1);
      expect(onLink).toHaveBeenCalledWith({ projectId: 'p-2', ticketId: 't-x' });

      // Unrelated service-worker chatter is ignored.
      swTarget.dispatchEvent(new MessageEvent('message', { data: { type: 'other' } }));
      swTarget.dispatchEvent(new MessageEvent('message', { data: 'not-an-object' }));
      expect(onLink).toHaveBeenCalledTimes(1);

      unsubscribe();
      swTarget.dispatchEvent(
        new MessageEvent('message', { data: { type: 'kiln:navigate', url: '/app?ticket=t-y' } }),
      );
      expect(onLink).toHaveBeenCalledTimes(1);
    } finally {
      if (orig) {
        Object.defineProperty(navigator, 'serviceWorker', orig);
      } else {
        Reflect.deleteProperty(navigator, 'serviceWorker');
      }
    }
  });

  it('is a no-op (and its unsubscribe is safe) with no service worker', () => {
    const orig = Object.getOwnPropertyDescriptor(navigator, 'serviceWorker');
    Reflect.deleteProperty(navigator, 'serviceWorker');
    try {
      expect(() => {
        subscribeDeepLink(vi.fn())();
      }).not.toThrow();
    } finally {
      if (orig) {
        Object.defineProperty(navigator, 'serviceWorker', orig);
      }
    }
  });
});

describe('the ticket hand-off across a project switch', () => {
  afterEach(() => {
    takeDeepLinkTicket();
  });

  it('yields the stashed ticket exactly once', () => {
    expect(takeDeepLinkTicket()).toBeNull();
    stashDeepLinkTicket('t-blocked');
    expect(takeDeepLinkTicket()).toBe('t-blocked');
    // Taken means consumed: the next screen to mount must not reopen it.
    expect(takeDeepLinkTicket()).toBeNull();
  });
});
