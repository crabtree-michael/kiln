// Pull-to-refresh gesture for the feed (this change). A downward drag that begins
// at the very top of the feed scroller pulls a spinner into view; releasing past
// the trigger threshold re-fetches the feed and holds the spinner up for the whole
// round-trip, then springs it back. Native (non-passive) touch listeners are used
// rather than React's synthetic handlers so `preventDefault` can actually suppress
// the browser's own scroll/rubber-band while we take the gesture over — the same
// reason SwipeToDismiss owns its pointer stream, but on the vertical (scroll) axis
// preventing default requires a passive:false listener, which only addEventListener
// can give us. Touch-only: the pattern is a mobile affordance, and desktop keeps
// its normal scroll (07 mobile-first client).
import { useEffect, useState, type RefObject } from 'react';

/** Finger travel is damped by this factor into visible pull, so the spinner
 * trails the finger with the elastic feel of a native rubber-band. */
const RESISTANCE = 0.5;

/** Visible pull (px) past which releasing triggers a refresh rather than a
 * spring-back — a deliberate pull, not an incidental drag. */
const TRIGGER_PX = 56;

/** Visible pull is clamped here so a long drag can't push the whole feed
 * arbitrarily far down. */
const MAX_PULL_PX = 90;

/** Where the spinner rests (px) while the refresh is in flight — enough to sit
 * clear of the header without shoving the feed far down. */
const REFRESH_REST_PX = 44;

/** Floor on how long the spinner stays up once triggered, even if the fetch
 * resolves sooner, so a fast refresh reads as a deliberate action instead of a
 * flicker. */
const MIN_SPIN_MS = 450;

export interface PullToRefresh {
  /** Current visible pull distance in px: tracks the finger while dragging, rests
   * at `REFRESH_REST_PX` while refreshing, and is 0 at rest. Drive the indicator's
   * height off this. */
  pull: number;
  /** True from the moment a pull is committed until the refresh round-trip settles
   * — spin the indicator while this holds. */
  refreshing: boolean;
  /** True only while the finger is actively dragging (not springing back or
   * refreshing), so the view can suppress the height transition and track 1:1. */
  dragging: boolean;
}

/**
 * Wire pull-to-refresh onto a scroll container. `onRefresh` should return a
 * promise that resolves once the reload has settled; the spinner is held up until
 * then. Pass `onRefresh: undefined` to disable the gesture entirely (no listeners
 * attach, the returned state stays inert) — lets the presentational screen omit it
 * so its DOM/snapshots are unchanged when unwired.
 */
export function usePullToRefresh(
  scrollRef: RefObject<HTMLElement | null>,
  onRefresh: (() => Promise<void>) | undefined,
): PullToRefresh {
  const [pull, setPull] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [dragging, setDragging] = useState(false);

  useEffect(() => {
    const el = scrollRef.current;
    if (el === null || onRefresh === undefined) {
      return;
    }
    // Capture the narrowed callback in a const so the nested handlers keep the
    // non-undefined type (TS doesn't carry a parameter's narrowing into closures).
    const refresh = onRefresh;

    let startY = 0;
    let active = false; // a downward-from-top pull has engaged
    let busy = false; // a refresh round-trip is in flight

    function onTouchStart(event: TouchEvent): void {
      const touch = event.touches[0];
      // Only a single-finger drag from the very top arms the gesture; a pinch or a
      // drag begun mid-scroll is left to the browser.
      if (
        busy ||
        touch === undefined ||
        event.touches.length !== 1 ||
        el === null ||
        el.scrollTop > 0
      ) {
        return;
      }
      startY = touch.clientY;
      active = true;
    }

    function onTouchMove(event: TouchEvent): void {
      const touch = event.touches[0];
      if (!active || busy || touch === undefined || el === null) {
        return;
      }
      const dy = touch.clientY - startY;
      // An upward move, or any move once the feed has scrolled off the top, hands
      // the gesture back to native scrolling.
      if (dy <= 0 || el.scrollTop > 0) {
        active = false;
        setDragging(false);
        setPull(0);
        return;
      }
      // Take the gesture over: suppress the native scroll/rubber-band and pull the
      // spinner down under the finger with resistance.
      if (event.cancelable) {
        event.preventDefault();
      }
      setDragging(true);
      setPull(Math.min(MAX_PULL_PX, dy * RESISTANCE));
    }

    function onTouchEnd(): void {
      if (!active || busy) {
        return;
      }
      active = false;
      setDragging(false);
      setPull((current) => {
        if (current < TRIGGER_PX) {
          return 0; // short pull — spring back, no refresh
        }
        busy = true;
        setRefreshing(true);
        const started = performance.now();
        void refresh().finally(() => {
          // Hold the spinner up for at least MIN_SPIN_MS so a fast fetch doesn't
          // flash the indicator on and off.
          const elapsed = performance.now() - started;
          const wait = Math.max(0, MIN_SPIN_MS - elapsed);
          window.setTimeout(() => {
            busy = false;
            setRefreshing(false);
            setPull(0);
          }, wait);
        });
        return REFRESH_REST_PX;
      });
    }

    el.addEventListener('touchstart', onTouchStart, { passive: true });
    el.addEventListener('touchmove', onTouchMove, { passive: false });
    el.addEventListener('touchend', onTouchEnd);
    el.addEventListener('touchcancel', onTouchEnd);
    return () => {
      el.removeEventListener('touchstart', onTouchStart);
      el.removeEventListener('touchmove', onTouchMove);
      el.removeEventListener('touchend', onTouchEnd);
      el.removeEventListener('touchcancel', onTouchEnd);
    };
  }, [scrollRef, onRefresh]);

  return { pull, refreshing, dragging };
}
