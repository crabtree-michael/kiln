// The shared status mark, pinned to the detail sheet's badge (the `?raw`
// technique used by DesktopScreen.layout.test.ts and TicketDetail.safe-area.test.ts).
//
// The bug this file exists to prevent: a ticket wearing one colour in a list and
// a different one in its own detail sheet. `building` used to paint the ACCENT
// in every list — the phone's ticket dropdown, the desktop in-progress panel,
// the kanban card — while the sheet called the same ticket ember, so an ordinary
// working ticket read as the one state (blocked) fire is reserved for (13 §4;
// 13 §8.2 gives the working strip "no accent" by name).
//
// jsdom performs no layout and computes no cascade, so the DOM tests next door
// can only assert that the attributes reach the mark, never that the two
// stylesheets agree about what they mean. That agreement is asserted here, in
// tokens: re-tinting either side alone fails this file.
import { describe, it, expect } from 'vitest';
import primaryCssRaw from '@/components/PrimaryScreen.css?raw';
import detailCssRaw from '@/components/TicketDetail.css?raw';

/** Both stylesheets with comments removed — the prose around these rules quotes
 * selectors and tokens, and a parser that reads it would assert about the
 * argument rather than about the CSS. */
function strip(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '');
}

const primaryCss: string = strip(primaryCssRaw);
const detailCss: string = strip(detailCssRaw);

/** Isolates a rule's declaration block by its selector, so an assertion is about
 * that rule rather than about the file containing the string somewhere. */
function ruleBody(css: string, selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

/** The same, for a block that nests blocks of its own (`@keyframes`). */
function nestedRuleBody(css: string, selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  let depth = 0;
  let index = open;
  for (; index < css.length; index += 1) {
    if (css[index] === '{') {
      depth += 1;
    } else if (css[index] === '}') {
      depth -= 1;
      if (depth === 0) {
        break;
      }
    }
  }
  return css.slice(open + 1, index);
}

/** Every declaration block in PrimaryScreen.css whose selector is the shared
 * mark — the mark's whole palette, however it is spelled. */
function statusDotRules(): { selector: string; body: string }[] {
  return primaryCss
    .split('}')
    .map((chunk) => {
      const parts = chunk.split('{');
      return { selector: (parts[0] ?? '').trim(), body: (parts[1] ?? '').trim() };
    })
    .filter((rule) => rule.selector.startsWith("[data-role='status-dot']"));
}

/** The token a declaration points at, e.g. `--warn` from
 * `background: var(--warn);`. Null when that property isn't set in the block. */
function token(body: string, property: string): string | null {
  const match = new RegExp(`${property}:[^;]*var\\((--[a-z-]+)`).exec(body);
  return match?.[1] ?? null;
}

describe('the shared status mark', () => {
  it('wears the sheet’s IN PROGRESS ember, not the accent', () => {
    // The reported bug, exactly: a working ticket's list mark was fire while its
    // own sheet's badge was ember. The sheet is the reference, so this reads the
    // ink out of TicketDetail.css rather than restating `--warn` here — tuning
    // the badge and forgetting the list still fails.
    const badge = ruleBody(
      detailCss,
      "[data-role='ticket-detail-status'][data-state='working'] [data-role='ticket-detail-status-dot'] {",
    );
    const mark = ruleBody(primaryCss, "[data-role='status-dot'][data-state='working'] {");
    expect(token(badge, 'background')).toBe('--warn');
    expect(token(mark, '--dot-ink')).toBe(token(badge, 'background'));
    expect(token(mark, '--dot-ring')).toBe(token(badge, 'box-shadow'));
  });

  it('wears the sheet’s BLOCKED fire, on the state the sheet spends it on', () => {
    const badge = ruleBody(
      detailCss,
      "[data-role='ticket-detail-status'][data-state='blocked'] [data-role='ticket-detail-status-dot'] {",
    );
    const mark = ruleBody(primaryCss, "[data-role='status-dot'][data-state='blocked'] {");
    expect(token(badge, 'background')).toBe('--danger');
    expect(token(mark, '--dot-ink')).toBe(token(badge, 'background'));
    expect(token(mark, '--dot-ring')).toBe(token(badge, 'box-shadow'));
  });

  it('wears the sheet’s DONE glaze', () => {
    // The sheet's badge dot is the done skin by default and is overridden for
    // the two louder states, so the unqualified rule is where its glaze lives.
    const badge = ruleBody(detailCss, "\n[data-role='ticket-detail-status-dot'] {");
    const mark = ruleBody(primaryCss, "[data-role='status-dot'][data-state='done'] {");
    expect(token(badge, 'background')).toBe('--calm');
    expect(token(mark, '--dot-ink')).toBe(token(badge, 'background'));
  });

  it('spends fire on the states that need a person, and on nothing else', () => {
    // The whole vocabulary audited in one place: only `blocked` (the ticket needs
    // a decision) and `errored` (its session has failed and needs a person) may
    // reach for the fire family. A building, starting, idle or stopped session on
    // a healthy ticket must not — that is the regression this catches.
    const fire = statusDotRules().filter((rule) => /var\(--(accent|danger)/.test(rule.body));
    expect(fire.map((rule) => rule.selector)).toEqual([
      "[data-role='status-dot'][data-state='blocked']",
      "[data-role='status-dot'][data-status='errored']",
    ]);
  });

  it('lets the session texture the mark without recolouring it', () => {
    // The split the fix rests on: `data-state` (the ticket) owns the ink,
    // `data-status` (the session) owns the texture. A status rule that set an ink
    // would be a second opinion about the same ticket — `errored` is the one
    // documented exception, and it is asserted above.
    const recoloured = statusDotRules().filter(
      (rule) =>
        rule.selector.includes('data-status') &&
        !rule.selector.includes("data-status='errored'") &&
        token(rule.body, '--dot-ink') !== null,
    );
    expect(recoloured.map((rule) => rule.selector)).toEqual([]);
  });

  it('moves for `building` and for nothing else', () => {
    // Only the actively-worked session breathes (13 §1: one thing on screen is
    // allowed to animate on its own). A second moving mark means neither reads.
    const moving = statusDotRules().filter((rule) => /animation:\s*kiln-/.test(rule.body));
    expect(moving.map((rule) => rule.selector)).toEqual([
      "[data-role='status-dot'][data-status='building']",
    ]);
  });

  it('breathes in whatever ink it landed on', () => {
    // The pulse is shared with `feed-empty-pulse`, which names no ticket and
    // keeps the accent — so the keyframe reads the mark's own ring variable with
    // the accent as its fallback. Hard-coding accent stops here would put fire
    // back on every building ticket through the animation alone, with the static
    // rules above still looking correct.
    const frames = nestedRuleBody(primaryCss, '@keyframes kiln-status-pulse {');
    expect(frames).toContain('var(--dot-ring, var(--accent-soft))');
    expect(frames).toContain('var(--dot-ring-far, var(--accent-faint))');
  });
});
