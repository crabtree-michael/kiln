// The desk shell's geometry, measured.
//
// The desk grew the phone's bottom reserve — `--feed-bottom-inset` reaches
// `[data-role='desktop-screen']` and the same "Show earlier" rule names both
// roots — so it inherited the same recurring overlap bug, and fixes for it have
// alternated between the two shells. Both are checked here, off the same
// harness, so a fix to one that breaks the other is visible in one run.
import { test, expect } from '@playwright/test';
import { mountShell, box, maybeBox, paintedAt, intersects, stableBox, computed } from './harness';

const DESK = { width: 1280, height: 860 };
// The threshold the shell switch fires at (use-desktop-layout.ts). The narrowest
// desk window is where the furniture is most likely to eat the feed.
const NARROW_DESK = { width: 1024, height: 800 };
// A tablet in landscape: ≥1024px, so it gets THIS shell (the breakpoint is
// width-only on purpose, 13 §11) — and it is the one place the desk layout is
// driven by a finger rather than a mouse.
const TABLET = { width: 1194, height: 834 };

/** How far the listening mic's glow reaches past the button's edge: the outer
 * stop of `kiln-mic-glow` is a 5px spread under a 28px blur at full level, and a
 * blur fades over roughly half its radius. */
const MIC_GLOW_REACH_PX = 20;

test.describe('desk shell', () => {
  test.use({ viewport: DESK });

  test('lays out as three columns that tile the window', async ({ page }) => {
    await mountShell(page, { band: 'none' });
    const rail = await box(page, "[data-role='desktop-rail']");
    const panel = await box(page, "[data-role='desktop-working-panel']");
    const feed = await box(page, "[data-role='desktop-feed']");

    // Left to right, side by side, none of them overlapping — the failure the
    // string tests could not see is a grid that collapses to one stacked column
    // while every DOM assertion still passes.
    expect(rail.right).toBeLessThanOrEqual(panel.left + 1);
    expect(panel.right).toBeLessThanOrEqual(feed.left + 1);
    expect(intersects(rail, panel)).toBe(false);
    expect(intersects(panel, feed)).toBe(false);
    // Nothing runs off the window, and the shell fills its height.
    expect(feed.right).toBeLessThanOrEqual(DESK.width + 1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      DESK.width,
    );
  });

  test('leaves the feed a real reading measure at the narrowest desk window', async ({ page }) => {
    await page.setViewportSize(NARROW_DESK);
    await mountShell(page, { band: 'none' });
    const feed = await box(page, "[data-role='desktop-feed']");
    // The furniture must not eat the feed: at the 1024px threshold the two fixed
    // columns have to leave a column worth reading behind.
    expect(feed.width).toBeGreaterThanOrEqual(500);
    // ...and the cards inside it are capped at one column, not spread.
    const list = await box(page, "[data-role='desktop-feed-list']");
    expect(list.width).toBeLessThanOrEqual(feed.width);

    // That cap is the FEED's, and it must not reach the working column beside
    // it: the backlog list is stated as its own rule for exactly this reason, so
    // it spans its column's content box rather than inheriting a reading measure
    // meant for prose.
    const panel = await box(page, "[data-role='desktop-working-panel']");
    const backlog = await box(page, "[data-role='desktop-backlog-list']");
    expect(backlog.width).toBeGreaterThan(panel.width * 0.8);
    expect(backlog.right).toBeLessThanOrEqual(panel.right + 1);
  });

  test('a toast overlays the desk’s "Show earlier" too, off the same rule', async ({ page }) => {
    const standoffs: Record<string, number> = {};
    const paintedOnTop: Record<string, string | null> = {};

    for (const band of ['none', 'say'] as const) {
      await mountShell(page, { band });
      const control = await box(page, "[data-role='feed-show-earlier']");
      const region = await box(page, "[data-role='desktop-composer-region']");
      standoffs[band] = Math.round(region.top - control.bottom);
      paintedOnTop[band] = await paintedAt(
        page,
        control.left + control.width / 2,
        control.top + control.height / 2,
      );
      await page.unrouteAll({ behavior: 'ignoreErrors' });
    }

    expect(standoffs['say']).toBe(standoffs['none']);
    expect(paintedOnTop['none']).toBe('feed-show-earlier');
    expect(paintedOnTop['say']).not.toBe('feed-show-earlier');
  });

  test('"Show earlier" ends the desk feed and shares the cards’ column', async ({ page }) => {
    await mountShell(page, { band: 'none', cards: 0 });
    const control = await box(page, "[data-role='feed-show-earlier']");
    const feed = await box(page, "[data-role='desktop-feed']");
    // At the foot of the region even with nothing above it — the regression this
    // pins is a control that only reached the foot when the feed was empty by
    // accident, because the resting block happened to push it there.
    expect(feed.bottom - control.bottom).toBeLessThan(feed.height / 2);

    await mountShell(page, { band: 'none', cards: 8 });
    const withCards = await box(page, "[data-role='feed-show-earlier']");
    const list = await box(page, "[data-role='desktop-feed-list']");
    // Centred on the cards' measure rather than hugging the region's left edge
    // (a button is inline-block by default, where auto margins compute to zero).
    expect(
      Math.abs((withCards.left + withCards.right) / 2 - (list.left + list.right) / 2),
    ).toBeLessThan(2);
  });

  test('reserves the mic glow above the composer, so a band cannot clip it', async ({ page }) => {
    // The activity row is anchored to the composer region's TOP edge with an
    // opaque fill. With the composer flush against that edge the band cut the
    // listening mic's glow off along a hard horizontal line.
    await mountShell(page, { band: 'none' });
    const region = await box(page, "[data-role='desktop-composer-region']");
    const composer = await box(page, "[data-role='desktop-composer']");
    expect(composer.top - region.top).toBeGreaterThanOrEqual(MIC_GLOW_REACH_PX);
  });

  test('holds a toast to the composer’s measure, so it never runs past the field', async ({
    page,
  }) => {
    // The band's markup is the phone's, sized for a viewport that IS the
    // composer. At a desk the composer is a centred column, so without a cap the
    // pill grows with its text until it overhangs the line it is answering.
    await mountShell(page, { band: 'say' });
    const composer = await box(page, "[data-role='desktop-composer']");
    const pill =
      (await maybeBox(page, "[data-role='say-pill']")) ??
      (await maybeBox(page, "[data-role='toast-pill']"));
    expect(pill, 'no toast pill on screen').not.toBeNull();
    if (pill !== null) {
      expect(pill.left).toBeGreaterThanOrEqual(composer.left - 1);
      expect(pill.right).toBeLessThanOrEqual(composer.right + 1);
    }

    // The BAND, by contrast, is not capped: its opaque fill is what hides the
    // feed's last card, right out to the region's edges.
    const stack = await box(page, "[data-role='toast-stack']");
    const region = await box(page, "[data-role='desktop-composer-region']");
    expect(stack.width).toBeGreaterThanOrEqual(region.width - 1);
  });

  test('the ticket sheet opens beside the feed, not over the whole window', async ({ page }) => {
    await mountShell(page, { band: 'none' });
    await page.click("[data-role='feed-card-open']");
    await page.waitForSelector("[data-role='ticket-detail']");
    // Vaul slides the panel in under an inline transform: wait for rest, or the
    // box read here is the one it starts from, off the right edge of the window.
    const sheet = await stableBox(page, "[data-role='ticket-detail']");
    const rail = await box(page, "[data-role='desktop-rail']");
    // Against the right edge, full height, and narrow enough that the work it is
    // being read against is still visible behind it.
    expect(Math.round(sheet.right)).toBe(DESK.width);
    expect(Math.round(sheet.top)).toBe(0);
    expect(Math.round(sheet.bottom)).toBe(DESK.height);
    expect(sheet.width).toBeLessThan(680);
    expect(intersects(sheet, rail)).toBe(false);
    // It paints above the shell, and the scrim above the feed behind it.
    expect(await paintedAt(page, sheet.left + sheet.width / 2, sheet.top + 40)).not.toBe(
      'desktop-feed',
    );
  });

  test('a resting feed buys no scrollbar on a mouse window', async ({ page }) => {
    // The fine-pointer half of the touch rule below: the pixel that keeps a
    // short feed bounceable is coarse-pointer-only precisely so it does not
    // surface a permanent scrollbar on every desk window.
    await mountShell(page, { band: 'none', cards: 0 });
    expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(false);
    expect(await feedOverflow(page)).toBe(0);
  });
});

/** How far the feed's scrolled content runs past its scrollport. The whole touch
 * rule is about keeping this above zero, so it is the measurement both sides of
 * it are stated in. */
async function feedOverflow(page: import('@playwright/test').Page): Promise<number> {
  return page.evaluate(() => {
    const region = document.querySelector("[data-role='desktop-feed']");
    return region === null ? -1 : region.scrollHeight - region.clientHeight;
  });
}

test.describe('desk shell on a tablet — a short feed still answers the finger', () => {
  // Touch is a CONTEXT property, not a viewport: `hasTouch` + `isMobile` is what
  // makes Chromium report `(hover: none) and (pointer: coarse)`, which is the
  // query the rule is behind.
  test.use({ viewport: TABLET, hasTouch: true, isMobile: true });

  test('holds the scrolled wrapper a pixel past the scrollport, keeping the bounce', async ({
    page,
  }) => {
    await mountShell(page, { band: 'none', cards: 0 });
    // First, because everything under it is meaningless if the emulation did not
    // take — the assertions would all pass against a plain desk window.
    expect(
      await page.evaluate(() => matchMedia('(hover: none) and (pointer: coarse)').matches),
      'the coarse-pointer emulation did not take',
    ).toBe(true);

    // A scroller whose content fits has nothing to scroll AND no rubber-band to
    // give, so a quiet feed stopped answering touch at all — a dead panel at
    // exactly the moment there is least on screen to say the app is alive.
    const overflow = await feedOverflow(page);
    expect(overflow, 'a resting feed has nothing to scroll, so it cannot bounce').toBeGreaterThan(0);
    // ...and imperceptibly so: this is one pixel of slack, not a scrollbar's worth
    // of dead space under the cards.
    expect(overflow).toBeLessThanOrEqual(2);

    // The bounce survives. `contain`/`none` here reads to iOS WebKit as "no
    // elastic bounce either" and would suppress the very thing the pixel unlocks;
    // chaining is already dead-ended at the locked html/body.
    expect(await computed(page, "[data-role='desktop-feed']", 'overscroll-behavior-y')).toBe('auto');

    // The pinned control stays INSIDE the wrapper: beside it, the wrapper's extra
    // height becomes scrollable slack the size of the control, and the control
    // loses the containing block its `position: sticky` hangs off.
    expect(
      await page.evaluate(
        () =>
          document
            .querySelector("[data-role='desktop-feed-scroll']")
            ?.contains(document.querySelector("[data-role='feed-show-earlier']")) ?? false,
      ),
      '"Show earlier" is outside the scrolled wrapper',
    ).toBe(true);
  });
});
