// The one theme mechanism, end to end: OS preference → `data-theme` on <html>
// (+ the theme-color meta), and a live subscription so a flip lands without a
// reload. `theme.test.ts` covers the two pure pieces; this covers the wiring,
// which is what every route — including the desktop shell, now that it no longer
// pins its own dark — actually depends on.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from '@testing-library/react';
import { act } from 'react';
import { ThemeColorSync } from '@/components/ThemeColorSync';
import { THEME_COLORS } from '@/theme';

interface FakeQuery {
  matches: boolean;
  media: string;
  addEventListener: (type: string, listener: () => void) => void;
  removeEventListener: (type: string, listener: () => void) => void;
}

/** A MediaQueryList stand-in whose `matches` can be flipped and broadcast, so an
 * OS theme change mid-session can be simulated. */
function stubPrefersDark(initial: boolean): {
  flip: (value: boolean) => void;
  listenerCount: () => number;
} {
  const listeners = new Set<() => void>();
  const query: FakeQuery = {
    matches: initial,
    media: '(prefers-color-scheme: dark)',
    addEventListener: (_type, listener) => listeners.add(listener),
    removeEventListener: (_type, listener) => listeners.delete(listener),
  };
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => query),
  );
  return {
    flip: (value: boolean) => {
      query.matches = value;
      act(() => {
        listeners.forEach((listener) => {
          listener();
        });
      });
    },
    listenerCount: () => listeners.size,
  };
}

function themeColorMeta(): HTMLMetaElement {
  const meta = document.createElement('meta');
  meta.setAttribute('name', 'theme-color');
  document.head.appendChild(meta);
  return meta;
}

describe('ThemeColorSync', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete document.documentElement.dataset.theme;
    document.querySelector('meta[name="theme-color"]')?.remove();
  });

  it('applies the system preference at mount', () => {
    const meta = themeColorMeta();
    stubPrefersDark(true);

    render(<ThemeColorSync />);

    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.dark);
  });

  it('follows a preference flip while the app is open, without a reload', () => {
    const meta = themeColorMeta();
    const query = stubPrefersDark(false);
    render(<ThemeColorSync />);
    expect(document.documentElement.dataset.theme).toBe('light');

    query.flip(true);

    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.dark);

    // And back — the subscription is not a one-shot.
    query.flip(false);

    expect(document.documentElement.dataset.theme).toBe('light');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.light);
  });

  it('drops its subscription on unmount', () => {
    const query = stubPrefersDark(true);
    const { unmount } = render(<ThemeColorSync />);
    expect(query.listenerCount()).toBe(1);

    unmount();

    expect(query.listenerCount()).toBe(0);
  });

  it('falls back to light where matchMedia is unavailable (jsdom, SSR)', () => {
    render(<ThemeColorSync />);
    expect(document.documentElement.dataset.theme).toBe('light');
  });
});
