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
}

export const VoiceStoreContext = createContext<VoiceStoreValue | undefined>(undefined);

export function useVoice(): VoiceStoreValue {
  const context = useContext(VoiceStoreContext);
  if (context === undefined) {
    throw new Error('useVoice must be used within a VoiceProvider');
  }
  return context;
}
