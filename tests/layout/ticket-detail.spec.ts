// The ticket sheet's geometry, measured.
//
// The sheet is `position: fixed` and portals to document.body, so it escapes
// every clipping/transforming ancestor and lands outside the shell's subtree —
// which is exactly why its arrangement was only ever asserted as CSS text. These
// are the same claims, asked of the browser: what is clipped, what is on the
// edge, and what is on screen twice.
import { test, expect } from '@playwright/test';
import type { Box } from './harness';
import { mountShell, settle, box, maybeBox, paintedAt, intersects, stableBox } from './harness';

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
