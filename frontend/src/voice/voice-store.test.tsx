// Voice-store tests (09 §3): the store owns the I/O the pure `commit-machine`
// avoids, so the mic/socket lifecycle is driven through fakes here — a stub
// `startVoiceStream` whose `stop` spy stands in for tearing the mic down, and
// whose captured `onEvent` lets a test push provider events through the seam.
// These pin the core rules: the mic never STARTS on its own — no start on mount,
// none on foreground — it opens ONLY on an explicit tap (`resume`). A send
// RELEASES the mic: both the send button and an auto-send (end-of-turn) tear the
// stream down and drop to Paused, so the play-and-record audio session ends and
// other apps' audio can resume (09 §3a). The user taps to talk for the next report.
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

// Post-turn-end grace window the store holds an armed final for before it POSTs
// (mirrors voice-store's COMMIT_DELAY_MS). Advancing fake timers past it fires
// the auto-send.
const GRACE_MS = 5_000;

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

  it('exposes connecting during the setup window and clears it once the socket opens', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    // Off the resting state there is nothing connecting.
    expect(result.current.connecting).toBe(false);
    // Tapping on starts the stream but the socket isn't live yet: connecting is
    // true so the dock shows a spinner around the mic (09 §3).
    act(() => {
      result.current.resume();
    });
    expect(result.current.connecting).toBe(true);
    // The provider's open confirms recording started -> spinner clears.
    act(() => {
      fireProviderEvent({ kind: 'open' });
    });
    expect(result.current.connecting).toBe(false);
  });

  it('sending releases the mic — it tears the stream down and drops to Paused', async () => {
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
    // ...then the user taps send: the commit effect POSTs and releases the mic —
    // the stream is torn down (ending the play-and-record audio session so other
    // apps' audio can resume, 09 §3a) and the machine drops to Paused. No fresh
    // socket is opened; the user taps to talk again for the next report.
    act(() => {
      result.current.sendNow();
    });
    expect(live.stop).toHaveBeenCalled();
    expect(result.current.micState).toBe('paused');
    // No replacement stream: only the initial tap's stream, now stopped.
    expect(startVoiceStream).toHaveBeenCalledTimes(1);
    // Flush the pending POST so its follow-up dispatch settles inside act.
    await act(async () => {
      await Promise.resolve();
    });
  });

  it('an auto-send (end-of-turn) releases the mic — the stream is stopped and drops to Paused', async () => {
    vi.useFakeTimers();
    try {
      const { result } = renderHook(() => useVoice(), { wrapper });
      // One tap starts the session.
      act(() => {
        result.current.resume();
      });
      const live = liveStream();
      // A turn completes: socket opens, then an end-of-turn final lands.
      act(() => {
        fireProviderEvent({ kind: 'open' });
      });
      act(() => {
        fireProviderEvent({ kind: 'final', text: 'Move it to done.' });
      });
      // The post-turn-end grace window closes -> the utterance auto-sends (POST)
      // and the mic is released: the stream is torn down (ending the audio session
      // so music can resume, 09 §3a) and the machine drops to Paused.
      await act(async () => {
        vi.advanceTimersByTime(GRACE_MS);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(live.stop).toHaveBeenCalled();
      expect(startVoiceStream).toHaveBeenCalledTimes(1);
      expect(result.current.micState).toBe('paused');
    } finally {
      vi.useRealTimers();
    }
  });

  it('the "+10" control pushes the auto-send deadline out — it fires only after the extension', async () => {
    vi.useFakeTimers();
    try {
      const { result } = renderHook(() => useVoice(), { wrapper });
      act(() => {
        result.current.resume();
      });
      act(() => {
        fireProviderEvent({ kind: 'open' });
      });
      act(() => {
        fireProviderEvent({ kind: 'final', text: 'Move it to done.' });
      });
      // Armed: the transcript is settled and the grace window is counting down.
      expect(result.current.countingDown).toBe(true);
      expect(result.current.settledText).toBe('Move it to done.');

      // Part-way through the 5s window, tap "+10" to extend the deadline by 10s.
      act(() => {
        vi.advanceTimersByTime(2_000);
      });
      act(() => {
        result.current.delaySend();
      });

      // The ORIGINAL 5s deadline now passes (advance well past it): without the
      // extension the send would have fired here, but it hasn't — still counting.
      await act(async () => {
        vi.advanceTimersByTime(4_000);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(result.current.countingDown).toBe(true);
      expect(result.current.settledText).toBe('Move it to done.');

      // Past the extended deadline (armed at 0 → 5s, +10s at 2s → fires at 15s):
      // the utterance auto-sends and the transcript clears back to idle.
      await act(async () => {
        vi.advanceTimersByTime(9_000);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(result.current.countingDown).toBe(false);
      expect(result.current.settledText).toBe('');
    } finally {
      vi.useRealTimers();
    }
  });

  it('`sendImminent` reveals the "+10" control only in the final stretch — a tap withdraws it until it nears again', async () => {
    vi.useFakeTimers();
    try {
      const { result } = renderHook(() => useVoice(), { wrapper });
      act(() => {
        result.current.resume();
      });
      act(() => {
        fireProviderEvent({ kind: 'open' });
      });
      act(() => {
        fireProviderEvent({ kind: 'final', text: 'Move it to done.' });
      });
      // The base 5s window is entirely inside the reveal stretch, so the control
      // surfaces the moment the send arms.
      expect(result.current.countingDown).toBe(true);
      expect(result.current.sendImminent).toBe(true);

      // Tap "+10" 2s in: the deadline jumps to 15s out — well past the final
      // stretch — so the control withdraws even though the send is still armed.
      act(() => {
        vi.advanceTimersByTime(2_000);
      });
      act(() => {
        result.current.delaySend();
      });
      expect(result.current.countingDown).toBe(true);
      expect(result.current.sendImminent).toBe(false);

      // Run the countdown back down to 5s before the extended deadline (fires at
      // 15s; advance to 10s): the control re-surfaces.
      act(() => {
        vi.advanceTimersByTime(8_000);
      });
      expect(result.current.sendImminent).toBe(true);

      // Past the extended deadline the send fires and the control is gone.
      await act(async () => {
        vi.advanceTimersByTime(5_000);
        await Promise.resolve();
        await Promise.resolve();
      });
      expect(result.current.countingDown).toBe(false);
      expect(result.current.sendImminent).toBe(false);
      expect(result.current.settledText).toBe('');
    } finally {
      vi.useRealTimers();
    }
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
