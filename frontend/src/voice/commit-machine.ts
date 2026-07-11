// The voice commit state machine (09 §3–§4): pure logic, no I/O — the unit-test
// target. It owns the mic states and the utterance-commit rules, consuming
// neutral provider events + user actions. It never calls the network: on an
// end-of-turn final it stamps `commit` with the text to POST; the store acts on
// it and dispatches `commitConsumed`. The provider client (assemblyai-client)
// and the store (voice-store) supply the I/O around this reducer.

/** The four mic states (09 §3). `listening` is the resting/default state. */
export type MicState = 'listening' | 'paused' | 'denied' | 'retry';

/** Neutral provider events — the AssemblyAI protocol is decoded to these
 *  (09 §7 provider client) so the machine is provider-agnostic and testable. */
export type VoiceProviderEvent =
  | { kind: 'open' }
  | { kind: 'partial'; text: string } // still-forming turn -> ghosted tail
  | { kind: 'final'; text: string } // formatted end-of-turn transcript -> settle + commit
  | { kind: 'error' }
  | { kind: 'close' };

/** User + lifecycle actions the store dispatches. */
export type VoiceAction =
  | { type: 'provider'; event: VoiceProviderEvent }
  | { type: 'providerFailed' } // the one silent reconnect (09 §5) already failed
  | { type: 'pause' }
  | { type: 'resume' }
  | { type: 'cancel' } // the X — discard the un-committed utterance (09 §4)
  | { type: 'sendNow' } // the send button — commit whatever is on screen now, without waiting for a final
  | { type: 'denied' }
  | { type: 'background' } // visibilitychange -> hidden: stop the mic (09 §3)
  | { type: 'commitConsumed' } // the store POSTed the pending commit successfully
  | { type: 'commitFailed' } // the POST failed — keep the finalized text on screen
  | { type: 'commitDelayElapsed' }; // the post-turn-end grace window closed — fire the armed send

export interface VoiceState {
  micState: MicState;
  /** True from the mic tap (`resume`) until the provider's `open` confirms the
   *  socket is live and recording (09 §3). During this brief setup window the mic
   *  is already `listening` but is not yet capturing audio — the dock shows a
   *  spinner around the mic so the user waits to speak instead of getting cut off.
   *  Cleared by `open` (connected), and by anything that stops the mic
   *  (pause/denied/background/providerFailed/a send). */
  connecting: boolean;
  settledText: string; // committed/finalized text, in ink
  tailText: string; // still-forming partial, ghosted
  /** An end-of-turn final that is armed to POST but held through the post-turn-end
   *  grace window (09 §4): the store runs a `COMMIT_DELAY_MS` timer off this and
   *  dispatches `commitDelayElapsed` when it closes. Resumed speech (a partial),
   *  a pause, the X, or a failure clears it, cancelling the send before it fires.
   *  Explicitly `| undefined` so the reducer can clear it. */
  pending?: string | undefined;
  /** Set for one tick when an utterance is ready to POST; the store consumes it.
   *  Explicitly `| undefined` so the reducer can clear it under
   *  `exactOptionalPropertyTypes` (02 §4b strictness). */
  commit?: string | undefined;
}

export function initialVoiceState(): VoiceState {
  // Mic off until an explicit tap: the app opens Paused ("Tap to talk"), never
  // listening on its own. The mic only turns on when the user taps the mic
  // control (→ `resume`); nothing here or in the store starts it automatically.
  return { micState: 'paused', connecting: false, settledText: '', tailText: '' };
}

/** Promote an armed end-of-turn final (`pending`) to the one-tick `commit` the
 *  store POSTs — the grace-window timer's "the window closed, send it" step
 *  (09 §4). A no-op when nothing is armed, so callers can apply it
 *  unconditionally. */
function fireArmedSend(state: VoiceState): VoiceState {
  if (state.pending === undefined) {
    return state;
  }
  // A send RELEASES the mic: the machine drops to Paused and the store tears the
  // stream down, ending the play-and-record audio session so iOS can resume the
  // other app's audio (music/podcast) our capture was ducking/holding — best
  // effort (09 §3a). This ends the turn; the user taps to talk again for the next
  // report. `connecting` clears in case a send lands mid-setup.
  return {
    ...state,
    pending: undefined,
    commit: state.pending,
    micState: 'paused',
    connecting: false,
  };
}

/** Commit whatever transcript is on screen right now — the settled ink plus any
 *  still-forming tail — as the one-tick `commit` the store POSTs. This backs the
 *  send button, which fires the current text immediately, without waiting for an
 *  end-of-turn final (09 §4): as soon as any text shows, it can be sent. A no-op
 *  when nothing is shown. */
function fireDisplayedSend(state: VoiceState): VoiceState {
  const text = [state.settledText, state.tailText]
    .filter((part) => part !== '')
    .join(' ')
    .trim();
  if (text === '') {
    return state;
  }
  // The send button commits whatever is on screen now, then RELEASES the mic (→
  // Paused): the store tears the stream down so the play-and-record audio session
  // ends and iOS can resume the other app's audio our capture was holding — best
  // effort (09 §3a). Closing the socket for good also means the just-sent interim
  // words can't return in that turn's trailing final and double-post — no reopen.
  return {
    ...state,
    settledText: text,
    tailText: '',
    pending: undefined,
    commit: text,
    micState: 'paused',
    connecting: false,
  };
}

export function voiceReducer(state: VoiceState, action: VoiceAction): VoiceState {
  switch (action.type) {
    case 'provider':
      return onProviderEvent(state, action.event);
    case 'providerFailed':
      // Preserve any un-committed transcript on screen (09 §5); drop the armed send
      // and the connecting flag (the setup window ended in failure, not a live mic).
      return {
        ...state,
        micState: 'retry',
        connecting: false,
        pending: undefined,
        commit: undefined,
      };
    case 'pause':
      // The mic/stop-listening button only stops listening — it does NOT
      // auto-send (that linkage was reverted). Keep any transcript on screen so
      // the send/X controls stay usable in the "stuck" case: the socket is now
      // closed, so a frozen interim can no longer finalize, but the user can
      // still send it (the send button) or clear it (the X). Drop the armed
      // grace-window send so its timer can't fire a stray commit while paused.
      return {
        ...state,
        micState: 'paused',
        connecting: false,
        pending: undefined,
        commit: undefined,
      };
    case 'resume':
      // The mic tap flips to listening immediately, but the socket/getUserMedia
      // setup is still in flight — mark `connecting` so the dock shows a spinner
      // (not the live glow) until the provider's `open` lands (09 §3). Cleared
      // there, or on any stop below.
      return {
        ...state,
        micState: 'listening',
        connecting: true,
        pending: undefined,
        commit: undefined,
      };
    case 'cancel':
      // The X clears the whole shown transcript — settled ink and the
      // still-forming tail alike — and disarms any pending send; nothing was
      // sent (09 §4). Clearing the ink too lets the X wipe a frozen transcript in
      // the "stuck" case, not just an in-progress tail.
      return {
        ...state,
        settledText: '',
        tailText: '',
        pending: undefined,
        commit: undefined,
      };
    case 'denied':
      return {
        ...state,
        micState: 'denied',
        connecting: false,
        tailText: '',
        pending: undefined,
        commit: undefined,
      };
    case 'background':
      // Leaving the app stops the mic for good: the store closes the socket and
      // the machine drops any live listen to Paused. Returning never re-opens the
      // mic on its own — the user taps to talk again (denied/retry are left as-is
      // so backgrounding doesn't paper over a permission/connection problem).
      return state.micState === 'listening'
        ? {
            ...state,
            micState: 'paused',
            connecting: false,
            pending: undefined,
            commit: undefined,
          }
        : state;
    case 'commitConsumed':
      // A sent utterance clears back to the idle transcript so stale text can't
      // linger or flash back (09 §4): the on-screen ink and the one-tick commit
      // are dropped. The mic was already released to Paused by the send.
      return { ...state, settledText: '', commit: undefined };
    case 'commitFailed':
      // The POST failed: keep the finalized text visible so the user can just
      // speak again (09 §4); only drop the one-tick commit. The mic was already
      // released to Paused by the send, so the user taps to talk to retry.
      return { ...state, commit: undefined };
    case 'commitDelayElapsed':
      // The post-turn-end grace window closed with the send still armed: promote
      // the held `pending` to the one-tick `commit` the store POSTs (09 §4). A
      // no-op if resumed speech, a cancel, or a pause already cleared `pending`.
      return fireArmedSend(state);
    case 'sendNow':
      // The send button fires whatever is on screen *now* — interim tail included
      // — without waiting for an end-of-turn final (09 §4). Commit the displayed
      // transcript verbatim, then release the mic (→ Paused) so the audio session
      // ends and other apps' audio can resume (09 §3a) — `fireDisplayedSend` does
      // both. A no-op if nothing is shown.
      return fireDisplayedSend(state);
    default:
      return state;
  }
}

function onProviderEvent(state: VoiceState, event: VoiceProviderEvent): VoiceState {
  // Ignore provider chatter while paused/denied — the store shouldn't feed it,
  // but be defensive.
  if (state.micState === 'paused' || state.micState === 'denied') {
    return state;
  }
  switch (event.kind) {
    case 'open':
      // The socket is live: recording has actually started, so clear the setup
      // spinner (09 §3). This is what the connecting window waits for.
      return { ...state, micState: 'listening', connecting: false };
    case 'partial':
      // Resumed speech within the grace window cancels the armed send (a pause
      // that read as end-of-turn was a false alarm): drop `pending`, keep
      // listening. Transcript flowing means we're connected — clear any lingering
      // setup spinner (open normally clears it first; this is belt-and-braces).
      return {
        ...state,
        micState: 'listening',
        connecting: false,
        tailText: event.text,
        pending: undefined,
      };
    case 'final': {
      const text = event.text.trim();
      if (text === '') {
        // Empty/whitespace finals never post (09 §4).
        return { ...state, tailText: '' };
      }
      // Arm the send but hold it: the store times the post-turn-end grace window
      // and dispatches `commitDelayElapsed` to actually commit (09 §4). When
      // unsent settled text is still on screen — resumed speech during the grace
      // window cancelled the previous send (dropping `pending`) but left its ink
      // — grow the transcript by appending this final rather than overwriting it,
      // so the whole utterance sends as one (09 §4).
      const settledText = state.settledText === '' ? text : `${state.settledText} ${text}`;
      return { ...state, connecting: false, settledText, tailText: '', pending: settledText };
    }
    case 'error':
      return state; // the store decides reconnect-then-retry; no state change here
    case 'close':
      return state;
    default:
      return state;
  }
}
