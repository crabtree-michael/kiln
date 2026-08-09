// How wide the tickets column is, and the drag that sets it (13 §3, §8.2).
//
// The column shipped at a fixed 248px, chosen as "enough for a ticket title and
// no more" — every pixel past that is width taken from the feed's legibility
// (13 §6). That is the right DEFAULT and the wrong ceiling: titles are written
// by the brain and by the user, projects differ, and on a wide window there is
// slack the reader may want to spend on seeing a title whole rather than
// elided. So the boundary between the column and the feed becomes a thing you
// can move, and the shipped width becomes the FLOOR rather than the whole
// story: the column can be widened to twice it and never narrowed past it,
// because below it the rows stop being readable and the panel's own reason for
// existing goes with them.
//
// **The width is published imperatively, not held in React state**, and that is
// the load-bearing decision here. A pointer drag fires a move event per frame;
// re-rendering the shell — the feed's whole card list included — on each of them
// is how a resize handle comes to feel like it is dragging through treacle. The
// live value therefore lives in a ref and is written straight to the two places
// that read it: a custom property on the shell root (which the grid template
// reads) and the separator's `aria-valuenow`. Both are single-writer — nothing
// renders either from props — so a re-render behind a drag cannot fight it.
//
// What is state is the one bit a stylesheet cannot derive: whether a drag is in
// flight, which the shell publishes as `data-resizing` so the whole window keeps
// the resize cursor and stops selecting text under the pointer. That flips twice
// per drag, not once per frame.
import { useCallback, useEffect, useRef, useState } from 'react';
import type { KeyboardEvent, PointerEvent, RefObject } from 'react';

/**
 * The column's resting width, and its floor.
 *
 * This is the width the column shipped at, restated in DesktopScreen.css as the
 * grid template's fallback so the layout is correct before this hook has written
 * anything (and in any test that renders the panel without it). The two are
 * allowed to be two literals because they are not the same claim: one is where
 * the column starts, the other is how narrow the user may make it, and they
 * happen to be the same number.
 */
export const WORKING_PANEL_MIN_WIDTH = 248;

/** ...and its ceiling: twice the resting width. Past that the column stops being
 * peripheral-vision furniture and starts competing with the feed for the eye's
 * starting point, which is the arrangement 13 §3 settled against. */
export const WORKING_PANEL_MAX_WIDTH = WORKING_PANEL_MIN_WIDTH * 2;

/** How far one arrow press moves the edge — the keyboard's equivalent of a drag
 * (the ARIA window-splitter pattern). Coarse enough to cross the whole range in
 * a manageable number of presses, fine enough to land where you meant to. */
const KEY_STEP_PX = 16;

/** Where the live width is published: a custom property on the shell root, which
 * is the element whose `grid-template-columns` reads it. */
export const WORKING_PANEL_WIDTH_VAR = '--desk-working-width';

/** The width the column may actually take, given one it was asked for. Rounded
 * because the value ends up in a CSS length and an `aria-valuenow` — a column
 * 291.40625px wide is a number nobody wants read out to them. */
export function clampWorkingPanelWidth(width: number): number {
  return Math.min(WORKING_PANEL_MAX_WIDTH, Math.max(WORKING_PANEL_MIN_WIDTH, Math.round(width)));
}

/**
 * What a key press does to the width, or `null` for a key this control does not
 * claim (which the caller must leave to the page — a separator that swallowed
 * Tab would be a trap).
 *
 * Home/End go straight to the ends rather than stepping, which is what the
 * splitter pattern asks for and is also the only way back to the default: the
 * default IS the minimum, so Home is the "reset" this control would otherwise
 * need a second gesture for.
 */
export function widthAfterKey(key: string, width: number): number | null {
  switch (key) {
    case 'ArrowLeft':
      return clampWorkingPanelWidth(width - KEY_STEP_PX);
    case 'ArrowRight':
      return clampWorkingPanelWidth(width + KEY_STEP_PX);
    case 'Home':
      return WORKING_PANEL_MIN_WIDTH;
    case 'End':
      return WORKING_PANEL_MAX_WIDTH;
    default:
      return null;
  }
}

export interface WorkingPanelResize {
  /** Attach to the shell root: the width is published there, because that is the
   * element whose grid template reads it. */
  shellRef: RefObject<HTMLDivElement>;
  /** Attach to the separator: its `aria-valuenow` is published from here, so the
   * control reports the width it is actually setting rather than the one it was
   * rendered with. */
  separatorRef: RefObject<HTMLDivElement>;
  /** True while a pointer drag is in flight — the shell wears it as
   * `data-resizing`. */
  dragging: boolean;
  onPointerDown: (event: PointerEvent<HTMLDivElement>) => void;
  onPointerMove: (event: PointerEvent<HTMLDivElement>) => void;
  /** Up and cancel are the same ending: the drag stops where the pointer left
   * it. There is nothing to spring back to — the width the user let go at IS
   * the width they chose. */
  onPointerEnd: (event: PointerEvent<HTMLDivElement>) => void;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
}

export function useWorkingPanelWidth(): WorkingPanelResize {
  const shellRef = useRef<HTMLDivElement>(null);
  const separatorRef = useRef<HTMLDivElement>(null);
  // The live width, and the two things a drag is measured from. Refs rather than
  // state for the reason in the header: a frame's worth of movement must not
  // cost a render of the feed beside it.
  const widthRef = useRef(WORKING_PANEL_MIN_WIDTH);
  const startXRef = useRef(0);
  const startWidthRef = useRef(WORKING_PANEL_MIN_WIDTH);
  const pointerRef = useRef<number | null>(null);
  const [dragging, setDragging] = useState(false);

  const publish = useCallback((width: number): void => {
    widthRef.current = width;
    shellRef.current?.style.setProperty(WORKING_PANEL_WIDTH_VAR, `${String(width)}px`);
    separatorRef.current?.setAttribute('aria-valuenow', String(width));
  }, []);

  // State the resting width once on mount. The custom property is a no-op here
  // (the stylesheet's fallback is the same number), but `aria-valuenow` is not:
  // a splitter with no value is one a screen reader cannot report, and this is
  // the only writer of that attribute — see the header.
  useEffect(() => {
    publish(widthRef.current);
  }, [publish]);

  const onPointerDown = (event: PointerEvent<HTMLDivElement>): void => {
    if (event.pointerType === 'mouse' && event.button !== 0) {
      return;
    }
    pointerRef.current = event.pointerId;
    startXRef.current = event.clientX;
    // Measured from where the drag STARTED, not from the pointer's absolute
    // position: the user grabs the handle somewhere within its width, and an
    // absolute reading would jump the edge by that offset the moment they
    // pressed. Deliberately no `preventDefault()` — that would suppress the
    // compatibility mouse events, and with them the focus this control needs to
    // be arrow-driveable after a click.
    startWidthRef.current = widthRef.current;
    const el = event.currentTarget;
    // Keep receiving move/up once the pointer outruns a 5px-wide handle, which
    // it does immediately. Guarded: jsdom implements no pointer capture.
    if (typeof el.setPointerCapture === 'function') {
      el.setPointerCapture(event.pointerId);
    }
    setDragging(true);
  };

  const onPointerMove = (event: PointerEvent<HTMLDivElement>): void => {
    if (pointerRef.current !== event.pointerId) {
      return;
    }
    publish(clampWorkingPanelWidth(startWidthRef.current + (event.clientX - startXRef.current)));
  };

  const onPointerEnd = (event: PointerEvent<HTMLDivElement>): void => {
    if (pointerRef.current !== event.pointerId) {
      return;
    }
    pointerRef.current = null;
    setDragging(false);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    const next = widthAfterKey(event.key, widthRef.current);
    if (next === null) {
      return;
    }
    event.preventDefault();
    publish(next);
  };

  return {
    shellRef,
    separatorRef,
    dragging,
    onPointerDown,
    onPointerMove,
    onPointerEnd,
    onKeyDown,
  };
}
