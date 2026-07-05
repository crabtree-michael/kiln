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
  | { type: 'sendNow' } // the send button — fire the armed send immediately, skipping the grace window
  | { type: 'denied' }
  | { type: 'background' } // visibilitychange -> hidden (09 §3)
  | { type: 'foreground' } // visibilitychange -> visible
  | { type: 'commitConsumed' } // the store POSTed the pending commit successfully
  | { type: 'commitFailed' } // the POST failed — keep the finalized text on screen
  | { type: 'commitDelayElapsed' }; // the post-turn-end grace window closed — fire the armed send

export interface VoiceState {
  micState: MicState;
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
  // Mic on by default (09 §3 D3): the app opens straight into Listening.
  return { micState: 'listening', settledText: '', tailText: '' };
}

/** Promote an armed end-of-turn final (`pending`) to the one-tick `commit` the
 *  store POSTs — the shared "fire the send now" step behind the send button, the
 *  grace-window timer, and stopping the mic (09 §4). A no-op when nothing is
 *  armed, so callers can apply it unconditionally. */
function fireArmedSend(state: VoiceState): VoiceState {
  if (state.pending === undefined) {
    return state;
  }
  return { ...state, pending: undefined, commit: state.pending };
}

export function voiceReducer(state: VoiceState, action: VoiceAction): VoiceState {
  switch (action.type) {
    case 'provider':
      return onProviderEvent(state, action.event);
    case 'providerFailed':
      // Preserve any un-committed transcript on screen (09 §5); drop the armed send.
      return { ...state, micState: 'retry', pending: undefined, commit: undefined };
    case 'pause':
      // Tapping the mic to stop listening fires any armed end-of-turn final on
      // the way out (same effect as the send button / the grace window elapsing,
      // 09 §4) rather than dropping it, then stops. A still-forming tail is still
      // discarded, and with nothing armed this is just a plain stop.
      return { ...fireArmedSend(state), micState: 'paused', tailText: '' };
    case 'resume':
      return { ...state, micState: 'listening', pending: undefined, commit: undefined };
    case 'cancel':
      // The X discards the un-committed utterance; nothing was sent (09 §4).
      return { ...state, tailText: '', pending: undefined, commit: undefined };
    case 'denied':
      return { ...state, micState: 'denied', tailText: '', pending: undefined, commit: undefined };
    case 'background':
      // The store closes the socket; the desired state is unchanged so
      // foregrounding resumes it. An explicit pause stays paused.
      return state;
    case 'foreground':
      return state;
    case 'commitConsumed':
      // A sent utterance clears back to the idle transcript so stale text can't
      // linger or flash back (09 §4): both the on-screen ink and the one-tick
      // commit are dropped.
      return { ...state, settledText: '', commit: undefined };
    case 'commitFailed':
      // The POST failed: keep the finalized text visible so the user can just
      // speak again (09 §4); only drop the one-tick commit.
      return { ...state, commit: undefined };
    case 'commitDelayElapsed':
    case 'sendNow':
      // Fire the armed send now. `commitDelayElapsed` reaches here when the
      // post-turn-end grace window closes on its own; `sendNow` when the user
      // taps the send button to skip that window (09 §4). Stopping the mic fires
      // it too — see the `pause` case. A no-op if resumed speech (or a cancel)
      // already cleared `pending`.
      return fireArmedSend(state);
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
      return { ...state, micState: 'listening' };
    case 'partial':
      // Resumed speech within the grace window cancels the armed send (a pause
      // that read as end-of-turn was a false alarm): drop `pending`, keep listening.
      return { ...state, micState: 'listening', tailText: event.text, pending: undefined };
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
      return { ...state, settledText, tailText: '', pending: settledText };
    }
    case 'error':
      return state; // the store decides reconnect-then-retry; no state change here
    case 'close':
      return state;
    default:
      return state;
  }
}
