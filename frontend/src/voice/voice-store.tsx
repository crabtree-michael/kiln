// Voice store (09 §3–§5, §7): the React glue around the pure commit machine. It
// owns the reducer via `useReducer` and all the I/O the machine deliberately
// avoids — the mic/socket lifecycle (`startVoiceStream`), token fetch + proactive
// refresh, the one-silent-reconnect-then-Retry policy, `visibilitychange`
// background/foreground, and the commit effect that POSTs a finalized utterance
// to `/api/message` (the unchanged 07 §4 seam) and then clears the machine's
// one-tick `commit`. Everything decision-shaped lives in `commit-machine`; this
// file only wires side effects to it. Mirrors the store → context split of the
// 07/08 stores.
import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  type JSX,
  type ReactNode,
} from 'react';
import { fetchVoiceToken, postMessage } from '@/transport/transport';
import { initialVoiceState, voiceReducer, type MicState } from '@/voice/commit-machine';
import { startVoiceStream, type VoiceStream } from '@/voice/assemblyai-client';
import { VoiceStoreContext, type VoiceStoreValue } from '@/voice/voice-context';

export interface VoiceProviderProps {
  children: ReactNode;
}

// Refresh the streaming token this long before its absolute expiry (09 §5): a
// proactive reconnect beats waiting for the socket to fail on an expired token.
const REFRESH_BUFFER_MS = 30_000;

export function VoiceProvider({ children }: VoiceProviderProps): JSX.Element {
  const [state, dispatch] = useReducer(voiceReducer, undefined, initialVoiceState);

  // Render-stable I/O handles (never props for re-render): the live stream, the
  // proactive-refresh timer, whether this failure episode has already used its
  // one silent reconnect (09 §5), and a mirror of `micState` for the
  // visibility handler to read without re-subscribing.
  const streamRef = useRef<VoiceStream | null>(null);
  const refreshTimerRef = useRef<number | null>(null);
  const reconnectedRef = useRef<boolean>(false);
  const micStateRef = useRef<MicState>(state.micState);
  const startStreamRef = useRef<() => void>(() => undefined);

  useEffect(() => {
    micStateRef.current = state.micState;
  }, [state.micState]);

  const stopStream = useCallback((): void => {
    if (streamRef.current !== null) {
      streamRef.current.stop();
      streamRef.current = null;
    }
    if (refreshTimerRef.current !== null) {
      clearTimeout(refreshTimerRef.current);
      refreshTimerRef.current = null;
    }
  }, []);

  const scheduleRefresh = useCallback(
    (expiresAt: string): void => {
      if (refreshTimerRef.current !== null) {
        clearTimeout(refreshTimerRef.current);
      }
      const delay = new Date(expiresAt).getTime() - Date.now() - REFRESH_BUFFER_MS;
      if (!Number.isFinite(delay)) {
        return;
      }
      refreshTimerRef.current = window.setTimeout(
        () => {
          // Proactive token refresh (09 §5): reconnect with a fresh token; the
          // machine state (and any on-screen transcript) is untouched.
          stopStream();
          startStreamRef.current();
        },
        Math.max(0, delay),
      );
    },
    [stopStream],
  );

  const handleProviderError = useCallback((): void => {
    // Socket/token failure (09 §5): one silent reconnect, then Retry.
    stopStream();
    if (reconnectedRef.current) {
      dispatch({ type: 'providerFailed' });
      return;
    }
    reconnectedRef.current = true;
    startStreamRef.current();
  }, [stopStream]);

  const startStream = useCallback((): void => {
    streamRef.current = startVoiceStream({
      getToken: async () => {
        const token = await fetchVoiceToken();
        scheduleRefresh(token.expires_at);
        return token;
      },
      onEvent: (event) => {
        if (event.kind === 'open') {
          // A healthy connection resets the reconnect budget for the next
          // failure episode.
          reconnectedRef.current = false;
          dispatch({ type: 'provider', event });
          return;
        }
        if (event.kind === 'error') {
          handleProviderError();
          return;
        }
        if (event.kind === 'close') {
          // Informational; the store drives reconnect off `error` only.
          return;
        }
        dispatch({ type: 'provider', event });
      },
      onDenied: () => {
        stopStream();
        dispatch({ type: 'denied' });
      },
    });
  }, [scheduleRefresh, stopStream, handleProviderError]);

  useEffect(() => {
    startStreamRef.current = startStream;
  }, [startStream]);

  // Auto-start on mount (09 §3 D3: mic on by default). Tears everything down on
  // unmount.
  useEffect(() => {
    reconnectedRef.current = false;
    startStream();
    return () => {
      stopStream();
    };
  }, [startStream, stopStream]);

  // Commit effect (09 §4): a finalized utterance is POSTed to the unchanged
  // /api/message seam. On success the transcript clears back to idle
  // (`commitConsumed`), ready for the next turn. On failure the finalized text
  // stays on screen (it is already `settledText`) — no modal (07 §8); the user
  // can simply speak again.
  useEffect(() => {
    const pending = state.commit;
    if (pending === undefined) {
      return;
    }
    let cancelled = false;
    // Read via a call so the post-await check keeps its `boolean` type rather
    // than being narrowed to the initial literal (the cleanup below flips it).
    const isCancelled = (): boolean => cancelled;
    void (async () => {
      let sent = true;
      try {
        await postMessage(pending);
      } catch {
        // Keep the finalized text visible; nothing else to do inline.
        sent = false;
      }
      if (!isCancelled()) {
        dispatch({ type: sent ? 'commitConsumed' : 'commitFailed' });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [state.commit]);

  // Foreground-only listening (09 §3): stop on hide, resume the default state on
  // show — unless the user explicitly paused (sticky across background).
  useEffect(() => {
    function handleVisibility(): void {
      if (document.visibilityState === 'hidden') {
        stopStream();
        dispatch({ type: 'background' });
        return;
      }
      dispatch({ type: 'foreground' });
      if (micStateRef.current === 'listening') {
        reconnectedRef.current = false;
        startStream();
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [startStream, stopStream]);

  const pause = useCallback((): void => {
    stopStream();
    dispatch({ type: 'pause' });
  }, [stopStream]);

  const resume = useCallback((): void => {
    reconnectedRef.current = false;
    dispatch({ type: 'resume' });
    startStream();
  }, [startStream]);

  const cancel = useCallback((): void => {
    // The X only discards the un-committed transcript; the mic keeps listening.
    dispatch({ type: 'cancel' });
  }, []);

  const value = useMemo<VoiceStoreValue>(
    () => ({
      micState: state.micState,
      settledText: state.settledText,
      tailText: state.tailText,
      pause,
      resume,
      cancel,
    }),
    [state.micState, state.settledText, state.tailText, pause, resume, cancel],
  );

  return <VoiceStoreContext.Provider value={value}>{children}</VoiceStoreContext.Provider>;
}
