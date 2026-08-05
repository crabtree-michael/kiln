// The footer's two arrangements are CSS, and jsdom does no layout — so the DOM
// tests next door can only see that `data-position` flips, never that the flip
// actually moves the cluster. This is the only thing in the gate that can catch
// the geometry regressing: same `?raw` technique as
// TicketDetail.header-layout.test.ts / TicketDetail.safe-area.test.ts.
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
  it('pins the resting cluster to the row’s left edge', () => {
    // `margin-right: auto` is the whole of the left-group / right-group reading:
    // it is what shoves Accept and Poke over to the trailing end.
    expect(ruleBody("[data-role='ticket-detail-voice-actions'] {")).toMatch(/margin-right:\s*auto/);
  });

  it('sends the speaking cluster to the end of the row instead', () => {
    // Both halves matter. Dropping the auto margin is what stops it holding the
    // left edge; `order` is what carries it past Poke and Delete to the trailing
    // end, so the row reads Send, ×, mic inward from the right — Send landing in
    // the slot Accept has just vacated.
    const body = ruleBody("[data-role='ticket-detail-voice-actions'][data-position='trail'] {");
    expect(body).toMatch(/margin-right:\s*0/);
    expect(body).toMatch(/order:\s*1/);
  });

  it('moves it by ORDER, never by re-parenting', () => {
    // The cluster keeps one fixed spot in the DOM in both readings, because
    // re-parenting it would unmount and remount the mic inside it mid-utterance —
    // taking its ticket-context registration and its volume-glow loop with it.
    // The rule below is the one that does the moving; if it ever disappears, the
    // component has almost certainly started rendering the cluster in two places.
    expect(css).toContain("[data-role='ticket-detail-voice-actions'][data-position='trail']");
  });

  it('leaves no rule behind for the old lead-only cluster', () => {
    // It was renamed when it stopped always being the lead; a stale rule would
    // silently keep styling nothing.
    expect(css).not.toContain('ticket-detail-lead-actions');
  });
});
