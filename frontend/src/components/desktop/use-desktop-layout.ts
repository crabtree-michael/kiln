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

/**
 * Publishes `data-shell="desktop"` on `<body>` for as long as a desktop shell is
 * mounted, restoring whatever was there before on unmount.
 *
 * This is NOT about theming — the desktop shell pins no theme and follows the OS
 * like every other route (13 D6a). It exists because the ticket-detail sheet
 * portals to `document.body`, landing OUTSIDE the shell's subtree where no
 * descendant selector can reach it — and it still has to stop being a full-bleed
 * phone sheet at a desk (13 D7/D7a: it opens beside the work, it is not the
 * window). The edge it slides in from is a prop (`placement="right"`); the
 * geometry that goes with it is CSS, and this attribute is what lets that CSS
 * find the portaled panel.
 *
 * A `min-width` media query would work and is deliberately not used: the shell is
 * chosen in JS (`useIsDesktop`), so a breakpoint restated in CSS is a second
 * source of truth that can silently disagree with the first. This attribute IS
 * the JS decision, published where the portal can see it, so the two can never
 * drift.
 *
 * Every desktop shell calls it — the feed shell and the `/kanban` board view —
 * which is the other reason it is a hook rather than an effect inlined in one of
 * them: the second shell to forget it would open its ticket panel as a bottom
 * sheet across a 1440px window, and nothing in the DOM gate could see that.
 */
export function useDesktopShellFlag(): void {
  useEffect(() => {
    const previousShell = document.body.dataset.shell;
    document.body.dataset.shell = 'desktop';
    return () => {
      if (previousShell === undefined) {
        delete document.body.dataset.shell;
      } else {
        document.body.dataset.shell = previousShell;
      }
    };
  }, []);
}
