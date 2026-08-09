// The two surfaces that are neither the feed shell nor the ticket sheet: the
// `/kanban` board view (a third shell over the same board) and the `/dashboard`
// settings page.
//
// They are here because their layout had exactly the same problem as the feed
// shells' — a grid that can silently collapse to one stacked column, or a column
// that loses its own scroller, with every DOM test still green — and their
// string-matched CSS tests went out with the rest. These are the same claims,
// measured. A handful each: this is a floor, not an inventory.
import { test, expect } from '@playwright/test';
import { mountShell, box, intersects } from './harness';

const DESK = { width: 1280, height: 860 };

test.describe('kanban board', () => {
  test.use({ viewport: DESK });

  test('is a rail beside a board of side-by-side columns', async ({ page }) => {
    await mountShell(page, {
      route: '/kanban',
      hasEarlier: false,
      ready: "[data-role='kanban-board']",
    });

    const rail = await box(page, "[data-role='desktop-rail']");
    const board = await box(page, "[data-role='kanban-board']");
    expect(rail.right).toBeLessThanOrEqual(board.left + 1);
    // The rail keeps the feed shell's exact width — the brief is that the
    // sidebar is IDENTICAL, and a rail a few pixels narrower on one route is
    // precisely the drift that claim is about.
    await mountShell(page, { hasEarlier: false });
    const feedShellRail = await box(page, "[data-role='desktop-rail']");
    expect(rail.width).toBe(feedShellRail.width);
  });

  test('columns sit side by side and each scrolls on its own', async ({ page }) => {
    await mountShell(page, {
      route: '/kanban',
      hasEarlier: false,
      ready: "[data-role='kanban-column']",
    });

    const columns = await page.evaluate(() =>
      [...document.querySelectorAll("[data-role='kanban-column']")].map((el) => {
        const r = el.getBoundingClientRect();
        const head = el.querySelector("[data-role='kanban-column-head']");
        const list = el.querySelector("[data-role='kanban-column-list']");
        return {
          left: r.left,
          top: r.top,
          bottom: r.bottom,
          headTop: head === null ? null : head.getBoundingClientRect().top,
          listScrolls: list === null ? null : getComputedStyle(list).overflowY,
        };
      }),
    );
    expect(columns.length).toBeGreaterThan(1);

    // Side by side, not stacked: every column shares a top edge and each starts
    // to the right of the last.
    const first = columns[0];
    expect(first).toBeDefined();
    for (let i = 1; i < columns.length; i += 1) {
      const previous = columns[i - 1];
      const column = columns[i];
      if (previous === undefined || column === undefined || first === undefined) {
        continue;
      }
      expect(column.left).toBeGreaterThan(previous.left);
      expect(Math.abs(column.top - first.top)).toBeLessThan(2);
      // The heading is never pushed off by a long queue, and the column fits the
      // window rather than running off the bottom of it.
      expect(column.headTop).toBe(column.top);
      expect(column.bottom).toBeLessThanOrEqual(DESK.height + 1);
    }

    // The overflow belongs to the LIST, not the column — without it a long queue
    // pushes its own heading off the top, which is the classic silent failure of
    // a flex child that refuses to shrink below its content. An empty column
    // renders a placeholder and no list at all, so this is asked of the columns
    // that have one.
    const scrollers = columns.map((c) => c.listScrolls).filter((v) => v !== null);
    expect(scrollers.length).toBeGreaterThan(0);
    expect(scrollers.every((v) => v === 'auto')).toBe(true);
  });
});

test.describe('landing nav', () => {
  // The front door, at both ends of the range. `/landing` is the alias that is
  // pinned to the marketing page for everyone, so it does not depend on whether
  // this context looks like an installed web app.
  for (const viewport of [
    { width: 390, height: 720 },
    { width: 1440, height: 900 },
  ]) {
    test(`sign-up and log-in share a row at ${String(viewport.width)}px`, async ({ page }) => {
      await page.setViewportSize(viewport);
      await mountShell(page, {
        route: '/landing',
        hasEarlier: false,
        ready: '.kiln-nav__actions',
      });

      const signup = await box(page, '.kiln-nav__signup');
      const login = await box(page, '.kiln-nav__login');
      const brand = await box(page, '.kiln-nav__brand');

      // One row, both on screen, clear of the brand and right of centre.
      expect(Math.abs(signup.top - login.top)).toBeLessThan(2);
      for (const button of [signup, login]) {
        expect(button.left).toBeGreaterThanOrEqual(0);
        expect(button.right).toBeLessThanOrEqual(viewport.width + 1);
        expect(button.left).toBeGreaterThan(viewport.width / 2);
        expect(intersects(button, brand)).toBe(false);
        // `.kiln-btn` states no height at all and has `line-height: 1`, so its
        // whole height comes from its padding — writing the mobile padding down
        // to nothing is what collapsed these to an unreadable sliver.
        expect(button.height).toBeGreaterThanOrEqual(32);
      }

      // The pair reads Log in, then Sign up on a phone — the primary CTA
      // outermost, under the thumb that reaches the top-right corner — and falls
      // back to source order on the wide bar. It is done with `order:` rather
      // than by swapping the markup, so the DOM says exactly the same thing at
      // both sizes and only the boxes can tell which arrangement shipped.
      if (viewport.width <= 720) {
        expect(login.left).toBeLessThan(signup.left);
      } else {
        expect(signup.left).toBeLessThan(login.left);
      }
    });
  }
});

test.describe('onboarding', () => {
  test.use({ viewport: DESK });

  test('sizes its glyphs off their own text, not at the SVG default', async ({ page }) => {
    // The icons carry no width/height attributes by design (icons.tsx), so they
    // are sized entirely by CSS — and onboarding needs its OWN rule, because the
    // settings one is scoped under [data-role='settings']. Without it every
    // glyph renders at the SVG default 300×150 and the flow is unusable, with
    // every DOM test still green: the elements are all there, at the wrong size.
    await mountShell(page, {
      route: '/dashboard',
      hasEarlier: false,
      noProject: true,
      ready: "[data-role='onboarding']",
    });

    const glyphs = await page.evaluate(() =>
      [...document.querySelectorAll("[data-role='onboarding'] svg[data-icon]")].map((el) => {
        const r = el.getBoundingClientRect();
        return { icon: el.getAttribute('data-icon'), width: r.width, height: r.height };
      }),
    );
    expect(glyphs.length).toBeGreaterThan(0);
    for (const glyph of glyphs) {
      expect(glyph.width, `${String(glyph.icon)} is at the SVG default width`).toBeLessThan(40);
      expect(glyph.height, `${String(glyph.icon)} is at the SVG default height`).toBeLessThan(40);
    }
  });
});

// Starting a project (full-screen repo picker): the claim is geometric — the
// picker IS the step, on a phone, without scrolling — and every part of it
// passes as a DOM assertion while looking wrong. The predecessor was a card at
// the foot of the project list, under a name field, which is exactly the shape
// jsdom cannot tell this apart from.
test.describe('new project', () => {
  const PHONE = { width: 390, height: 720 };
  test.use({ viewport: PHONE });

  test('the repo picker is the step: one control, on screen, spanning the column', async ({
    page,
  }) => {
    await mountShell(page, {
      route: '/projects?new=1',
      hasEarlier: false,
      ready: "[data-role='new-project-step']",
    });

    const step = await box(page, "[data-role='new-project-step']");
    const picker = await box(page, "[data-role='repo-select']");
    const options = await box(page, "[data-role='new-project-options']");

    // Above the fold, at the column's full width, and a real touch target —
    // not one field among six in a scrolled form.
    expect(picker.bottom).toBeLessThanOrEqual(PHONE.height);
    expect(picker.width).toBeGreaterThan(step.width - 4);
    expect(picker.height).toBeGreaterThanOrEqual(44);
    // Everything with a working default is BELOW it, and below the derived name
    // — the picker leads, the rest is subordinate.
    const note = await box(page, "[data-role='project-name-note']");
    expect(note.top).toBeGreaterThanOrEqual(picker.bottom);
    expect(options.top).toBeGreaterThan(note.top);
    // The step takes the screen: the list it was reached from is not under it.
    expect(await page.locator("[data-role='project-row']").count()).toBe(0);
  });

  test('the create action ends the step, spanning the column at its foot', async ({ page }) => {
    await mountShell(page, {
      route: '/projects?new=1',
      hasEarlier: false,
      ready: "[data-role='new-project-step']",
    });

    const step = await box(page, "[data-role='new-project-step']");
    const submit = await box(page, "[data-role='new-project-step'] button[type='submit']");
    const options = await box(page, "[data-role='new-project-options']");

    expect(submit.top).toBeGreaterThanOrEqual(options.bottom);
    expect(submit.width).toBeGreaterThan(step.width - 4);
    expect(submit.height).toBeGreaterThanOrEqual(44);
    // The header's cancel replaces the back link while the step is up, and stays
    // a header control rather than a pill somewhere in the form.
    const cancel = await box(page, "[data-role='cancel-new-project']");
    const heading = await box(page, "[data-role='projects-header'] h1");
    expect(cancel.right).toBeLessThanOrEqual(heading.left + 1);
    expect(cancel.top).toBeLessThan(step.top);
  });
});

test.describe('settings', () => {
  test.use({ viewport: DESK });

  test('lays out as a pinned nav column beside the content column', async ({ page }) => {
    await mountShell(page, {
      route: '/dashboard',
      hasEarlier: false,
      ready: "[data-role='settings-layout']",
    });

    const nav = await box(page, "[data-role='settings-nav']");
    const layout = await box(page, "[data-role='settings-layout']");
    // Two columns, not one stacked mobile column — the failure the string test
    // existed for is a page that still renders, still passes every DOM test, and
    // is quietly a phone layout on a 1280px window.
    expect(nav.right).toBeLessThanOrEqual(layout.left + layout.width / 2);
    expect(nav.height).toBeLessThan(layout.height);
    expect(layout.right).toBeLessThanOrEqual(DESK.width + 1);
    // Nothing runs off the page sideways: the content track is `minmax(0, 1fr)`
    // so a long repo URL cannot push the column wider than the window.
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      DESK.width,
    );
  });

  test('the nav stays put while the content scrolls', async ({ page }) => {
    await mountShell(page, {
      route: '/dashboard',
      hasEarlier: false,
      ready: "[data-role='settings-nav']",
    });

    const before = await box(page, "[data-role='settings-nav']");
    await page.evaluate(() => {
      window.scrollTo(0, 400);
    });
    const after = await box(page, "[data-role='settings-nav']");
    const scrolled = await page.evaluate(() => window.scrollY);
    if (scrolled > 0) {
      // Sticky: the nav gives up less than it would if it simply scrolled away.
      expect(before.top - after.top).toBeLessThan(scrolled);
    }
    // ...and it never lands on the panes it indexes: sticky moves the nav within
    // its own track, it does not float it over the content.
    const panes = await box(page, "[data-role='settings-panes']");
    expect(intersects(after, panes)).toBe(false);
  });
});
