// Accept, Delete and Poke are supposed to be the MICROPHONE, wearing different
// glyphs: same disc, same fill, same outline, same raised shadow that collapses
// under the thumb. Nothing else in the gate can see that. jsdom does no layout, so
// the DOM tests next door only know the buttons exist; and the mic is dressed in a
// DIFFERENT stylesheet (PrimaryScreen.css, unscoped, so the sheet's footer picks
// it up wherever it is placed), which is exactly the kind of seam a later edit to
// one side walks past without noticing the other.
//
// So this reads both files as strings (the `?raw` technique of
// TicketDetail.safe-area.test.ts) and holds the two rules to the same numbers.
// If the mic is restyled, this fails — and the fix is to restyle these with it,
// not to relax the assertion.
import detailCssRaw from './TicketDetail.css?raw';
import primaryCssRaw from './PrimaryScreen.css?raw';

/** Both files explain themselves at length INSIDE their rule bodies, and a
 *  comment sitting between two declarations breaks a naive "previous `;`" read of
 *  the next one — so the prose comes out before anything is matched. Selectors are
 *  untouched by this, which is what the `toContain` cases below rely on. */
function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

const detailCss = stripComments(detailCssRaw);
const primaryCss = stripComments(primaryCssRaw);

function ruleBody(css: string, selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

/** One declaration's value, whitespace-normalised, so `54px` and `54px ` compare
 *  equal and a multi-line `transition` reads as one string. */
function declaration(body: string, property: string): string {
  const match = new RegExp(`(?:^|;)\\s*${property}\\s*:([^;]*)`).exec(body);
  expect(match, `declaration not found: ${property}`).not.toBeNull();
  return (match?.[1] ?? '').replace(/\s+/g, ' ').trim();
}

const micGlyph = ruleBody(primaryCss, "[data-role='dock-mic'] {");
const micPressed = ruleBody(primaryCss, "[data-role='dock-talk']:active [data-role='dock-mic'] {");
const actionDisc = ruleBody(
  detailCss,
  "[data-role='detail-accept'],\n[data-role='detail-delete'],\n[data-role='detail-poke'] {",
);
const actionPressed = ruleBody(
  detailCss,
  "[data-role='detail-accept']:active,\n[data-role='detail-delete']:active,\n[data-role='detail-poke']:active {",
);

describe('TicketDetail Accept/Delete/Poke wear the mic’s treatment', () => {
  // The disc itself: size, shape, fill, outline, lift. Every one of these is a
  // thing the eye compares directly when the three sit in one row.
  it.each(['width', 'height', 'background', 'border', 'box-shadow'])(
    'matches the mic glyph’s %s',
    (property) => {
      expect(declaration(actionDisc, property)).toBe(declaration(micGlyph, property));
    },
  );

  it('is round like the mic', () => {
    expect(declaration(actionDisc, 'border-radius')).toBe('50%');
    expect(declaration(micGlyph, 'border-radius')).toBe('50%');
  });

  it('presses like the mic — the same push down onto the same collapsed shadow', () => {
    expect(declaration(actionPressed, 'transform')).toBe(declaration(micPressed, 'transform'));
    expect(declaration(actionPressed, 'box-shadow')).toBe(declaration(micPressed, 'box-shadow'));
    // …and animates the press with the same easing, or the two feel different
    // under the thumb even though they land in the same place.
    expect(declaration(actionDisc, 'transition')).toBe(declaration(micGlyph, 'transition'));
  });

  it('keeps meaning in the glyph colour, the one thing that may differ', () => {
    // Matched as whole rules rather than through `ruleBody`, which finds a
    // selector by its first mention — and both of these are first mentioned in
    // the shared disc rule above, whose body says nothing about colour.
    expect(detailCss).toMatch(/\[data-role='detail-accept'\] \{\s*color: var\(--accent\);\s*\}/);
    expect(detailCss).toMatch(/\[data-role='detail-delete'\] \{\s*color: var\(--danger\);\s*\}/);
  });

  it('sizes Poke’s emoji to the stroked glyphs beside it, and states nothing else', () => {
    // The 👉 is a colour glyph, so it takes no `color` the way the check and the
    // trash do — a size is the whole of its own rule, and 22px is what the trash
    // and the check are drawn at (their svg width/height). Matched as a whole
    // rule rather than through `ruleBody`, which finds a selector by its first
    // mention — and that is the last line of the shared disc rule above. Any
    // width/border/background creeping in here means Poke is drifting back out of
    // the row it now belongs to, so the match is exact.
    expect(detailCss).toMatch(
      /\[data-role='detail-poke'\] \{\s*font-family: inherit;\s*font-size: 22px;\s*line-height: 1;\s*\}/,
    );
  });

  it('is not re-skinned by the primary surface', () => {
    // The old skin gave Accept an accent pill, Delete a 40px danger circle and
    // Poke a 13px ghost pill at higher specificity, which is how the footer would
    // quietly drift back apart on the one surface the app actually renders.
    expect(detailCss).not.toContain("[data-surface='primary'] [data-role='detail-accept']");
    expect(detailCss).not.toContain("[data-surface='primary'] [data-role='detail-delete']");
    expect(detailCss).not.toContain("[data-surface='primary'] [data-role='detail-poke']");
  });

  it('answers a press, never a hover — the mic doesn’t either', () => {
    // A touch device emulates hover, so a finger that merely starts a scroll on
    // one of these would paint it as pressed. Poke's primary-surface skin carried
    // exactly such a rule before it joined the disc.
    expect(detailCss).not.toContain("[data-role='detail-accept']:hover");
    expect(detailCss).not.toContain("[data-role='detail-delete']:hover");
    expect(detailCss).not.toContain("[data-role='detail-poke']:hover");
  });
});
