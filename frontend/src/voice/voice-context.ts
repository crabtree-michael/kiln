// Split from voice-store.tsx so that file exports only the `VoiceProvider`
// component (react-refresh/only-export-components) — this file carries the
// context, its value shape, and the `useVoice` consumer hook. Mirrors
// feed-context.ts / board-context.ts.
import { createContext, useContext } from 'react';
import type { MicState } from '@/voice/commit-machine';

export interface VoiceStoreValue {
  /** The mic lifecycle state (09 §3) — drives the dock's `data-dock-state`. */
  micState: MicState;
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
  /** True while an end-of-turn final is armed and holding through the grace
   *  window (09 §4) — i.e. there is transcribed input pending to send. Drives
   *  the dock's send button visibility. */
  pendingSend: boolean;
  /** The send button → fire the armed send immediately, skipping the
   *  post-turn-end grace window (09 §4). A no-op unless `pendingSend`. */
  sendNow: () => void;
  /** Current mic input loudness as a raw RMS (0..~1); the dock samples this each
   *  animation frame to size the volume orb (09 §3). 0 when not listening. */
  getLevel: () => number;
}

export const VoiceStoreContext = createContext<VoiceStoreValue | undefined>(undefined);

export function useVoice(): VoiceStoreValue {
  const context = useContext(VoiceStoreContext);
  if (context === undefined) {
    throw new Error('useVoice must be used within a VoiceProvider');
  }
  return context;
}
