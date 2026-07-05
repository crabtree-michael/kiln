// The AssemblyAI Universal-Streaming provider client (09 §7) — the ONLY file
// that knows AssemblyAI's wire protocol. It wires the mic to the socket
// (getUserMedia → AudioContext → the PCM16 downsample worklet → binary WS
// frames) and decodes AssemblyAI's JSON messages back to the neutral
// `VoiceProviderEvent`s the commit machine consumes. Audio never transits our
// backend (09 §2); the socket is opened directly with a backend-minted temp
// token.
//
// The pure `decodeAssemblyMessage` is split out and unit-tested; the socket/mic
// plumbing has no branch worth testing without a real browser, so tests mock at
// the store boundary instead (09 §8).
import pcmWorkletUrl from '@/voice/pcm-worklet.ts?url';
import type { VoiceProviderEvent } from '@/voice/commit-machine';
import type { VoiceToken } from '@/transport/transport';
import { computeRms } from '@/voice/volume-meter';

// The processor name registered inside `pcm-worklet.ts`. Kept as a local literal
// (not imported) because importing that module here would run its top-level
// `registerProcessor` call on the main thread, where that global does not exist.
const PCM_WORKLET_NAME = 'pcm16-downsample';

// AssemblyAI's streaming WebSocket host (09 §2). The client sends binary PCM16
// mono 16 kHz frames and receives Begin/Turn JSON messages.
const WS_BASE = 'wss://streaming.assemblyai.com/v3/ws';

export interface StartVoiceStreamOptions {
  /** Mints a fresh short-lived streaming token (`POST /api/voice/token`). */
  getToken: () => Promise<VoiceToken>;
  /** Receives every decoded provider event (open/partial/final/error/close). */
  onEvent: (event: VoiceProviderEvent) => void;
  /** Mic permission was denied (NotAllowedError) — distinct from a transient
   *  socket error so the store can enter Denied rather than Retry (09 §3, §5). */
  onDenied?: () => void;
}

export interface VoiceStream {
  /** Sends Terminate, closes the socket, and stops the mic + AudioContext. */
  stop: () => void;
  /** Current mic input loudness as a raw RMS (0..~1), sampled live off an
   *  AnalyserNode tapping the mic — the dock's volume orb reads this each frame
   *  (09 §3). Returns 0 before the audio graph is up or after teardown. */
  getLevel: () => number;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

function isNotAllowedError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'NotAllowedError';
}

/**
 * Decodes one AssemblyAI Universal-Streaming message to a neutral provider
 * event (09 §7). `Begin` → open; a `Turn` with `end_of_turn && turn_is_formatted`
 * is the formatted final (→ commit); any other `Turn` with a transcript is a
 * still-forming partial. Anything unrecognised (or non-JSON) → `null`. Pure and
 * exported so it is the unit-test target for the provider protocol.
 */
export function decodeAssemblyMessage(data: string): VoiceProviderEvent | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  if (!isRecord(parsed)) {
    return null;
  }
  if (parsed.type === 'Begin') {
    return { kind: 'open' };
  }
  if (parsed.type === 'Turn') {
    const transcript = parsed.transcript;
    if (typeof transcript !== 'string') {
      return null;
    }
    const isFinal = parsed.end_of_turn === true && parsed.turn_is_formatted === true;
    return isFinal ? { kind: 'final', text: transcript } : { kind: 'partial', text: transcript };
  }
  return null;
}

/**
 * Opens the mic + AssemblyAI socket and streams PCM16 to it, decoding every
 * message through `onEvent`. Returns synchronously with a `stop()`; the async
 * setup (permission, token, audio graph, socket) runs in the background and
 * respects a `stop()` that lands mid-setup. Mic-permission denial routes to
 * `onDenied`; every other failure surfaces as an `error` event so the store's
 * one-reconnect-then-Retry policy (09 §5) can act on it.
 */
export function startVoiceStream(options: StartVoiceStreamOptions): VoiceStream {
  // `stop()` may land during any of the async setup awaits below. `stopped` is
  // read through `isStopped()` so each check sees plain `boolean` — read as a
  // bare local it would be narrowed to its last literal and the mid-setup guards
  // would look like dead code to the type-checker.
  const session = { stopped: false };
  const isStopped = (): boolean => session.stopped;
  let socket: WebSocket | null = null;
  let mediaStream: MediaStream | null = null;
  let audioContext: AudioContext | null = null;
  let workletNode: AudioWorkletNode | null = null;
  let sourceNode: MediaStreamAudioSourceNode | null = null;
  // The metering tap: an AnalyserNode fanned out from the same source that feeds
  // the worklet, plus a reused scratch buffer `getLevel` fills each call. Kept
  // separate from the PCM path so metering never perturbs what is streamed.
  let analyserNode: AnalyserNode | null = null;
  // `getFloatTimeDomainData` requires an `ArrayBuffer`-backed view (not the
  // default `ArrayBufferLike`), so pin the buffer type on the declaration.
  let levelBuffer: Float32Array<ArrayBuffer> | null = null;

  function getLevel(): number {
    if (analyserNode === null || levelBuffer === null) {
      return 0;
    }
    analyserNode.getFloatTimeDomainData(levelBuffer);
    return computeRms(levelBuffer);
  }

  function teardown(): void {
    session.stopped = true;
    if (socket !== null) {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'Terminate' }));
      }
      socket.onmessage = null;
      socket.onerror = null;
      socket.onclose = null;
      socket.close();
      socket = null;
    }
    if (workletNode !== null) {
      workletNode.port.onmessage = null;
      workletNode.disconnect();
      workletNode = null;
    }
    if (analyserNode !== null) {
      analyserNode.disconnect();
      analyserNode = null;
      levelBuffer = null;
    }
    if (sourceNode !== null) {
      sourceNode.disconnect();
      sourceNode = null;
    }
    if (mediaStream !== null) {
      for (const track of mediaStream.getTracks()) {
        track.stop();
      }
      mediaStream = null;
    }
    if (audioContext !== null) {
      void audioContext.close();
      audioContext = null;
    }
  }

  function stopTracks(stream: MediaStream): void {
    for (const track of stream.getTracks()) {
      track.stop();
    }
  }

  async function init(): Promise<void> {
    // Declare a play-and-record audio session *before* opening the mic so iOS
    // lets other apps' audio (Spotify, Apple Music, a podcast) keep playing —
    // ducked — instead of interrupting it the moment we capture (09 §3). Without
    // this, WebKit's default recording session is exclusive and silences the
    // device. Only Safari 16.4+ exposes `navigator.audioSession`; everywhere
    // else it is absent and this is a no-op (Android Chrome already does not
    // hard-stop other audio, so it needs no equivalent). The exact duck-vs-mix
    // outcome is decided by the platform, not us — see the device-test checklist
    // in docs/specs/09. We keep `echoCancellation` on for cleaner STT input; if
    // on-device testing shows it re-forces an exclusive session and still stops
    // music, the fallback is to drop it to `false`.
    if (navigator.audioSession !== undefined) {
      navigator.audioSession.type = 'play-and-record';
    }
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
      });
    } catch (error) {
      if (isNotAllowedError(error)) {
        options.onDenied?.();
      } else {
        options.onEvent({ kind: 'error' });
      }
      return;
    }
    if (isStopped()) {
      stopTracks(stream);
      return;
    }
    mediaStream = stream;

    let token: VoiceToken;
    try {
      token = await options.getToken();
    } catch {
      options.onEvent({ kind: 'error' });
      teardown();
      return;
    }
    if (isStopped()) {
      teardown();
      return;
    }

    try {
      const context = new AudioContext();
      audioContext = context;
      await context.audioWorklet.addModule(pcmWorkletUrl);
      if (isStopped()) {
        teardown();
        return;
      }
      const node = new AudioWorkletNode(context, PCM_WORKLET_NAME);
      workletNode = node;
      const source = context.createMediaStreamSource(stream);
      sourceNode = source;
      source.connect(node);
      // Fan the same source into an AnalyserNode so the dock's volume orb can
      // read live loudness (09 §3). It is a read-only tap — not connected to the
      // worklet or destination, so it never alters the PCM that is streamed.
      const analyser = context.createAnalyser();
      analyser.fftSize = 1024;
      analyserNode = analyser;
      levelBuffer = new Float32Array(analyser.fftSize);
      source.connect(analyser);
      // Intentionally NOT connected to `context.destination` — the user must not
      // hear their own mic echoed back. The worklet's job is only to emit PCM.
      node.port.onmessage = (event: MessageEvent): void => {
        const buffer: unknown = event.data;
        if (
          buffer instanceof ArrayBuffer &&
          socket !== null &&
          socket.readyState === WebSocket.OPEN
        ) {
          socket.send(buffer);
        }
      };
    } catch {
      options.onEvent({ kind: 'error' });
      teardown();
      return;
    }

    const query = new URLSearchParams({
      sample_rate: '16000',
      encoding: 'pcm_s16le',
      format_turns: 'true',
      token: token.token,
    });
    const ws = new WebSocket(`${WS_BASE}?${query.toString()}`);
    ws.binaryType = 'arraybuffer';
    socket = ws;
    ws.onmessage = (event: MessageEvent): void => {
      const raw: unknown = event.data;
      if (typeof raw !== 'string') {
        return;
      }
      const decoded = decodeAssemblyMessage(raw);
      if (decoded !== null) {
        options.onEvent(decoded);
      }
    };
    ws.onerror = (): void => {
      options.onEvent({ kind: 'error' });
    };
    ws.onclose = (): void => {
      if (!isStopped()) {
        options.onEvent({ kind: 'close' });
      }
    };
  }

  void init();

  return { stop: teardown, getLevel };
}
