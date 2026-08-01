// Ticket-detail transcript space regression (08 §5, 09 §4): the live transcript in
// the sheet's dock was cut off mid-utterance — the text clipped behind an invisible
// internal scroll (touch draws no scrollbar), so only the tail of what the user said
// could be read.
//
// The cause was flex shrink priority, not the transcript's own cap. The sheet is a
// clipped flex column pinned at max-height, and flexbox removes the overflow in
// proportion to (shrink factor × flex base size): with the body and the dock both at
// the default shrink factor of 1, any ticket long enough to cap the sheet took a slice
// out of the DOCK too, and the transcript inside it lost that slice. The body — the
// sheet's designated scrolling region — must be the part that yields instead.
//
// These assert the layout contract that keeps the transcript whole:
//   1. the body outweighs the dock in the shrink distribution;
//   2. the dock can still shrink as a last resort (min-height: 0), so a viewport too
//      short to seat header + dock never clips the action buttons off the bottom;
//   3. the transcript's cap is measured in the same viewport unit as the sheet it
//      spends space from, and it scrolls inside that cap.
// Reverting the body to a plain `flex: 1 1 auto` (the pre-fix state) trips (1).
//
// Same idiom as TicketDetail.safe-area.test.ts: the stylesheet is pulled in as a raw
// string (Vite `?raw`) so the test asserts the exact CSS the app ships.
import cssRaw from './TicketDetail.css?raw';

const css: string = cssRaw;

// Isolate a rule's declaration block by its selector so each assertion lands on the
// intended rule, not merely somewhere in the file.
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

// `flex: <grow> <shrink> <basis>` — the shrink factor is the middle number, and the
// property defaults it to 1 when the shorthand omits it.
function shrinkFactor(selector: string): number {
  const declaration = /(?:^|[;\s])flex:\s*([^;]+);/.exec(ruleBody(selector));
  expect(declaration, `no flex shorthand on ${selector}`).not.toBeNull();
  const parts = (declaration?.[1] ?? '').trim().split(/\s+/);
  return parts.length > 1 ? Number(parts[1]) : 1;
}

describe('TicketDetail transcript space', () => {
  it('yields the capped sheet out of the scrolling body, not the transcript dock', () => {
    const body = shrinkFactor("[data-role='ticket-detail-body'] {");
    // The dock leaves the shorthand off entirely (it only sets min-height), so its
    // shrink factor is the initial 1 — the body must outweigh that by a wide margin
    // for the dock to keep its intrinsic height while the sheet is capped.
    expect(body).toBeGreaterThan(1);
  });

  it('keeps the dock shrinkable as a last resort so the controls are never clipped', () => {
    expect(ruleBody("[data-role='ticket-detail-dock'] {")).toMatch(/min-height:\s*0/);
  });

  it('bounds the transcript in the sheet\u2019s own viewport unit and scrolls inside it', () => {
    const transcript = ruleBody("[data-role='ticket-detail-transcript'] {");
    // dvh — the unit the sheet's own max-height uses. `vh` is the LARGE viewport on
    // mobile Safari, so it would claim more room than the sheet has.
    expect(transcript).toMatch(/max-height:\s*\d+dvh/);
    expect(transcript).toMatch(/overflow-y:\s*auto/);
    expect(ruleBody("[data-role='ticket-detail'] {")).toMatch(/max-height:\s*\d+dvh/);
  });
});
