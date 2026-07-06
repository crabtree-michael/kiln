import { useEffect } from 'react';

// Keep the primary-screen column matched to the *visible* viewport while the
// on-screen keyboard is open, so the dock (the bottom bar) stays docked above
// the keyboard instead of being pushed off-screen behind it.
//
// The bug: the screen is a `height: 100dvh` flex column with the dock as its
// last child. On iOS Safari the software keyboard shrinks the VISUAL viewport
// but not the LAYOUT viewport that `vh`/`dvh` track — so the 100dvh column keeps
// its full height and its bottom edge (the dock) slides down behind the
// keyboard, out of view. (Chrome Android's default `interactive-widget` behaves
// the same way: the visual viewport shrinks, the layout viewport does not.)
//
// The fix, part 1: while the keyboard is up, publish the visual viewport's height
// as `--app-viewport-height` on the document root; `PrimaryScreen.css` reads it
// (`height: var(--app-viewport-height, 100dvh)`) so the column shrinks to the
// visible area and the dock rides just above the keyboard. When the keyboard is
// closed the var is removed and the column falls back to the tuned `100dvh`
// behaviour — so normal scrolling / address-bar resize is untouched.
//
// The fix, part 2 (why part 1 alone wasn't enough): shrinking the column keeps
// the dock ABOVE the keyboard, but the instant a text field is focused iOS Safari
// ALSO scrolls the whole *document* up to lift that field above the keyboard. The
// dock's own typed-input field (`data-role="dock-input"`) sits at the very bottom
// of the column — right where the keyboard opens — so focusing it forces exactly
// that document scroll, which drags the pinned nav bar off the TOP of the screen.
// (A field higher on the page is already above the keyboard, so it never triggers
// the scroll — which is why the height-only fix looked fine against a generic
// input but failed against the in-dock one.) Shrinking the column doesn't undo the
// scroll offset, so we pin the document back to the top ourselves while the
// keyboard is up. Scroll-only (no `transform`) so the fixed `TicketDetail` modal
// keeps its viewport containing block — a transform on this ancestor would break
// its `position: fixed; inset: 0` anchoring.
//
// A no-op where `visualViewport` is unavailable (jsdom / older engines), which
// is why the presentational snapshot tests (which render the view directly) are
// unaffected — this hook only runs in the live composing wrapper.
export function useKeyboardViewport(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    if (vv == null) {
      return;
    }
    const root = document.documentElement;
    let keyboardOpen = false;
    // Undo any document scroll iOS applied to lift the focused field above the
    // keyboard — that scroll is what pushes the nav bar off the top. Guarded so
    // it never fires when the page is already pinned (avoids a needless scroll
    // event feedback loop).
    const pinToTop = (): void => {
      if (window.scrollX !== 0 || window.scrollY !== 0) {
        window.scrollTo(0, 0);
      }
    };
    const update = (): void => {
      // The layout viewport height does NOT shrink for the keyboard, so the
      // amount of it the visual viewport no longer covers — allowing for any
      // page scroll the browser applied to reveal the focused field — is the
      // keyboard's height. Only override once that clears a threshold well above
      // any address-bar delta (~60–100px) but below a keyboard (~250px+), so a
      // showing/hiding address bar never trips it.
      const covered = root.clientHeight - vv.height - vv.offsetTop;
      if (covered > 150) {
        keyboardOpen = true;
        root.style.setProperty('--app-viewport-height', `${vv.height.toString()}px`);
        pinToTop();
      } else {
        keyboardOpen = false;
        root.style.removeProperty('--app-viewport-height');
      }
    };
    // The document scroll iOS applies on focus can land AFTER the resize that
    // opened the keyboard, so also snap back on any window scroll while the
    // keyboard is up. The page is never meant to scroll here (the dock is pinned,
    // the feed scrolls inside its own region), so any window scroll in this state
    // is the field-lift we want to cancel.
    const onWindowScroll = (): void => {
      if (keyboardOpen) {
        pinToTop();
      }
    };
    update();
    vv.addEventListener('resize', update);
    vv.addEventListener('scroll', update);
    window.addEventListener('scroll', onWindowScroll, { passive: true });
    return () => {
      vv.removeEventListener('resize', update);
      vv.removeEventListener('scroll', update);
      window.removeEventListener('scroll', onWindowScroll);
      root.style.removeProperty('--app-viewport-height');
    };
  }, []);
}
