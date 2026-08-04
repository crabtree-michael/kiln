// The shell switch (13 D8 / §13 Q4). jsdom ships no `matchMedia`, which is also
// the production fallback path worth pinning: without it the app stays on the
// mobile-first default rather than guessing a desk.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import {
  useIsDesktop,
  DESKTOP_MEDIA_QUERY,
  DESKTOP_MIN_WIDTH,
} from '@/components/desktop/use-desktop-layout';

interface FakeQuery {
  matches: boolean;
  media: string;
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
}

/** A minimal MediaQueryList stand-in whose `matches` can be flipped and
 * broadcast, so a window resize can be simulated. */
function stubMatchMedia(initial: boolean): {
  flip: (value: boolean) => void;
  removed: () => number;
} {
  const listeners = new Set<() => void>();
  let removals = 0;
  const query: FakeQuery = {
    matches: initial,
    media: DESKTOP_MEDIA_QUERY,
    addEventListener: (_type, listener) => listeners.add(listener),
    removeEventListener: (_type, listener) => {
      removals += 1;
      listeners.delete(listener);
    },
  };
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => query),
  );
  return {
    flip: (value: boolean) => {
      query.matches = value;
      listeners.forEach((listener) => {
        listener();
      });
    },
    removed: () => removals,
  };
}

describe('useIsDesktop', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('the JS breakpoint is stated once and the query is built from it', () => {
    expect(DESKTOP_MEDIA_QUERY).toBe(`(min-width: ${String(DESKTOP_MIN_WIDTH)}px)`);
  });

  it('falls back to the mobile-first default where matchMedia is unavailable', () => {
    const { result } = renderHook(() => useIsDesktop());
    expect(result.current).toBe(false);
  });

  it('reports desktop when the query matches at mount', () => {
    stubMatchMedia(true);
    const { result } = renderHook(() => useIsDesktop());
    expect(result.current).toBe(true);
  });

  it('swaps live on a resize rather than waiting for a reload', () => {
    const query = stubMatchMedia(false);
    const { result } = renderHook(() => useIsDesktop());
    expect(result.current).toBe(false);

    act(() => {
      query.flip(true);
    });
    expect(result.current).toBe(true);

    act(() => {
      query.flip(false);
    });
    expect(result.current).toBe(false);
  });

  it('unsubscribes on unmount', () => {
    const query = stubMatchMedia(true);
    const { unmount } = renderHook(() => useIsDesktop());
    unmount();
    expect(query.removed()).toBe(1);
  });
});
