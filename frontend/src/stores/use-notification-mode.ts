// Push-notification frequency (02 §10). A small self-contained hook — the bell
// menu is the only surface for it — that reads the global mode on mount and
// writes changes back. The mode gates when the runtime fires a Web Push: the
// default `blocked` notifies only when a ticket needs a human decision; `all`
// notifies on every feed update (a testing aid). Single user in v1, so the mode
// is one global value.
//
// It degrades gracefully: while the initial read is in flight, or if it fails,
// the hook reports the `blocked` default so the menu renders the current
// behavior rather than erroring.
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchNotificationMode, putNotificationMode } from '@/transport/transport';
import type { NotificationModeValue } from '@/transport/transport';

export interface NotificationModeControl {
  /** The current frequency; `blocked` until the initial read resolves. */
  mode: NotificationModeValue;
  /** True once the initial read has resolved (success or failure). */
  ready: boolean;
  /** Persist a new mode. Optimistic — reverts if the write fails. */
  setMode: (mode: NotificationModeValue) => void;
}

export function useNotificationMode(): NotificationModeControl {
  const [mode, setModeState] = useState<NotificationModeValue>('blocked');
  const [ready, setReady] = useState(false);

  useEffect(() => {
    let cancelled = false;
    async function load(): Promise<void> {
      try {
        const current = await fetchNotificationMode();
        if (!cancelled) setModeState(current);
      } catch {
        // Leave the default in place; the menu still works and a later write
        // will surface any real backend problem.
      } finally {
        if (!cancelled) setReady(true);
      }
    }
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  // The value shown at the moment a write starts, so a failed write can roll
  // the optimistic change back to exactly what the user saw before.
  const previousRef = useRef<NotificationModeValue>('blocked');

  const setMode = useCallback((next: NotificationModeValue): void => {
    setModeState((current) => {
      previousRef.current = current;
      return next;
    });
    void putNotificationMode(next)
      .then((stored) => {
        setModeState(stored);
      })
      .catch(() => {
        setModeState(previousRef.current);
      });
  }, []);

  return { mode, ready, setMode };
}
