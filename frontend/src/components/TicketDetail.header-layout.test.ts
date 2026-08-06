// TicketDetail header geometry, after the × came out and the gear moved to the
// status row's right end. jsdom does no layout, so the DOM tests next door can
// only see that the close button is gone and that the gear comes last in the
// status row — not that the header is actually a single full-width column, nor
// that the auto margin carries the gear to the right edge, nor that its dropdown
// opens from the correct corner. Those are CSS, and this is the only thing in
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

  it('pushes the gear to the status row’s end, on the sheet’s right edge', () => {
    // `margin-left: auto` is the whole mechanism: DOM order puts the gear after
    // the badge, and the auto margin eats the row's spare width so it lands in
    // the right corner however short the badge's word — or on a ticket that
    // wears no badge at all.
    expect(ruleBody("[data-role='detail-sandbox-menu'] {")).toMatch(/margin-left:\s*auto/);
  });

  it('cancels the trigger’s hit-area padding so the glyph lands on the sheet’s edge', () => {
    // The button keeps a comfortable tap target (padding) while the negative
    // margin pulls its box back out of flow, so what aligns with the sheet's
    // right edge is the gear itself and not the padding around it.
    const body = ruleBody("[data-role='detail-sandbox-trigger'] {");
    expect(body).toMatch(/padding:\s*5px/);
    expect(body).toMatch(/margin:\s*-5px/);
  });

  it('anchors the gear’s dropdown to open down-and-left from its corner', () => {
    // Anchored `left: 0` it would hang off the sheet's right edge now that the
    // trigger sits there — clipped by the panel's own overflow: hidden.
    const body = ruleBody("[data-role='detail-sandbox-panel'] {");
    expect(body).toMatch(/right:\s*0/);
    expect(body).not.toMatch(/left:\s*0/);
    expect(body).toMatch(/transform-origin:\s*top right/);
  });
});
