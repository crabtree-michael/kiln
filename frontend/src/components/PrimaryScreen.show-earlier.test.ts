// "Show earlier" is anchored to the foot of the feed region — in every state,
// not just the empty one (08 D2‴).
//
// The control used to be nothing but the last thing in the backlog, in normal
// flow. That reads as "the foot of the feed" only when the feed is EMPTY, where
// `[data-role='feed-empty']` is `flex: 1` and incidentally pushes it down; with
// any cards at all it sat at the end of the scrolled content, out of sight until
// the user scrolled to the bottom. Two declarations make the placement
// unconditional, and jsdom does no layout, so asserting the CSS as a string is
// the only thing in the gate that can see either of them (the `?raw` technique
// used by TicketDetail.safe-area.test.ts). The DOM half — the control being last
// in the region — is pinned in PrimaryScreenView.test.tsx; without the rules
// below that order buys nothing.
import cssRaw from './PrimaryScreen.css?raw';

const css: string = cssRaw;

/** Isolate a rule's declaration block by its selector, so an assertion is about
 * that rule rather than about the file containing the string somewhere. */
function ruleBody(selector: string): string {
  const start = css.indexOf(selector);
  expect(start, `selector not found: ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const close = css.indexOf('}', open);
  return css.slice(open + 1, close);
}

describe('feed "Show earlier" anchoring', () => {
  it('sits at the foot when the cards fall short of the scrollport', () => {
    // Free space in the backlog column goes ABOVE the control. The bottom margin
    // is 0 on purpose: the sticky offset below is what stands it off the dock,
    // and a margin there would be added to it.
    expect(ruleBody("[data-role='feed-show-earlier'] {")).toMatch(/margin:\s*auto\s+0\s+0/);
  });

  it('has free space to absorb — the backlog is a column that spans the scrollport', () => {
    // `margin-top: auto` only bites inside a flex container, and only if that
    // container is taller than its content. Turning the backlog back into a
    // plain block would silently return the control to the end of the cards.
    const backlog = ruleBody("[data-role='backlog'] {");
    expect(backlog).toMatch(/display:\s*flex/);
    expect(backlog).toMatch(/flex-direction:\s*column/);
    expect(backlog).toMatch(/flex:\s*1/);
    // ...and the wrapper it grows inside never shrinks below the feed's height,
    // which is what guarantees the free space exists on a short backlog.
    expect(ruleBody("[data-role='feed-scroll'] {")).toMatch(/flex:\s*1 0 auto/);
  });

  it('stays at the foot once the cards overflow, at every scroll offset', () => {
    const body = ruleBody("[data-role='feed-show-earlier'] {");
    expect(body).toMatch(/position:\s*sticky/);
    // In flow, so at the very bottom of the scroll it settles into its natural
    // slot and the last card is never trapped underneath it.
    expect(body).not.toMatch(/position:\s*(fixed|absolute)/);
  });

  it('pins flush — the overlay clearance is the feed’s padding, not this offset', () => {
    // A sticky box is clamped to its containing block, and the backlog's bottom
    // edge is the feed's content box — already held clear of the transcript and
    // the toast band by the region's `padding-bottom`. So the offset is 0 and the
    // control rests on top of that reserve. Restating the vars here would double
    // the clearance: a sliver of the next card stranded under the control at
    // rest, and the control floating a transcript's height off the dock while
    // someone is speaking.
    const bottom = /bottom:\s*([^;]+);/.exec(ruleBody("[data-role='feed-show-earlier'] {"))?.[1];
    expect(bottom?.trim()).toBe('0');

    // ...and the reserve it rests on is the one that tracks those two layers, so
    // deleting it from the feed would take the clearance with it.
    const padding = ruleBody("[data-role='feed'] {");
    expect(padding).toMatch(/padding:[^;]*var\(--dock-overlay-height,\s*0px\)/);
    expect(padding).toMatch(/padding:[^;]*var\(--feed-bottom-inset,\s*0px\)/);
  });

  it('holds its place under a toast band — the band overlays it, it is not pushed up', () => {
    // The reserve above still grows with the band (the assertion above), because
    // the newest card must stay scrollable clear of it. What changed is that the
    // control no longer RIDES that growth: it is translated back down by the
    // band's own height, so it lands where it sits with no band at all and the
    // band — opaque, full-width, in the dock layer — simply covers it. Paint
    // only: nothing reflows, no card moves and no scroll offset shifts as toasts
    // come and go.
    //
    // The reserve MINUS the row's resting height, not the whole reserve: an
    // empty activity row is `--activity-rest-gap` tall (the gap that floats the
    // thinking chip off the dock), and that much is already under the control
    // with no toast in sight. Giving it back too settles the control 12px lower
    // under a toast than it sits without one — the same bug as the lift, in the
    // other direction. Both readers of that gap are pinned below.
    const drop = ruleBody(
      "[data-role='primary-screen']:has([data-role='toast-stack']) [data-role='feed-show-earlier'],",
    );
    expect(drop).toMatch(
      /--show-earlier-drop:\s*calc\(var\(--feed-bottom-inset,\s*0px\)\s*-\s*var\(--activity-rest-gap\)\)/,
    );
    expect(ruleBody("[data-role='activity-row']:not(:has([data-role='toast-stack'])) {")).toMatch(
      /padding-bottom:\s*var\(--activity-rest-gap\)/,
    );
    // Layout is untouched by the drop — a margin or a `bottom` here would move
    // the sticky box itself and take the reserve (and the cards in it) with it.
    expect(drop).not.toMatch(/^\s*(margin|padding|bottom|top)[\w-]*\s*:/m);

    const body = ruleBody("[data-role='feed-show-earlier'] {");
    expect(body).toMatch(/transform:\s*translateY\(calc\(var\(--show-earlier-drop,\s*0px\)/);
  });

  it('only drops when the band is really there — the thinking chip still lifts it', () => {
    // The gate is the whole reason the drop is a separate rule. The reserve's
    // other occupant is the "Kiln is thinking…" chip: narrow, centred, floating,
    // with no fill to hide anything behind it. Dropping through THAT would land
    // the chip on the control's label. So the drop is conditional on a toast
    // stack being on screen; thinking alone leaves `--show-earlier-drop` unset
    // and the control lifts clear of the chip exactly as before. So the base
    // rule may only READ the var — giving it a value there would drop the
    // control through the reserve in every state, chip included.
    expect(ruleBody("[data-role='feed-show-earlier'] {")).not.toMatch(/--show-earlier-drop:/);
  });

  it('holds the DESK’s control still too, off the same rule', () => {
    // The desk grew the same reserve (`--feed-bottom-inset` reaches
    // `[data-role='desktop-screen']`, and `[data-role='desktop-feed']` spends it
    // — DesktopScreen.css) and with it the same lift. One mechanism, one
    // declaration: a second copy in the desktop sheet is how the two shells
    // would drift apart, so the rule names both roots and this pins that.
    expect(css).toMatch(
      /\[data-role='desktop-screen']:has\(\[data-role='toast-stack']\)\s+\[data-role='feed-show-earlier']\s*\{/,
    );
    // Both roots also carry the gap the drop subtracts — an unset var there
    // would make the whole `calc()` invalid and silently drop nothing.
    expect(ruleBody("[data-role='primary-screen'],")).toMatch(/--activity-rest-gap:\s*12px/);
    expect(css).toMatch(/\[data-role='desktop-screen']\s*\{\s*--activity-rest-gap/);
  });

  it('presses without undoing the drop — one transform, two terms', () => {
    // A `transform` of its own on `:active` would overwrite the drop, snapping
    // the control up out of the band at the moment of the tap. The press is a
    // term in the one transform instead.
    const active = ruleBody("[data-role='feed-show-earlier']:active {");
    expect(active).toMatch(/--show-earlier-press:\s*1px/);
    expect(active).not.toMatch(/transform/);
    expect(ruleBody("[data-role='feed-show-earlier'] {")).toMatch(
      /transform:\s*translateY\(calc\([^)]*var\(--show-earlier-drop,\s*0px\)\s*\+\s*var\(--show-earlier-press,\s*0px\)\)\)/,
    );
  });

  it('carries its fill BELOW itself, so the reserve underneath shows no cards', () => {
    // `bottom: 0` pins the control to the feed's content box, but the reserve
    // beneath it (the region's `padding-bottom`) is still scrollable area that
    // cards pass through — so without this the control ends the feed with a line
    // of the next card stranded under it. The feed's own `overflow-y: auto`
    // clips the skirt to the region's bottom edge, so it covers exactly the
    // reserve and never reaches the dock; it only has to be TALLER than the part
    // of that reserve nothing else paints over.
    //
    // FOUR lengths, not three: offset THEN spread. An outer shadow is the border
    // box moved down and then clipped back out of itself, so the pure 40px offset
    // this started as began 2px below a 38px-tall control — and that 2px of
    // scrolled card, immediately above the dock's separator, is precisely what the
    // skirt exists to hide. Splitting the same 40px reach into 20 offset + 20
    // spread lands the rect's top edge on the control's own at any height.
    const body = ruleBody("[data-role='feed-show-earlier'] {");
    expect(body).toMatch(
      /box-shadow:\s*0\s+var\(--space-5\)\s+0\s+var\(--space-5\)\s+var\(--surface-page\)/,
    );
  });

  it('never lifts itself over the dock layer, whose overlays stand in that reserve', () => {
    // The bug this pins: the skirt painted a page-tone blob across the toast
    // band's separator hairline and the first line of every toast, because the
    // control carried a `z-index: 1`. The band and the transcript are anchored
    // ABOVE the dock (`bottom: 100%`), so they stand inside the very reserve the
    // skirt covers, and they are the layer that owns it.
    const control = ruleBody("[data-role='feed-show-earlier'] {");
    expect(control).not.toMatch(/z-index/);

    // The dock region can't answer this from its own side without help: its
    // keyboard-lift transform makes it a stacking context, sealing the band's
    // `z-index: 6` inside — so the whole layer is lifted by one number here.
    // Above the feed's pinned control, below the header's 5 (the status /
    // project / notification dropdowns still drop over the dock).
    const dock = ruleBody("[data-role='dock-region'] {");
    expect(dock).toMatch(/z-index:\s*1/);
    expect(ruleBody("[data-role='feed-header'] {")).toMatch(/z-index:\s*5/);
  });

  it('is opaque, because cards now scroll underneath it', () => {
    // Transparent was fine while it was the last thing in the flow with nothing
    // ever passing behind it. Pinned, it needs the tone the cards would be
    // scrolling under anyway — the same fill the toast band takes, so it reads
    // as the feed's own bottom rather than a floating panel.
    const body = ruleBody("[data-role='feed-show-earlier'] {");
    expect(body).toMatch(/background:\s*var\(--surface-page\)/);
    expect(body).not.toMatch(/background:\s*transparent/);
  });
});
