// The dock's voice controls, measured.
//
// Two claims, and both are pure geometry — which is to say neither is visible
// from jsdom, and the DOM they render is identical whether they hold or not:
//
//   * Send, the discard × and the mic are ONE size — the mic's 54px disc. The
//     two flanking controls used to be 40px satellites around it, which made the
//     smaller thumb targets the ones carrying the irreversible actions.
//   * In the speaking row they sit on the FLANKS — send hard left, × hard right,
//     the mic centred between them — rather than clustered against the mic. At
//     54px apiece, clustered means "send" and "throw away" a few pixels apart.
//
// Reaching the speaking row at all is what `speak` in the harness is for: a fake
// capture device, a stubbed token and a mocked provider socket, so the real store
// runs its real state machine with no key and no network.
import { test, expect } from '@playwright/test';
import type { Box } from './harness';
import { mountShell, settle, box, speak } from './harness';

const PHONE = { width: 390, height: 720 };

const DOCK_MIC = "[data-role='dock-region'] [data-role='dock-talk']";

/** Two boxes' centres on the horizontal axis, for "is this on the same line". */
function midY(b: Box): number {
  return b.top + b.height / 2;
}

test.describe('phone dock — the voice controls', () => {
  test.use({ viewport: PHONE });

  test('speaking: send left, × right, mic centred, all three the mic’s size', async ({ page }) => {
    await mountShell(page, { band: 'none' });
    await speak(page, DOCK_MIC, 'Rate-limit the webhook retry before the release goes out');

    const controls = await box(page, "[data-role='dock-controls']");
    const mic = await box(page, "[data-role='dock-region'] [data-role='dock-mic']");
    const send = await box(page, "[data-role='dock-send']");
    const cancel = await box(page, "[data-role='dock-cancel']");

    // One size, and it is the mic's. Not "roughly": these are three circles on
    // one row and a 14px difference between them is exactly the thing that read
    // as two lesser controls beside a real one.
    for (const [role, disc] of [
      ['dock-send', send],
      ['dock-cancel', cancel],
    ] as const) {
      expect(disc.width, `${role} is not the mic's disc`).toBe(mic.width);
      expect(disc.height, `${role} is not the mic's disc`).toBe(mic.height);
    }

    // The mic holds the centre of the row — the 1fr/auto/1fr grid's whole job, and
    // the reason showing or hiding either flank never shifts it.
    const rowCentre = controls.left + controls.width / 2;
    expect(Math.abs(mic.left + mic.width / 2 - rowCentre)).toBeLessThan(1);

    // Send at the row's left edge, × at its right edge.
    expect(Math.abs(send.left - controls.left)).toBeLessThan(1);
    expect(Math.abs(cancel.right - controls.right)).toBeLessThan(1);

    // ...which is a different claim from "in the right order": clustered against
    // the mic they would still read left-to-right as send, mic, ×. What this is
    // about is the space between them, so measure the space.
    expect(mic.left - send.right).toBeGreaterThan(40);
    expect(cancel.left - mic.right).toBeGreaterThan(40);

    // All on one optical line, so the row reads as three peers rather than a
    // centrepiece with two things tucked under it.
    expect(Math.abs(midY(send) - midY(mic))).toBeLessThan(2);
    expect(Math.abs(midY(cancel) - midY(mic))).toBeLessThan(2);
  });

  test('typing: send stays a 40px peer of the row it is actually in', async ({ page }) => {
    // The deliberate exception. Keyboard mode has no mic orb to be sized against
    // — send's peers are the voice toggle and the dismiss button — so it keeps
    // the 40px circle the three of them share. Measured because it is an override
    // of the rule above, and an override is precisely what quietly stops applying.
    await mountShell(page, { band: 'none' });
    await page.click("[data-role='dock-keyboard']");
    await page.waitForSelector("[data-role='dock-input']");
    await settle(page);

    const controls = await box(page, "[data-role='dock-controls']");
    const voice = await box(page, "[data-role='dock-voice']");
    const send = await box(page, "[data-role='dock-send']");

    expect(send.width).toBe(voice.width);
    expect(send.height).toBe(voice.height);
    // The voice toggle takes the left edge and send the right — the balanced
    // two-button bar, with the dismiss control between them.
    expect(Math.abs(voice.left - controls.left)).toBeLessThan(1);
    expect(Math.abs(send.right - controls.right)).toBeLessThan(1);
  });
});
