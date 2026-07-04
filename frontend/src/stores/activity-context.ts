// Split from activity-store.tsx so that file exports only the
// `ActivityProvider` component (react-refresh/only-export-components) — this
// file carries the pill shape, the context, and the consumer hook.
import { createContext, useContext } from 'react';

/** The toast transition verbs (08 §4 / wire `ActivityEvent.verb`). */
export type ToastVerb = 'started' | 'nudged' | 'finished' | 'queued';

/**
 * The single activity pill (08 §4). `say` is a persistent brain utterance that
 * outranks toasts; `toast` is an auto-dismissing side-effect confirmation;
 * `null` means the row is clear (the UI may then show `thinking`).
 */
export type ActivityPill =
  { kind: 'say'; text: string } | { kind: 'toast'; verb: ToastVerb; ticketTitle: string } | null;

export interface ActivityStoreValue {
  /** Brain-pass spinner flag (08 §4). The UI shows it only when `pill` is null. */
  thinking: boolean;
  /** The currently displayed pill, or `null` when the activity row is clear. */
  pill: ActivityPill;
  /** Dismisses the current pill (e.g. a persistent `say`), draining any queued toasts. */
  dismiss: () => void;
}

export const ActivityStoreContext = createContext<ActivityStoreValue | undefined>(undefined);

export function useActivityStore(): ActivityStoreValue {
  const context = useContext(ActivityStoreContext);
  if (context === undefined) {
    throw new Error('useActivityStore must be used within an ActivityProvider');
  }
  return context;
}
