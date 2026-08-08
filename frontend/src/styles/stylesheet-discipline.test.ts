// What the stylesheets may CONTAIN — the handful of claims that are genuinely
// about the text of a CSS file and not about where anything ends up on screen.
//
// This is what is left of eleven files (~2000 lines) that asserted layout by
// slicing a rule's declaration block out of a stylesheet by selector and
// matching its text. Those are gone: layout is checked as computed geometry in a
// real browser now (tests/layout/), which is the only thing that can tell a rule
// that is present and correct from one that is present and wrong.
//
// The claims below survive because a browser cannot make them. "No hex literal
// anywhere in this sheet" and "the accent is spent exactly once" are statements
// about the source, not about a rendering — you cannot observe the absence of a
// colour you did not use. They are whole-file scans: nothing here reconstructs a
// rule body or depends on how a sheet is formatted, so re-wrapping a
// declaration cannot break them.
import { describe, it, expect } from 'vitest';
import tokensRaw from '@/styles/tokens.css?raw';
import primaryRaw from '@/components/PrimaryScreen.css?raw';
import detailRaw from '@/components/TicketDetail.css?raw';
import desktopRaw from '@/components/desktop/DesktopScreen.css?raw';
import kanbanRaw from '@/components/desktop/Kanban.css?raw';
import { DESKTOP_MIN_WIDTH } from '@/components/desktop/use-desktop-layout';

/** A sheet with its prose removed. Every claim here is about what a file
 * DECLARES; the comments in these sheets quote selectors, tokens and the very
 * numbers being ruled out, and matching those would make the explanation itself
 * the failure. */
function declarations(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

/** The sheets held to the palette (13 D6/D6a). The desk sheets and the ticket
 * sheet, and deliberately NOT PrimaryScreen.css: the phone shell predates the
 * rule and still carries three hand-tuned `rgba()` shadows. Widening this to it
 * is a real cleanup and a separate one — it would fail today, which is not the
 * same claim as "no new sheet may fork the palette". */
const PALETTE_SHEETS: [string, string][] = [
  ['TicketDetail.css', detailRaw],
  ['DesktopScreen.css', desktopRaw],
  ['Kanban.css', kanbanRaw],
];

/** The app shell's sheets: the ones the layer scale in tokens.css speaks for. */
const SHELL_SHEETS: [string, string][] = [
  ['PrimaryScreen.css', primaryRaw],
  ['DesktopScreen.css', desktopRaw],
  ['Kanban.css', kanbanRaw],
];

describe('one palette, one theme switch', () => {
  it.each(PALETTE_SHEETS)('%s paints only in semantic tokens', (_name, css) => {
    // A hex or rgb literal forks the palette instead of re-pointing it, and is
    // wrong in whichever theme the author did not have open (13 D6).
    const declared = declarations(css);
    expect(declared).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(declared).not.toMatch(/\brgba?\(/);
  });

  it.each(PALETTE_SHEETS)('%s names no theme of its own', (_name, css) => {
    // Every route follows the OS through one mechanism: `data-theme` on <html>
    // (ThemeColorSync) re-pointing the semantic tokens. A second switch in a
    // component sheet could disagree with <html> about which theme is up.
    const declared = declarations(css);
    expect(declared).not.toMatch(/\[data-theme=/);
    expect(declared).not.toMatch(/prefers-color-scheme/);
  });

  it('tokens.css is where the literals and the theme switch live', () => {
    // The counterpart to the two cases above: they are only a discipline if the
    // one file allowed to hold the palette actually holds it.
    expect(tokensRaw).toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(tokensRaw).toMatch(/\[data-theme='dark'\]/);
  });
});

describe('the accent budget', () => {
  // 13 §4: "If the accent is on screen, it means something. That is the entire
  // contrast budget, and spending it on anything else breaks the one loud thing
  // that is supposed to work." Each desk sheet may light it exactly once, on the
  // one state that means a person is needed for a decision.
  it.each([
    ['DesktopScreen.css', desktopRaw, "data-state='needs-you'"],
    ['Kanban.css', kanbanRaw, "data-state='blocked'"],
  ])('%s spends the accent once, on %s', (_name, css, state) => {
    const spends = declarations(css)
      .split('}')
      .filter((rule) => rule.includes('var(--accent'))
      .map((rule) => rule.split('{')[0]?.trim() ?? '');
    expect(spends).toHaveLength(1);
    expect(spends[0]).toContain(state);
  });
});

describe('the layer scale is the only place a stacking order is written', () => {
  it('publishes every rung the shell reads', () => {
    // The order itself is asserted as real paint order in
    // tests/layout/bottom-stack.spec.ts; this is the other half — that the scale
    // exists to be read at all.
    for (const rung of [
      'layer-feed',
      'layer-dock',
      'layer-header',
      'layer-popover',
      'layer-detail-scrim',
      'layer-detail-panel',
      'layer-dock-transcript',
      'layer-dock-hub',
    ]) {
      expect(tokensRaw, `--${rung} is missing from the scale`).toMatch(
        new RegExp(`--${rung}:\\s*\\d+;`),
      );
    }
  });

  it.each(SHELL_SHEETS)('%s reads the scale instead of writing a number', (_name, css) => {
    // A literal here is how a new surface lands between two layers that were
    // meant to be adjacent — which is exactly how the pinned "Show earlier"
    // control came to paint over the toast band, five times. The sheet may still
    // carry small local numbers INSIDE its own stacking contexts (a dropdown over
    // the body it opens on); what it may not do is name a rung of the shell's
    // scale by value. Those rungs start at 5.
    const declared = declarations(css);
    const literals = [...declared.matchAll(/z-index:\s*(\d+)/g)].map((m) => Number(m[1]));
    expect(literals.filter((n) => n >= 5)).toEqual([]);
  });
});

describe('motion', () => {
  it.each([
    ['PrimaryScreen.css', primaryRaw],
    ['DesktopScreen.css', desktopRaw],
  ])('%s suppresses its self-starting animations under reduced motion', (_name, css) => {
    const declared = declarations(css);
    const query = declared.indexOf('@media (prefers-reduced-motion: reduce)');
    expect(query, 'no reduced-motion block at all').toBeGreaterThanOrEqual(0);
    // A sheet that animates and never mentions reduced motion is the failure
    // here — not the exact list of selectors inside the query, which is layout's
    // business and changes with every element added.
    expect(declared).toMatch(/@keyframes\s+[\w-]+/);
    expect(declared.slice(query)).toMatch(/animation[^;]*:\s*none/);
  });

  it('the kanban board declares nothing that moves on its own', () => {
    // The one thing permitted to animate on a desk shell is the working
    // indication, and this file does not own it — it borrows the status mark.
    const declared = declarations(kanbanRaw);
    expect(declared).not.toMatch(/@keyframes/);
    expect(declared).not.toMatch(/animation:/);
  });
});

describe('the shell breakpoint', () => {
  it('is chosen in JS, and stated nowhere in CSS', () => {
    // The shell is picked by `useIsDesktop`, so a min-width query in the desk
    // sheet would be a second breakpoint that can silently disagree with it.
    expect(declarations(desktopRaw)).not.toMatch(/@media[^{]*min-width/);
    expect(DESKTOP_MIN_WIDTH).toBe(1024);
  });
});
