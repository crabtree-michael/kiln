// Layout-critical kanban CSS, asserted as a string (the `?raw` technique used by
// DesktopScreen.layout.test.ts and TicketDetail.safe-area.test.ts). jsdom
// performs no layout, so without this the board could silently collapse to one
// stacked column, a column could lose its own scroller, or the accent could leak
// onto a second state — and every DOM test next door would still pass.
import { describe, it, expect } from 'vitest';
import cssRaw from './Kanban.css?raw';
import desktopCssRaw from './DesktopScreen.css?raw';

const css: string = cssRaw;
const desktopCss: string = desktopCssRaw;

/** Isolates a rule's declaration block by its selector, so an assertion is about
 * that rule rather than about the file containing the string somewhere. */
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('Kanban.css', () => {
  it('narrows the shell to two columns — rail, then board', () => {
    const body = ruleBody("[data-role='desktop-screen'][data-view='kanban'] {");
    expect(body).toMatch(/grid-template-columns:\s*(\d+)px\s+minmax\(0,\s*1fr\)/);
    // The rail keeps the feed shell's exact width. The brief is that the sidebar
    // is IDENTICAL, and a rail four pixels narrower on one route is precisely the
    // drift that claim is about — so the two are compared rather than eyeballed.
    const here = /grid-template-columns:\s*(\d+)px/.exec(body);
    const there = /grid-template-columns:\s*(\d+)px/.exec(desktopCss);
    expect(here?.[1]).toBe(there?.[1]);
  });

  it('lays the board out as columns that scroll sideways rather than shrink', () => {
    const body = ruleBody("[data-role='kanban-board'] {");
    expect(body).toMatch(/display:\s*grid/);
    expect(body).toMatch(/grid-auto-flow:\s*column/);
    // A floor, not a fixed width: the columns share the width they have and stop
    // at a readable title, after which the board scrolls.
    expect(body).toMatch(/grid-auto-columns:\s*minmax\(\d+px,\s*1fr\)/);
    expect(body).toMatch(/overflow-x:\s*auto/);
  });

  it('gives every column its own scroller so the headings can’t be pushed off', () => {
    const column = ruleBody("[data-role='kanban-column'] {");
    expect(column).toMatch(/display:\s*flex/);
    expect(column).toMatch(/flex-direction:\s*column/);
    // Without `min-height: 0` a flex child refuses to shrink below its content
    // and the list's own overflow never engages — the classic silent failure.
    expect(column).toMatch(/min-height:\s*0/);

    const head = ruleBody("[data-role='kanban-column-head'] {");
    expect(head).toMatch(/flex:\s*none/);

    const list = ruleBody("[data-role='kanban-column-list'] {");
    expect(list).toMatch(/flex:\s*1/);
    expect(list).toMatch(/min-height:\s*0/);
    expect(list).toMatch(/overflow-y:\s*auto/);
  });

  it('re-anchors the shared loading line off the feed’s reading measure', () => {
    // The line's markup is the feed shell's, and there it is centred on a 720px
    // column — which on a five-column board parks it mid-third-column.
    const body = ruleBody("[data-view='kanban'] [data-role='desktop-loading-line'] {");
    expect(body).toMatch(/max-width:\s*none/);
    expect(body).toMatch(/margin:\s*0/);
  });

  it('firms the card’s border on hover rather than its fill', () => {
    // The trap this rule exists to avoid: `--surface-raised` lifts clearly above
    // `--surface-card` in the dark palette and is three hex points off it in the
    // light one, so a fill-based hover reads as "firms" at night and as nothing
    // at all in daylight. The border tokens carry the intent in both registers.
    const hover = ruleBody("[data-role='kanban-card']:hover {");
    expect(hover).toMatch(/border-color:\s*var\(--border-strong\)/);
    expect(hover).not.toMatch(/background/);
  });

  it('spends the whole accent budget on blocked, and on nothing else', () => {
    // 13 §4: the accent means "a person is needed for a decision". On this board
    // that is a blocked ticket and nothing else. A card's session mark takes its
    // colour from PrimaryScreen.css's shared status-dot vocabulary, which this
    // file deliberately does not restate — and which now agrees: fire there is
    // blocked (or a failed session), never a healthy working ticket.
    const accentRules = css
      .split('}')
      .filter((rule) => rule.includes('var(--accent'))
      .map((rule) => rule.trim());
    expect(accentRules).toHaveLength(1);
    expect(accentRules[0]).toContain("data-state='blocked'");
  });

  it('paints only in semantic tokens, so both themes come from tokens.css', () => {
    // A hex or rgb literal here would fork the palette instead of re-pointing it,
    // and would be wrong in whichever theme the author did not have open.
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/\brgba?\(/);
  });

  it('names no theme — the shell follows the OS (13 D6a)', () => {
    expect(css).not.toMatch(/\[data-theme=/);
    expect(css).not.toMatch(/prefers-color-scheme/);
  });

  it('declares nothing that moves on its own', () => {
    // The one thing permitted to animate on a desktop shell is the working
    // indication, and this file does not own it — it borrows the status dot.
    expect(css).not.toMatch(/@keyframes/);
    expect(css).not.toMatch(/animation:/);
  });
});
