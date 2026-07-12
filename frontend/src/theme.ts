// One theme mechanism: `data-theme` on <html>, consumed by styles/tokens.css.
// Every route follows the system preference. ThemeColorSync owns the matchMedia
// subscription and calls these on route / preference changes.
export type Theme = 'light' | 'dark';

// Must match the two `--surface-page` values in styles/tokens.css — this is
// what the OS chrome (status bar / address bar / safe-area strips) paints.
export const THEME_COLORS: Record<Theme, string> = {
  light: '#faf6ef',
  dark: '#16110d',
};

export function resolveTheme(prefersDark: boolean): Theme {
  return prefersDark ? 'dark' : 'light';
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_COLORS[theme]);
}
