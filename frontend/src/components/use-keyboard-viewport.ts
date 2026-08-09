import { useEffect } from 'react';

// Keep the dock (the bottom bar) riding just above the on-screen keyboard as it
// opens and closes, smoothly, without the nav bar or dock juddering.
//
// The bug: the screen is a `height: 100dvh` flex column with the dock as its
// last child. On iOS Safari the software keyboard shrinks the VISUAL viewport
// but not the LAYOUT viewport that `vh`/`dvh` track — so the 100dvh column keeps
// its full height and its bottom edge (the dock) slides down behind the keyboard,
// out of view. Worse, focusing the dock's own bottom-anchored input makes iOS
// scroll the whole document up to lift that field above the keyboard, dragging
// the pinned nav bar off the TOP of the screen.
//
// The earlier fix reacted to all of this: it shrank the column (rewriting its
// height on every intermediate resize, reflowing the feed each frame) and fought
// iOS's document scroll by snapping `scrollTo(0, 0)` on every scroll event, with
// a hard open/closed threshold. That kept things on-screen but the constant
// reflow + scroll-fight made the open/close animation judder.
//
// This fix rides the animation instead of fighting it, in three parts:
//
//   1. The document root is locked (no scrollable overflow — see the `html, body`
//      rules in tokens.css), so iOS has nothing to document-scroll when a bottom
//      field is focused. That removes the field-lift scroll at the source, so we
//      never touch `window.scrollTo` at all.
//
//   2. We publish the keyboard's overlap of the bottom edge as `--keyboard-inset`
//      on the document root, frame-synced to the visual viewport, and lift ONLY
//      the dock by exactly that (a compositor `translateY` in PrimaryScreen.css —
//      no column resize, no reflow). Tracking `covered` continuously (no hard
//      snap) lets the dock ride the OS open/close animation smoothly. A focused
//      editable field ("armed") lets the lift engage from the first pixel so the
//      OPEN animation is smooth too; with nothing focused we require a delta no
//      address-bar can reach, so a showing/hiding address bar never nudges the
//      dock. Once engaged we track all the way back down to ~0 before releasing,
//      so the CLOSE animation rides down smoothly as well.
//
//   3. The same inset is what every scroll region on the screen reserves as extra
//      bottom padding (the feed, the ticket sheet), so the strip the keyboard
//      covers becomes scroll room rather than lost content — and once the OS
//      animation has QUIETED, the focused field is nudged into view inside its own
//      scroller (`revealFocused` below). That is the native contract the whole
//      screen keeps: the keyboard overlays, the layout holds still, and the scroll
//      view underneath adjusts.
//
// Chrome Android used to be handled natively via `interactive-widget=resizes-content`
// — the browser shrank the layout viewport (and `dvh`) as the keyboard animated,
// so the 100dvh column shrank and the dock rode up with it. That is exactly the
// behaviour that was reported as wrong: the whole app screen was squeezed upward
// and the feed reflowed under the user's eye. index.html now asks for
// `overlays-content`, so Chrome leaves the layout viewport alone and behaves like
// iOS Safari — one keyboard model, and this hook is the single path that serves
// both engines.
//
// Mounted by the two shells that lock the document and own all their own
// scrolling — `PrimaryScreen` (which covers both the phone and the desk feed
// shells) and `KanbanScreen`. Everything else reads `--keyboard-inset` off the
// root; nothing else publishes it.
//
// A no-op where `visualViewport` is unavailable (jsdom / older engines), which is
// why the presentational snapshot tests (which render the view directly) are
// unaffected — this hook only runs in the live composing wrappers.

// With nothing focused, only a bottom-edge delta past this (well above any
// address-bar delta ~60–100px, below any soft keyboard ~250px+) engages the lift.
const KEYBOARD_MIN_PX = 150;
// With an editable field focused a keyboard is expected, so engage from the first
// real pixel of lift — this makes the OPEN animation smooth. A hardware keyboard
// never moves the viewport, so this still stays at rest when no soft keyboard shows.
const ARMED_MIN_PX = 30;
// Release the lift once the overlap has settled back to roughly nothing, so a
// close animation rides all the way down before we let go.
const SETTLE_MAX_PX = 30;
// How long the visual viewport must hold still before we call the OS keyboard
// animation finished and reveal the focused field. Long enough to outlast the
// gaps between resize events mid-animation, short enough that the nudge reads as
// part of the keyboard opening rather than as a second, later movement.
const QUIET_MS = 120;

function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable;
}

/**
 * Bring the focused field back into view inside its own scroller, once the
 * keyboard has finished opening.
 *
 * This is the "the scroll view handles it" half of the overlay model. The dock's
 * own field needs nothing — it rides up with the dock — and `block: 'nearest'` is
 * a no-op for anything already visible, so this costs nothing there. What it is
 * for is a field inside a scroll region the keyboard now covers part of (the
 * ticket sheet's body editor): the region has already reserved `--keyboard-inset`
 * as bottom padding, so scrolling to `nearest` lands the field above the keyboard
 * rather than under it.
 *
 * Deliberately NOT run per frame: called only after the viewport has quieted, so
 * it never fights the OS animation. `scrollIntoView` is absent in jsdom, hence
 * the guard.
 */
function revealFocused(): void {
  const focused = document.activeElement;
  if (!isEditable(focused) || !(focused instanceof HTMLElement)) {
    return;
  }
  if (typeof focused.scrollIntoView !== 'function') {
    return;
  }
  focused.scrollIntoView({ block: 'nearest' });
}

export function useKeyboardViewport(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    if (vv == null) {
      return;
    }
    const root = document.documentElement;
    // `armed`: an editable field is focused, so a soft keyboard is expected and the
    // lift may engage from the first pixel. `engaged`: the lift is currently active
    // and tracking the keyboard — latched, so the close animation rides down before
    // we release. `frame`: pending rAF id (0 = none) so bursts of resize/scroll
    // events coalesce to one measurement per frame.
    let armed = false;
    let engaged = false;
    let frame = 0;
    // Pending "the keyboard has stopped moving" timer (0 = none). Re-armed on
    // every measurement, so it only ever fires once the viewport has been quiet
    // for `QUIET_MS` — i.e. at the end of the OS animation, not during it.
    let quiet = 0;

    const publish = (): void => {
      frame = 0;
      // The layout viewport height does NOT shrink for the keyboard on either
      // engine (see the header), so the amount of it the visual viewport no longer
      // covers — allowing for any pan the browser applied (`offsetTop`) — is the
      // keyboard's overlap of the bottom-anchored dock. Subtracting `offsetTop` is
      // what makes this hold if a browser pans the visual viewport to reveal a
      // focused field instead of leaving it to us: the dock still lands exactly on
      // the visible bottom edge either way.
      const covered = Math.max(0, root.clientHeight - vv.height - vv.offsetTop);
      if (!engaged) {
        if (covered > (armed ? ARMED_MIN_PX : KEYBOARD_MIN_PX)) {
          engaged = true;
        }
      } else if (covered < SETTLE_MAX_PX) {
        engaged = false;
      }
      const inset = engaged ? covered : 0;
      root.style.setProperty('--keyboard-inset', `${inset.toString()}px`);

      // Re-arm the settle timer on every measurement. While the keyboard is
      // animating this just keeps pushing the deadline out; the reveal lands once
      // on the far side of it. A close (`inset` back to 0) cancels rather than
      // re-arms — there is no keyboard left to clear the field of.
      window.clearTimeout(quiet);
      quiet = 0;
      if (inset > 0) {
        quiet = window.setTimeout(revealFocused, QUIET_MS);
      }
    };

    // Frame-sync every update so a storm of resize/scroll events during the OS
    // keyboard animation collapses to one measurement per painted frame — the
    // dock rides the animation instead of thrashing on every intermediate event.
    const schedule = (): void => {
      if (frame === 0) {
        frame = window.requestAnimationFrame(publish);
      }
    };

    const onFocusIn = (event: FocusEvent): void => {
      if (isEditable(event.target)) {
        armed = true;
        schedule();
      }
    };
    // Only disarm — never force-close here. The keyboard closes over an animation
    // that outlives the synchronous blur, and the `engaged` latch (driven by the
    // measured overlap, not focus) tracks it all the way down.
    const onFocusOut = (): void => {
      armed = false;
    };

    publish();
    vv.addEventListener('resize', schedule);
    vv.addEventListener('scroll', schedule);
    document.addEventListener('focusin', onFocusIn);
    document.addEventListener('focusout', onFocusOut);
    return () => {
      if (frame !== 0) {
        window.cancelAnimationFrame(frame);
      }
      window.clearTimeout(quiet);
      vv.removeEventListener('resize', schedule);
      vv.removeEventListener('scroll', schedule);
      document.removeEventListener('focusin', onFocusIn);
      document.removeEventListener('focusout', onFocusOut);
      root.style.removeProperty('--keyboard-inset');
    };
  }, []);
}
