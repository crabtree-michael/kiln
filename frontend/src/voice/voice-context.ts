// Split from voice-store.tsx so that file exports only the `VoiceProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the `useVoice` consumer hook. Mirrors
// feed-context.ts / board-context.ts.
import { createContext, useContext } from 'react';
import type { MicState } from '@/voice/commit-machine';

export interface VoiceStoreValue {
  /** The mic lifecycle state (09 §3) — drives the dock's `data-dock-state`. */
  micState: MicState;
  /** True during the mic setup window: the mic has been tapped on (`micState` is
   *  already `listening`) but the socket isn't recording yet (09 §3). The dock
   *  shows a spinner around the mic while this is true so the user waits to speak
   *  instead of being cut off. Clears the instant the provider connects. */
  connecting: boolean;
  /** Committed/finalized transcript, rendered in ink (09 §4). */
  settledText: string;
  /** Still-forming partial, rendered ghosted with a caret (09 §4). */
  tailText: string;
  /** Tap the mic while listening → pause (09 §3). */
  pause: () => void;
  /** Tap the mic while paused/denied/retry → resume/retry (09 §3, §5). */
  resume: () => void;
  /** The X → discard the un-committed utterance client-side (09 §4). */
  cancel: () => void;
  /** The send button → commit whatever transcript is on screen now, without
   *  waiting for an end-of-turn final (09 §4). A no-op when nothing is shown; the
   *  dock gates its visibility on there being transcript text. */
  sendNow: () => void;
  /** True while an end-of-turn auto-send is armed and counting down through the
   *  post-turn-end grace window before it POSTs (09 §4). */
  countingDown: boolean;
  /** True only in the final stretch (DELAY_REVEAL_WINDOW_MS) before an armed
   *  auto-send fires (09 §4) — a subset of `countingDown`. Drives the dock's "+10"
   *  control, which surfaces just above the mic as the deadline nears; a "+10" tap
   *  pushes the deadline back out and so withdraws the control until it counts down
   *  into the stretch again. */
  sendImminent: boolean;
  /** The "+10" control → push the armed auto-send 10s further out, giving the user
   *  time to catch a not-yet-ready utterance before it fires (09 §4). A no-op when
   *  nothing is counting down; the dock gates its visibility on `sendImminent`. */
  delaySend: () => void;
  /** Current mic input loudness as a raw RMS (0..~1); the dock samples this each
   *  animation frame to size the volume orb (09 §3). 0 when not listening. */
  getLevel: () => number;
  /** True while the keyboard-input mode is active: voice is an alternate to the
   *  default mic, never intermixed with it. Entering it stops the mic; leaving it
   *  resumes listening. Drives the dock to swap the live transcript for an
   *  editable field. */
  keyboardMode: boolean;
  /** The keyboard toggle → switch from voice to typed input. Stops the mic and
   *  clears any un-committed transcript so the two inputs never overlap. */
  openKeyboard: () => void;
  /** Leave keyboard mode → back to the default voice input (mic resumes). */
  closeKeyboard: () => void;
  /** Submit a typed message through the same downstream seam as a transcribed
   *  utterance (`POST /api/message`, 07 §4). Resolves `true` on a successful POST
   *  (the dock clears the field) and `false` on failure (the text is kept so the
   *  user can retry). A no-op resolving `false` when the text is empty. */
  submitText: (text: string) => Promise<boolean>;
}

export const VoiceStoreContext = createContext<VoiceStoreValue | undefined>(undefined);

export function useVoice(): VoiceStoreValue {
  const context = useContext(VoiceStoreContext);
  if (context === undefined) {
    throw new Error('useVoice must be used within a VoiceProvider');
  }
  return context;
}
