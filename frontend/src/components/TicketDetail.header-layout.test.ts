// TicketDetail header geometry, after the × came out and the gear moved left.
// jsdom does no layout, so the DOM tests next door can only see that the close
// button is gone and that the gear leads the status row — not that the header is
// actually a single full-width column, nor that the gear's dropdown now opens
// from the correct corner. Those three are CSS, and this is the only thing in
// the gate that can catch them regressing.
//
// The stylesheet is pulled in as a raw string (Vite `?raw`, typed via
// vite/client) rather than read off disk, so no untyped node built-ins are
// needed and the test asserts the exact CSS the app ships — the same technique
// as TicketDetail.safe-area.test.ts.
import cssRaw from './TicketDetail.css?raw';

const css: string = cssRaw;

function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('TicketDetail header layout', () => {
  it('styles no close button at all — the × is gone from both skins', () => {
    // Not merely unrendered: the rules that sized and skinned it are gone too,
    // so nothing invites it back in.
    expect(css).not.toContain('ticket-detail-close');
  });

  it('lets the heading take the whole header width', () => {
    // It is the header's only child now, so it grows into the space the button
    // used to hold rather than sharing the row with it.
    expect(ruleBody("[data-role='ticket-detail-heading'] {")).toMatch(/flex:\s*1 1 auto/);
  });

  it('leaves the gear at the status row’s start rather than pushing it to the end', () => {
    // `margin-left: auto` is what parked it in the sheet's right corner, under
    // the ×. Without it the gear sits where the row starts — the title's own
    // left edge — which is the whole point of the move.
    expect(ruleBody("[data-role='detail-sandbox-menu'] {")).not.toMatch(/margin-left:\s*auto/);
  });

  it('cancels the trigger’s hit-area padding so the glyph lands on the title’s edge', () => {
    // The button keeps a comfortable tap target (padding) while the negative
    // margin pulls its box back out of flow, so what aligns with the title is
    // the gear itself and not the padding around it.
    const body = ruleBody("[data-role='detail-sandbox-trigger'] {");
    expect(body).toMatch(/padding:\s*5px/);
    expect(body).toMatch(/margin:\s*-5px/);
  });

  it('re-anchors the gear’s dropdown to open down-and-right from its new corner', () => {
    // Anchored `right: 0` it would hang off the sheet's left edge now that the
    // trigger sits there — clipped by the panel's own overflow: hidden.
    const body = ruleBody("[data-role='detail-sandbox-panel'] {");
    expect(body).toMatch(/left:\s*0/);
    expect(body).not.toMatch(/right:\s*0/);
    expect(body).toMatch(/transform-origin:\s*top left/);
  });
});
