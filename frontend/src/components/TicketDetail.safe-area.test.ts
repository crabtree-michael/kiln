// TicketDetail safe-area regression (07 §7): the ticket detail sheet is
// `position: fixed; bottom: 0` and portals to document.body, so — like the
// primary screen's dock ([data-role='dock'] in PrimaryScreen.css) — its own
// bottom padding must add env(safe-area-inset-bottom) or its content slips
// under the iPhone home indicator / bottom bar. This asserts the inset is
// present on BOTH the base rule and the primary-screen skin; reverting either
// to a plain fixed bottom padding (the pre-fix state) trips the check.
//
// The stylesheet is pulled in as a raw string (Vite `?raw`, typed via
// vite/client) rather than read off disk, so no untyped node built-ins are
// needed and the test asserts the exact CSS the app ships.
import cssRaw from './TicketDetail.css?raw';

const css: string = cssRaw;

// Isolate a rule's declaration block by its selector so we assert the inset is
// on the sheet's own padding, not merely present somewhere in the file.
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('TicketDetail safe area', () => {
  it('adds the home-indicator inset to the base sheet padding', () => {
    const body = ruleBody("[data-role='ticket-detail'] {");
    expect(body).toMatch(/padding:[^;]*env\(safe-area-inset-bottom/);
  });

  it('adds the home-indicator inset to the primary-screen skin padding', () => {
    const body = ruleBody("[data-role='ticket-detail'][data-surface='primary'] {");
    expect(body).toMatch(/padding:[^;]*env\(safe-area-inset-bottom/);
  });
});
