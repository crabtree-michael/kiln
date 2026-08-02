// The settings redesign's geometry, asserted against the shipped CSS. jsdom does
// no layout, so nothing else in the gate can catch a regression here: the page
// would still render, still pass every DOM test, and quietly be a single
// mobile-style column again.
//
// Same technique as TicketDetail.safe-area.test.ts — the stylesheet is pulled in
// as a raw string (Vite `?raw`, typed via vite/client) rather than read off disk,
// so no untyped node built-ins are needed and the test asserts the exact CSS the
// app ships.
import cssRaw from './Dashboard.css?raw';

const css: string = cssRaw;

/** Isolate a rule's declaration block by its selector, so an assertion is about
 * that rule rather than about the file containing the string somewhere. */
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

/** The declaration block of `selector` as written inside `@media (<query>)`. */
function mediaRuleBody(query: string, selector: string): string {
  const mediaStart = css.indexOf(`@media (${query})`);
  expect(mediaStart, `media query not found: ${query}`).toBeGreaterThanOrEqual(0);
  const scoped = css.slice(mediaStart);
  const start = scoped.indexOf(selector);
  expect(start, `selector not found in @media (${query}): ${selector}`).toBeGreaterThanOrEqual(0);
  const open = scoped.indexOf('{', start);
  const close = scoped.indexOf('}', open);
  return scoped.slice(open + 1, close);
}

describe('settings desktop layout', () => {
  it('lays the page out as a nav column beside a content column', () => {
    const body = ruleBody("[data-role='settings-layout'] {");
    expect(body).toMatch(/display:\s*grid/);
    // A fixed nav track plus a content track that may shrink (minmax(0, 1fr) —
    // a bare 1fr would let a long repo URL push the column wider than the page).
    expect(body).toMatch(/grid-template-columns:\s*200px minmax\(0, 1fr\)/);
  });

  it('gives the settings column enough width for a multi-column form', () => {
    // 720px (the pre-redesign width, still used by the single-purpose phases)
    // cannot hold a nav column plus a 2–3-up form grid.
    expect(ruleBody("[data-role='settings'] {")).toMatch(/max-width:\s*1120px/);
    expect(ruleBody("[data-role='onboarding'] {")).toMatch(/max-width:\s*720px/);
  });

  it('keeps the section nav pinned while the content scrolls', () => {
    expect(ruleBody("[data-role='settings-nav'] {")).toMatch(/position:\s*sticky/);
  });

  it('lays the project form out in columns, widening to three on a large screen', () => {
    const base = ruleBody("[data-role='settings'] [data-role='project-form'] {");
    expect(base).toMatch(/display:\s*grid/);
    expect(base).toMatch(/grid-template-columns:\s*repeat\(2, minmax\(0, 1fr\)\)/);

    const wide = mediaRuleBody(
      'min-width: 1100px',
      "[data-role='settings'] [data-role='project-form'] {",
    );
    expect(wide).toMatch(/grid-template-columns:\s*repeat\(3, minmax\(0, 1fr\)\)/);
  });

  it('spans the full grid width for the form blocks that need it', () => {
    const spanning = ruleBody(
      "[data-role='settings'] [data-role='project-form'] > fieldset,\n[data-role='settings'] [data-role='project-form'] > button {",
    );
    expect(spanning).toMatch(/grid-column:\s*1 \/ -1/);
  });

  it('lists the credentials in columns rather than one per line', () => {
    const body = ruleBody("[data-role='settings-form'] {");
    expect(body).toMatch(/display:\s*grid/);
    expect(body).toMatch(/grid-template-columns:\s*repeat\(auto-fit, minmax\(250px, 1fr\)\)/);
  });

  it('collapses to a single column below the two-column threshold', () => {
    expect(mediaRuleBody('max-width: 900px', "[data-role='settings-layout'] {")).toMatch(
      /grid-template-columns:\s*minmax\(0, 1fr\)/,
    );
    // The nav survives the collapse as a horizontal strip — it is the page's
    // index, not decoration, so it must not simply be display:none'd.
    const nav = mediaRuleBody('max-width: 900px', "[data-role='settings-nav'] {");
    expect(nav).toMatch(/flex-direction:\s*row/);
    expect(nav).not.toMatch(/display:\s*none/);
  });

  it('keeps anchor jumps clear of the pinned mobile nav strip', () => {
    // Without a scroll-margin the sticky strip covers the heading of whichever
    // section the nav just jumped to.
    expect(ruleBody("[data-role='settings-section'] {")).toMatch(/scroll-margin-top:/);
    expect(mediaRuleBody('max-width: 900px', "[data-role='settings-section'] {")).toMatch(
      /scroll-margin-top:\s*56px/,
    );
  });

  it('keeps settings controls compact rather than full-size pill buttons', () => {
    const button = ruleBody("[data-role='settings'] button {");
    // Caption type (12.5px) and a small radius — not the --type-body-strong
    // pill the single-purpose phases still use.
    expect(button).toMatch(/font:\s*var\(--type-caption\)/);
    expect(button).toMatch(/border-radius:\s*var\(--radius-sm\)/);
    expect(button).toMatch(/min-height:\s*32px/);
  });

  it('sizes every settings icon off its own text', () => {
    // One rule for the whole tree — icons carry no width/height attributes, so
    // dropping this would render them at the SVG default 300×150.
    const body = ruleBody("[data-role='settings'] svg[data-icon] {");
    expect(body).toMatch(/width:\s*1\.15em/);
    expect(body).toMatch(/height:\s*1\.15em/);
  });
});
