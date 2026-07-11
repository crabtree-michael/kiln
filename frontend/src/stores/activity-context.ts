// Split from activity-store.tsx so that file exports only the
// `ActivityProvider` component (react-refresh/only-export-components) — this
// file carries the pill shape, the context, and the consumer hook.
import { createContext, useContext } from 'react';

/** The toast transition verbs (08 §4 / wire `ActivityEvent.verb`). */
export type ToastVerb = 'started' | 'nudged' | 'finished' | 'queued';

/**
 * The content of one activity pill (08 §4). `say` is a brain utterance (agent
 * says something); `toast` is a side-effect board-transition confirmation. Both
 * sources share the same notification surface — they stack rather than overwrite
 * each other, and each auto-dismisses independently.
 */
export type ActivityPill =
  { kind: 'say'; text: string } | { kind: 'toast'; verb: ToastVerb; ticketTitle: string };

/**
 * One live notification in the activity stack. `id` is a stable, unique key so
 * React can reflow the stack smoothly as individual toasts dismiss, and so a
 * dismiss/timer can target exactly one entry without disturbing its neighbours.
 */
export interface ActivityToast {
  id: number;
  pill: ActivityPill;
}

export interface ActivityStoreValue {
  /** Brain-pass spinner flag (08 §4). The UI shows it only when the stack is empty. */
  thinking: boolean;
  /** The live notification stack, oldest first — rendered as a stacked list. */
  toasts: ActivityToast[];
  /** Dismisses one toast by id (early-dismiss for a `say`; also used by timers). */
  dismiss: (id: number) => void;
  /**
   * Dismisses every transient `toast` on the row at once, leaving persistent
   * `say` pills and an already-clear row untouched. Used when the user sends
   * input, which supersedes any lingering toast.
   */
  dismissToast: () => void;
  /**
   * Pause (`true`) or resume (`false`) one toast's auto-dismiss timer as the user
   * expands or collapses it. Expanding a clamped message stops the timer so the
   * toast can't disappear mid-read; collapsing restarts a fresh dwell.
   */
  setToastExpanded: (id: number, expanded: boolean) => void;
}

export const ActivityStoreContext = createContext<ActivityStoreValue | undefined>(undefined);

export function useActivityStore(): ActivityStoreValue {
  const context = useContext(ActivityStoreContext);
  if (context === undefined) {
    throw new Error('useActivityStore must be used within an ActivityProvider');
  }
  return context;
}
