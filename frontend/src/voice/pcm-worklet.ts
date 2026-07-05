// The mic downsample AudioWorkletProcessor (09 §7): runs off the main thread in
// the AudioWorklet global scope, turning the mic's Float32 frames (at the device
// sample rate, typically 44.1/48 kHz) into 16 kHz mono PCM16 — the frame format
// AssemblyAI's Universal-Streaming socket expects (encoding=pcm_s16le,
// sample_rate=16000). Complete frames are posted to the main thread as a
// transferred ArrayBuffer; `assemblyai-client` forwards each as a binary WS frame.
//
// `assemblyai-client` imports this file with Vite's `?worker&url` suffix, so Vite
// bundles it (rolling in the `pcm-batch` import) and transpiles it to a real,
// self-contained `.js` asset, then hands `addModule` that asset's URL. A plain
// `?url` import would NOT work: Vite would emit the raw `.ts` source as a
// `data:video/mp2t` URL (bare imports + TS syntax + a non-JS MIME), which
// `addModule` rejects with "Unable to load a worklet's module". It runs in
// `AudioWorkletGlobalScope`, not the DOM, so that scope's globals
// (`AudioWorkletProcessor`, `registerProcessor`, `sampleRate`) are not in the
// DOM lib and we declare exactly the slice we use rather than reaching for `any`
// (02 §4b: no `any`, no `as`).
//
// The decimate-and-batch logic lives in the pure `pcm-batch` module so it can be
// unit-tested off this thread; this file is just the AudioWorklet shell.
import { PcmFramer } from '@/voice/pcm-batch';

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

class PCM16DownsampleProcessor extends AudioWorkletProcessor {
  // Decimates to 16 kHz and batches render quanta into ~100 ms frames — a bare
  // quantum (~2.6 ms) would trip AssemblyAI's 50–1000 ms frame-duration rule
  // (error 3007). The batching/clamping logic is the unit-tested `PcmFramer`.
  private readonly framer = new PcmFramer();

  override process(inputs: Float32Array[][]): boolean {
    const input = inputs[0];
    if (input === undefined) {
      return true;
    }
    const channel = input[0];
    if (channel === undefined || channel.length === 0) {
      return true;
    }
    for (const frame of this.framer.push(channel, sampleRate)) {
      // Transfer each complete frame's buffer to the main thread; the framer
      // returns fresh copies, so it retains ownership of its accumulator.
      this.port.postMessage(frame.buffer, [frame.buffer]);
    }
    return true;
  }
}

registerProcessor(PCM_WORKLET_NAME, PCM16DownsampleProcessor);
