// The soft keyboard overlays the app; it never resizes or pushes it.
//
// What was wrong: Chrome Android's default `interactive-widget=resizes-content`
// shrinks the LAYOUT viewport as the keyboard animates in, and the whole app is a
// `100dvh` column — so the entire screen was squeezed upward and the feed
// reflowed under the user's eye. A native app doesn't do that: the keyboard sits
// OVER the screen and the scroll view underneath adjusts. index.html now asks for
// `overlays-content`, which lines Chrome up with iOS Safari (which never resized
// the layout viewport), and the layout answers the keyboard in three ways —
// the dock rides above it, the feed reserves the covered strip as scroll room,
// and the ticket sheet stands its footer clear of it.
//
// A spec cannot raise a real soft keyboard, and `visualViewport` is read-only. So
// these drive the ONE seam everything above hangs off: `--keyboard-inset`, the
// overlap `use-keyboard-viewport` publishes on the document root. That the hook
// MEASURES that number correctly is the unit suite's claim
// (`use-keyboard-viewport.test.ts`); what is asserted here is the half jsdom
// cannot see — what the layout does with it.
import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { mountShell, settle, box, stableBox } from './harness';

const PHONE = { width: 390, height: 720 };
// The desk shell is reached by WIDTH alone, so a tablet in landscape gets it
// under a finger — and therefore under a soft keyboard.
const TABLET = { width: 1194, height: 834 };
// A plausible phone keyboard: well past the hook's unarmed gate, and deep enough
// that anything still sitting under it is unmistakably under it.
const KEYBOARD = 300;
/** The screen line the top of the keyboard would be on, on the phone. */
const KEYBOARD_TOP = PHONE.height - KEYBOARD;

/** Raise / drop the simulated keyboard by writing the hook's own output. */
async function keyboard(page: Page, px: number): Promise<void> {
  await page.evaluate((value) => {
    document.documentElement.style.setProperty('--keyboard-inset', `${String(value)}px`);
  }, px);
  await settle(page);
}

/** A scroll region's total scrollable range — what grows when the region turns
 * the covered strip into scroll room. */
async function scrollRange(page: Page, selector: string): Promise<number> {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    return el === null ? 0 : el.scrollHeight - el.clientHeight;
  }, selector);
}

test.describe('phone shell — the keyboard overlays the app', () => {
  test.use({ viewport: PHONE });

  test('the page asks the browser to overlay rather than resize', async ({ page }) => {
    // The whole model rests on this one token, and nothing else in the gate can
    // see it: with `resizes-content` back in place every assertion below still
    // passes (they drive the CSS var directly) while the real phone reflows.
    await mountShell(page);
    const content = await page.evaluate(
      () => document.querySelector('meta[name="viewport"]')?.getAttribute('content') ?? '',
    );
    expect(content).toContain('interactive-widget=overlays-content');
  });

  test('nothing the user is looking at moves when the keyboard opens', async ({ page }) => {
    // The reported bug, stated as geometry: the app column, the pinned header and
    // the card under the user's eye are all in exactly the same place with the
    // keyboard up as without it.
    await mountShell(page, { band: 'none' });

    const before = {
      screen: await box(page, "[data-role='primary-screen']"),
      header: await box(page, "[data-role='feed-header']"),
      card: await box(page, "[data-role='feed-card']"),
    };

    await keyboard(page, KEYBOARD);

    const after = {
      screen: await box(page, "[data-role='primary-screen']"),
      header: await box(page, "[data-role='feed-header']"),
      card: await box(page, "[data-role='feed-card']"),
    };

    expect(after.screen).toEqual(before.screen);
    expect(after.header).toEqual(before.header);
    expect(after.card).toEqual(before.card);
    // Said once more the way the regression would read: the column still measures
    // the full viewport rather than the space left over beside the keyboard.
    expect(Math.round(after.screen.height)).toBe(PHONE.height);
  });

  test('the dock rides up to sit flush on top of the keyboard', async ({ page }) => {
    await mountShell(page, { band: 'none' });
    const rest = await box(page, "[data-role='dock-region']");
    expect(Math.round(rest.bottom)).toBe(PHONE.height);

    await keyboard(page, KEYBOARD);
    const lifted = await box(page, "[data-role='dock-region']");

    expect(Math.round(lifted.bottom)).toBe(KEYBOARD_TOP);
    // A transform, not a resize: the dock is the same dock, moved.
    expect(Math.round(lifted.height)).toBe(Math.round(rest.height));
  });

  test('the feed turns the covered strip into scroll room', async ({ page }) => {
    // The other half of "overlay, don't resize". The feed's box still runs to the
    // bottom of the screen, so without this the last cards would be permanently
    // stranded behind the keyboard with no way to bring them up.
    await mountShell(page, { band: 'none' });
    const rest = await scrollRange(page, "[data-role='feed']");

    await keyboard(page, KEYBOARD);
    const withKeyboard = await scrollRange(page, "[data-role='feed']");
    expect(Math.round(withKeyboard - rest)).toBe(KEYBOARD);

    // And it is real room: scrolled to the end, the last card clears the keyboard
    // — and the lifted dock above it.
    await page.evaluate(() => {
      const feed = document.querySelector("[data-role='feed']");
      if (feed !== null) {
        feed.scrollTop = feed.scrollHeight;
      }
    });
    await settle(page);

    const dock = await box(page, "[data-role='dock-region']");
    const last = await page.evaluate(() => {
      const cards = document.querySelectorAll("[data-role='feed-card']");
      const el = cards[cards.length - 1];
      return el === undefined ? null : el.getBoundingClientRect().bottom;
    });
    expect(last).not.toBeNull();
    expect(last ?? Infinity).toBeLessThanOrEqual(dock.top + 1);
  });

  test('"Show earlier" keeps its standoff from the dock, above the keyboard', async ({ page }) => {
    // The control is sticky against the feed's CONTENT box, which the same
    // padding defines — so it comes up with the dock for free rather than needing
    // the inset named a second time in its own offset (the trap its rule warns
    // about, and which would double the clearance).
    await mountShell(page, { band: 'none' });
    const restDock = await box(page, "[data-role='dock-region']");
    const restControl = await box(page, "[data-role='feed-show-earlier']");
    const restStandoff = Math.round(restDock.top - restControl.bottom);

    await keyboard(page, KEYBOARD);
    const dock = await box(page, "[data-role='dock-region']");
    const control = await box(page, "[data-role='feed-show-earlier']");

    expect(Math.round(dock.top - control.bottom)).toBe(restStandoff);
    expect(control.bottom).toBeLessThanOrEqual(KEYBOARD_TOP);
  });

  test('the ticket sheet rides up onto the keyboard — it does not grow into it', async ({
    page,
  }) => {
    // The sheet is fixed to the bottom edge and portals to document.body, so it
    // gets none of the screen column's arrangement — and it is the one surface
    // with an editable field the dock's lift cannot reach (the body editor, the
    // transcript, the typed message).
    //
    // The regression this pins: the clearance used to be bottom PADDING, and a
    // bottom-anchored box that gains 300px of padding grows 300px UPWARD. The
    // footer did clear the keyboard, but a short ticket's sheet ballooned from a
    // third of the screen to nearly all of it — the ticket thrown to the top edge,
    // a keyboard's worth of blank paper under the controls — which is what "the
    // content flies to the top" was. Every claim below passed in that state except
    // the two about the sheet's own box.
    await mountShell(page, { band: 'none' });
    await page.click("[data-role='feed-card-open']");
    await page.waitForSelector("[data-role='ticket-detail']");
    const rest = await stableBox(page, "[data-role='ticket-detail']");
    const restTitle = await box(page, "[data-role='ticket-detail-title']");
    expect(Math.round(rest.bottom)).toBe(PHONE.height);

    await keyboard(page, KEYBOARD);
    const sheet = await box(page, "[data-role='ticket-detail']");
    const dock = await box(page, "[data-role='ticket-detail-dock']");
    const title = await box(page, "[data-role='ticket-detail-title']");

    // It STANDS ON the keyboard: its own bottom edge is the keyboard's top edge,
    // so there is no strip of sheet left behind the keys to read as dead space.
    expect(Math.round(sheet.bottom)).toBe(KEYBOARD_TOP);
    // ...at the size it already was. A short ticket is the case that exposes this:
    // uncapped, the old padding went straight into the sheet's height.
    expect(Math.round(sheet.height)).toBe(Math.round(rest.height));
    // ...so the ticket moved by the keyboard's height and not a pixel more — the
    // reading position is preserved, just lifted.
    expect(Math.round(restTitle.top - title.top)).toBe(KEYBOARD);

    // The footer is clear of the keys and still on screen at the top.
    expect(dock.bottom).toBeLessThanOrEqual(KEYBOARD_TOP);
    expect(dock.top).toBeGreaterThanOrEqual(0);
    // And the sheet's own foot is the footer's foot: nothing but its padding
    // between the controls and the keyboard.
    expect(sheet.bottom - dock.bottom).toBeLessThan(40);
  });

  test('a long ticket shrinks to fit above the keyboard rather than off the top', async ({
    page,
  }) => {
    // The other half of the lift: the cap comes off `--keyboard-inset` too, so a
    // sheet already at its 85dvh maximum yields the space instead of running out
    // of the top of the screen. Its body carries the outsized flex-shrink, so what
    // gives is the scrolling region — the dock, with the field in it, keeps its
    // intrinsic height.
    await mountShell(page, { band: 'none', longTicketBody: true });
    await page.click("[data-role='feed-card-open']");
    await page.waitForSelector("[data-role='ticket-detail']");
    await stableBox(page, "[data-role='ticket-detail']");

    await keyboard(page, KEYBOARD);
    const sheet = await box(page, "[data-role='ticket-detail']");
    expect(sheet.top).toBeGreaterThanOrEqual(0);
    expect(Math.round(sheet.bottom)).toBe(KEYBOARD_TOP);

    const dock = await page.evaluate(() => {
      const el = document.querySelector("[data-role='ticket-detail-dock']");
      if (el === null) {
        return null;
      }
      return { height: el.getBoundingClientRect().height, scrollHeight: el.scrollHeight };
    });
    expect(dock, 'no dock in the sheet').not.toBeNull();
    expect(dock?.height ?? 0).toBeGreaterThanOrEqual((dock?.scrollHeight ?? 0) - 1);
  });

  test('the typed field stays above the keyboard it opened', async ({ page }) => {
    // The whole point of the sheet's keyboard toggle: what you are typing has to
    // be on screen. The field lives in the sheet's dock panel, so it rides the
    // lift with everything else — this is the assertion that the two features hold
    // together, since either one alone leaves it behind the keys.
    await mountShell(page, { band: 'none', longTicketBody: true });
    await page.click("[data-role='feed-card-open']");
    await page.waitForSelector("[data-role='ticket-detail']");
    await stableBox(page, "[data-role='ticket-detail']");

    await page.click("[data-role='ticket-detail'] [data-role='dock-keyboard']");
    await page.waitForSelector("[data-role='ticket-detail-input']");
    await keyboard(page, KEYBOARD);

    const field = await box(page, "[data-role='ticket-detail-input']");
    const send = await box(page, "[data-role='ticket-detail'] [data-role='dock-send']");
    expect(field.height).toBeGreaterThan(0);
    expect(field.top).toBeGreaterThanOrEqual(0);
    expect(field.bottom).toBeLessThanOrEqual(KEYBOARD_TOP);
    // Send with it — a field you can see and no way to post it is the same bug.
    expect(send.bottom).toBeLessThanOrEqual(KEYBOARD_TOP);
  });
});

test.describe('desk shell on a tablet — the same model, under a finger', () => {
  test.use({ viewport: TABLET });

  test('the composer rides above the keyboard and the feed reserves the strip', async ({
    page,
  }) => {
    // The shell switch is width-only (13 §11), so this layout is what a landscape
    // tablet gets — with a soft keyboard, and with the composer pinned to the foot
    // of a 100dvh column. Without the lift it would be behind the keyboard the
    // moment anyone tapped the field.
    await mountShell(page, { band: 'none' });
    const restComposer = await box(page, "[data-role='desktop-composer-region']");
    const restRange = await scrollRange(page, "[data-role='desktop-feed']");
    expect(Math.round(restComposer.bottom)).toBe(TABLET.height);

    await keyboard(page, KEYBOARD);

    const composer = await box(page, "[data-role='desktop-composer-region']");
    expect(Math.round(composer.bottom)).toBe(TABLET.height - KEYBOARD);
    expect(Math.round(composer.height)).toBe(Math.round(restComposer.height));
    expect(Math.round((await scrollRange(page, "[data-role='desktop-feed']")) - restRange)).toBe(
      KEYBOARD,
    );

    // And the column itself is untouched: overlay, not resize, on this shell too.
    const screen = await box(page, "[data-role='desktop-screen']");
    expect(Math.round(screen.height)).toBe(TABLET.height);
  });
});
