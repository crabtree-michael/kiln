// The ticket sheet's geometry, measured.
//
// The sheet is `position: fixed` and portals to document.body, so it escapes
// every clipping/transforming ancestor and lands outside the shell's subtree —
// which is exactly why its arrangement was only ever asserted as CSS text. These
// are the same claims, asked of the browser: what is clipped, what is on the
// edge, and what is on screen twice.
import { test, expect } from '@playwright/test';
import type { Box } from './harness';
import {
  mountShell,
  settle,
  box,
  maybeBox,
  paintedAt,
  intersects,
  stableBox,
  speak,
} from './harness';

const PHONE = { width: 390, height: 720 };

/** Open the sheet and wait for it to come to rest — vaul slides it in under an
 * inline transform, so a box read on the opening frame is off-screen by design. */
async function openSheet(page: import('@playwright/test').Page): Promise<void> {
  await page.click("[data-role='feed-card-open']");
  await page.waitForSelector("[data-role='ticket-detail']");
  await stableBox(page, "[data-role='ticket-detail']");
}

test.describe('ticket sheet — phone', () => {
  test.use({ viewport: PHONE });

  test('caps at the viewport and never hangs off the bottom edge', async ({ page }) => {
    await mountShell(page, { band: 'none', longTicketBody: true });
    await openSheet(page);

    const sheet = await box(page, "[data-role='ticket-detail']");
    expect(Math.round(sheet.bottom)).toBe(PHONE.height);
    expect(sheet.top).toBeGreaterThanOrEqual(0);
    expect(sheet.height).toBeLessThanOrEqual(PHONE.height);
    // A long ticket is what caps it — the state the shrink distribution below
    // only matters in.
    expect(sheet.height).toBeGreaterThan(PHONE.height * 0.5);
  });

  test('yields the capped sheet out of the body, never out of the dock', async ({ page }) => {
    // The bug this pins: the sheet is a clipped flex column at max height, and
    // flexbox removes the overflow in proportion to shrink × basis. With the body
    // and the dock both at the default factor, a long ticket took a slice out of
    // the DOCK too — and the live transcript inside it lost that slice, clipping
    // the user's own words behind an invisible scroll.
    await mountShell(page, { band: 'none', longTicketBody: true });
    await openSheet(page);

    const dock = await page.evaluate(() => {
      const el = document.querySelector("[data-role='ticket-detail-dock']");
      if (el === null) {
        return null;
      }
      return { height: el.getBoundingClientRect().height, scrollHeight: el.scrollHeight };
    });
    expect(dock, 'no dock in the sheet').not.toBeNull();
    // The dock renders at its intrinsic height: nothing inside it is clipped.
    expect(dock?.height ?? 0).toBeGreaterThanOrEqual((dock?.scrollHeight ?? 0) - 1);

    // ...and the body IS the part that yields — it is the sheet's scrolling
    // region, with more content than box.
    const body = await page.evaluate(() => {
      const el = document.querySelector("[data-role='ticket-detail-body']");
      if (el === null) {
        return null;
      }
      return { height: el.getBoundingClientRect().height, scrollHeight: el.scrollHeight };
    });
    expect(body?.scrollHeight ?? 0).toBeGreaterThan(body?.height ?? 0);

    // Neither region overlaps the other, and the actions stay on screen.
    const dockBox = await box(page, "[data-role='ticket-detail-dock']");
    const bodyBox = await box(page, "[data-role='ticket-detail-body']");
    expect(intersects(dockBox, bodyBox)).toBe(false);
    expect(dockBox.bottom).toBeLessThanOrEqual(PHONE.height + 1);
  });

  test('the gear lands on the sheet’s right edge, and opens back into the sheet', async ({
    page,
  }) => {
    await mountShell(page, { band: 'none' });
    await openSheet(page);

    const sheet = await box(page, "[data-role='ticket-detail']");
    const gear = await box(page, "[data-role='detail-sandbox-trigger']");
    const heading = await box(page, "[data-role='ticket-detail-heading']");
    // The glyph itself sits on the sheet's inner edge — its hit-area padding is
    // pulled back out of flow, so what aligns is the gear and not the box round
    // it. Within the sheet's own side padding, and hard right within the row.
    expect(sheet.right - gear.right).toBeLessThan(28);
    expect(gear.left).toBeGreaterThan(sheet.left + sheet.width / 2);
    // The heading takes the whole header width — the × that used to share the
    // row is gone, not merely unrendered.
    expect(heading.width).toBeGreaterThan(sheet.width * 0.7);

    await page.click("[data-role='detail-sandbox-trigger']");
    await settle(page);
    const panel = await maybeBox(page, "[data-role='detail-sandbox-panel']");
    expect(panel, 'the sandbox menu did not open').not.toBeNull();
    if (panel !== null) {
      // Down-and-left from its corner: anchored the other way it hangs off the
      // sheet's right edge and is clipped away by the panel's own overflow.
      expect(panel.right).toBeLessThanOrEqual(sheet.right + 1);
      expect(panel.left).toBeGreaterThanOrEqual(sheet.left - 1);
      expect(panel.top).toBeGreaterThanOrEqual(gear.top);
    }
  });

  test('the footer wears the dock mic’s disc, at the mic’s size', async ({ page }) => {
    // The footer's controls are dressed ONCE, in the mic's treatment, and the
    // mic itself is dressed by unscoped rules in a different file
    // (PrimaryScreen.css) so it looks the same wherever it is placed. Nothing in
    // the DOM couples the two sheets, and the skin that used to re-dress these
    // as an accent pill and a 40px danger circle is what this measures the
    // absence of — a drift no DOM test can see, since both sizes render.
    await mountShell(page, { band: 'none' });
    const dockMic = await box(page, "[data-role='dock-region'] [data-role='dock-mic']");
    await openSheet(page);

    // The sheet opens on the shaping proposal, so Accept and Delete are both up;
    // Poke rides a blocked/working ticket and is deliberately not in this row.
    const discs: [string, Box][] = [];
    for (const role of ['dock-mic', 'detail-delete', 'detail-accept']) {
      discs.push([role, await box(page, `[data-role='ticket-detail'] [data-role='${role}']`)]);
    }
    for (const [role, disc] of discs) {
      expect(disc.width, `${role} is not the mic's disc`).toBe(dockMic.width);
      expect(disc.height, `${role} is not the mic's disc`).toBe(dockMic.height);
    }
    // ...and they sit on one optical line, rather than merely sharing a size.
    const tops = discs.map(([, disc]) => disc.top);
    expect(Math.max(...tops) - Math.min(...tops)).toBeLessThan(2);
  });

  test('the keyboard toggle sits beside the mic, and the mic does not move for it', async ({
    page,
  }) => {
    // The footer's one rule is that the mic never travels, and the toggle is the
    // first thing added beside it since. Both halves are geometry: a loose toggle
    // would be flung to the row's far end by the cluster's `space-between` (next
    // to Accept, where Send is about to be), and opening typed input withdraws it
    // — which is exactly the kind of removal that shuffles a row sideways.
    await mountShell(page, { band: 'none' });
    await openSheet(page);

    const micAtRest = await box(page, "[data-role='ticket-detail'] [data-role='dock-talk']");
    const toggle = await box(page, "[data-role='ticket-detail'] [data-role='dock-keyboard']");
    const accept = await box(page, "[data-role='detail-accept']");

    // Beside the mic, on its right, on the same optical line — the mic's
    // neighbour and not the row's other end.
    expect(toggle.left).toBeGreaterThanOrEqual(micAtRest.right - 1);
    expect(toggle.left - micAtRest.right).toBeLessThan(24);
    expect(
      Math.abs(toggle.top + toggle.height / 2 - (micAtRest.top + micAtRest.height / 2)),
    ).toBeLessThan(2);
    expect(toggle.right).toBeLessThan(accept.left);

    await page.click("[data-role='ticket-detail'] [data-role='dock-keyboard']");
    await page.waitForSelector("[data-role='ticket-detail-input']");
    await settle(page);

    // The mic held its slot, to the pixel, through the toggle's withdrawal and
    // the panel opening above it.
    const micTyping = await box(page, "[data-role='ticket-detail'] [data-role='dock-talk']");
    expect(Math.abs(micTyping.left - micAtRest.left)).toBeLessThan(0.5);
    expect(Math.abs(micTyping.top - micAtRest.top)).toBeLessThan(0.5);

    // ...and the trailing slot changed hands, exactly as it does for a spoken
    // utterance: the state actions out, Send and × in, nothing sliding.
    expect(await maybeBox(page, "[data-role='detail-accept']")).toBeNull();
    const send = await box(page, "[data-role='ticket-detail'] [data-role='dock-send']");
    expect(Math.abs(send.right - accept.right)).toBeLessThan(1);

    // The field stands above the controls in the sheet's own dock, not over them.
    const field = await box(page, "[data-role='ticket-detail-input']");
    expect(field.bottom).toBeLessThanOrEqual(micTyping.top + 1);
  });

  test('speaking: Send and × are the mic’s disc, like the actions they replace', async ({
    page,
  }) => {
    // The footer is one row of peer discs — that is what the mic-disc test above
    // pins for Accept and Delete, and it has to hold for the pair that takes
    // their slot mid-utterance too. It did not: Send and × came over from the
    // dock at 40px, so starting to speak swapped two mic-sized buttons for two
    // small ones in the same place. Both sizes render, so only a measurement
    // sees it.
    await mountShell(page, { band: 'none' });
    await openSheet(page);
    await speak(
      page,
      "[data-role='ticket-detail'] [data-role='dock-talk']",
      'Back off on 5xx and cap it at five attempts',
    );

    const mic = await box(page, "[data-role='ticket-detail'] [data-role='dock-mic']");
    const send = await box(page, "[data-role='ticket-detail'] [data-role='dock-send']");
    const cancel = await box(page, "[data-role='ticket-detail'] [data-role='dock-cancel']");

    for (const [role, disc] of [
      ['dock-send', send],
      ['dock-cancel', cancel],
    ] as const) {
      expect(disc.width, `${role} is not the mic's disc`).toBe(mic.width);
      expect(disc.height, `${role} is not the mic's disc`).toBe(mic.height);
    }
    // On one optical line with the mic, as the state actions were.
    const tops = [mic, send, cancel].map((disc) => disc.top + disc.height / 2);
    expect(Math.max(...tops) - Math.min(...tops)).toBeLessThan(2);
  });

  test('editing shows one title, not the dialog’s name above the field', async ({ page }) => {
    // While editing, the sheet's <Drawer.Title> stays in the DOM — Radix names
    // the dialog by it — and the title FIELD stands in for it on screen. Without
    // the clip rule the two render one above the other, which every DOM test
    // passes straight through.
    await mountShell(page, { band: 'none' });
    await openSheet(page);
    await page.click("[data-role='detail-body-edit-target']");
    await page.waitForSelector("[data-role='detail-edit-title']");
    await settle(page);

    const field = await box(page, "[data-role='detail-edit-title']");
    const title = await maybeBox(page, "[data-role='ticket-detail-title']");
    expect(field.height).toBeGreaterThan(0);
    // The accessible name survives, but takes no room and paints nothing: a 1px
    // clipped box, out of flow, with the field — not the title — the thing the
    // browser finds at the title's own position.
    expect(title, 'the dialog lost its accessible name').not.toBeNull();
    if (title !== null) {
      expect(title.width * title.height).toBeLessThanOrEqual(1);
      expect(
        await paintedAt(page, field.left + field.width / 2, field.top + field.height / 2),
      ).not.toBe('ticket-detail-title');
    }
    // ...and the field stands where the title stood, rather than under it.
    const heading = await box(page, "[data-role='ticket-detail-heading']");
    expect(field.top - heading.top).toBeLessThan(field.height);
  });
});
