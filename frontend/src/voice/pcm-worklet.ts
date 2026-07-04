// The mic downsample AudioWorkletProcessor (09 §7): runs off the main thread in
// the AudioWorklet global scope, turning the mic's Float32 frames (at the device
// sample rate, typically 44.1/48 kHz) into 16 kHz mono PCM16 — the frame format
// AssemblyAI's Universal-Streaming socket expects (encoding=pcm_s16le,
// sample_rate=16000). Each processed block is posted to the main thread as a
// transferred ArrayBuffer; `assemblyai-client` forwards it as a binary WS frame.
//
// This module is loaded via `addModule(new URL('./pcm-worklet.ts', ...))`, so it
// executes in `AudioWorkletGlobalScope`, not the DOM. That scope's globals
// (`AudioWorkletProcessor`, `registerProcessor`, `sampleRate`) are not in the
// DOM lib, so we declare exactly the slice we use rather than reaching for `any`
// (02 §4b: no `any`, no `as`).

/** The device's audio-context sample rate, provided by AudioWorkletGlobalScope. */
declare const sampleRate: number;

/** The base processor class the worklet scope provides. `process` is supplied by
 *  the subclass, so it is abstract here. */
declare abstract class AudioWorkletProcessor {
  readonly port: MessagePort;
  abstract process(inputs: Float32Array[][]): boolean;
}

/** Registers a processor class under `name` for the main thread to instantiate. */
declare function registerProcessor(
  name: string,
  processorCtor: new () => AudioWorkletProcessor,
): void;

/** The single processor name both sides agree on. */
export const PCM_WORKLET_NAME = 'pcm16-downsample';

const TARGET_SAMPLE_RATE = 16000;
const INT16_MAX = 0x7fff;
const INT16_MIN = 0x8000;

class PCM16DownsampleProcessor extends AudioWorkletProcessor {
  override process(inputs: Float32Array[][]): boolean {
    const input = inputs[0];
    if (input === undefined) {
      return true;
    }
    const channel = input[0];
    if (channel === undefined || channel.length === 0) {
      return true;
    }

    // Nearest-neighbour linear decimation to 16 kHz. The ratio is > 1 for the
    // usual 44.1/48 kHz capture rates; if capture is already <= 16 kHz we pass
    // frames through 1:1.
    const ratio = sampleRate > TARGET_SAMPLE_RATE ? sampleRate / TARGET_SAMPLE_RATE : 1;
    const outLength = Math.max(1, Math.floor(channel.length / ratio));
    const pcm = new Int16Array(outLength);
    for (let i = 0; i < outLength; i += 1) {
      const sample = channel[Math.floor(i * ratio)] ?? 0;
      const clamped = Math.max(-1, Math.min(1, sample));
      pcm[i] = clamped < 0 ? clamped * INT16_MIN : clamped * INT16_MAX;
    }

    this.port.postMessage(pcm.buffer, [pcm.buffer]);
    return true;
  }
}

registerProcessor(PCM_WORKLET_NAME, PCM16DownsampleProcessor);
