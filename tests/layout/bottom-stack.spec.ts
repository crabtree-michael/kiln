// The bottom-anchored stack on the phone shell, measured.
//
// The bottom of the primary screen is a stack of layers that all grow UPWARD
// over the feed: the dock is the base, the live transcript overlays just above
// it, the notification hub (toast band / thinking chip) sits on top. The rules
// that hold it together used to live as ~75 lines of prose in a skill doc and as
// assertions about the text of PrimaryScreen.css. The same overlap bug was fixed
// five times with all of that green. These are the same claims, asked of the
// browser.
import { test, expect } from '@playwright/test';
import {
  mountShell,
  settle,
  box,
  maybeBox,
  paintedAt,
  intersects,
  layer,
  computedLayer,
} from './harness';

// A phone, and a SHORT phone: the short one is where the reserve, the band and
// the control are all competing for the same strip of screen.
const PHONE = { width: 390, height: 720 };
const SHORT_PHONE = { width: 390, height: 520 };

test.describe('phone shell — the bottom-anchored stack', () => {
  test.use({ viewport: PHONE });

  test('the hub sits above the dock’s current top, collapsed and expanded', async ({ page }) => {
    await mountShell(page, { band: 'say' });

    // Collapsed: the hub is anchored above the dock region's top edge. The band
    // deliberately overlaps that edge by 1px so the two fills read as one
    // continuous surface, so this is "clear of the dock", not "not touching it".
    const dock = await box(page, "[data-role='dock-region']");
    const hub = await box(page, "[data-role='activity-row']");
    expect(hub.bottom).toBeLessThanOrEqual(dock.top + 1);

    // Expanded: the dock is NOT a fixed height. A typed draft mounts the same
    // transcript overlay that speech does, and the hub has to clear its live
    // height rather than the dock's collapsed top.
    await page.click("[data-role='dock-keyboard']");
    await page.fill(
      "[data-role='dock-input']",
      'Take another look at the retry cap before the release goes out, and say ' +
        'whether it wants a second pass — the backoff table is the part I am unsure about.',
    );
    await settle(page);

    const transcript = await box(page, "[data-role='dock-transcript']");
    expect(transcript.height).toBeGreaterThan(0);
    const hubExpanded = await box(page, "[data-role='activity-row']");
    expect(hubExpanded.bottom).toBeLessThanOrEqual(transcript.top + 1);
    // ...and it really did move: anchoring to the collapsed dock instead of the
    // transcript's live height is the failure this is about, and it looks
    // identical from the DOM.
    expect(hubExpanded.bottom).toBeLessThan(hub.bottom);
  });

  test('a toast overlays "Show earlier" — it never pushes it up', async ({ page }) => {
    // The recurring bug, both halves. The feed's bottom reserve grows with the
    // band so the newest card can still be scrolled clear of it; the control is
    // pinned at the top of that reserve and must NOT ride the growth. So: same
    // standoff from the dock in every band state, and the band painting over the
    // control rather than the control poking out of the band.
    const standoffs: Record<string, number> = {};
    const painted: Record<string, (string | null)[]> = {};

    for (const band of ['none', 'toast', 'say'] as const) {
      await mountShell(page, { band });
      const control = await box(page, "[data-role='feed-show-earlier']");
      const dock = await box(page, "[data-role='dock-region']");
      standoffs[band] = Math.round(dock.top - control.bottom);
      const cx = control.left + control.width / 2;
      painted[band] = [
        // The middle of the control, and its top edge — a rounded hairline
        // poking out over the band's separator is the visible failure.
        await paintedAt(page, cx, control.top + control.height / 2),
        await paintedAt(page, cx, control.top + 1),
      ];
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }

    expect(standoffs['toast']).toBe(standoffs['none']);
    expect(standoffs['say']).toBe(standoffs['none']);

    // With no band the control is of course the thing on screen...
    expect(painted['none']).toContain('feed-show-earlier');
    // ...and with one, at neither point.
    for (const band of ['toast', 'say'] as const) {
      expect(painted[band], `${band}: the control is painting over the band`).not.toContain(
        'feed-show-earlier',
      );
    }
  });

  test('the thinking chip overlays "Show earlier" too — it does not lift it', async ({ page }) => {
    // The reserve's other occupant, and the half the old paint-only drop
    // deliberately left out: the drop was gated on a real toast band, so Kiln
    // starting to think lifted this control ~29px up the screen and dropped it
    // back when the chip went. The chip is a transient indicator like a toast,
    // and it floats over the feed for the same reason — so it moves nothing
    // either, and simply hovers over the control it lands on.
    const standoffs: Record<string, number> = {};
    for (const thinking of [false, true]) {
      await mountShell(page, { band: 'none', thinking });
      const control = await box(page, "[data-role='feed-show-earlier']");
      const dock = await box(page, "[data-role='dock-region']");
      standoffs[String(thinking)] = Math.round(dock.top - control.bottom);
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }
    expect(standoffs['true']).toBe(standoffs['false']);

    // ...and it is genuinely on top of it, not merely beside it: the chip is
    // opaque, so where the two meet the chip is what the user sees.
    await mountShell(page, { band: 'none', thinking: true });
    const chip = await box(page, "[data-role='thinking-indicator']");
    const control = await box(page, "[data-role='feed-show-earlier']");
    expect(intersects(chip, control)).toBe(true);
    // The chip's own word is what answers there, not the control underneath it
    // (`thinking-text` is the span inside the pill — either is the chip).
    expect(
      await paintedAt(page, chip.left + chip.width / 2, chip.top + chip.height / 2),
    ).toMatch(/^thinking-/);
  });

  test('the board is one size in every band state', async ({ page }) => {
    // The regression underneath the control's: the reserve is `padding-bottom`
    // on a scroll container, which extends the scroll AND shrinks the content
    // box — so every toast and every "Kiln is thinking" resized the board behind
    // it (529 → 500 → 478 → 411px at this viewport) and everything anchored to
    // its foot rode the change. The wrapper hands the band's growth back
    // (`--feed-overlay-slack`), so the laid-out board is one height throughout
    // and only the scroll extent moves.
    const heights: Record<string, number> = {};
    for (const state of [
      { key: 'rest', band: 'none', thinking: false },
      { key: 'thinking', band: 'none', thinking: true },
      { key: 'toast', band: 'toast', thinking: false },
      { key: 'say', band: 'say', thinking: false },
      { key: 'both', band: 'say', thinking: true },
    ] as const) {
      await mountShell(page, { band: state.band, thinking: state.thinking, cards: 0 });
      // The band really is standing there — a state that silently failed to
      // produce one would pass this test by not testing anything.
      const row = await box(page, "[data-role='activity-row']");
      expect(row.height, `${state.key}: no band to be unaffected by`).toBeGreaterThan(
        state.key === 'rest' ? 0 : 12,
      );
      heights[state.key] = Math.round((await box(page, "[data-role='feed-scroll']")).height);
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }
    for (const key of ['thinking', 'toast', 'say', 'both']) {
      expect(heights[key], `the board resized when the band was "${key}"`).toBe(heights['rest']);
    }
  });

  test('nothing the feed pins into the reserve paints over the dock layer', async ({ page }) => {
    // "Show earlier" carries an opaque 40px skirt below itself, to hide the
    // cards scrolling through the reserve. The dock layer's overlays stand in
    // that same reserve and own it. This is the assertion that fails when the
    // control is given a z-index of its own.
    await mountShell(page, { band: 'say' });
    const dock = await box(page, "[data-role='dock-region']");
    const stack = await box(page, "[data-role='toast-stack']");

    // Down the strip between the band's top edge and the dock's own top: every
    // sample must belong to the dock layer, never to the feed.
    const cx = stack.left + stack.width / 2;
    for (const y of [stack.top + 2, (stack.top + dock.top) / 2, dock.top - 2]) {
      const role = await paintedAt(page, cx, y);
      expect(role, `the feed is painting at y=${String(Math.round(y))}`).not.toBe(
        'feed-show-earlier',
      );
      expect(role).not.toBe('feed-card');
    }
  });

  test('the layer order is the published scale, not a literal per stylesheet', async ({ page }) => {
    await mountShell(page, { band: 'say' });

    // The scale itself, in the order tokens.css states it.
    const dock = await layer(page, 'layer-dock');
    const header = await layer(page, 'layer-header');
    const popover = await layer(page, 'layer-popover');
    const feed = await layer(page, 'layer-feed');
    expect(feed).toBeLessThan(dock);
    expect(dock).toBeLessThan(header);
    expect(header).toBeLessThan(popover);

    // Sealed inside the dock region's own stacking context (it carries the
    // keyboard-lift transform), so these order the dock's overlays and say
    // nothing about the dock versus the feed.
    const transcript = await layer(page, 'layer-dock-transcript');
    const hub = await layer(page, 'layer-dock-hub');
    expect(transcript).toBeLessThan(hub);

    // And the shell actually reads the scale rather than restating numbers.
    expect(await computedLayer(page, "[data-role='dock-region']")).toBe(dock);
    expect(await computedLayer(page, "[data-role='feed-header']")).toBe(header);
    expect(await computedLayer(page, "[data-role='activity-row']")).toBe(hub);
    // The transcript only exists while there is something to transcribe; a typed
    // draft mounts the same overlay speech does.
    await page.click("[data-role='dock-keyboard']");
    await page.fill("[data-role='dock-input']", 'Another look at the retry cap, please.');
    await page.waitForSelector("[data-role='dock-transcript']");
    expect(await computedLayer(page, "[data-role='dock-transcript']")).toBe(transcript);
    // The feed's pinned control carries NO layer of its own — that is the whole
    // rule, and `--layer-feed` exists to name the rung, not to be applied.
    expect(await computedLayer(page, "[data-role='feed-show-earlier']")).toBeNaN();
  });

  test('"Show earlier" ends the feed at every card count', async ({ page }) => {
    // Free space in the backlog goes ABOVE the control, so a feed too short to
    // scroll still puts it at the foot rather than under the last card. The
    // failure it replaced only looked right when the feed was empty.
    for (const cards of [0, 12]) {
      await mountShell(page, { band: 'none', cards });
      const control = await box(page, "[data-role='feed-show-earlier']");
      const feed = await box(page, "[data-role='feed']");
      // Within the feed region, and at its foot: no card sits below it.
      expect(control.bottom).toBeLessThanOrEqual(feed.bottom + 1);
      expect(feed.bottom - control.bottom).toBeLessThan(feed.height / 2);
      const below = await paintedAt(page, control.left + control.width / 2, control.bottom + 6);
      expect(below, `a card is showing under the control at ${String(cards)} cards`).not.toBe(
        'feed-card',
      );
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }
  });

  test('the reserve lets the last card be scrolled clear of the band', async ({ page }) => {
    // The reserve is for the CARDS. With a band up the feed must still be
    // scrollable far enough to bring the last card out from under it — that is
    // the one thing the band's height is allowed to change.
    await mountShell(page, { band: 'say', cards: 12 });
    await page.evaluate(() => {
      const feed = document.querySelector("[data-role='feed']");
      if (feed !== null) {
        feed.scrollTop = feed.scrollHeight;
      }
    });
    await settle(page);

    const stack = await box(page, "[data-role='toast-stack']");
    const last = await page.evaluate(() => {
      const cards = document.querySelectorAll("[data-role='feed-card']");
      const el = cards[cards.length - 1];
      if (el === undefined) {
        return null;
      }
      const r = el.getBoundingClientRect();
      return { top: r.top, right: r.right, bottom: r.bottom, left: r.left };
    });
    expect(last).not.toBeNull();
    expect(last?.bottom ?? Infinity).toBeLessThanOrEqual(stack.top);
  });
});

test.describe('short phone — the same stack with no room to spare', () => {
  test.use({ viewport: SHORT_PHONE });

  test('the band, the control and the dock still stack in order', async ({ page }) => {
    await mountShell(page, { band: 'say' });
    const hub = await box(page, "[data-role='activity-row']");
    const dock = await box(page, "[data-role='dock-region']");
    const control = await box(page, "[data-role='feed-show-earlier']");

    expect(hub.bottom).toBeLessThanOrEqual(dock.top + 1);
    // Everything stays on screen: a stack that grows upward off the top of a
    // short viewport is the failure mode this size exists to catch.
    expect(hub.top).toBeGreaterThanOrEqual(0);
    expect(dock.bottom).toBeLessThanOrEqual(SHORT_PHONE.height + 1);
    // The control is still under the band, not above it.
    const cx = control.left + control.width / 2;
    expect(await paintedAt(page, cx, control.top + 1)).not.toBe('feed-show-earlier');
  });

  test('the header’s dropdown still falls over the dock', async ({ page }) => {
    // The other end of the scale, and the reason the dock layer is 1 rather than
    // "as high as it needs to be": the header sits above it, so the status /
    // project / notification dropdowns drop over the dock instead of behind it.
    // On a 520px viewport the panel is long enough to reach it.
    await mountShell(page, { band: 'say', cards: 12 });
    const header = await box(page, "[data-role='feed-header']");
    const control = await box(page, "[data-role='feed-show-earlier']");
    expect(intersects(header, control)).toBe(false);

    await page.click("[data-role='feed-status']");
    await settle(page);
    const panel = await maybeBox(page, "[data-role='header-status-panel']");
    expect(panel, 'the status dropdown did not open').not.toBeNull();
    const dock = await box(page, "[data-role='dock-region']");
    if (panel !== null && panel.bottom > dock.top) {
      const role = await paintedAt(page, panel.left + panel.width / 2, dock.top + 2);
      expect(role, 'the dock is painting over the header dropdown').not.toBe('dock-region');
    }
  });
});
