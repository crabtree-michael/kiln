// Activity store (08 §4): the ephemeral activity row. SSE-only — nothing is
// fetched on mount and nothing is persisted. Holds a `thinking` flag (from
// `activity` kind=thinking `{on}`) and a *stack* of notifications, under these
// rules:
//   - every source pushes onto the stack rather than overwriting — `say` (brain
//     utterance, reused via onSay) and `toast` (`activity` kind=toast, a board
//     side-effect) share one surface and stack when several are live at once;
//   - each toast auto-dismisses independently after 20s (its own timer), and a
//     `say` also carries a manual dismiss;
//   - `thinking` is merely exposed; the UI shows it only when the stack is empty.
// Each entry gets a unique id so its timer and dismiss target exactly one toast
// and the stack reflows smoothly as individual entries fall off.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import type { ActivityEvent } from '@/transport/transport';
import {
  ActivityStoreContext,
  type ActivityPill,
  type ActivityStoreValue,
  type ActivityToast,
} from '@/stores/activity-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface ActivityProviderProps {
  children: ReactNode;
}

/** How long each toast dwells before it auto-dismisses itself (08 §4). */
const TOAST_MS = 20000;

export function ActivityProvider({ children }: ActivityProviderProps): JSX.Element {
  const [thinking, setThinking] = useState(false);
  const [toasts, setToasts] = useState<ActivityToast[]>([]);

  // One live auto-dismiss timer per toast id, so each entry expires on its own
  // clock independent of its neighbours.
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());
  const nextIdRef = useRef(0);

  const dismiss = useCallback((id: number): void => {
    const timer = timersRef.current.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      timersRef.current.delete(id);
    }
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  }, []);

  const push = useCallback(
    (pill: ActivityPill): void => {
      nextIdRef.current += 1;
      const id = nextIdRef.current;
      setToasts((prev) => [...prev, { id, pill }]);
      timersRef.current.set(
        id,
        setTimeout(() => {
          dismiss(id);
        }, TOAST_MS),
      );
    },
    [dismiss],
  );

  // Dismiss every transient toast on the row at once, clearing each one's
  // auto-dismiss timer. Used when the user sends input: the fresh turn
  // supersedes lingering board toasts, but persistent `say` pills and an
  // already-clear row are left untouched (a submission with no toast up is a
  // no-op).
  const dismissToast = useCallback((): void => {
    setToasts((prev) => {
      for (const toast of prev) {
        if (toast.pill.kind === 'toast') {
          const timer = timersRef.current.get(toast.id);
          if (timer !== undefined) {
            clearTimeout(timer);
            timersRef.current.delete(toast.id);
          }
        }
      }
      return prev.filter((toast) => toast.pill.kind !== 'toast');
    });
  }, []);

  const handleActivity = useCallback(
    (event: ActivityEvent): void => {
      if (event.kind === 'thinking') {
        setThinking(event.on === true);
        return;
      }
      // kind === 'toast' — needs a verb to render.
      if (event.verb === undefined || event.verb === null) {
        return;
      }
      push({ kind: 'toast', verb: event.verb, ticketTitle: event.ticket_title ?? '' });
    },
    [push],
  );

  useEffect(
    () =>
      subscribeStream({
        onBoard: () => {
          // The activity store doesn't care about board snapshots.
        },
        onSay: (event) => {
          push({ kind: 'say', text: event.text });
        },
        onActivity: handleActivity,
        onConnectionStateChange: () => {
          // The activity row has no connection-state affordance of its own.
        },
      }),
    [push, handleActivity],
  );

  // Cancel every pending auto-dismiss on unmount.
  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      for (const timer of timers.values()) {
        clearTimeout(timer);
      }
      timers.clear();
    };
  }, []);

  const value = useMemo<ActivityStoreValue>(
    () => ({ thinking, toasts, dismiss, dismissToast }),
    [thinking, toasts, dismiss, dismissToast],
  );

  return <ActivityStoreContext.Provider value={value}>{children}</ActivityStoreContext.Provider>;
}
