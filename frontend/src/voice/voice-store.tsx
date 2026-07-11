// Voice store (09 §3–§5, §7): the React glue around the pure commit machine. It
// owns the reducer via `useReducer` and all the I/O the machine deliberately
// avoids — the mic/socket lifecycle (`startVoiceStream`), token fetch + proactive
// refresh, the one-silent-reconnect-then-Retry policy, `visibilitychange`
// stop-on-background, and the commit effect that POSTs a finalized utterance
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
import { initialVoiceState, voiceReducer } from '@/voice/commit-machine';
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
const COMMIT_DELAY_MS = 5_000;

// How much the "+10" control pushes the auto-send out per tap (09 §4): a user who
// isn't ready when the countdown is about to fire taps it to buy another 10s. It
// extends the CURRENT deadline (additive), so repeated taps stack.
const SEND_DELAY_EXTENSION_MS = 10_000;

// The "+10" control is offered only in this final stretch before the armed send
// fires (09 §4). It surfaces as the deadline draws near — not for the whole window
// — so a "+10" tap that pushes the deadline out past this stretch withdraws the
// control until it counts back down into it. Equal to COMMIT_DELAY_MS, so the base
// (un-extended) window is entirely inside it and the control shows the moment a
// send arms.
const DELAY_REVEAL_WINDOW_MS = 5_000;

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
  // proactive-refresh timer, and whether this failure episode has already used
  // its one silent reconnect (09 §5).
  const streamRef = useRef<VoiceStream | null>(null);
  const refreshTimerRef = useRef<number | null>(null);
  const reconnectedRef = useRef<boolean>(false);
  const startStreamRef = useRef<() => void>(() => undefined);
  // The single grace-window timer and the absolute deadline it fires at (09 §4).
  // The deadline (not a fixed duration) is what "+10" extends, so the timer can be
  // rescheduled further out without losing time already elapsed. `null` deadline =
  // no send armed.
  const graceTimerRef = useRef<number | null>(null);
  const graceDeadlineRef = useRef<number | null>(null);
  // The reveal timer that flips `sendImminent` on when the countdown enters its
  // final DELAY_REVEAL_WINDOW_MS — the only stretch the "+10" control is offered
  // in. Rescheduled alongside the grace timer on every arm/extend.
  const revealTimerRef = useRef<number | null>(null);
  // Whether the armed auto-send is within its final reveal stretch — drives the
  // dock's "+10" control visibility. Store state (not the pure machine's): it is a
  // function of the wall-clock deadline the store owns, not of transcript state.
  const [sendImminent, setSendImminent] = useState(false);

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

  // The mic never starts on its own — no start on mount. It opens only when the
  // user taps the mic control (`resume`). This effect exists solely to tear the
  // stream down on unmount, so a mic the user did start never outlives the view.
  useEffect(() => {
    return () => {
      stopStream();
    };
  }, [stopStream]);

  const clearGraceTimer = useCallback((): void => {
    if (graceTimerRef.current !== null) {
      clearTimeout(graceTimerRef.current);
      graceTimerRef.current = null;
    }
    if (revealTimerRef.current !== null) {
      clearTimeout(revealTimerRef.current);
      revealTimerRef.current = null;
    }
    // Any disarm/reschedule starts from "control hidden"; `armGraceTimer` flips it
    // back on synchronously when the deadline is already inside the reveal stretch.
    setSendImminent(false);
  }, []);

  // (Re)schedule the grace-window timer (and the reveal timer behind the "+10"
  // control) to the current deadline (09 §4). Called on arm and again on every
  // "+10" extension, so both always reflect the latest deadline. The grace timer
  // fires at the deadline to promote the armed send to a real commit; the reveal
  // timer flips `sendImminent` on once the deadline is within DELAY_REVEAL_WINDOW_MS.
  // A no-op when nothing is armed.
  const armGraceTimer = useCallback((): void => {
    clearGraceTimer();
    const deadline = graceDeadlineRef.current;
    if (deadline === null) {
      return;
    }
    const remaining = Math.max(0, deadline - Date.now());
    graceTimerRef.current = window.setTimeout(() => {
      graceDeadlineRef.current = null;
      setSendImminent(false);
      dispatch({ type: 'commitDelayElapsed' });
    }, remaining);
    // Offer the "+10" control only in the final stretch: reveal now if the deadline
    // is already that close (the base window, or a run-down extension), else arm a
    // timer to reveal it when the countdown reaches the stretch. `clearGraceTimer`
    // above already left it hidden, so an extension past the stretch withdraws it.
    if (remaining <= DELAY_REVEAL_WINDOW_MS) {
      setSendImminent(true);
    } else {
      revealTimerRef.current = window.setTimeout(() => {
        setSendImminent(true);
      }, remaining - DELAY_REVEAL_WINDOW_MS);
    }
  }, [clearGraceTimer]);

  // Grace-window effect (09 §4): an end-of-turn final arms `pending` rather than
  // committing outright. Set a deadline COMMIT_DELAY_MS out and hold the send until
  // it passes, then `commitDelayElapsed` promotes it to a real commit. If the user
  // keeps talking, a fresh partial clears `pending` (or a new final re-arms it),
  // which re-runs this effect and the cleanup cancels the timer — so a pause
  // misread as turn-end never sends. `delaySend` pushes the deadline out mid-window.
  useEffect(() => {
    if (state.pending === undefined) {
      graceDeadlineRef.current = null;
      clearGraceTimer();
      return;
    }
    graceDeadlineRef.current = Date.now() + COMMIT_DELAY_MS;
    armGraceTimer();
    return () => {
      graceDeadlineRef.current = null;
      clearGraceTimer();
    };
  }, [state.pending, armGraceTimer, clearGraceTimer]);

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
    // A send RELEASES the mic (the machine drops to Paused): tear the stream down
    // so getUserMedia stops and the play-and-record audio session goes inactive.
    // That is the only signal iOS acts on to resume the other app's audio
    // (music/podcast) our capture was ducking/holding — best effort (09 §3a). The
    // session never survives a send now, so there's no reopen and no trailing-final
    // double-post to guard against.
    stopStream();
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
  }, [state.commit, stopStream]);

  // Leaving the app stops the mic (09 §3): close the socket on hide and drop a
  // live listen to Paused. Returning does NOT reopen it — the mic only ever
  // starts on an explicit tap, so there's nothing to do on show.
  useEffect(() => {
    function handleVisibility(): void {
      if (document.visibilityState === 'hidden') {
        stopStream();
        dispatch({ type: 'background' });
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [stopStream]);

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
    // The X only discards the un-committed transcript; it doesn't touch the mic
    // (if listening it keeps listening, if paused it stays paused — nothing here
    // starts the mic).
    dispatch({ type: 'cancel' });
  }, []);

  const sendNow = useCallback((): void => {
    // The send button commits whatever transcript is on screen right now, without
    // waiting for an end-of-turn final (09 §4). The commit effect POSTs the text
    // and releases the mic (→ Paused) so the audio session ends and other apps'
    // audio can resume (09 §3a); the user taps to talk again for the next report.
    dispatch({ type: 'sendNow' });
  }, []);

  const delaySend = useCallback((): void => {
    // The "+10" control extends the post-turn-end auto-send countdown by 10s
    // (09 §4). The timer lives in the store (the machine stays pure and owns no
    // I/O), so this pushes the deadline out and reschedules directly — no reducer
    // action. A no-op when no send is armed (the dock hides the control then, but
    // guard anyway). Additive to the current deadline, so taps stack.
    if (graceDeadlineRef.current === null) {
      return;
    }
    graceDeadlineRef.current += SEND_DELAY_EXTENSION_MS;
    armGraceTimer();
  }, [armGraceTimer]);

  const openKeyboard = useCallback((): void => {
    // Switch to typed input. Stop the mic and clear any un-committed transcript,
    // so a spoken and a typed message never overlap in one submission. Nothing
    // reopens the mic on its own while the field is up.
    setKeyboardMode(true);
    pause();
    dispatch({ type: 'cancel' });
  }, [pause]);

  const closeKeyboard = useCallback((): void => {
    // Back to voice input: just drop keyboard mode. The mic stays off ("Tap to
    // talk") — closing the keyboard is not a tap on the mic, so it must not start
    // listening. `openKeyboard` already paused and cleared the transcript.
    setKeyboardMode(false);
  }, []);

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
      connecting: state.connecting,
      settledText: state.settledText,
      tailText: state.tailText,
      pause,
      resume,
      cancel,
      sendNow,
      countingDown: state.pending !== undefined,
      sendImminent,
      delaySend,
      getLevel,
      keyboardMode,
      openKeyboard,
      closeKeyboard,
      submitText,
    }),
    [
      state.micState,
      state.connecting,
      state.settledText,
      state.tailText,
      state.pending,
      sendImminent,
      pause,
      resume,
      cancel,
      sendNow,
      delaySend,
      getLevel,
      keyboardMode,
      openKeyboard,
      closeKeyboard,
      submitText,
    ],
  );

  return <VoiceStoreContext.Provider value={value}>{children}</VoiceStoreContext.Provider>;
}
