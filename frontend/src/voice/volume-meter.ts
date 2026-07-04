// Mic input level metering for the dock's volume orb (09 §3 visual): the pure,
// testable math behind the red orb that grows with the user's voice. `computeRms`
// reduces one Float32 audio block to a single 0..~1 loudness; `toDisplayLevel`
// maps that raw RMS onto the 0..1 range the orb's CSS scale expects (noise-floor
// gate + gain + clamp); and `VolumeSmoother` gives the signal a fast attack /
// slow release so the orb rises promptly on speech but eases back to nothing on
// silence instead of flickering per frame. The live AnalyserNode read + rAF loop
// that feed these sit in `assemblyai-client` / `Dock`, so the tunable math stays
// here where it can be unit-tested off the audio thread (mirrors `pcm-batch`).

/** Root-mean-square of a Float32 audio block — a single 0..~1 loudness value.
 *  An empty block reads as silence. */
export function computeRms(samples: Float32Array): number {
  if (samples.length === 0) {
    return 0;
  }
  let sum = 0;
  for (const sample of samples) {
    sum += sample * sample;
  }
  return Math.sqrt(sum / samples.length);
}

// Below this RMS the input is treated as room noise rather than speech, so the
// orb settles all the way to nothing (the "drops to ~silence → fades away" rule).
const NOISE_FLOOR = 0.008;
// Speech RMS rarely climbs past this; mapping it to a full-size orb keeps normal
// talking inside the visible range without pinning to max on every syllable.
const FULL_SCALE_RMS = 0.25;

/** Map a raw RMS to the 0..1 display level the orb's CSS consumes: gate room
 *  noise to 0, then scale [NOISE_FLOOR, FULL_SCALE_RMS] across [0, 1]. */
export function toDisplayLevel(rms: number): number {
  if (rms <= NOISE_FLOOR) {
    return 0;
  }
  const level = (rms - NOISE_FLOOR) / (FULL_SCALE_RMS - NOISE_FLOOR);
  return Math.max(0, Math.min(1, level));
}

/**
 * One-pole exponential smoother with asymmetric attack/release so the orb feels
 * organic: it rises quickly toward a louder target (attack) but falls slowly
 * back toward silence (release), so the brief dips between syllables don't make
 * it flicker. Coefficients are per-frame, tuned for ~60 fps rAF stepping.
 */
export class VolumeSmoother {
  private value = 0;

  constructor(
    private readonly attack = 0.45,
    private readonly release = 0.12,
  ) {}

  /** Advance one frame toward `target` (0..1) and return the new smoothed level. */
  push(target: number): number {
    const coeff = target > this.value ? this.attack : this.release;
    this.value += (target - this.value) * coeff;
    return this.value;
  }
}
