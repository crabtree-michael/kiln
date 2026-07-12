// Activity store (08 §4): the ephemeral activity row. Holds a `thinking` flag
// (from `activity` kind=thinking `{on}`) and a *stack* of notifications.
//   - `thinking` is driven live by the SSE stream, but that event is ephemeral
//     and never replayed, so a client backgrounded mid-pass can miss the closing
//     `on:false` and be left with a stuck spinner. To close that gap it is also
//     resynced from GET /api/activity on mount, on foreground/resume
//     (visibilitychange), and on every stream reconnect — the authoritative
//     current state, mirroring the feed store's reconnect-refetch.
//   - the notification stack is pure SSE and is not resynced (toasts and `say`
//     pills auto-dismiss; there is nothing to recover).
// The stack rules:
//   - every source pushes onto the stack rather than overwriting — `say` (brain
//     utterance, reused via onSay) and `toast` (`activity` kind=toast, a board
//     side-effect) share one surface and stack when several are live at once;
//   - each pill auto-dismisses independently on its own timer (a `say` dwells
//     30s so it can be read; a board `toast` clears fast at 5s); opening
//     a toast (to read its full content) pauses that timer so it can't vanish
//     mid-read, and closing an open toast dismisses it outright (the manual way
//     out, replacing the old always-on ×). The pause/resume API still resumes a
//     fresh dwell on `false`, but the UI now only ever pauses — a close removes
//     the entry rather than collapsing it;
//   - `thinking` is merely exposed; the UI shows it only when the stack is empty.
// Each entry gets a unique id so its timer and dismiss target exactly one toast
// and the stack reflows smoothly as individual entries fall off.
import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react';
import {
  fetchActivityStatus,
  type ActivityEvent,
  type ConnectionState,
} from '@/transport/transport';
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

/**
 * How long each pill dwells before it auto-dismisses itself (08 §4). A `say`
 * (brain utterance) lingers so it can be read; a `toast` (board side-effect
 * confirmation) is more incidental, so it clears fast to keep the row responsive.
 */
const SAY_MS = 30000;
const TOAST_MS = 5000;

/** Dwell for one pill, keyed off its kind. */
function dwellMs(pill: ActivityPill): number {
  return pill.kind === 'say' ? SAY_MS : TOAST_MS;
}

export function ActivityProvider({ children }: ActivityProviderProps): JSX.Element {
  const [thinking, setThinking] = useState(false);
  const [toasts, setToasts] = useState<ActivityToast[]>([]);

  // Monotonic generation of the live `thinking` value, bumped by every SSE
  // bracket. A resync fetch captures it before awaiting and applies its result
  // only if it hasn't advanced — so a live frame that lands mid-fetch (fresher
  // truth) is never clobbered by the older pulled snapshot.
  const thinkingGenRef = useRef(0);

  // One live auto-dismiss timer per toast id, so each entry expires on its own
  // clock independent of its neighbours.
  const timersRef = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());
  // Each toast's dwell, kept by id so a resume (collapse) restarts the same
  // clock the pill was pushed with rather than a one-size-fits-all default.
  const dwellsRef = useRef<Map<number, number>>(new Map());
  const nextIdRef = useRef(0);

  const dismiss = useCallback((id: number): void => {
    const timer = timersRef.current.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      timersRef.current.delete(id);
    }
    dwellsRef.current.delete(id);
    setToasts((prev) => prev.filter((toast) => toast.id !== id));
  }, []);

  const push = useCallback(
    (pill: ActivityPill): void => {
      nextIdRef.current += 1;
      const id = nextIdRef.current;
      setToasts((prev) => [...prev, { id, pill }]);
      const dwell = dwellMs(pill);
      dwellsRef.current.set(id, dwell);
      timersRef.current.set(
        id,
        setTimeout(() => {
          dismiss(id);
        }, dwell),
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
          dwellsRef.current.delete(toast.id);
        }
      }
      return prev.filter((toast) => toast.pill.kind !== 'toast');
    });
  }, []);

  // Pause / resume a toast's auto-dismiss around its expanded state. Expanding a
  // toast to read a clamped message cancels the running timer so it can't vanish
  // mid-read; collapsing it back down starts a fresh dwell. A collapse only ever
  // fires while the pill is still mounted (dismiss unmounts it without a collapse
  // event), so on resume the toast is guaranteed to still be on the stack.
  const setToastExpanded = useCallback(
    (id: number, expanded: boolean): void => {
      const timer = timersRef.current.get(id);
      if (expanded) {
        if (timer !== undefined) {
          clearTimeout(timer);
          timersRef.current.delete(id);
        }
      } else if (timer === undefined) {
        timersRef.current.set(
          id,
          setTimeout(
            () => {
              dismiss(id);
            },
            dwellsRef.current.get(id) ?? TOAST_MS,
          ),
        );
      }
    },
    [dismiss],
  );

  const handleActivity = useCallback(
    (event: ActivityEvent): void => {
      if (event.kind === 'thinking') {
        thinkingGenRef.current += 1;
        setThinking(event.on === true);
        return;
      }
      // kind === 'toast' — needs a verb to render.
      if (event.verb === undefined || event.verb === null) {
        return;
      }
      push({
        kind: 'toast',
        verb: event.verb,
        ticketTitle: event.ticket_title ?? '',
        ticketId: event.ticket_id ?? '',
      });
    },
    [push],
  );

  // Resync the spinner to the server's authoritative state (08 §4). The
  // `thinking` bracket is an ephemeral `activity` event, never replayed on
  // connect — so if the stream drops mid-pass (e.g. the app is backgrounded
  // while Kiln is thinking) the closing `thinking off` frame is missed and the
  // spinner would stay stuck on forever. GET /api/activity is the recovery
  // pull: it reflects a genuinely-still-running pass as true and a finished one
  // as false, so it can't wrongly hide (or show) the spinner the way a blind
  // reset-to-false would. A failed fetch leaves the current state untouched.
  const resyncThinking = useCallback(async (): Promise<void> => {
    const gen = thinkingGenRef.current;
    try {
      const status = await fetchActivityStatus();
      // A live bracket that arrived while the fetch was in flight is fresher
      // than this snapshot — don't let the pull overwrite it.
      if (thinkingGenRef.current === gen) {
        setThinking(status.thinking);
      }
    } catch {
      // Leave the existing (stale-but-harmless) spinner state in place.
    }
  }, []);

  // Mount resync: pick up an in-flight pass that started before this client
  // attached, since the stream pushes no activity snapshot on connect.
  useEffect(() => {
    void resyncThinking();
  }, [resyncThinking]);

  // Foreground/resume resync: a backgrounded app is the exact window in which
  // the closing bracket is missed, so re-pull the moment the tab is visible.
  useEffect(() => {
    function handleVisibility(): void {
      if (document.visibilityState === 'visible') {
        void resyncThinking();
      }
    }
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [resyncThinking]);

  useEffect(() => {
    // Reconnect resync (mirrors feed-store, 07 §5/§8): re-pull on every
    // reconnecting -> connected transition to recover any bracket missed while
    // the stream was down. The initial connect is already covered by the mount
    // resync above, so this doesn't double-fetch on first load.
    let previousState: ConnectionState = 'connecting';

    return subscribeStream({
      onBoard: () => {
        // The activity store doesn't care about board snapshots.
      },
      onSay: (event) => {
        push({ kind: 'say', text: event.text });
      },
      onActivity: handleActivity,
      onConnectionStateChange: (state) => {
        if (state === 'connected' && previousState === 'reconnecting') {
          void resyncThinking();
        }
        previousState = state;
      },
    });
  }, [push, handleActivity, resyncThinking]);

  // Cancel every pending auto-dismiss on unmount.
  useEffect(() => {
    const timers = timersRef.current;
    const dwells = dwellsRef.current;
    return () => {
      for (const timer of timers.values()) {
        clearTimeout(timer);
      }
      timers.clear();
      dwells.clear();
    };
  }, []);

  const value = useMemo<ActivityStoreValue>(
    () => ({ thinking, toasts, dismiss, dismissToast, setToastExpanded }),
    [thinking, toasts, dismiss, dismissToast, setToastExpanded],
  );

  return <ActivityStoreContext.Provider value={value}>{children}</ActivityStoreContext.Provider>;
}
