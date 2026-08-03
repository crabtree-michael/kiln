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

  it('lays a project panel out as one full-width row', () => {
    // The whole panel is the button, so it must override the compact
    // settings-button defaults (inline, left-aligned, single-line).
    const body = ruleBody("[data-role='settings'] [data-role='project-panel'] {");
    expect(body).toMatch(/display:\s*flex/);
    expect(body).toMatch(/width:\s*100%/);
    expect(body).toMatch(/align-self:\s*stretch/);
  });

  it('centers the project dialog over a dimmed page, capped to the viewport', () => {
    const scrim = ruleBody("[data-role='modal-scrim'] {");
    expect(scrim).toMatch(/position:\s*fixed/);
    expect(scrim).toMatch(/inset:\s*0/);
    expect(scrim).toMatch(/background:\s*var\(--scrim\)/);

    const panel = ruleBody("[data-role='project-modal'] {");
    expect(panel).toMatch(/max-width:\s*680px/);
    // Without a cap the dialog grows past the viewport and its own body — not
    // the page — is what has to scroll.
    expect(panel).toMatch(/max-height:\s*min\(88dvh, 880px\)/);
    expect(ruleBody("[data-role='project-modal-body'] {")).toMatch(/overflow-y:\s*auto/);
  });

  it('puts the project name and its repository side by side in the dialog header', () => {
    const body = ruleBody("[data-role='project-identity'] {");
    expect(body).toMatch(/display:\s*grid/);
    expect(body).toMatch(/grid-template-columns:\s*minmax\(0, 1fr\) minmax\(0, 1fr\)/);
    // They stack on a phone, where two ~160px columns would be unusable.
    expect(mediaRuleBody('max-width: 600px', "[data-role='project-identity'] {")).toMatch(
      /grid-template-columns:\s*minmax\(0, 1fr\)/,
    );
  });

  it('lays the short fields inside a dialog group out in columns', () => {
    const body = ruleBody("[data-role='project-group-fields'] {");
    expect(body).toMatch(/display:\s*grid/);
    expect(body).toMatch(/grid-template-columns:\s*repeat\(auto-fit, minmax\(180px, 1fr\)\)/);
  });

  // Credentials are connect cards now, not a grid of inputs. Each card is a
  // header row whose action is pinned to the right edge — that `margin-left:
  // auto` is what keeps "Switch account" off the identity line, so it is the
  // part worth asserting.
  it('pins each integration card’s action to the right edge', () => {
    const body = ruleBody("[data-role='integration-action'] {");
    expect(body).toMatch(/margin-left:\s*auto/);
    expect(ruleBody("[data-role='integration-header'] {")).toMatch(/display:\s*flex/);
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

  it('splits the project dialog’s action bar — delete leading, save trailing', () => {
    const bar = ruleBody("[data-role='project-form-actions'] {");
    expect(bar).toMatch(/display:\s*flex/);
    // flex-end, not space-between: create mode has no delete, and Save must not
    // slide to the left edge just because it is the only child.
    expect(bar).toMatch(/justify-content:\s*flex-end/);
    // …which makes the delete's auto margin the thing that pushes it left.
    expect(
      ruleBody(
        "[data-role='settings'] [data-role='project-form-actions'] [data-role='delete-project'] {",
      ),
    ).toMatch(/margin-right:\s*auto/);
  });

  it('draws the dropdown chevron itself instead of leaving platform chrome', () => {
    // Anchored on the declaration rather than the bare selector: the shared
    // `input, select` box rule above lists `select {` on its own line too, and
    // indexOf would stop there.
    const body = ruleBody("[data-role='dashboard'] select {\n  appearance: none;");
    expect(body).toMatch(/appearance:\s*none/);
    // The chevron is painted in currentColor so it follows the text in both
    // themes; an SVG data URI would have to hard-code a hex.
    expect(body).toMatch(/background-image:\s*[\s\S]*currentColor/);
    // Without the reserved room a long option label runs under the chevron.
    expect(body).toMatch(/padding-right:/);
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
