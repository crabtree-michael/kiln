import { describe, it, expect } from 'vitest';
import { PcmFramer, FRAME_SAMPLES, TARGET_SAMPLE_RATE } from '@/voice/pcm-batch';

// Regression cover for the AssemblyAI "Input Duration Violation" (error 3007):
// frames must carry 50–1000 ms of audio, so the worklet must batch render
// quanta (~128 samples) into FRAME_SAMPLES-length frames before sending.

describe('PcmFramer', () => {
  it('never emits a frame before FRAME_SAMPLES accumulate (a render quantum is too small)', () => {
    const framer = new PcmFramer();
    // One 128-sample quantum at 16 kHz -> 128 output samples, well under 1600.
    const emitted = framer.push(new Float32Array(128), TARGET_SAMPLE_RATE);
    expect(emitted).toEqual([]);
  });

  it('emits exactly-FRAME_SAMPLES frames once enough audio has accumulated', () => {
    const framer = new PcmFramer();
    let total = 0;
    // Feed 128-sample quanta at 16 kHz until at least two frames have flushed.
    const frames: Int16Array[] = [];
    for (let i = 0; i < 30; i += 1) {
      frames.push(...framer.push(new Float32Array(128).fill(1), TARGET_SAMPLE_RATE));
    }
    for (const f of frames) {
      expect(f.length).toBe(FRAME_SAMPLES);
      total += 1;
    }
    // 30 quanta * 128 = 3840 samples -> 2 full 1600 frames (with 640 carried over).
    expect(total).toBe(2);
  });

  it("every emitted frame is within AssemblyAI's 50-1000 ms window at 16 kHz", () => {
    const framer = new PcmFramer();
    const frames = framer.push(new Float32Array(FRAME_SAMPLES).fill(0.5), TARGET_SAMPLE_RATE);
    expect(frames).toHaveLength(1);
    const durationMs = (frames[0]!.length / TARGET_SAMPLE_RATE) * 1000;
    expect(durationMs).toBeGreaterThanOrEqual(50);
    expect(durationMs).toBeLessThanOrEqual(1000);
  });

  it('decimates 48 kHz input to 16 kHz (3:1)', () => {
    const framer = new PcmFramer();
    // 4800 input samples at 48 kHz -> 1600 output samples -> exactly one frame.
    const frames = framer.push(new Float32Array(4800).fill(1), 48000);
    expect(frames).toHaveLength(1);
    expect(frames[0]!.length).toBe(FRAME_SAMPLES);
  });

  it('clamps and scales Float32 [-1,1] to full-scale Int16', () => {
    const framer = new PcmFramer();
    const input = new Float32Array(FRAME_SAMPLES);
    input[0] = 1; // +full scale
    input[1] = -1; // -full scale
    input[2] = 2; // over-range, clamps to +1
    input[3] = 0; // silence
    const [frame] = framer.push(input, TARGET_SAMPLE_RATE);
    expect(frame![0]).toBe(0x7fff);
    expect(frame![1]).toBe(-0x8000);
    expect(frame![2]).toBe(0x7fff);
    expect(frame![3]).toBe(0);
  });
});
