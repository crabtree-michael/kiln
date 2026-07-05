// Route + system preference → data-theme (design spec 2026-07-05 §3):
// /debug always renders "Kiln at night"; / follows the OS.
import { afterEach, describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme, THEME_COLORS } from '@/theme';

describe('resolveTheme', () => {
  it('follows the system preference on /', () => {
    expect(resolveTheme('/', false)).toBe('light');
    expect(resolveTheme('/', true)).toBe('dark');
  });

  it('forces dark on /debug regardless of preference', () => {
    expect(resolveTheme('/debug', false)).toBe('dark');
    expect(resolveTheme('/debug', true)).toBe('dark');
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
