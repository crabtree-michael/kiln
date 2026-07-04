import { describe, it, expect } from 'vitest';
import { computeRms, toDisplayLevel, VolumeSmoother } from '@/voice/volume-meter';

describe('computeRms', () => {
  it('is 0 for silence and for an empty block', () => {
    expect(computeRms(new Float32Array(0))).toBe(0);
    expect(computeRms(new Float32Array(256))).toBe(0);
  });

  it('is the constant magnitude for a DC block, and 1 for full scale', () => {
    expect(computeRms(new Float32Array(64).fill(0.5))).toBeCloseTo(0.5, 6);
    expect(computeRms(new Float32Array(64).fill(1))).toBeCloseTo(1, 6);
  });

  it('rises monotonically with amplitude', () => {
    const quiet = computeRms(new Float32Array(64).fill(0.1));
    const loud = computeRms(new Float32Array(64).fill(0.4));
    expect(loud).toBeGreaterThan(quiet);
  });
});

describe('toDisplayLevel', () => {
  it('gates room noise below the floor to 0', () => {
    expect(toDisplayLevel(0)).toBe(0);
    expect(toDisplayLevel(0.005)).toBe(0);
  });

  it('maps loud speech to a full-size (clamped) orb', () => {
    expect(toDisplayLevel(0.25)).toBeCloseTo(1, 6);
    expect(toDisplayLevel(5)).toBe(1);
  });

  it('is monotonic across the speech range', () => {
    expect(toDisplayLevel(0.15)).toBeGreaterThan(toDisplayLevel(0.05));
  });
});

describe('VolumeSmoother', () => {
  it('starts at 0 and never overshoots a steady target', () => {
    const smoother = new VolumeSmoother();
    let value = 0;
    for (let i = 0; i < 100; i += 1) {
      value = smoother.push(1);
      expect(value).toBeLessThanOrEqual(1);
    }
    expect(value).toBeCloseTo(1, 2);
  });

  it('rises faster than it falls (fast attack, slow release)', () => {
    const attack = new VolumeSmoother();
    const release = new VolumeSmoother();
    // One step up from 0 toward 1...
    const afterAttack = attack.push(1);
    // ...vs one step down from a settled 1 toward 0.
    for (let i = 0; i < 100; i += 1) {
      release.push(1);
    }
    const settled = release.push(1);
    const afterRelease = settled - release.push(0);
    expect(afterAttack).toBeGreaterThan(afterRelease);
  });

  it('decays toward silence when the input goes quiet', () => {
    const smoother = new VolumeSmoother();
    for (let i = 0; i < 100; i += 1) {
      smoother.push(1);
    }
    let value = smoother.push(0);
    for (let i = 0; i < 100; i += 1) {
      value = smoother.push(0);
    }
    expect(value).toBeCloseTo(0, 2);
  });
});
