// Layout-critical desktop CSS, asserted as a string (the `?raw` technique used
// by TicketDetail.safe-area.test.ts). jsdom performs no layout, so without this
// the whole two-region shell could silently collapse to one column — or the
// accent could leak onto a second state — and every DOM test above would still
// pass.
import { describe, it, expect } from 'vitest';
import cssRaw from './DesktopScreen.css?raw';
import tokensRaw from '@/styles/tokens.css?raw';
import { DESKTOP_MIN_WIDTH } from '@/components/desktop/use-desktop-layout';

const css: string = cssRaw;
const tokens: string = tokensRaw;

/** How far the listening mic's glow reaches past the button's edge, in px: the
 * outer stop of `kiln-mic-glow` (PrimaryScreen.css) is a 5px spread under a 28px
 * blur at full `--mic-level`, and a blur fades over roughly half its radius. The
 * composer region reserves at least this much above itself. */
const MIC_GLOW_REACH_PX = 20;

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
  it('lays the shell out as three columns — rail, in-progress panel, feed', () => {
    const body = ruleBody("[data-role='desktop-screen'] {");
    expect(body).toMatch(/display:\s*grid/);
    // Two fixed columns of furniture, then the feed taking every remaining pixel
    // (`minmax(0, …)` so a long unbreakable card can never push it wider than
    // the window and start the page scrolling horizontally).
    expect(body).toMatch(/grid-template-columns:\s*\d+px\s+\d+px\s+minmax\(0,\s*1fr\)/);
    expect(body).toMatch(/height:\s*100dvh/);
    // The furniture must not eat the feed: at the 1024px shell threshold the two
    // fixed columns have to leave a real reading measure behind.
    const columns = /grid-template-columns:\s*(\d+)px\s+(\d+)px/.exec(body);
    const fixed = Number(columns?.[1]) + Number(columns?.[2]);
    expect(DESKTOP_MIN_WIDTH - fixed).toBeGreaterThanOrEqual(500);
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

  it('holds "Show earlier" to the same column as the cards above it (08 D2‴)', () => {
    // The control renders below the list, not inside it, so that it survives a
    // feed whose cards have all collapsed away — which also means the list's own
    // measure does not reach it and it has to be given one.
    const body = ruleBody("[data-role='desktop-screen'] [data-role='feed-show-earlier'] {");
    expect(body).toMatch(/max-width:\s*720px/);
    expect(body).toMatch(/margin:[^;]*auto/);
    // And a button is inline-block by default, on which auto margins compute to
    // zero — without this the control hugs the left edge of the feed region.
    expect(body).toMatch(/display:\s*block/);
  });

  it('centres the resting state and carries "Show earlier" to the foot of it', () => {
    // The empty feed is not a short list, it is one composed state with the way
    // back beneath it. jsdom does no layout, so this string is the only thing in
    // the gate that can tell the intended arrangement from the control simply
    // rendering directly under the text with the window blank below it.
    const region = ruleBody("[data-role='desktop-feed'][data-empty='true'] {");
    expect(region).toMatch(/display:\s*flex/);
    expect(region).toMatch(/flex-direction:\s*column/);

    const rest = ruleBody("[data-role='desktop-rest'] {");
    // Grows into the region's free height, which is what pushes the sibling
    // control down; centred on both axes, and centred text with it.
    expect(rest).toMatch(/flex:\s*1 0 auto/);
    expect(rest).toMatch(/justify-content:\s*center/);
    expect(rest).toMatch(/align-items:\s*center/);
    expect(rest).toMatch(/text-align:\s*center/);

    // The fallback for the one empty state with no resting block in it: mid
    // project-switch, "All quiet." is withheld and nothing else would hold the
    // control down.
    const control = ruleBody(
      "[data-role='desktop-feed'][data-empty='true'] [data-role='feed-show-earlier'] {",
    );
    expect(control).toMatch(/margin-top:\s*auto/);
    // Longhand only — the shorthand would drop the horizontal `auto` that keeps
    // the control on the same centred measure as the cards.
    expect(control).not.toMatch(/(^|;)\s*margin\s*:/);
  });

  it('gives the resting state a large mark, sized in the sheet rather than on the tag', () => {
    // An <img> with no CSS size renders at the asset's intrinsic box; stating it
    // here keeps the one number in the same place as the rest of the geometry.
    const mark = ruleBody("[data-role='desktop-rest-mark'] {");
    const width = /width:\s*(\d+)px/.exec(mark);
    expect(width).not.toBeNull();
    // "Large" is the whole point — bigger than the phone's 64px mark, since a
    // desk has the room for it.
    expect(Number(width?.[1])).toBeGreaterThanOrEqual(64);
    expect(mark).toMatch(/height:\s*\d+px/);
  });

  it('spends the whole accent budget on needs-you, and on nothing else at all', () => {
    // 13 §4: "If the accent is on screen, it means something. That is the entire
    // contrast budget, and spending it on anything else breaks the one loud thing
    // that is supposed to work." So this file may paint the accent EXACTLY once —
    // the rail's needs-you dot. Not the disconnected band, and not the send
    // button (a window left open all day must not carry a permanently lit accent
    // in the corner). Adding a second use is the failure this catches.
    //
    // One thing this deliberately does NOT cover: the in-progress panel's
    // per-ticket status marks, which are the phone's `[data-role='status-dot']`
    // and carry the accent for `building` from PrimaryScreen.css. That is a
    // knowing spend, made so the two platforms show the same mark for the same
    // state rather than two vocabularies for it — and it stays out of this file
    // precisely because it is the SHARED mark, not a desktop invention.
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
    // Only the LIVE reading breathes. The panel is permanent now, so a bare
    // `[data-role='desktop-working-dot']` animation would be a mark looping all
    // day in a column with nothing in it — manufactured activity (13 §1).
    expect(ruleBody("[data-role='desktop-working-dot'] {")).not.toMatch(/animation/);
    const live = ruleBody("[data-role='desktop-working-dot'][data-active='true'] {");
    expect(live).toMatch(/animation:\s*kiln-breathe/);
  });

  it('gives the in-progress panel its own column, ruled off from the feed', () => {
    const body = ruleBody("[data-role='desktop-working-panel'] {");
    // The separator the panel is defined by: a line on the edge it shares with
    // the feed, because both are readings of the same project (the rail, which
    // is furniture, sets itself apart with a recessed surface instead). It has
    // to be legible ink and not the rail's hairline — a seam you have to look
    // for reads as the feed's left margin, not as a region boundary.
    expect(body).toMatch(/border-right:\s*1px solid var\(--border-strong\)/);
    // It owns its own overflow, so a busy project scrolls the panel rather than
    // pushing the composer off the bottom of a locked-height window.
    expect(body).toMatch(/overflow-y:\s*auto/);
    expect(body).toMatch(/min-height:\s*0/);
    // And it never grows to fit a long title — a column that widened with its
    // content would shove the feed sideways every time work started.
    expect(body).toMatch(/min-width:\s*0/);
    // The strip inside it holds its own height rather than stretching.
    expect(ruleBody("[data-role='desktop-working'] {")).toMatch(/flex:\s*none/);
  });

  it('does not restate the feed’s reading measure inside the panel', () => {
    // The 720px measure belongs to the feed's column. Carrying it over here (as
    // the old above-the-feed strip did, to line the two up) would now be a
    // 720px-wide block trying to live in a 248px column.
    expect(ruleBody("[data-role='desktop-working-head'] {")).not.toMatch(/max-width/);
    expect(ruleBody("[data-role='desktop-working-list'] {")).not.toMatch(/max-width/);
  });

  it('the in-progress panel lists, it never measures — no bar, no ticking counter', () => {
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
    // measure or gutter reads as a second column, which is now literally what
    // the region to its left is. The accent assertion is covered in full by the
    // budget case above; what matters here is that a *waiting* state, of all
    // things, never spends it.
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

  it('stands the ticket detail up against the right edge, at a reading measure', () => {
    // 13 D7a: detail opens BESIDE the feed. The phone's bottom sheet lands in
    // the middle of a window and covers the feed and the working strip — the
    // ongoing work the ticket is being read against — so at a desk it is
    // anchored to the right edge instead. `top: 0` with the base rule's
    // `bottom: 0` is what makes it full height; `left: auto` is what releases
    // the phone's full-bleed span (without it the width below is ignored, since
    // left+right+width over-constrain the box and `left` wins).
    const body = ruleBody(
      "body[data-shell='desktop'] [data-role='ticket-detail'][data-surface='primary'] {",
    );
    expect(body).toMatch(/top:\s*0/);
    expect(body).toMatch(/left:\s*auto/);
    expect(body).toMatch(/right:\s*0/);
    expect(body).toMatch(/width:\s*min\(\d+px/);
    // The phone's 85dvh cap would leave a top-and-bottom-anchored panel hanging
    // off the top edge.
    expect(body).toMatch(/max-height:\s*none/);
    // Every pixel the panel claims is feed the user can no longer see behind
    // it, which is the whole reason it moved to the edge.
    const width = /width:\s*min\((\d+)px/.exec(body);
    expect(Number(width?.[1])).toBeLessThan(680);
  });

  it('dims the window behind the panel, so the open ticket is plainly the lit surface', () => {
    // 13 D7b, amending D7a: the scrim is back. Undimmed, an open ticket read as
    // one more surface among several on a wide window, and the panel's hairline
    // is too quiet a boundary to carry "this is what you are attending to" on
    // its own. `--scrim` and nothing else — a desktop-only dim value would fork
    // the token that already means exactly this in both themes.
    const body = ruleBody("body[data-shell='desktop'] [data-role='ticket-detail-backdrop'] {");
    expect(body).toMatch(/background:\s*var\(--scrim\)/);
  });

  it('never overrides the sheet geometry vaul owns', () => {
    // Vaul writes the slide and the drag as INLINE transforms keyed to its own
    // open/closed state and to the `direction` the shell passes as `placement`.
    // A CSS `transform` here would be ignored (inline wins) and, forced with
    // `!important`, would strand the panel permanently open; moving `bottom` off
    // the edge vaul translates away from would put the closed position out of
    // step with the translate that is supposed to hide it. Comments are stripped
    // first — this is about what the rule DECLARES, not about the prose
    // explaining it.
    const declared = ruleBody(
      "body[data-shell='desktop'] [data-role='ticket-detail'][data-surface='primary'] {",
    ).replace(/\/\*[\s\S]*?\*\//g, '');
    expect(declared).not.toMatch(/(^|;)\s*transform\s*:/);
    expect(declared).not.toMatch(/!important/);
    expect(declared).not.toMatch(/(^|;)\s*bottom\s*:/);
  });

  it('drops only the three edges that meet the window, so a blocked panel keeps its tint', () => {
    // `border: none` plus a fresh `border-left` here would out-specify the
    // blocked ticket's `border-color` rule in TicketDetail.css and quietly erase
    // the one border the panel still shows.
    const declared = ruleBody(
      "body[data-shell='desktop'] [data-role='ticket-detail'][data-surface='primary'] {",
    ).replace(/\/\*[\s\S]*?\*\//g, '');
    expect(declared).toMatch(/border-top:\s*none/);
    expect(declared).toMatch(/border-right:\s*none/);
    expect(declared).not.toMatch(/border-left:/);
    expect(declared).not.toMatch(/(^|;)\s*border\s*:/);
  });

  it('leads the composer with the mic and paints no box around the pair', () => {
    // 13 D5a: the mic is the composer's centrepiece, so the row is a bare flex
    // line — the mic is a raised object and the field is the capsule. A box
    // around both would put the mic INSIDE a text field, which is the weighting
    // this layout exists to undo.
    const body = ruleBody("[data-role='desktop-composer'] {");
    expect(body).toMatch(/display:\s*flex/);
    expect(body).not.toMatch(/background/);
    expect(body).not.toMatch(/border/);
    // And it is never shrunk: the previous typing-first line sized the orb down
    // to a peer of the send button, which is exactly how it read.
    expect(css).not.toMatch(/\[data-role='desktop-composer'\] \[data-role='dock-mic'\]/);
  });

  it('reserves the mic glow room above the composer, so a toast cannot clip it', () => {
    // The listening mic radiates a breathing box-shadow ring reaching ~20px past
    // the button's edge (`kiln-mic-glow`, PrimaryScreen.css), and the activity
    // row is anchored to this region's TOP edge with an opaque `--surface-page`
    // band and z-index 6. With the composer flush against that edge the band cut
    // the glow off along a hard horizontal line whenever a toast was up. The
    // phone never showed it (the dock's own padding stands the mic off its top
    // edge); the desk has to reserve the reach itself. jsdom does no layout, so
    // this string is the only thing in the gate that can catch the `0` coming
    // back.
    const body = ruleBody("[data-role='desktop-composer-region'] {");
    expect(body).toMatch(/position:\s*relative/);
    const padding = /padding:\s*var\(--(space-\d+)\)/.exec(body);
    expect(padding, 'the composer region must declare a top padding').not.toBeNull();
    const scale = new RegExp(`--${padding?.[1] ?? ''}:\\s*(\\d+)px`).exec(tokens);
    expect(Number(scale?.[1])).toBeGreaterThanOrEqual(MIC_GLOW_REACH_PX);
  });

  it('makes the field itself the painted surface, and the field alone', () => {
    const body = ruleBody("[data-role='desktop-field'] {");
    expect(body).toMatch(/background:\s*var\(--surface-card\)/);
    expect(body).toMatch(/border-radius:\s*var\(--radius-xl\)/);
    expect(body).toMatch(/flex:\s*1/);
  });

  it('keeps the heard words and the typed input on identical metrics', () => {
    // They are ONE field (13 §7): the textarea lies over the heard block while
    // speech is on screen, so any divergence in type or padding shows up as the
    // words shifting the moment the transcript is adopted into the draft.
    const heard = ruleBody("[data-role='desktop-heard'] {");
    const input = ruleBody("[data-role='desktop-input'] {");
    for (const declaration of [/font:\s*var\(--type-body\)/, /padding:\s*var\(--space-2\) 0/]) {
      expect(heard).toMatch(declaration);
      expect(input).toMatch(declaration);
    }
  });

  it('lays the input over the heard words while they are being heard', () => {
    // The textarea stays the thing you click into and the thing "/" focuses —
    // that focus is the handover — so it covers the words rather than yielding
    // to them, and paints in no ink of its own while it does.
    const body = ruleBody(
      "[data-role='desktop-field'][data-hearing='true'] [data-role='desktop-input'] {",
    );
    expect(body).toMatch(/position:\s*absolute/);
    expect(body).toMatch(/inset:\s*0/);
    expect(body).toMatch(/color:\s*transparent/);
  });

  it('the JS breakpoint stays the single source of the desktop threshold', () => {
    // The CSS deliberately carries NO min-width media query for the shell switch:
    // the shell is chosen in JS (useIsDesktop), so a second breakpoint here could
    // silently disagree with it.
    expect(css).not.toMatch(/@media[^{]*min-width/);
    expect(DESKTOP_MIN_WIDTH).toBe(1024);
  });
});
