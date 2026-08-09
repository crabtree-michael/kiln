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
//
// The gesture is deliberately hard to claim and impossible to claim by accident
// (amended): a touch is only a pull once the finger has travelled a slop distance
// decisively downward, from the top, more vertically than horizontally. Everything
// else stays the browser's, with its momentum intact, so the feed scrolls up as
// naturally as it pulls down. See DIRECTION_SLOP_PX.
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

/** Finger travel (px) before a touch is read as having a direction at all.
 *
 * This is what keeps the feed scrollable BOTH ways. `preventDefault` is a
 * one-way door: the moment it suppresses the native scroll, the browser will not
 * hand that touch back, momentum and all. Claiming on the first pixel of
 * downward travel therefore took over every gesture that opened with a hair of
 * downward jitter — including the upward flicks that start with one — and the
 * feed only ever moved down. Below this threshold nothing is claimed and nothing
 * is prevented, so a flick that turns out to be upward is still the browser's. */
const DIRECTION_SLOP_PX = 8;

/** How one touch is being read.
 * - `watching` — begun at the top, direction not yet decided; nothing claimed.
 * - `pulling`  — claimed as a deliberate downward pull; we own the gesture.
 * - `released` — left to native scrolling for the rest of this touch. */
type Phase = 'watching' | 'pulling' | 'released';

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
    let startX = 0;
    let phase: Phase = 'released';
    let busy = false; // a refresh round-trip is in flight

    function onTouchStart(event: TouchEvent): void {
      const touch = event.touches[0];
      // Only a single-finger drag from the very top can become a pull; a pinch or
      // a drag begun mid-scroll is the browser's for the whole touch.
      if (
        busy ||
        touch === undefined ||
        event.touches.length !== 1 ||
        el === null ||
        el.scrollTop > 0
      ) {
        phase = 'released';
        return;
      }
      startY = touch.clientY;
      startX = touch.clientX;
      // Watching, not claiming: which gesture this is isn't known for another
      // DIRECTION_SLOP_PX of travel, and guessing costs the user their scroll.
      phase = 'watching';
    }

    function onTouchMove(event: TouchEvent): void {
      const touch = event.touches[0];
      if (busy || touch === undefined || el === null || phase === 'released') {
        return;
      }
      const dy = touch.clientY - startY;
      const dx = touch.clientX - startX;

      if (phase === 'watching') {
        // Still inside the slop: the direction isn't decided, so claim nothing
        // and prevent nothing — the browser is free to start scrolling.
        if (Math.abs(dy) < DIRECTION_SLOP_PX && Math.abs(dx) < DIRECTION_SLOP_PX) {
          return;
        }
        // Past it, and this is a pull only if the finger went decisively DOWN,
        // more down than sideways (a card's swipe-to-clear is the other axis),
        // and the feed is still at the top. Anything else — an upward flick, a
        // sideways swipe, a feed already scrolled — is native scrolling's, and
        // stays so for the rest of this touch.
        if (dy < DIRECTION_SLOP_PX || Math.abs(dx) > Math.abs(dy) || el.scrollTop > 0) {
          phase = 'released';
          return;
        }
        phase = 'pulling';
      }

      // Ours now: suppress the native scroll/rubber-band for the rest of the
      // touch (the browser won't give it back once we do, which is why the
      // reversal below is handled here rather than by handing over).
      if (event.cancelable) {
        event.preventDefault();
      }

      if (dy > 0) {
        // Pull the spinner down under the finger with resistance.
        setDragging(true);
        setPull(Math.min(MAX_PULL_PX, dy * RESISTANCE));
        return;
      }
      // The finger has come back past where it started: mid-gesture, the user
      // stopped pulling and started scrolling the list. Ease the indicator shut
      // and drive the scroll ourselves, 1:1 with the finger — this touch can no
      // longer be given back to the browser, so the alternative is a feed that
      // goes dead the moment a pull is reversed.
      setDragging(false);
      setPull(0);
      el.scrollTop = Math.min(-dy, Math.max(0, el.scrollHeight - el.clientHeight));
    }

    function onTouchEnd(): void {
      // A refresh in flight owns the indicator (it rests open for the round-trip);
      // a finger lifting must not close it.
      if (busy) {
        phase = 'released';
        return;
      }
      const wasPulling = phase === 'pulling';
      phase = 'released';
      setDragging(false);
      if (!wasPulling) {
        // Nothing claimed this touch — or a claim was cut short by a second
        // finger landing. Either way, close anything left hanging open.
        setPull(0);
        return;
      }
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
