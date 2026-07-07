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
//   - each toast auto-dismisses independently after 20s (its own timer), and a
//     `say` also carries a manual dismiss;
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

/** How long each toast dwells before it auto-dismisses itself (08 §4). */
const TOAST_MS = 20000;

// When a ticket runs through several transitions in one brain pass (e.g.
// queue → ready → working), the board emits a toast per step and they arrive
// near-simultaneously. Rather than flood the row with a burst of pills for one
// logical transition, we hold each ticket's toast for this window and, on new
// toasts for the same ticket landing inside it, keep only the latest — the most
// recent/relevant state — flushing exactly one pill once the burst settles.
// Tunable; ~100ms comfortably spans one worker-drained outbox burst without a
// perceptible delay. Keyed off ticket id (stable across a ticket's transitions,
// where its title is not).
const TOAST_DEBOUNCE_MS = 100;

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
  const nextIdRef = useRef(0);
  // Per-ticket debounce buffer: ticket id -> the in-flight timer that will flush
  // that ticket's latest pending toast once its burst settles. A fresh toast for
  // a ticket already buffered here cancels and replaces the timer, so only the
  // last pill survives the window.
  const pendingToastsRef = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());

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

  // Buffer a board toast for its ticket, collapsing a rapid burst to the latest
  // (08 §5, this change). A new toast for a ticket still inside its window cancels
  // the prior pending flush and replaces it, so only the last pill is pushed once
  // the burst settles. `say` pills never route through here — each brain utterance
  // is distinct and shows immediately.
  const queueToast = useCallback(
    (key: string, pill: ActivityPill): void => {
      const existing = pendingToastsRef.current.get(key);
      if (existing !== undefined) {
        clearTimeout(existing);
      }
      pendingToastsRef.current.set(
        key,
        setTimeout(() => {
          pendingToastsRef.current.delete(key);
          push(pill);
        }, TOAST_DEBOUNCE_MS),
      );
    },
    [push],
  );

  // Dismiss every transient toast on the row at once, clearing each one's
  // auto-dismiss timer. Used when the user sends input: the fresh turn
  // supersedes lingering board toasts, but persistent `say` pills and an
  // already-clear row are left untouched (a submission with no toast up is a
  // no-op).
  const dismissToast = useCallback((): void => {
    // Drop toasts still buffered in the per-ticket debounce window too: the fresh
    // turn supersedes them, so they should never surface after the user speaks.
    for (const timer of pendingToastsRef.current.values()) {
      clearTimeout(timer);
    }
    pendingToastsRef.current.clear();
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
        thinkingGenRef.current += 1;
        setThinking(event.on === true);
        return;
      }
      // kind === 'toast' — needs a verb to render.
      if (event.verb === undefined || event.verb === null) {
        return;
      }
      const pill: ActivityPill = {
        kind: 'toast',
        verb: event.verb,
        ticketTitle: event.ticket_title ?? '',
      };
      // Debounce per ticket. A toast with no ticket id can't be grouped, so it
      // shows at once rather than risk collapsing unrelated toasts together.
      const key = event.ticket_id ?? '';
      if (key === '') {
        push(pill);
        return;
      }
      queueToast(key, pill);
    },
    [push, queueToast],
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

  // Cancel every pending timer on unmount — both the live auto-dismiss timers and
  // any per-ticket debounce flushes still buffered — so none fire into an
  // unmounted store.
  useEffect(() => {
    const timers = timersRef.current;
    const pending = pendingToastsRef.current;
    return () => {
      for (const timer of timers.values()) {
        clearTimeout(timer);
      }
      timers.clear();
      for (const timer of pending.values()) {
        clearTimeout(timer);
      }
      pending.clear();
    };
  }, []);

  const value = useMemo<ActivityStoreValue>(
    () => ({ thinking, toasts, dismiss, dismissToast }),
    [thinking, toasts, dismiss, dismissToast],
  );

  return <ActivityStoreContext.Provider value={value}>{children}</ActivityStoreContext.Provider>;
}
