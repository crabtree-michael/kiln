// Detects whether the client is running as an installed, standalone web app
// (added to the home screen / an installed PWA) rather than in a normal browser
// tab. Two signals, because iOS and the web standard disagree:
//
//   - `navigator.standalone === true` is iOS Safari's non-standard flag, set
//     only when the page is launched from a home-screen icon. iOS does NOT
//     implement the `display-mode` media query, so this is the only reliable
//     signal there — and iOS is exactly the case we care about, since a
//     home-screen launch always opens the manifest `start_url` (`/`).
//   - `matchMedia('(display-mode: standalone)')` is the web-standard signal,
//     honoured by installed Chrome/Edge/Android web apps.
//
// Either being true means "installed web app". jsdom (vitest) has neither, so
// this returns false there — the browser-tab default.
export function isStandaloneWebApp(): boolean {
  if (navigator.standalone === true) {
    return true;
  }
  if (typeof window.matchMedia === 'function') {
    return window.matchMedia('(display-mode: standalone)').matches;
  }
  return false;
}
