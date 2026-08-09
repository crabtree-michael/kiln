// The tickets column's width, as arithmetic. The DRAG is exercised where it can
// be seen end to end — DesktopScreenView.test.tsx fires real pointer events at
// the separator and reads what the shell publishes — and the geometry it
// produces is measured in a browser (tests/layout/desktop-shell.spec.ts). What
// is left here is the pair of pure decisions underneath both: what width the
// column is allowed to take, and what a key press asks for.
import { describe, expect, it } from 'vitest';
import {
  clampWorkingPanelWidth,
  widthAfterKey,
  WORKING_PANEL_MAX_WIDTH,
  WORKING_PANEL_MIN_WIDTH,
} from '@/components/desktop/use-working-panel-width';

describe('the width the column may take', () => {
  it('floors at the width the column shipped at', () => {
    // The floor is not a taste call: below it the two-line rows stop holding a
    // ticket title, which is the one thing the panel exists to show. A drag that
    // runs off the left of the screen still lands on it.
    expect(clampWorkingPanelWidth(WORKING_PANEL_MIN_WIDTH - 1)).toBe(WORKING_PANEL_MIN_WIDTH);
    expect(clampWorkingPanelWidth(0)).toBe(WORKING_PANEL_MIN_WIDTH);
    expect(clampWorkingPanelWidth(-4000)).toBe(WORKING_PANEL_MIN_WIDTH);
  });

  it('ceilings at twice it', () => {
    expect(WORKING_PANEL_MAX_WIDTH).toBe(WORKING_PANEL_MIN_WIDTH * 2);
    expect(clampWorkingPanelWidth(WORKING_PANEL_MAX_WIDTH + 1)).toBe(WORKING_PANEL_MAX_WIDTH);
    expect(clampWorkingPanelWidth(4000)).toBe(WORKING_PANEL_MAX_WIDTH);
  });

  it('passes anything between them through, as a whole pixel', () => {
    expect(clampWorkingPanelWidth(300)).toBe(300);
    // A pointer reports fractions on a scaled display, and the value ends up in
    // both a CSS length and an `aria-valuenow` — one of which gets read out.
    expect(clampWorkingPanelWidth(291.40625)).toBe(291);
  });
});

describe('the keyboard path', () => {
  it('steps the boundary with the arrows, in the direction it moves', () => {
    // Right widens the column, left narrows it — the key moves the boundary the
    // way the drag would, not the way a "value" would.
    const wider = widthAfterKey('ArrowRight', 300);
    const narrower = widthAfterKey('ArrowLeft', 300);
    expect(wider).toBeGreaterThan(300);
    expect(narrower).toBeLessThan(300);
    // Symmetric, so a press and its opposite return to where they started.
    expect(widthAfterKey('ArrowLeft', wider ?? 0)).toBe(300);
  });

  it('clamps the steps to the same two bounds a drag has', () => {
    expect(widthAfterKey('ArrowLeft', WORKING_PANEL_MIN_WIDTH)).toBe(WORKING_PANEL_MIN_WIDTH);
    expect(widthAfterKey('ArrowRight', WORKING_PANEL_MAX_WIDTH)).toBe(WORKING_PANEL_MAX_WIDTH);
  });

  it('sends Home and End straight to the ends', () => {
    // Home is also the only "reset" this control needs: the default width IS the
    // minimum, so there is nothing for a second gesture (a double-click, a menu
    // item) to do that this key does not already do.
    expect(widthAfterKey('Home', 400)).toBe(WORKING_PANEL_MIN_WIDTH);
    expect(widthAfterKey('End', 260)).toBe(WORKING_PANEL_MAX_WIDTH);
  });

  it('claims nothing else — a separator that swallowed Tab would be a trap', () => {
    for (const key of ['Tab', 'Enter', ' ', 'Escape', 'ArrowUp', 'ArrowDown', 'a']) {
      expect(widthAfterKey(key, 300), `${key} was claimed`).toBeNull();
    }
  });
});
