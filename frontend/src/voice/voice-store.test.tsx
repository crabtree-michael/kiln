// Voice-store tests (09 §3): the store owns the I/O the pure `commit-machine`
// avoids, so the mic/socket lifecycle is driven through fakes here — a stub
// `startVoiceStream` whose `stop` spy stands in for tearing the mic down, and
// whose captured `onEvent` lets a test push provider events through the seam.
// These pin the core rule: the mic never starts on its own — no start on mount,
// none on foreground, none after a send — it opens ONLY on an explicit tap
// (`resume`), and any send tears the socket back down.
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { VoiceProvider } from '@/voice/voice-store';
import { useVoice } from '@/voice/voice-context';
import type { VoiceStream, StartVoiceStreamOptions } from '@/voice/assemblyai-client';
import type { VoiceProviderEvent } from '@/voice/commit-machine';

// Each `startVoiceStream` call yields a fresh stub with its own `stop` spy; the
// live one is always the last created (the store nulls its ref after a stop).
// The most recent call's options are captured so a test can drive the provider
// event seam (`onEvent`) the real socket would feed.
const streams: VoiceStream[] = [];
let lastOptions: StartVoiceStreamOptions | undefined;
const startVoiceStream = vi.fn((options: StartVoiceStreamOptions): VoiceStream => {
  lastOptions = options;
  const stream: VoiceStream = { stop: vi.fn(), getLevel: vi.fn(() => 0) };
  streams.push(stream);
  return stream;
});

vi.mock('@/voice/assemblyai-client', () => ({
  startVoiceStream: (options: StartVoiceStreamOptions): VoiceStream => startVoiceStream(options),
}));

vi.mock('@/transport/transport', () => ({
  fetchVoiceToken: vi.fn(() => Promise.resolve({ token: 't', expires_at: '2099-01-01T00:00:00Z' })),
  postMessage: vi.fn(() => Promise.resolve({ message_id: 'm' })),
}));

vi.mock('@/stores/activity-context', () => ({
  useActivityStore: () => ({
    thinking: false,
    toasts: [],
    dismiss: vi.fn(),
    dismissToast: vi.fn(),
  }),
}));

function wrapper({ children }: { children: ReactNode }): ReactNode {
  return <VoiceProvider>{children}</VoiceProvider>;
}

// The most recently started stream — the store nulls its ref after a stop, so
// the live one is always the last created. Throws rather than returning
// `undefined` so callers get a narrowed `VoiceStream` (no non-null assertions).
function liveStream(): VoiceStream {
  const stream = streams.at(-1);
  if (stream === undefined) {
    throw new Error('expected a started voice stream');
  }
  return stream;
}

// Push a provider event through the live stream's captured `onEvent`, the way
// the real AssemblyAI socket would.
function fireProviderEvent(event: VoiceProviderEvent): void {
  if (lastOptions === undefined) {
    throw new Error('no stream started — nothing to feed events to');
  }
  lastOptions.onEvent(event);
}

// Flip document visibility and dispatch the event the store listens on.
function setVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true });
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('VoiceProvider mic activation', () => {
  beforeEach(() => {
    streams.length = 0;
    lastOptions = undefined;
    startVoiceStream.mockClear();
  });

  it('does not start the mic on mount — opens Paused with no stream (tap to talk)', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    expect(result.current.micState).toBe('paused');
    expect(result.current.keyboardMode).toBe(false);
    // The crux: mounting must not open a socket or touch getUserMedia.
    expect(startVoiceStream).not.toHaveBeenCalled();
    expect(streams).toHaveLength(0);
  });

  it('tapping the mic (resume) is the only thing that starts the stream', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    expect(result.current.micState).toBe('listening');
    expect(startVoiceStream).toHaveBeenCalledTimes(1);
  });

  it('sending tears the mic down — it does not keep listening after a send', async () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    const live = liveStream();
    // Speech shows on screen through the provider seam...
    act(() => {
      fireProviderEvent({ kind: 'partial', text: 'ship it' });
    });
    expect(result.current.tailText).toBe('ship it');
    // ...then the user taps send: the commit effect POSTs and stops the stream,
    // and the machine drops to Paused rather than opening a fresh socket.
    act(() => {
      result.current.sendNow();
    });
    expect(live.stop).toHaveBeenCalled();
    expect(result.current.micState).toBe('paused');
    // No replacement stream was started — only the one from the initial tap.
    expect(startVoiceStream).toHaveBeenCalledTimes(1);
    // Flush the pending POST so its follow-up dispatch settles inside act.
    await act(async () => {
      await Promise.resolve();
    });
  });

  it('backgrounding stops the mic and returning does NOT restart it', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    const live = liveStream();
    startVoiceStream.mockClear();

    act(() => {
      setVisibility('hidden');
    });
    expect(live.stop).toHaveBeenCalled();
    expect(result.current.micState).toBe('paused');

    // Coming back to the foreground must not silently reopen the mic.
    act(() => {
      setVisibility('visible');
    });
    expect(startVoiceStream).not.toHaveBeenCalled();
    expect(result.current.micState).toBe('paused');
  });

  it('openKeyboard stops the mic: pauses the machine and tears the live stream down', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    const live = liveStream();

    act(() => {
      result.current.openKeyboard();
    });

    // Keyboard mode is up, and the mic is genuinely stopped — the machine sits
    // in the sticky `paused` state and the live stream's teardown ran (getUserMedia
    // tracks released, socket closed), not merely a UI flag flip.
    expect(result.current.keyboardMode).toBe(true);
    expect(result.current.micState).toBe('paused');
    expect(live.stop).toHaveBeenCalledTimes(1);
  });

  it('closeKeyboard drops keyboard mode without starting the mic (stays tap-to-talk)', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    act(() => {
      result.current.openKeyboard();
    });
    startVoiceStream.mockClear();

    act(() => {
      result.current.closeKeyboard();
    });

    // Closing the keyboard is not a tap on the mic, so it must not reopen it: the
    // machine stays Paused and no stream is started.
    expect(result.current.keyboardMode).toBe(false);
    expect(result.current.micState).toBe('paused');
    expect(startVoiceStream).not.toHaveBeenCalled();
  });

  it('unmount tears a live stream down (no mic left listening)', () => {
    const { result, unmount } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.resume();
    });
    const live = liveStream();
    unmount();
    expect(live.stop).toHaveBeenCalled();
  });
});
