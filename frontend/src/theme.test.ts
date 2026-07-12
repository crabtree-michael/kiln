// System preference → data-theme (design spec 2026-07-05 §3): every route
// follows the OS preference.
import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme, THEME_COLORS } from '@/theme';

describe('resolveTheme', () => {
  it('follows the system preference', () => {
    expect(resolveTheme(false)).toBe('light');
    expect(resolveTheme(true)).toBe('dark');
  });
});

describe('applyTheme', () => {
  afterEach(() => {
    delete document.documentElement.dataset.theme;
    document.querySelector('meta[name="theme-color"]')?.remove();
  });

  it('stamps data-theme on <html> and syncs the theme-color meta', () => {
    const meta = document.createElement('meta');
    meta.setAttribute('name', 'theme-color');
    document.head.appendChild(meta);

    applyTheme('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.dark);

    applyTheme('light');
    expect(document.documentElement.dataset.theme).toBe('light');
    expect(meta.getAttribute('content')).toBe(THEME_COLORS.light);
  });

  it('tolerates a missing theme-color meta', () => {
    expect(() => {
      applyTheme('light');
    }).not.toThrow();
    expect(document.documentElement.dataset.theme).toBe('light');
  });
});
