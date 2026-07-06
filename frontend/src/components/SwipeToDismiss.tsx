// Swipe-left-to-clear wrapper (08 §3). Wraps one dismissible feed row and turns a
// leftward drag into a dismiss: the row follows the finger, a "Clear" affordance
// is revealed behind it, and releasing past the threshold flings the row off and
// fires `onDismiss`; releasing short springs it back. Pure pointer events + CSS
// transforms — no gesture library — matching the client's no-dependencies rule
// (07 D4) and the pointer-listener style already in HeaderStatusMenu.
//
// Vertical intent is left alone: until a drag is clearly more horizontal than
// vertical it doesn't engage, so the feed still scrolls normally through a row.
// Only used where a card is actually clearable (update/preview with a
// notification id) — the caller gates that, so a wrapped row is always dismissible.
import { useEffect, useRef, useState, type JSX, type PointerEvent, type ReactNode } from 'react';

/** Past this many px of leftward travel on release, the row is dismissed rather
 * than sprung back — a deliberate swipe, not an incidental drag. */
const DISMISS_THRESHOLD_PX = 72;

/** Below this much movement the gesture hasn't engaged either axis yet — used to
 * decide horizontal-vs-vertical intent before committing to a drag. */
const ENGAGE_SLOP_PX = 8;

/** How long the fling-off plays before the clear is handed up. Matches the
 * `--duration-base` transform transition on `[data-role='swipe-content']`; a
 * timer (not `transitionend`) drives it so it still fires under
 * `prefers-reduced-motion`, where the transition is suppressed. */
const FLING_MS = 240;

export interface SwipeToDismissProps {
  /** Fired once when the row is swiped past the threshold and has flung off. */
  onDismiss: () => void;
  /** The row to make swipeable (a single feed card). */
  children: ReactNode;
}

export function SwipeToDismiss({ onDismiss, children }: SwipeToDismissProps): JSX.Element {
  // Live drag offset (<= 0; left is negative). `null` while not dragging so the
  // content sits at rest with its spring-back transition applied.
  const [offset, setOffset] = useState<number | null>(null);
  // True from the moment a dismiss is committed until `onDismiss` fires — drives
  // the fling-off transition and blocks any further gesture.
  const [dismissing, setDismissing] = useState(false);

  const startXRef = useRef(0);
  const startYRef = useRef(0);
  // null = undecided, true = horizontal drag engaged, false = yielded to scroll.
  const horizontalRef = useRef<boolean | null>(null);
  const pointerRef = useRef<number | null>(null);
  const flingTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Clear a pending fling timer if the row unmounts first (e.g. the card was
  // removed for another reason) so it never fires into an unmounted component.
  useEffect(() => {
    return () => {
      if (flingTimerRef.current !== null) {
        clearTimeout(flingTimerRef.current);
      }
    };
  }, []);

  const onPointerDown = (event: PointerEvent<HTMLDivElement>): void => {
    if (dismissing || (event.pointerType === 'mouse' && event.button !== 0)) {
      return;
    }
    startXRef.current = event.clientX;
    startYRef.current = event.clientY;
    horizontalRef.current = null;
    pointerRef.current = event.pointerId;
  };

  const onPointerMove = (event: PointerEvent<HTMLDivElement>): void => {
    if (pointerRef.current !== event.pointerId || dismissing) {
      return;
    }
    const dx = event.clientX - startXRef.current;
    const dy = event.clientY - startYRef.current;

    // Decide intent once past the slop: a mostly-vertical move yields to the
    // feed's own scroll and this row stops tracking the pointer for good.
    if (horizontalRef.current === null) {
      if (Math.abs(dx) < ENGAGE_SLOP_PX && Math.abs(dy) < ENGAGE_SLOP_PX) {
        return;
      }
      horizontalRef.current = Math.abs(dx) > Math.abs(dy);
      if (horizontalRef.current) {
        // Keep receiving move/up even if the finger leaves the row mid-drag.
        const el = event.currentTarget;
        if (typeof el.setPointerCapture === 'function') {
          el.setPointerCapture(event.pointerId);
        }
      }
    }
    if (!horizontalRef.current) {
      return;
    }
    // Only leftward travel clears; a rightward drag clamps at rest so the row
    // never slides off its right edge.
    event.preventDefault();
    setOffset(Math.min(0, dx));
  };

  const endDrag = (event: PointerEvent<HTMLDivElement>): void => {
    if (pointerRef.current !== event.pointerId || dismissing) {
      return;
    }
    pointerRef.current = null;
    const engaged = horizontalRef.current === true;
    horizontalRef.current = null;
    const travelled = event.clientX - startXRef.current;
    if (engaged && travelled <= -DISMISS_THRESHOLD_PX) {
      setDismissing(true);
      flingTimerRef.current = setTimeout(() => {
        flingTimerRef.current = null;
        onDismiss();
      }, FLING_MS);
      return;
    }
    setOffset(null); // spring back to rest.
  };

  const dragging = offset !== null && !dismissing;
  // While dismissing, slide the row fully off to the left; the store then drops
  // the card from the feed, unmounting this row.
  const translate = dismissing ? -1000 : (offset ?? 0);

  return (
    <div data-role="swipe" data-dismissing={dismissing ? 'true' : undefined}>
      <span data-role="swipe-action" aria-hidden="true">
        Clear
      </span>
      <div
        data-role="swipe-content"
        data-dragging={dragging ? 'true' : undefined}
        style={{ transform: `translateX(${String(translate)}px)` }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
      >
        {children}
      </div>
    </div>
  );
}
