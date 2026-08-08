// One clock for every mark that shares a cadence.
//
// The in-progress panel's head and the status marks listed directly under it are
// now the SAME element painted by the SAME declaration — one `status-dot`, one
// `kiln-status-pulse` rule in PrimaryScreen.css. They still drifted, because
// sharing a rule is not sharing a clock: a CSS animation's timeline starts when
// its own element starts animating. The head begins breathing when the panel
// first goes live; each row's mark begins when that ticket is picked up, seconds
// or minutes later. One animation, arbitrary phase per element — so the marks
// crossed in and out of step forever.
//
// That is the worst of the readings this column has had. Marks that differ in
// look are two things; marks that are identical and peak apart are one thing
// visibly failing to agree with itself, and the eye tracks the shimmer between
// them rather than either mark. Two marks reporting one thing have to peak
// together, not merely at the same rate.
//
// There is no CSS for this. `animation-delay` is the only phase control, and the
// offset each element needs depends on when it happened to mount, which the
// stylesheet cannot know. So the phase is pinned here instead: pin an animation's
// `startTime` to the document timeline's origin and its progress becomes
// `timelineTime % duration` — a function of the clock alone, identical for every
// mark on the same duration no matter when it appeared.
//
// WHICH marks share the clock stays a CSS decision, declared by the rule that
// declares the animation (`--pulse-phase: shared`). Listing animation names here
// would restate a motion decision in a second place and go stale the first time
// one is retuned — and it could not pick out the marks that want the sync from
// the ones that don't: the rail's project dot breathes at `--breathe-duration` on
// purpose, alone in its column with nothing to keep time against. The property
// lets the rules that mean "keep time with the others" say so, and lets that one
// stay silent.

/** The opt-in a rule declares to have its animation pinned to the shared clock. */
export const PULSE_PHASE_PROPERTY = '--pulse-phase';
/** Its only meaningful value. */
export const PULSE_PHASE_SHARED = 'shared';

export interface PulsePhaseOptions {
  /** Document to listen on. Defaults to the global document. */
  doc?: Document;
}

/**
 * Installs the shared-cadence clock and returns a teardown function (used by
 * tests; production installs once for the page's lifetime).
 *
 * One delegated listener rather than a hook per mark: `animationstart` bubbles,
 * so a single listener at the document catches every opted-in mark on every
 * surface — the desk's in-progress head and rows, the phone's ticket list, the
 * kanban cards — including ones mounted long after install, and including a mark
 * whose animation is created a second time when its ticket goes from idle back to
 * building. Nothing has to remember to opt in at the call site; the CSS rule
 * already did.
 */
export function installPulsePhase(options: PulsePhaseOptions = {}): () => void {
  const doc = options.doc ?? document;

  const onAnimationStart = (event: Event): void => {
    // Read the name off the event rather than narrowing with `instanceof
    // AnimationEvent`: jsdom defines no such global, so the type guard itself
    // would throw in the one environment this has to stay inert in.
    const animationName = animationNameOf(event);
    if (animationName === null) return;

    const target = event.target;
    // `getAnimations` is the whole mechanism and is absent in jsdom, so a test
    // rendering these components gets the un-pinned CSS behaviour rather than a
    // crash. Under `prefers-reduced-motion` the animations are `none` and this
    // never fires at all.
    if (!(target instanceof Element) || typeof target.getAnimations !== 'function') return;
    if (!sharesPulsePhase(target)) return;

    for (const animation of target.getAnimations({ subtree: false })) {
      // Only the animation this event is about: an element may run others
      // (an arrival fade, a transition) that are one-shot and must keep their
      // own start.
      if (!isNamed(animation, animationName)) continue;
      // Already on the shared clock — a re-entrant event, or a re-render that
      // did not recreate the animation.
      if (animation.startTime === 0) continue;
      try {
        animation.startTime = 0;
      } catch {
        // A timeline that refuses an explicit start time (or an engine that
        // treats a CSS animation's start as read-only) costs us the sync, not
        // the animation — it keeps running on its own phase.
      }
    }
  };

  // Capture phase: `animationstart` bubbles, but capture reaches the mark
  // whatever a handler in between decides to do with propagation.
  //
  // The event fires a frame after the animation is created, so a mark is painted
  // for exactly one frame (~16ms of 2400) at progress 0 before it joins the
  // shared clock. Measured in Chromium, not assumed. That is worth leaving: both
  // keyframe sets put progress 0 at their quiet end (opacity 0.3, a 3px ring), so
  // the one off-phase frame is the mark at its dimmest, on the frame it first
  // appears — it reads as the mark fading up, and the drift this exists to remove
  // is a permanent one. Killing it would mean pinning before first paint, which
  // means a layout effect at every call site: the head, the phone's ticket list,
  // the kanban card, the header menu — each having to remember to opt in, which
  // is the coupling this delegated listener exists to avoid.
  doc.addEventListener('animationstart', onAnimationStart, true);

  return () => {
    doc.removeEventListener('animationstart', onAnimationStart, true);
  };
}

/**
 * Reports whether `el`'s rule opted into the shared clock. Read from computed
 * style rather than a `data-` attribute so the opt-in sits in the stylesheet
 * beside the `animation` it qualifies, where the rest of the motion vocabulary
 * lives — a mark cannot declare the cadence in one file and the phase in another.
 */
export function sharesPulsePhase(el: Element): boolean {
  const view = el.ownerDocument.defaultView;
  if (view === null) return false;
  return (
    view.getComputedStyle(el).getPropertyValue(PULSE_PHASE_PROPERTY).trim() === PULSE_PHASE_SHARED
  );
}

/**
 * The keyframe name an `animationstart` event carries, or null if it carries
 * none. Structural rather than an `instanceof AnimationEvent` narrowing, which
 * would throw outright in jsdom — where the global does not exist and this module
 * is supposed to sit quietly inert.
 */
function animationNameOf(event: Event): string | null {
  if (!('animationName' in event)) return null;
  const name: unknown = event.animationName;
  return typeof name === 'string' && name !== '' ? name : null;
}

/** Whether `animation` is the CSS animation running `name`'s keyframes. */
function isNamed(animation: Animation, name: string): boolean {
  if (!('animationName' in animation)) return false;
  const actual: unknown = animation.animationName;
  return actual === name;
}
