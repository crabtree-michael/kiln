import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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

const INSET_VAR = '--keyboard-inset';

function inset(): string {
  return document.documentElement.style.getPropertyValue(INSET_VAR);
}

// Focus an editable field so the hook arms (a soft keyboard is expected), driving
// the `focusin` path the same way a real focus would.
function focusField(): void {
  const field = document.createElement('textarea');
  document.body.append(field);
  act(() => {
    field.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
  });
}

beforeEach(() => {
  // Frame-synced updates run through requestAnimationFrame; invoke it
  // synchronously so a fired event settles within the same `act`.
  // Run synchronously and return 0 (the hook's "no frame pending" sentinel) so the
  // callback having already run leaves the guard consistent for the next event.
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback): number => {
    cb(0);
    return 0;
  });
  vi.stubGlobal('cancelAnimationFrame', () => undefined);
});

afterEach(() => {
  document.documentElement.style.removeProperty(INSET_VAR);
  document.body.replaceChildren();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('useKeyboardViewport', () => {
  it('publishes the keyboard overlap when it covers the bottom edge', () => {
    // Layout viewport 800px, visual viewport 500px → 300px covered (> threshold).
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    expect(inset()).toBe('300px');
  });

  it('leaves the dock at rest for an address-bar delta with nothing focused', () => {
    // Only a ~60px address-bar delta — below the unarmed threshold, so no lift.
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(740);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    expect(inset()).toBe('0px');
  });

  it('engages from the first pixels of lift once a field is focused', () => {
    // 80px covered is below the unarmed 150px gate but, with the field focused, a
    // keyboard is expected so the lift engages immediately — the open animation
    // rides from the start instead of snapping in partway.
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv, fire } = fakeVisualViewport(800);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });
    expect(inset()).toBe('0px');

    focusField();
    act(() => {
      vv.height = 720;
      fire();
    });

    expect(inset()).toBe('80px');
  });

  it('tracks the overlap continuously as the keyboard closes', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv, fire } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });
    expect(inset()).toBe('300px');

    // Mid-close: an overlap that is below the open gate still tracks, because the
    // lift stays engaged (latched) until it settles near zero — so the dock rides
    // the close animation down smoothly rather than dropping at a threshold.
    act(() => {
      vv.height = 700;
      fire();
    });
    expect(inset()).toBe('100px');

    // Settled: overlap back near zero releases the lift.
    act(() => {
      vv.height = 795;
      fire();
    });
    expect(inset()).toBe('0px');
  });

  it('ignores a stray viewport change with nothing focused', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv, fire } = fakeVisualViewport(800);
    vi.stubGlobal('visualViewport', vv);

    renderHook(() => {
      useKeyboardViewport();
    });

    // An address-bar-sized change, no field focused → the dock stays put.
    act(() => {
      vv.height = 730;
      fire();
    });
    expect(inset()).toBe('0px');
  });

  it('removes the override on unmount', () => {
    vi.spyOn(document.documentElement, 'clientHeight', 'get').mockReturnValue(800);
    const { vv } = fakeVisualViewport(500);
    vi.stubGlobal('visualViewport', vv);

    const { unmount } = renderHook(() => {
      useKeyboardViewport();
    });
    expect(inset()).toBe('300px');

    unmount();
    expect(inset()).toBe('');
    expect(vv.removeEventListener).toHaveBeenCalledTimes(2);
  });

  it('is a no-op where visualViewport is unavailable', () => {
    vi.stubGlobal('visualViewport', undefined);
    expect(() => {
      renderHook(() => {
        useKeyboardViewport();
      });
    }).not.toThrow();
    expect(inset()).toBe('');
  });
});
