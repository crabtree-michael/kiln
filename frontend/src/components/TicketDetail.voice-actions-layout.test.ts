// The footer's arrangement is CSS, and jsdom does no layout — so the DOM tests
// next door can only see WHICH controls render, never where they end up. This is
// the only thing in the gate that can catch the geometry regressing: same `?raw`
// technique as TicketDetail.header-layout.test.ts / TicketDetail.safe-area.test.ts.
//
// What it is protecting is one property: **the mic does not move when a voice
// session starts.** The cluster spans the row and distributes its own children,
// so the mic sits at the row's left edge in both readings and the send group
// lands on the right edge, in the slot the state actions vacate. The failure this
// replaced moved the whole cluster across the row instead, dragging the mic out
// from under the finger that had just tapped it and shoving Poke/Accept aside to
// make room.
import cssRaw from './TicketDetail.css?raw';

const css: string = cssRaw;

function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('TicketDetail voice cluster layout', () => {
  it('spans the row and distributes its own contents', () => {
    // Both halves are what hold the mic still. `flex: 1` makes the cluster the
    // full width of the free space, so its first child (the mic) is at the row's
    // left edge — and `space-between` is what puts the send group at the other
    // end rather than bunched up beside the mic. With the mic alone inside,
    // `space-between` is simply flex-start, which is also what pushes the state
    // actions to the right at rest.
    const body = ruleBody("[data-role='ticket-detail-voice-actions'] {");
    expect(body).toMatch(/flex:\s*1/);
    expect(body).toMatch(/justify-content:\s*space-between/);
  });

  it('never moves the cluster — no order, no margin flip, no position switch', () => {
    // The old arrangement carried the cluster across the row with `order` and an
    // auto margin it dropped mid-utterance. Any of the three coming back means the
    // mic has started travelling again.
    const body = ruleBody("[data-role='ticket-detail-voice-actions'] {");
    expect(body).not.toMatch(/order:/);
    expect(body).not.toMatch(/margin-right:\s*auto/);
    expect(css).not.toContain("[data-role='ticket-detail-voice-actions'][data-position=");
  });

  it('keeps Send and × as one grouped box', () => {
    // `space-between` over three loose children would strand the × in the middle
    // of the row; the group is what makes the pair arrive and leave together, in
    // the slot the state actions hand over.
    const body = ruleBody("[data-role='ticket-detail-voice-send'] {");
    expect(body).toMatch(/display:\s*flex/);
    expect(body).toMatch(/gap:/);
  });

  it('leaves no rule behind for the old travelling cluster', () => {
    // Both earlier names for it. A stale rule would silently keep styling nothing.
    expect(css).not.toContain('ticket-detail-lead-actions');
    expect(css).not.toContain("data-position='trail'");
  });
});
