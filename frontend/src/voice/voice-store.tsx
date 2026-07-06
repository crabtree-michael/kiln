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
  useState,
  type JSX,
  type ReactNode,
} from 'react';
import { fetchVoiceToken, postMessage } from '@/transport/transport';
import { initialVoiceState, voiceReducer, type MicState } from '@/voice/commit-machine';
import { startVoiceStream, type VoiceStream } from '@/voice/assemblyai-client';
import { VoiceStoreContext, type VoiceStoreValue } from '@/voice/voice-context';
import { useActivityStore } from '@/stores/activity-context';

export interface VoiceProviderProps {
  children: ReactNode;
}

// Refresh the streaming token this long before its absolute expiry (09 §5): a
// proactive reconnect beats waiting for the socket to fail on an expired token.
const REFRESH_BUFFER_MS = 30_000;

// Post-turn-end grace window (09 §4): hold an armed end-of-turn final this long
// before actually POSTing it, so a mid-thought pause misread as turn-end doesn't
// send. Resumed speech within the window cancels the send. Tune here.
const COMMIT_DELAY_MS = 10_000;

export function VoiceProvider({ children }: VoiceProviderProps): JSX.Element {
  const [state, dispatch] = useReducer(voiceReducer, undefined, initialVoiceState);

  // Keyboard-input mode is a UI-level toggle sitting alongside the voice machine,
  // never woven into it: entering it stops the mic and the two inputs stay
  // separate (a typed message is not merged with a spoken one). Kept as plain
  // React state rather than a new voice-machine action so `commit-machine` stays
  // the pure voice reducer it was built to be.
  const [keyboardMode, setKeyboardMode] = useState(false);

  // Sending a finalized utterance supersedes any toast on the activity row
  // (08 §4); the commit effect calls this to clear it. Held in a ref so the
  // effect can fire purely on `state.commit` without re-POSTing on identity
  // churn (the store is always mounted under an ActivityProvider in 08/09).
  const { dismissToast } = useActivityStore();
  const dismissToastRef = useRef(dismissToast);
  useEffect(() => {
    dismissToastRef.current = dismissToast;
  }, [dismissToast]);

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
          // An *unexpected* socket close is a provider failure, not just noise:
          // AssemblyAI can end a session with a clean close and no preceding
          // `onerror` (an application-level error, or a token that expired before
          // the proactive refresh fired). Left unhandled, the socket dies while
          // `micState` stays at its default `listening` and the mic-driven orb
          // keeps glowing — the dock looks live but no transcript ever lands. Run
          // it through the same one-silent-reconnect-then-Retry recovery as an
          // error (09 §5). A stop()-initiated close never reaches here — the
          // client suppresses the event once stopped.
          handleProviderError();
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

  // Grace-window effect (09 §4): an end-of-turn final arms `pending` rather than
  // committing outright. Hold it COMMIT_DELAY_MS, then dispatch `commitDelayElapsed`
  // to promote it to a real commit. If the user keeps talking, a fresh partial
  // clears `pending` (or a new final re-arms it), which re-runs this effect and the
  // cleanup cancels the timer — so a pause misread as turn-end never sends.
  useEffect(() => {
    if (state.pending === undefined) {
      return;
    }
    const timer = window.setTimeout(() => {
      dispatch({ type: 'commitDelayElapsed' });
    }, COMMIT_DELAY_MS);
    return () => {
      clearTimeout(timer);
    };
  }, [state.pending]);

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
    // Dismiss any toast on the activity row as part of handling this submission
    // (a no-op when the row shows a say or is already clear).
    dismissToastRef.current();
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

  const sendNow = useCallback((): void => {
    // The send button commits whatever transcript is on screen right now, without
    // waiting for an end-of-turn final (09 §4); the commit effect POSTs it. When
    // we're still listening, restart the stream so the words we just sent don't
    // come back in a trailing final and double-post — a fresh socket begins a
    // clean turn. When paused (the "stuck" case) there's no socket to restart.
    dispatch({ type: 'sendNow' });
    if (micStateRef.current === 'listening') {
      reconnectedRef.current = false;
      stopStream();
      startStream();
    }
  }, [stopStream, startStream]);

  const openKeyboard = useCallback((): void => {
    // Switch from the default voice input to typed input. Stop the mic (pause is
    // sticky, so foregrounding won't silently resume it while the field is up) and
    // clear any un-committed transcript, so a spoken and a typed message never
    // overlap in one submission.
    setKeyboardMode(true);
    pause();
    dispatch({ type: 'cancel' });
  }, [pause]);

  const closeKeyboard = useCallback((): void => {
    // Back to the default voice input: drop keyboard mode and resume listening.
    setKeyboardMode(false);
    resume();
  }, [resume]);

  const submitText = useCallback(async (text: string): Promise<boolean> => {
    // Typed input rides the exact same seam as a transcribed utterance — a plain
    // POST to /api/message (07 §4) that lands as a `human.message` — so the brain
    // handles it identically. It deliberately does NOT touch the voice machine's
    // transcript state (keyboard and voice are separate inputs). Mirrors the
    // commit effect's error stance: keep the text on failure (the dock retains the
    // field), no modal.
    const trimmed = text.trim();
    if (trimmed === '') {
      return false;
    }
    // A sent message supersedes any toast on the activity row (08 §4), same as a
    // committed utterance.
    dismissToastRef.current();
    try {
      await postMessage(trimmed);
      return true;
    } catch {
      return false;
    }
  }, []);

  // Live mic loudness for the dock's volume orb (09 §3). Reads the current
  // stream's AnalyserNode each call; 0 while no stream is up (paused/denied/
  // between reconnects) so the orb naturally shrinks away.
  const getLevel = useCallback((): number => streamRef.current?.getLevel() ?? 0, []);

  const value = useMemo<VoiceStoreValue>(
    () => ({
      micState: state.micState,
      settledText: state.settledText,
      tailText: state.tailText,
      pause,
      resume,
      cancel,
      sendNow,
      getLevel,
      keyboardMode,
      openKeyboard,
      closeKeyboard,
      submitText,
    }),
    [
      state.micState,
      state.settledText,
      state.tailText,
      pause,
      resume,
      cancel,
      sendNow,
      getLevel,
      keyboardMode,
      openKeyboard,
      closeKeyboard,
      submitText,
    ],
  );

  return <VoiceStoreContext.Provider value={value}>{children}</VoiceStoreContext.Provider>;
}
