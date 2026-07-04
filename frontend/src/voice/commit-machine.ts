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
  | { type: 'denied' }
  | { type: 'background' } // visibilitychange -> hidden (09 §3)
  | { type: 'foreground' } // visibilitychange -> visible
  | { type: 'commitConsumed' } // the store POSTed the pending commit successfully
  | { type: 'commitFailed' }; // the POST failed — keep the finalized text on screen

export interface VoiceState {
  micState: MicState;
  settledText: string; // committed/finalized text, in ink
  tailText: string; // still-forming partial, ghosted
  /** Set for one tick when an utterance is ready to POST; the store consumes it.
   *  Explicitly `| undefined` so the reducer can clear it under
   *  `exactOptionalPropertyTypes` (02 §4b strictness). */
  commit?: string | undefined;
}

export function initialVoiceState(): VoiceState {
  // Mic on by default (09 §3 D3): the app opens straight into Listening.
  return { micState: 'listening', settledText: '', tailText: '' };
}

export function voiceReducer(state: VoiceState, action: VoiceAction): VoiceState {
  switch (action.type) {
    case 'provider':
      return onProviderEvent(state, action.event);
    case 'providerFailed':
      // Preserve any un-committed transcript on screen (09 §5).
      return { ...state, micState: 'retry', commit: undefined };
    case 'pause':
      return { ...state, micState: 'paused', tailText: '', commit: undefined };
    case 'resume':
      return { ...state, micState: 'listening', commit: undefined };
    case 'cancel':
      // The X discards the un-committed utterance; nothing was sent (09 §4).
      return { ...state, tailText: '', commit: undefined };
    case 'denied':
      return { ...state, micState: 'denied', tailText: '', commit: undefined };
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
      return { ...state, micState: 'listening', tailText: event.text };
    case 'final': {
      const text = event.text.trim();
      if (text === '') {
        // Empty/whitespace finals never post (09 §4).
        return { ...state, tailText: '' };
      }
      return { ...state, settledText: text, tailText: '', commit: text };
    }
    case 'error':
      return state; // the store decides reconnect-then-retry; no state change here
    case 'close':
      return state;
    default:
      return state;
  }
}
