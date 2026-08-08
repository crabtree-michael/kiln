import { describe, it, expect, afterEach } from 'vitest';
import { installPulsePhase, sharesPulsePhase, PULSE_PHASE_PROPERTY } from './pulse-phase';

// jsdom implements neither `AnimationEvent` nor `Element.getAnimations`, so both
// are faked here. That is not a shortcut around the real thing: what this module
// decides is *which* animations get pinned and *what* they get pinned to, and
// both are observable through those two seams. Whether a real engine then paints
// them in step is the browser's contract, not ours.

/** A stand-in for the CSS animation `getAnimations()` hands back. */
interface FakeAnimation {
  animationName: string;
  startTime: number | null;
}

function fakeAnimation(animationName: string, startTime: number | null): FakeAnimation {
  return { animationName, startTime };
}

const cleanups: (() => void)[] = [];

afterEach(() => {
  while (cleanups.length > 0) cleanups.pop()?.();
  document.body.innerHTML = '';
});

function install(): void {
  cleanups.push(installPulsePhase({ doc: document }));
}

/**
 * A mark in the document, carrying the animations it is "running". `shared` is
 * whether its rule opted into the common clock — set inline, since jsdom does not
 * cascade custom properties from stylesheets.
 */
function mark(options: { shared: boolean; animations: FakeAnimation[] }): HTMLElement {
  const el = document.createElement('span');
  if (options.shared) el.style.setProperty(PULSE_PHASE_PROPERTY, 'shared');
  Object.defineProperty(el, 'getAnimations', {
    value: () => options.animations,
    configurable: true,
  });
  document.body.appendChild(el);
  return el;
}

/** Dispatches `animationstart` the way the engine does: on the element, bubbling. */
function startAnimation(el: Element, animationName: string): void {
  const event = new Event('animationstart', { bubbles: true });
  Object.defineProperty(event, 'animationName', { value: animationName });
  el.dispatchEvent(event);
}

describe('sharesPulsePhase', () => {
  it('reads the opt-in off the element, not off a list of animation names', () => {
    // The decision lives in CSS beside the `animation` it qualifies. Nothing in
    // this module names a keyframe: `kiln-breathe` is run by BOTH the desk's
    // in-progress head (on the ticket list's tempo, shared) and the rail's
    // project dot (on its own slower one, alone in its column) — a name list
    // could not tell those two apart.
    expect(sharesPulsePhase(mark({ shared: true, animations: [] }))).toBe(true);
    expect(sharesPulsePhase(mark({ shared: false, animations: [] }))).toBe(false);
  });
});

describe('installPulsePhase', () => {
  it('pins an opted-in mark to the timeline origin', () => {
    install();
    const animation = fakeAnimation('kiln-status-pulse', 8123.5);
    const el = mark({ shared: true, animations: [animation] });

    startAnimation(el, 'kiln-status-pulse');

    expect(animation.startTime).toBe(0);
  });

  it('puts marks that started minutes apart on the same phase', () => {
    // The bug this pins down. The desk's in-progress head and the status marks
    // listed under it already declare the same duration and the same curve, and
    // still drifted: a CSS animation's clock starts when its own element starts
    // animating, so the head (live from the panel's first pass) and a row (live
    // from when that ticket was picked up) ran the same tempo from arbitrary
    // starts and crossed in and out of step forever. Pinned to one origin,
    // progress is `timelineTime % duration` — identical for both, whenever they
    // appeared.
    install();
    const head = fakeAnimation('kiln-breathe', 1_000);
    const rowPickedUpLater = fakeAnimation('kiln-status-pulse', 214_000);

    startAnimation(mark({ shared: true, animations: [head] }), 'kiln-breathe');
    startAnimation(mark({ shared: true, animations: [rowPickedUpLater] }), 'kiln-status-pulse');

    expect(head.startTime).toBe(rowPickedUpLater.startTime);
    expect(head.startTime).toBe(0);
  });

  it('leaves a mark whose rule did not opt in on its own clock', () => {
    // The rail's project dot is the live case: same `kiln-breathe` keyframes at a
    // deliberately slower tempo, in a column with nothing to keep time against.
    install();
    const railDot = fakeAnimation('kiln-breathe', 4_500);

    startAnimation(mark({ shared: false, animations: [railDot] }), 'kiln-breathe');

    expect(railDot.startTime).toBe(4_500);
  });

  it('pins only the animation the event is about', () => {
    // An opted-in element can be running one-shot animations too — an arrival
    // fade, say. Rewinding those to the timeline origin would place their start
    // hours in the past and skip them entirely.
    install();
    const pulse = fakeAnimation('kiln-status-pulse', 9_000);
    const arrive = fakeAnimation('kiln-desktop-arrive', 9_000);

    startAnimation(mark({ shared: true, animations: [pulse, arrive] }), 'kiln-status-pulse');

    expect(pulse.startTime).toBe(0);
    expect(arrive.startTime).toBe(9_000);
  });

  it('re-pins when a mark starts animating a second time', () => {
    // A ticket going idle → building drops the `animation` and creates it anew,
    // on a fresh clock. Delegating one listener at the document rather than
    // syncing once at mount is what covers this.
    install();
    const animation = fakeAnimation('kiln-status-pulse', 0);
    const el = mark({ shared: true, animations: [animation] });

    startAnimation(el, 'kiln-status-pulse');
    animation.startTime = 306_000;
    startAnimation(el, 'kiln-status-pulse');

    expect(animation.startTime).toBe(0);
  });

  it('survives a mark with no getAnimations at all', () => {
    // jsdom, and any engine without the Web Animations API: the marks keep the
    // plain CSS behaviour instead of the page throwing on every animation start.
    install();
    const el = document.createElement('span');
    el.style.setProperty(PULSE_PHASE_PROPERTY, 'shared');
    document.body.appendChild(el);

    expect(() => {
      startAnimation(el, 'kiln-status-pulse');
    }).not.toThrow();
  });

  it('survives an engine that refuses an explicit start time', () => {
    // Losing the sync is the acceptable failure; taking the animation down with
    // it is not.
    install();
    const animation = fakeAnimation('kiln-status-pulse', 7_000);
    Object.defineProperty(animation, 'startTime', {
      get: () => 7_000,
      set: () => {
        throw new Error('read-only');
      },
    });
    const el = mark({ shared: true, animations: [animation] });

    expect(() => {
      startAnimation(el, 'kiln-status-pulse');
    }).not.toThrow();
  });

  it('stops pinning once torn down', () => {
    const teardown = installPulsePhase({ doc: document });
    const animation = fakeAnimation('kiln-status-pulse', 5_000);
    const el = mark({ shared: true, animations: [animation] });

    teardown();
    startAnimation(el, 'kiln-status-pulse');

    expect(animation.startTime).toBe(5_000);
  });
});
