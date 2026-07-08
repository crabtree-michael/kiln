// Web Push opt-in (02 §10). A self-contained hook — the "Enable notifications"
// control lives only in Settings, so this needs no context/provider. It owns the
// browser-side subscription flow: request Notification permission, register the
// push service worker (src/push-sw.ts → /push-sw.js), subscribe with the
// backend's VAPID key, and POST the subscription so notify.send can reach it.
//
// It degrades gracefully: unsupported browsers and an unconfigured backend
// (GET /api/push/key → 404) resolve to a disabled, explanatory state rather than
// throwing, so the toggle can render "unavailable" instead of erroring.
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchPushKey, postPushSubscription } from '@/transport/transport';
import type { PushSubscriptionPayload } from '@/transport/transport';

/** Where the static service worker is served (public/push-sw.js). */
const SERVICE_WORKER_URL = '/push-sw.js';

export type WebPushStatus =
  | 'checking' // initial capability + backend probe in flight
  | 'unsupported' // this browser lacks service worker / Push / Notification APIs
  | 'unconfigured' // backend has no VAPID key (GET /api/push/key → 404)
  | 'default' // supported + configured, not yet subscribed, permission not decided
  | 'denied' // the user blocked notifications at the browser level
  | 'enabling' // subscription flow running
  | 'enabled' // an active push subscription exists
  | 'error'; // the last enable attempt failed (see `error`)

export interface WebPush {
  status: WebPushStatus;
  /** A human-readable reason when `status === 'error'`, else null. */
  error: string | null;
  /** Run the opt-in flow: permission → register SW → subscribe → POST. */
  enable: () => Promise<void>;
  /** Unsubscribe this browser so pushes stop; drops back to `default`. The
   * backend keeps the now-dead endpoint until its next send prunes it (404/410). */
  disable: () => Promise<void>;
}

function browserSupportsPush(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    'serviceWorker' in navigator &&
    typeof window !== 'undefined' &&
    'PushManager' in window &&
    'Notification' in window
  );
}

// Decode the backend's base64url VAPID key into the byte array
// pushManager.subscribe expects as applicationServerKey (standard helper).
function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4);
  const normalized = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(normalized);
  // Back the array with an explicit ArrayBuffer so its type is Uint8Array<ArrayBuffer>
  // (a BufferSource), not the SharedArrayBuffer-inclusive default lib.dom infers.
  const output = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i += 1) {
    output[i] = raw.charCodeAt(i);
  }
  return output;
}

// Narrow the loosely-typed PushSubscription.toJSON() into the wire payload,
// without escape hatches — the keys are optional in lib.dom's typings.
function toPayload(sub: PushSubscription): PushSubscriptionPayload {
  const json = sub.toJSON();
  const { endpoint, keys } = json;
  const p256dh = keys?.p256dh;
  const auth = keys?.auth;
  if (
    typeof endpoint !== 'string' ||
    endpoint === '' ||
    typeof p256dh !== 'string' ||
    typeof auth !== 'string'
  ) {
    throw new Error('push subscription is missing its endpoint or keys');
  }
  return { endpoint, keys: { p256dh, auth } };
}

export function useWebPush(): WebPush {
  const [status, setStatus] = useState<WebPushStatus>('checking');
  const [error, setError] = useState<string | null>(null);
  // The VAPID key, cached from the mount probe so enable() need not refetch.
  const keyRef = useRef<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    async function probe(): Promise<void> {
      if (!browserSupportsPush()) {
        if (!cancelled) setStatus('unsupported');
        return;
      }
      try {
        const key = await fetchPushKey();
        if (key === null) {
          if (!cancelled) setStatus('unconfigured');
          return;
        }
        keyRef.current = key;
        // Already subscribed from a previous visit? Reflect that without prompting.
        const registration = await navigator.serviceWorker.getRegistration(SERVICE_WORKER_URL);
        const existing = registration ? await registration.pushManager.getSubscription() : null;
        const next: WebPushStatus =
          existing !== null
            ? 'enabled'
            : Notification.permission === 'denied'
              ? 'denied'
              : 'default';
        if (!cancelled) setStatus(next);
      } catch {
        if (!cancelled) setStatus('unconfigured');
      }
    }
    void probe();
    return () => {
      cancelled = true;
    };
  }, []);

  const enable = useCallback(async (): Promise<void> => {
    if (!browserSupportsPush()) {
      setStatus('unsupported');
      return;
    }
    setStatus('enabling');
    setError(null);
    try {
      const permission = await Notification.requestPermission();
      if (permission !== 'granted') {
        setStatus(permission === 'denied' ? 'denied' : 'default');
        return;
      }
      const key = keyRef.current ?? (await fetchPushKey());
      if (key === null) {
        setStatus('unconfigured');
        return;
      }
      keyRef.current = key;
      const registration = await navigator.serviceWorker.register(SERVICE_WORKER_URL);
      await navigator.serviceWorker.ready;
      const existing = await registration.pushManager.getSubscription();
      const subscription =
        existing ??
        (await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(key),
        }));
      await postPushSubscription(toPayload(subscription));
      setStatus('enabled');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to enable notifications');
      setStatus('error');
    }
  }, []);

  const disable = useCallback(async (): Promise<void> => {
    if (!browserSupportsPush()) {
      setStatus('unsupported');
      return;
    }
    setError(null);
    try {
      const registration = await navigator.serviceWorker.getRegistration(SERVICE_WORKER_URL);
      const existing = registration ? await registration.pushManager.getSubscription() : null;
      // unsubscribe() invalidates the endpoint browser-side; the next notify.send
      // to it 404/410s and the sender prunes it, so no server call is needed here.
      if (existing !== null) {
        await existing.unsubscribe();
      }
      // Permission stays granted, but with no subscription we're back to 'default'
      // (supported + configured, not yet subscribed) — the enable affordance.
      setStatus('default');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to disable notifications');
      setStatus('error');
    }
  }, []);

  return { status, error, enable, disable };
}
