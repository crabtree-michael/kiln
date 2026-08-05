// TicketDetail edit-mode visibility regression. While editing, the sheet's
// <Drawer.Title> stays in the DOM — Radix names the dialog by it
// (aria-labelledby), so removing it would leave the sheet nameless — and the
// title *field* stands in for it on screen. The only thing keeping the two from
// rendering one above the other is the clip rule asserted here.
//
// jsdom does no layout, so nothing else in the gate can catch this: every DOM
// test would still pass with a duplicated title on screen. Same `?raw`
// technique (and same reason) as TicketDetail.safe-area.test.ts.
import cssRaw from './TicketDetail.css?raw';

const css: string = cssRaw;

function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

/** Everything inside an `@media (...)` block, found by its condition and read to
 * its matching brace — so a test can assert not just that a rule exists but that
 * it is *gated*, which is the whole point of the hover rule below. */
function mediaBlock(condition: string): string {
  const start = css.indexOf(`@media ${condition}`);
  expect(start, `media query not found: ${condition}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  let depth = 0;
  for (let i = open; i < css.length; i += 1) {
    if (css[i] === '{') {
      depth += 1;
    } else if (css[i] === '}') {
      depth -= 1;
      if (depth === 0) {
        return css.slice(open + 1, i);
      }
    }
  }
  throw new Error(`unterminated media query: ${condition}`);
}

describe('TicketDetail edit mode', () => {
  it('clips the title out of view while editing, rather than removing it', () => {
    const body = ruleBody("[data-role='ticket-detail-title'][data-editing='true'] {");
    // Clipped, not `display: none` / `visibility: hidden` — an element hidden
    // those ways is the one case an accessible-name computation may skip.
    expect(body).toMatch(/clip-path:\s*inset\(50%\)/);
    expect(body).not.toMatch(/display:\s*none/);
    expect(body).not.toMatch(/visibility:\s*hidden/);
    // Out of flow, so the field below takes its place instead of sitting under
    // a one-pixel ghost that still reserves a line.
    expect(body).toMatch(/position:\s*absolute/);
  });

  it('gives the body field room to type in', () => {
    const body = ruleBody("[data-role='detail-edit-body'] {");
    expect(body).toMatch(/width:\s*100%/);
    expect(body).toMatch(/min-height:/);
  });

  // The body is the only pointer route into edit mode now, so it has to *read*
  // as pressable — a caret over the words, and a wash under them on hover.
  it('makes the pressable body read as a field-in-waiting', () => {
    expect(ruleBody("[data-role='detail-body-edit-target'] {")).toMatch(/cursor:\s*text/);
    expect(ruleBody("[data-role='detail-body-edit-target']:hover {")).toMatch(/background:/);
  });

  // A phone has no hover, so it emulates one on touch: an ungated :hover wash is
  // painted by the finger that merely starts a *scroll* on the body, darkening
  // the ticket as though it had been pressed. The gate is what keeps the rule
  // off touch entirely — and jsdom does no layout and no media matching, so
  // nothing else in the gate can catch its removal.
  it('keeps the hover wash off devices that only emulate hover', () => {
    expect(mediaBlock('(hover: hover)')).toContain("[data-role='detail-body-edit-target']:hover");
  });

  // Touch's replacement for that hover: the component decides when a finger is
  // pressing rather than scrolling and says so here, so a real tap still lights
  // the words up.
  it('washes the body while it is genuinely pressed', () => {
    expect(ruleBody("[data-role='detail-body-edit-target'][data-pressed='true'] {")).toMatch(
      /background:/,
    );
  });

  // The keyboard stand-in for that press is off-screen until focused. Clipped,
  // not `display: none` — the latter would take it out of the tab order, which
  // is the one thing it exists for, and jsdom does no layout so only this can
  // catch it.
  it('keeps the keyboard route tabbable while it is off screen', () => {
    const body = ruleBody("[data-role='detail-body-edit-key'] {");
    expect(body).toMatch(/clip-path:\s*inset\(50%\)/);
    expect(body).not.toMatch(/display:\s*none/);
    expect(body).not.toMatch(/visibility:\s*hidden/);
    expect(body).toMatch(/position:\s*absolute/);
    // And it comes back into view — as a real button — once focused.
    const focused = ruleBody("[data-role='detail-body-edit-key']:focus-visible {");
    expect(focused).toMatch(/clip-path:\s*none/);
    expect(focused).toMatch(/position:\s*static/);
  });
});
