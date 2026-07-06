import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useKeyboardViewport } from '@/components/use-keyboard-viewport';

// A minimal stand-in for `window.visualViewport` whose `height`/`offsetTop` we
// can drive, capturing the resize/scroll listeners so the test can fire them.
function fakeVisualViewport(height: number) {
  const listeners: (() => void)[] = [];
  const vv = {
    height,
    offsetTop: 0,
    addEventListener: vi.fn((_type: string, cb: () => void) => {
      listeners.push(cb);
    }),
    removeEventListener: vi.fn(),
  };
  const fire = (): void => {
    for (const cb of listeners) {
      cb();
    }
  };
  return { vv, fire };
}

const APP_VAR = '--app-viewport-height';

afterEach(() => {
  document.documentElement.style.removeProperty(APP_VAR);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('useKeyboardViewport', () => {
  it('publishes the visual-viewport height when the keyboard covers the layout', () => {
    // Layout viewport 800px, visual viewport 500px → 300px covered (> threshold).
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('500px');
  });

  it('leaves the height at the 100dvh fallback when no keyboard is open', () => {
    // Only a ~60px address-bar delta — below the threshold, so no override.
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(740);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('');
  });

  it('clears the override when the keyboard closes', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv, fire } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });
    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('500px');

    act(() => {
      vv.height = 800;
      fire();
    });
    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('');
  });

  it('removes the override on unmount', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    const { unmount } = renderHook(() => {
      useKeyboardViewport();
    });
    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('500px');

    unmount();
    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('');
    expect(vv.removeEventListener).toHaveBeenCalledTimes(2);
  });

  it('is a no-op where visualViewport is unavailable', () => {
    vi.stubGlobal('visualViewport', undefined);
    expect(() => {
      renderHook(() => {
        useKeyboardViewport();
      });
    }).not.toThrow();
    expect(document.documentElement.style.getPropertyValue(APP_VAR)).toBe('');
  });

  it('pins the document back to the top when the keyboard opens', () => {
    // iOS scrolls the document up to lift the focused (bottom-anchored) dock input
    // above the keyboard; that scroll pushes the nav bar off the top. The hook must
    // snap the page back to (0, 0) when it detects the keyboard.
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    Object.defineProperty(window, 'scrollY', { value: 220, configurable: true });
    Object.defineProperty(window, 'scrollX', { value: 0, configurable: true });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it('re-pins on a window scroll that fires after the keyboard is up', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    Object.defineProperty(window, 'scrollX', { value: 0, configurable: true });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    // Keyboard open, but the page is at the top on the initial pass (nothing to pin).
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true });
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });
    expect(scrollTo).not.toHaveBeenCalled();

    // iOS then scrolls the field into view, firing a delayed window scroll.
    Object.defineProperty(window, 'scrollY', { value: 180, configurable: true });
    act(() => {
      window.dispatchEvent(new Event('scroll'));
    });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it('does not pin the page when no keyboard is open', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    Object.defineProperty(window, 'scrollX', { value: 0, configurable: true });
    Object.defineProperty(window, 'scrollY', { value: 140, configurable: true });
    const scrollTo = vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined);
    // Only an address-bar delta — below the keyboard threshold.
    const { vv } = fakeVisualViewport(740);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });
    // A stray window scroll with no keyboard up must be left alone.
    act(() => {
      window.dispatchEvent(new Event('scroll'));
    });

    expect(scrollTo).not.toHaveBeenCalled();
  });
});
