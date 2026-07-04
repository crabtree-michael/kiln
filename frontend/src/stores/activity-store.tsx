// Activity store (08 §4): the ephemeral activity row. SSE-only — nothing is
// fetched on mount and nothing is persisted. Holds a `thinking` flag (from
// `activity` kind=thinking `{on}`) and a single `pill` with a toast queue, under
// these contention rules:
//   - a `say` (from the existing `say` SSE — reused via onSay) replaces any
//     active toast and is persistent until the next utterance or an explicit
//     dismiss;
//   - toasts (`activity` kind=toast) queue behind an active say and each
//     auto-dismiss after ~4s, draining one at a time;
//   - `thinking` is merely exposed; the UI shows it only when `pill` is null.
// Uses the chat-store ref pattern so the SSE handlers and the auto-dismiss timer
// read the live pill/queue without re-subscribing.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import type { ActivityEvent } from '@/transport/transport';
import {
  ActivityStoreContext,
  type ActivityPill,
  type ActivityStoreValue,
  type ToastVerb,
} from '@/stores/activity-context';
import { subscribeStream } from '@/stores/stream-connection';

export interface ActivityProviderProps {
  children: ReactNode;
}

/** Toast dwell time before auto-dismiss (08 §4 "~4s"). */
const TOAST_MS = 4000;

interface ToastPill {
  kind: 'toast';
  verb: ToastVerb;
  ticketTitle: string;
}

export function ActivityProvider({ children }: ActivityProviderProps): JSX.Element {
  const [thinking, setThinking] = useState(false);
  const [pill, setPillState] = useState<ActivityPill>(null);

  const pillRef = useRef<ActivityPill>(null);
  const queueRef = useRef<ToastPill[]>([]);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pumpRef = useRef<() => void>(() => {
    // Replaced by the real `pump` in the effect below.
  });

  const setPill = useCallback((next: ActivityPill): void => {
    pillRef.current = next;
    setPillState(next);
  }, []);

  const clearTimer = useCallback((): void => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  // Show the next queued toast, but only when the row is clear — an active say
  // (or a still-dwelling toast) blocks the queue until it is dismissed.
  const pump = useCallback((): void => {
    if (pillRef.current !== null) {
      return;
    }
    const next = queueRef.current.shift();
    if (next === undefined) {
      return;
    }
    setPill(next);
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      setPill(null);
      pumpRef.current();
    }, TOAST_MS);
  }, [setPill]);

  useEffect(() => {
    pumpRef.current = pump;
  }, [pump]);

  const enqueueToast = useCallback(
    (toast: ToastPill): void => {
      queueRef.current = [...queueRef.current, toast];
      pump();
    },
    [pump],
  );

  const showSay = useCallback(
    (text: string): void => {
      // A say outranks and replaces any active toast; queued toasts wait.
      clearTimer();
      setPill({ kind: 'say', text });
    },
    [clearTimer, setPill],
  );

  const dismiss = useCallback((): void => {
    clearTimer();
    setPill(null);
    pump();
  }, [clearTimer, pump, setPill]);

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
      enqueueToast({ kind: 'toast', verb: event.verb, ticketTitle: event.ticket_title ?? '' });
    },
    [enqueueToast],
  );

  useEffect(
    () =>
      subscribeStream({
        onBoard: () => {
          // The activity store doesn't care about board snapshots.
        },
        onSay: (event) => {
          showSay(event.text);
        },
        onActivity: handleActivity,
        onConnectionStateChange: () => {
          // The activity row has no connection-state affordance of its own.
        },
      }),
    [showSay, handleActivity],
  );

  // Cancel any pending auto-dismiss on unmount.
  useEffect(() => clearTimer, [clearTimer]);

  const value = useMemo<ActivityStoreValue>(
    () => ({ thinking, pill, dismiss }),
    [thinking, pill, dismiss],
  );

  return <ActivityStoreContext.Provider value={value}>{children}</ActivityStoreContext.Provider>;
}
