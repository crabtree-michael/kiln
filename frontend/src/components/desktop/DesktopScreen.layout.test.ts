// Layout-critical desktop CSS, asserted as a string (the `?raw` technique used
// by TicketDetail.safe-area.test.ts). jsdom performs no layout, so without this
// the whole two-region shell could silently collapse to one column — or the
// accent could leak onto a second state — and every DOM test above would still
// pass.
import { describe, it, expect } from 'vitest';
import cssRaw from './DesktopScreen.css?raw';
import { DESKTOP_MIN_WIDTH } from '@/components/desktop/use-desktop-layout';

const css: string = cssRaw;

/** Isolates a rule's declaration block by its selector, so an assertion is about
 * that rule rather than about the file containing the string somewhere. */
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('DesktopScreen.css', () => {
  it('lays the shell out as two columns — the rail beside the feed', () => {
    const body = ruleBody("[data-role='desktop-screen'] {");
    expect(body).toMatch(/display:\s*grid/);
    expect(body).toMatch(/grid-template-columns:\s*\d+px\s+minmax\(0,\s*1fr\)/);
    expect(body).toMatch(/height:\s*100dvh/);
  });

  it('locks the document so all scrolling happens inside the feed', () => {
    expect(css).toMatch(/html:has\(\[data-role='desktop-screen'\]\)/);
    const body = ruleBody("html:has([data-role='desktop-screen']) body {");
    expect(body).toMatch(/overflow:\s*hidden/);
  });

  it('gives the feed its own scroll region with scroll anchoring, so arrivals land in place', () => {
    const body = ruleBody("[data-role='desktop-feed'] {");
    expect(body).toMatch(/overflow-y:\s*auto/);
    // The property that keeps a card arriving at the top from shoving the line
    // being read downward (13 §6) — the single most important behavioural detail.
    expect(body).toMatch(/overflow-anchor:\s*auto/);
  });

  it('caps the feed at one readable column rather than spending width on more', () => {
    const body = ruleBody("[data-role='desktop-feed-list'] {");
    expect(body).toMatch(/max-width:\s*\d+px/);
    expect(body).toMatch(/margin:\s*0 auto/);
    // No grid, no columns: the room goes to legibility (13 §6).
    expect(body).not.toMatch(/grid-template-columns/);
    expect(body).not.toMatch(/column-count/);
  });

  it('spends the whole accent budget on needs-you, and on nothing else at all', () => {
    // 13 §4: "If the accent is on screen, it means something. That is the entire
    // contrast budget, and spending it on anything else breaks the one loud thing
    // that is supposed to work." So this file may paint the accent EXACTLY once —
    // the rail's needs-you dot. Not `working`, not the disconnected band, and not
    // the send button (a window left open all day must not carry a permanently
    // lit accent in the corner). Adding a second use is the failure this catches.
    const accentRules = css
      .split('}')
      .filter((rule) => rule.includes('var(--accent'))
      .map((rule) => rule.split('{')[0]?.trim() ?? '');
    expect(accentRules).toHaveLength(1);
    expect(accentRules[0]).toContain("data-state='needs-you'");
  });

  it('shows a blocker and a proposal body in full, beating the seen-state clamp', () => {
    const body = ruleBody(
      "[data-role='desktop-feed-row'][data-kind='blocker'] [data-role='feed-card-body'],",
    );
    expect(body).toMatch(/line-clamp:\s*unset/);
    expect(body).toMatch(/overflow:\s*visible/);
  });

  it('reveals the timestamp on hover AND on focus, so hover is never the only way', () => {
    expect(css).toMatch(
      /\[data-role='desktop-feed-row'\]:hover \[data-role='feed-card-age'\],\s*\[data-role='desktop-feed-row'\]:focus-within \[data-role='feed-card-age'\]/,
    );
  });

  it('arrivals fade and nothing slides in from an edge', () => {
    const arrive = css.slice(css.indexOf('@keyframes kiln-desktop-arrive'));
    const block = arrive.slice(0, arrive.indexOf('}\n}') + 3);
    expect(block).toMatch(/opacity:\s*0/);
    expect(block).not.toMatch(/transform/);
    expect(block).not.toMatch(/translate/);
  });

  it('the working indication breathes — slow, shallow, and never a progress bar', () => {
    const breathe = css.slice(css.indexOf('@keyframes kiln-breathe'));
    const block = breathe.slice(0, breathe.indexOf('}\n}') + 3);
    expect(block).toMatch(/opacity/);
    expect(block).not.toMatch(/width/);
    const dot = ruleBody("[data-role='desktop-working-dot'] {");
    expect(dot).toMatch(/animation:\s*kiln-breathe/);
  });

  it('keeps the working strip in the same reading column as the feed', () => {
    // The strip lists what is being worked on directly above the cards, so a
    // different measure or a different gutter would read as a second column
    // rather than as the head of this one.
    const head = ruleBody("[data-role='desktop-working-head'] {");
    const list = ruleBody("[data-role='desktop-working-list'] {");
    expect(head).toMatch(/max-width:\s*720px/);
    expect(list).toMatch(/max-width:\s*720px/);
    expect(list).toMatch(/margin:\s*var\(--space-2\) auto 0/);
    // And it holds its own height rather than scrolling with the feed — the
    // whole of "not buried in the feed".
    expect(ruleBody("[data-role='desktop-working'] {")).toMatch(/flex:\s*none/);
  });

  it('the working strip lists, it never measures — no bar, no ticking counter', () => {
    // 13 §8's deliberate absences. A row is a title, a word, and a relative age;
    // anything that fills or counts up converts "present" into "demanding".
    // Comments stripped first: this is about what the region DECLARES, not about
    // the prose explaining why it doesn't.
    const strip = css
      .slice(
        css.indexOf("[data-role='desktop-working'] {"),
        css.indexOf('/* The resting state is the real state'),
      )
      .replace(/\/\*[\s\S]*?\*\//g, '');
    expect(strip).not.toMatch(/@keyframes/);
    expect(strip).not.toMatch(/progress/);
    // The one animation in the region is the head's breathing dot, declared
    // once — the rows themselves are still.
    expect(strip.match(/animation:/g)).toHaveLength(1);
  });

  it('keeps the loading line in the feed’s reading column, and off the accent', () => {
    // It sits directly above the feed and says something about the whole
    // project, so it has to line up with the column it is about — a different
    // measure or gutter reads as a second column (the working strip's rule).
    // The accent assertion is covered in full by the budget case above; what
    // matters here is that a *waiting* state, of all things, never spends it.
    const line = ruleBody("[data-role='desktop-loading-line'] {");
    expect(line).toMatch(/max-width:\s*720px/);
    expect(line).toMatch(/margin:\s*0 auto/);
    expect(ruleBody("[data-role='desktop-loading'] {")).toMatch(/flex:\s*none/);
    const mark = ruleBody("[data-role='desktop-loading-mark'] {");
    expect(mark).not.toMatch(/var\(--accent/);
    // Indeterminate by construction: a turning mark, never a filling bar — a
    // fetch has no measurable progress and a bar that measures nothing is a lie.
    expect(mark).toMatch(/animation:\s*kiln-spin/);
    expect(mark).not.toMatch(/width:\s*\d+%/);
  });

  it('suppresses every self-starting animation under prefers-reduced-motion', () => {
    const query = css.slice(css.indexOf('@media (prefers-reduced-motion: reduce)'));
    expect(query).toMatch(/\[data-role='desktop-feed-row'\]/);
    expect(query).toMatch(/desktop-working-dot/);
    expect(query).toMatch(/rail-project-dot/);
    expect(query).toMatch(/desktop-loading-mark/);
  });

  it('uses desktop density in the rail — no thumb-sized targets', () => {
    const body = ruleBody("[data-role='rail-project'] {");
    const match = /min-height:\s*(\d+)px/.exec(body);
    expect(match).not.toBeNull();
    expect(Number(match?.[1])).toBeLessThan(44);
  });

  it('opens the bell panel up and to the right, away from the rail foot', () => {
    // The bell sits at the BOTTOM LEFT here, not in a top-right header cluster,
    // so the mobile anchoring it would otherwise inherit (`top: 100%; right: 0`)
    // puts the panel off the bottom of a 100dvh shell and off the left edge at
    // once — invisible, with nothing to scroll it back. jsdom does no layout, so
    // this string assertion is the only thing in the gate that can see it.
    const body = ruleBody(
      "[data-role='desktop-screen'] [data-role='notify-settings-panel'] {",
    ).replace(/\/\*[\s\S]*?\*\//g, '');
    // Both mobile anchors must be released, or `top` and `bottom` together
    // stretch the panel instead of moving it.
    expect(body).toMatch(/top:\s*auto/);
    expect(body).toMatch(/right:\s*auto/);
    expect(body).toMatch(/bottom:\s*calc\(100% \+ \d+px\)/);
    expect(body).toMatch(/left:\s*0/);
    expect(body).toMatch(/transform-origin:\s*bottom left/);
  });

  it('re-states the open transform for the desktop panel, beating the mobile rule', () => {
    // Specificity trap: the closed desktop rule and the mobile [data-open='true']
    // rule both weigh (0,2,0), and this file loads second — so without a
    // higher-specificity open rule the closed transform wins and the panel opens
    // stuck 6px out of place.
    const body = ruleBody(
      "[data-role='desktop-screen'] [data-role='notify-settings-panel'][data-open='true'] {",
    );
    expect(body).toMatch(/transform:\s*translateY\(0\)\s*scale\(1\)/);
  });

  it('reads only semantic tokens — no literal colors forked for desktop', () => {
    // Hex literals would fork the palette instead of re-pointing the existing
    // warm near-black (13 D6). `--accent`, `--surface-*` etc. are the contract.
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).not.toMatch(/\brgba?\(/);
  });

  it('states each rule once for both themes, rather than branching on the theme', () => {
    // The desk follows the OS preference through the one mechanism every other
    // route uses: `data-theme` on <html> (ThemeColorSync) re-pointing the
    // semantic tokens. So this sheet never names a theme. A `[data-theme='dark']`
    // override or a `prefers-color-scheme` query here would be a second, desktop-
    // only theme switch — the exact palette fork 13 D6 rules out, and one that
    // could disagree with <html> about which theme is up.
    //
    // Comments are stripped first, the way the vaul-geometry test below does it:
    // this is about what the sheet DECLARES, not about the prose naming the
    // tokens dark mode re-points.
    const declared = css.replace(/\/\*[\s\S]*?\*\//g, '');
    expect(declared).not.toMatch(/data-theme/);
    expect(declared).not.toMatch(/prefers-color-scheme/);
  });

  it('caps the ticket sheet to a reading measure instead of the whole monitor', () => {
    // 13 D7: detail opens OVER the feed. Left at its phone geometry the sheet
    // spans a 2000px monitor for a paragraph of text, which is exactly the
    // mobile-stretched view this shell exists to replace.
    const body = ruleBody(
      "body[data-shell='desktop'] [data-role='ticket-detail'][data-surface='primary'] {",
    );
    expect(body).toMatch(/width:\s*min\(\d+px/);
    expect(body).toMatch(/margin:\s*0 auto/);
    expect(body).toMatch(/border-radius:\s*var\(--radius-xl\)/);
  });

  it('never overrides the sheet geometry vaul owns', () => {
    // Vaul writes the slide and the drag as INLINE transforms keyed to its own
    // open/closed state. A CSS `transform` here would be ignored (inline wins)
    // and, forced with `!important`, would strand the sheet permanently open;
    // moving its `bottom` would put the closed position out of step with the
    // translate that is supposed to hide it. Comments are stripped first — this
    // is about what the rule DECLARES, not about the prose explaining it.
    const declared = ruleBody(
      "body[data-shell='desktop'] [data-role='ticket-detail'][data-surface='primary'] {",
    ).replace(/\/\*[\s\S]*?\*\//g, '');
    expect(declared).not.toMatch(/(^|;)\s*transform\s*:/);
    expect(declared).not.toMatch(/!important/);
    expect(declared).not.toMatch(/(^|;)\s*bottom\s*:/);
  });

  it('the JS breakpoint stays the single source of the desktop threshold', () => {
    // The CSS deliberately carries NO min-width media query for the shell switch:
    // the shell is chosen in JS (useIsDesktop), so a second breakpoint here could
    // silently disagree with it.
    expect(css).not.toMatch(/@media[^{]*min-width/);
    expect(DESKTOP_MIN_WIDTH).toBe(1024);
  });
});
