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
});
