// Keeps the browser's `theme-color` meta in sync with the active screen, so the
// OS system chrome (iOS status bar, Android address bar) and the safe-area
// strips some shells tint with it read as one surface with the app rather than
// a black letterbox. `/` is the light "paper" primary screen (08, and the
// index.html default); `/debug` is the dark developer shell (07). This mirrors
// the `color-scheme` split in App.css, which drives the UA-painted regions.
import { useEffect } from 'react';
import { useLocation } from 'react-router-dom';

// Paper tone of the primary screen (flat base of its gradient,
// oklch(0.955 0.004 45) ≈ #f3efee) and the dark debug shell background.
const THEME_COLOR_PRIMARY = '#f3efee';
const THEME_COLOR_DEBUG = '#0b0b0f';

export function ThemeColorSync(): null {
  const location = useLocation();
  useEffect(() => {
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta === null) {
      return;
    }
    const isDebug = location.pathname.startsWith('/debug');
    meta.setAttribute('content', isDebug ? THEME_COLOR_DEBUG : THEME_COLOR_PRIMARY);
  }, [location.pathname]);
  return null;
}
