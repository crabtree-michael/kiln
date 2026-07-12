// Applies the resolved theme (system preference → src/theme.ts) to the
// document: `data-theme` on <html> for styles/tokens.css, and the `theme-color`
// meta for OS chrome / safe-area strips. Subscribes to the OS
// prefers-color-scheme query so the app follows live theme flips without a
// reload.
import { useEffect } from 'react';
import { applyTheme, resolveTheme } from '@/theme';

export function ThemeColorSync(): null {
  useEffect(() => {
    // jsdom (vitest) has no matchMedia — default to light there.
    if (typeof window.matchMedia !== 'function') {
      applyTheme(resolveTheme(false));
      return;
    }
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const sync = () => {
      applyTheme(resolveTheme(query.matches));
    };
    sync();
    query.addEventListener('change', sync);
    return () => {
      query.removeEventListener('change', sync);
    };
  }, []);
  return null;
}
