// The shared status mark, held to the sheet it is supposed to agree with.
//
// The bug this file exists to prevent: a ticket wearing one colour in a list and
// a different one in its own detail sheet. `building` used to paint the ACCENT
// in every list — the phone's ticket dropdown, the desk's in-progress panel, the
// kanban card — while the sheet called the same ticket ember, so an ordinary
// working ticket read as the one state fire is reserved for (13 §4).
//
// It was pinned by matching the TEXT of two stylesheets: pulling `--warn` out of
// PrimaryScreen.css's rule and `--warn` out of TicketDetail.css's and comparing
// the two names. That proves the sheets quote the same variable and nothing
// more — not that the cascade lands there, not that a later rule doesn't win,
// and not that the two marks end up the same colour on screen. Here the question
// is put to the browser: what did it actually paint, on both surfaces, for one
// ticket. The comparison is between two RESOLVED colours, which is the claim the
// bug was about.
//
// The second half is phase. The head and the rows are one element painted by one
// rule, and they still drifted, because sharing a rule is not sharing a clock (an
// animation's timeline starts when its own element starts animating). jsdom has
// no `getAnimations`, so pulse-phase.ts's unit tests can only check the pinning
// logic against a fake; whether the marks on screen actually breathe together is
// only answerable here.
import { test, expect } from '@playwright/test';
import { mountShell, settle, computed, stableBox } from './harness';

const PHONE = { width: 390, height: 720 };
const DESK = { width: 1280, height: 860 };

/** Every mark on screen that opted into the shared clock, with the state of the
 * animation the browser is actually running for it. */
async function breathingMarks(page: import('@playwright/test').Page): Promise<
  { phase: string; start: number | null; progress: number | null }[]
> {
  return page.evaluate(() =>
    [...document.querySelectorAll("[data-role='status-dot'][data-status='building']")].map((el) => {
      const animation = el.getAnimations({ subtree: false })[0];
      const time = animation?.currentTime ?? null;
      return {
        phase: getComputedStyle(el).getPropertyValue('--pulse-phase').trim(),
        start: typeof animation?.startTime === 'number' ? animation.startTime : null,
        progress: typeof time === 'number' ? time : null,
      };
    }),
  );
}

test.describe('the shared status mark — desk', () => {
  test.use({ viewport: DESK });

  test('a working ticket wears one ink in the head, the row, and its own sheet', async ({
    page,
  }) => {
    await mountShell(page, { band: 'none' });

    // The head is literally one of the rows' marks — same element, same rule —
    // so a difference here is the panel having grown a second dot of its own.
    const head = await computed(
      page,
      "[data-role='desktop-working-head'] [data-role='status-dot']",
      'background-color',
    );
    const row = await computed(
      page,
      "[data-role='desktop-working-ticket'] [data-role='status-dot']",
      'background-color',
    );
    expect(head).toBe(row);
    // A real colour, not a transparent default — the comparison above passes
    // trivially if the cascade landed nowhere at all.
    expect(row).toMatch(/^rgba?\(/);
    expect(row).not.toBe('rgba(0, 0, 0, 0)');

    // ...and the sheet the row opens agrees with the row. Two rules, two
    // stylesheets, one ticket: this is the assertion the reported bug failed.
    await page.click("[data-role='desktop-working-ticket']");
    await page.waitForSelector("[data-role='ticket-detail']");
    await stableBox(page, "[data-role='ticket-detail']");
    expect(await page.getAttribute("[data-role='ticket-detail-status']", 'data-state')).toBe(
      'working',
    );
    const badge = await computed(
      page,
      "[data-role='ticket-detail-status-dot']",
      'background-color',
    );
    expect(badge, 'the list mark and the sheet badge disagree about this ticket').toBe(row);
  });

  test('every breathing mark keeps one clock, not merely one tempo', async ({ page }) => {
    await mountShell(page, { band: 'none' });
    const marks = await breathingMarks(page);

    // The head and the row it summarises — the pair that visibly shimmered
    // against each other.
    expect(marks.length, 'no breathing marks to compare').toBeGreaterThan(1);
    for (const mark of marks) {
      // The opt-in is declared by the rule that declares the animation, so a
      // mark that stops sharing the clock does it by losing this.
      expect(mark.phase).toBe('shared');
      // Pinned to the document timeline's origin: progress becomes a function of
      // the clock alone rather than of when the element happened to mount.
      expect(mark.start).toBe(0);
    }
    // ...and therefore they are at the same point of the same breath. Read in
    // one evaluate, so this is one instant and not two.
    const progress = marks.map((m) => m.progress);
    expect(new Set(progress).size, `marks peak apart: ${JSON.stringify(progress)}`).toBe(1);
  });
});

test.describe('the shared status mark — phone', () => {
  test.use({ viewport: PHONE });

  test('the list spends fire on blocked alone, and the sheet spends it there too', async ({
    page,
  }) => {
    await mountShell(page, { band: 'none' });
    await page.click("[data-role='feed-status']");
    await page.waitForSelector("[data-role='header-status-panel']");
    await settle(page);

    const panel = "[data-role='header-status-panel'] [data-role='status-dot']";
    const working = await computed(page, `${panel}[data-state='working']`, 'background-color');
    const blocked = await computed(page, `${panel}[data-state='blocked']`, 'background-color');
    const resting = await computed(page, `${panel}[data-state='ready']`, 'background-color');

    // The reported bug in its visible form: an ordinary working ticket reading
    // as the state that needs a person.
    expect(working, 'a working ticket is wearing the blocked ink').not.toBe(blocked);
    expect(resting).not.toBe(blocked);
    expect(blocked).not.toBe('rgba(0, 0, 0, 0)');

    // And the blocked ticket's own sheet lands on the same fire. The deep link is
    // how a tapped notification opens a ticket (02 §10), which makes it the
    // cheapest way in to a sheet the feed does not offer a card for.
    await mountShell(page, { route: '/app?ticket=t1', band: 'none' });
    await page.waitForSelector("[data-role='ticket-detail']");
    await stableBox(page, "[data-role='ticket-detail']");
    expect(await page.getAttribute("[data-role='ticket-detail-status']", 'data-state')).toBe(
      'blocked',
    );
    expect(
      await computed(page, "[data-role='ticket-detail-status-dot']", 'background-color'),
      'the blocked mark and the blocked badge disagree',
    ).toBe(blocked);
  });

  // The case that stood here measured the dropdown's HEAD — its mark against the
  // top row's, and the two breathing in phase. The head is gone (the panel opens
  // from a button that already reports what it holds, and a lone mark with no
  // word is decoration), so there is nothing left for it to compare. The claim it
  // was really protecting — every breathing mark on one clock, not merely one
  // tempo — is asserted over the desk's panel above, and that is the surface that
  // still has a head to keep in step with its rows.
});
