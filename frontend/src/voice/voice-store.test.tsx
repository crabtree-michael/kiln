// Voice-store tests (09 §3): the store owns the I/O the pure `commit-machine`
// avoids, so the mic/socket lifecycle is driven through fakes here — a stub
// `startVoiceStream` whose `stop` spy stands in for tearing the mic down. This
// pins the keyboard-input seam's contract with the mic: entering keyboard mode
// must stop the mic (not just flip a UI flag), so a spoken and a typed message
// never overlap and the mic never keeps listening behind the field (09 §3, D3).
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { VoiceProvider } from '@/voice/voice-store';
import { useVoice } from '@/voice/voice-context';
import type { VoiceStream, StartVoiceStreamOptions } from '@/voice/assemblyai-client';

// Each `startVoiceStream` call yields a fresh stub with its own `stop` spy; the
// live one is always the last created (the store nulls its ref after a stop).
const streams: VoiceStream[] = [];
const startVoiceStream = vi.fn((_options: StartVoiceStreamOptions): VoiceStream => {
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

describe('VoiceProvider keyboard mode', () => {
  beforeEach(() => {
    streams.length = 0;
    startVoiceStream.mockClear();
  });

  it('mounts listening with the mic stream started (mic on by default, 09 D3)', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    expect(result.current.micState).toBe('listening');
    expect(result.current.keyboardMode).toBe(false);
    expect(startVoiceStream).toHaveBeenCalledTimes(1);
  });

  it('openKeyboard stops the mic: pauses the machine and tears the live stream down', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
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

  it('closeKeyboard resumes the mic: drops keyboard mode and restarts the stream', () => {
    const { result } = renderHook(() => useVoice(), { wrapper });
    act(() => {
      result.current.openKeyboard();
    });
    startVoiceStream.mockClear();

    act(() => {
      result.current.closeKeyboard();
    });

    expect(result.current.keyboardMode).toBe(false);
    expect(result.current.micState).toBe('listening');
    expect(startVoiceStream).toHaveBeenCalledTimes(1);
  });

  it('unmount tears the live stream down (no mic left listening)', () => {
    const { unmount } = renderHook(() => useVoice(), { wrapper });
    const live = liveStream();
    unmount();
    expect(live.stop).toHaveBeenCalled();
  });
});
