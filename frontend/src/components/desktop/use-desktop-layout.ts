// Which shell the app wears (13 §11, §13 Q4).
//
// 13 D8 settles the delivery form: desktop is "the responsive web app widening
// out", not a separate application. So the answer to Q4 taken here is **one
// responsive tree, two shells over the same stores**: `PrimaryScreen` still owns
// all the wiring (stores → props, actions → transport) and simply hands those
// props to the mobile view or the desktop view depending on the viewport. Both
// shells consume identical data; neither knows the other exists.
//
// The alternative — one shell restyled by media queries — was rejected because
// the two layouts do not share a DOM shape: mobile is a header/feed/dock column
// with a bottom-anchored overlay stack, desktop is a rail beside a feed. Forcing
// one tree to be both is how the mobile screen's carefully-tuned layering (see
// the web-client skill's bottom-anchored-UI principle) gets broken by a rule
// meant for the desk.
import { useEffect, useState } from 'react';

/**
 * The width at which the desktop layout takes over. Chosen as the point where a
 * rail plus a feed column with real reading air both fit — below it the rail
 * would be stealing width the feed needs, which is the opposite of 13 §6's
 * "room goes to legibility". Deliberately width-only: a tablet in landscape with
 * a keyboard gets the desk layout, and a narrow desktop window gets the phone
 * one, which is the honest reading of "responsive".
 */
export const DESKTOP_MIN_WIDTH = 1024;

/** The media query the shell switch is driven by — exported so the CSS-side and
 * the JS-side breakpoint can be asserted to agree. */
export const DESKTOP_MEDIA_QUERY = `(min-width: ${String(DESKTOP_MIN_WIDTH)}px)`;

/**
 * True while the viewport is desktop-sized. Subscribes to the query so a window
 * resize (or a dragged split) swaps shells live rather than on reload.
 *
 * Returns `false` where `matchMedia` is unavailable (jsdom, SSR) — the
 * mobile-first default (02 §11), so nothing that doesn't opt in gets the desk
 * layout by accident.
 */
export function useIsDesktop(): boolean {
  // Explicitly `<boolean>`: `useState(false)` infers the literal type `false`,
  // which makes every `if (isDesktop)` downstream look statically dead to the
  // lint gate's no-unnecessary-condition rule.
  const [isDesktop, setIsDesktop] = useState<boolean>(false);

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') {
      return;
    }
    const query = window.matchMedia(DESKTOP_MEDIA_QUERY);
    const sync = (): void => {
      setIsDesktop(query.matches);
    };
    sync();
    query.addEventListener('change', sync);
    return () => {
      query.removeEventListener('change', sync);
    };
  }, []);

  return isDesktop;
}
