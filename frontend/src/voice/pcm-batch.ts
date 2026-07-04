// Pure PCM framing logic for the mic worklet (09 §7), split out of
// `pcm-worklet.ts` so it can be unit-tested off the AudioWorklet thread.
//
// AssemblyAI's Universal-Streaming socket rejects audio frames outside a
// 50–1000 ms duration window (error 3007 "Input Duration Violation"). A single
// AudioWorklet render quantum is 128 samples (~2.6 ms at 16 kHz), far below the
// floor, so frames MUST be batched before they are sent. `PcmFramer` decimates
// each incoming Float32 block to 16 kHz PCM16 and accumulates until it has a
// full ~100 ms frame, returning zero or more complete frames per push.

export const TARGET_SAMPLE_RATE = 16000;

// 100 ms at 16 kHz — comfortably inside AssemblyAI's 50–1000 ms window and still
// low-latency. Each emitted frame is exactly this many Int16 samples.
export const FRAME_SAMPLES = 1600;

const INT16_MAX = 0x7fff;
const INT16_MIN = 0x8000;

/**
 * Stateful accumulator turning device-rate Float32 mic blocks into fixed-size
 * 16 kHz PCM16 frames. `push` returns every complete frame that became ready
 * (usually none, occasionally one), never a partial frame — partial samples are
 * carried over to the next call.
 */
export class PcmFramer {
  private frame = new Int16Array(FRAME_SAMPLES);
  private filled = 0;

  /** Decimate `channel` (sampled at `sourceRate`) to 16 kHz PCM16 and return any
   *  now-complete FRAME_SAMPLES-length frames. */
  push(channel: Float32Array, sourceRate: number): Int16Array[] {
    // Nearest-neighbour decimation to 16 kHz. Ratio > 1 for the usual 44.1/48 kHz
    // capture rates; if capture is already <= 16 kHz, pass through 1:1.
    const ratio = sourceRate > TARGET_SAMPLE_RATE ? sourceRate / TARGET_SAMPLE_RATE : 1;
    const outLength = Math.max(1, Math.floor(channel.length / ratio));
    const frames: Int16Array[] = [];
    for (let i = 0; i < outLength; i += 1) {
      const sample = channel[Math.floor(i * ratio)] ?? 0;
      const clamped = Math.max(-1, Math.min(1, sample));
      this.frame[this.filled] = clamped < 0 ? clamped * INT16_MIN : clamped * INT16_MAX;
      this.filled += 1;
      if (this.filled === FRAME_SAMPLES) {
        frames.push(this.frame.slice());
        this.filled = 0;
      }
    }
    return frames;
  }
}
