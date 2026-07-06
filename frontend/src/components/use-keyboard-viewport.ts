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
// The fix: while the keyboard is up, publish the visual viewport's height as
// `--app-viewport-height` on the document root; `PrimaryScreen.css` reads it
// (`height: var(--app-viewport-height, 100dvh)`) so the column shrinks to the
// visible area and the dock rides just above the keyboard. When the keyboard is
// closed the var is removed and the column falls back to the tuned `100dvh`
// behaviour — so normal scrolling / address-bar resize is untouched.
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
    const update = (): void => {
      // The layout viewport height does NOT shrink for the keyboard, so the
      // amount of it the visual viewport no longer covers — allowing for any
      // page scroll the browser applied to reveal the focused field — is the
      // keyboard's height. Only override once that clears a threshold well above
      // any address-bar delta (~60–100px) but below a keyboard (~250px+), so a
      // showing/hiding address bar never trips it.
      const covered = root.clientHeight - vv.height - vv.offsetTop;
      if (covered > 150) {
        root.style.setProperty('--app-viewport-height', `${vv.height.toString()}px`);
      } else {
        root.style.removeProperty('--app-viewport-height');
      }
    };
    update();
    vv.addEventListener('resize', update);
    vv.addEventListener('scroll', update);
    return () => {
      vv.removeEventListener('resize', update);
      vv.removeEventListener('scroll', update);
      root.style.removeProperty('--app-viewport-height');
    };
  }, []);
}
