// Foreground-presence heartbeat (02 §10 push dedup). Mounted once behind the
// authed app shell, this hook reports the single positive fact "this device is
// visible" so the backend can withhold a Web Push while the in-app toast already
// surfaces the event — avoiding a duplicate OS banner. It NEVER asserts "gone":
// per the design (R3) the absence of a leave signal can't be trusted, so the
// server's short lease TTL — not this hook — is what resumes sending on a crash.
//
// Reliability shape: every path here is a best-effort optimization over
// send-by-default. A failed heartbeat just leaves the lease to age out (→ send);
// a missing push subscription makes the hook a no-op (nothing to suppress). It
// can only ever cause a *duplicate*, never a *missed* notification.
import { useEffect } from 'react';
import { beaconPresenceHidden, postPresence } from '@/transport/transport';

/** Where the static push service worker is registered (public/push-sw.js) —
 * the same URL useWebPush registers it under, so getSubscription() finds it. */
const SERVICE_WORKER_URL = '/push-sw.js';

/** Heartbeat cadence while visible. ~2 fit inside the server's 30s presence TTL,
 * so a single dropped beat never ages the lease out (02 §10). */
const HEARTBEAT_MS = 15_000;

/**
 * usePresence heartbeats this device's foreground presence while visible and
 * fires a best-effort leave beacon when it backgrounds. It takes no arguments
 * and returns nothing — mount it once inside the authed shell. Gated on an
 * existing push subscription: with notifications off there is no push to
 * suppress, so it sends nothing.
 */
export function usePresence(): void {
  useEffect(() => {
    if (typeof document === 'undefined' || typeof navigator === 'undefined') {
      return;
    }

    // The device's own subscription endpoint, resolved lazily on the first beat
    // and cached — it identifies which row the server stamps. Null until a push
    // subscription exists (notifications enabled), which keeps the hook a no-op
    // until there's actually a push to suppress.
    let endpoint: string | null = null;
    let timer: ReturnType<typeof setInterval> | null = null;
    let cancelled = false;

    async function resolveEndpoint(): Promise<string | null> {
      if (endpoint !== null) {
        return endpoint;
      }
      if (!('serviceWorker' in navigator)) {
        return null;
      }
      const registration = await navigator.serviceWorker.getRegistration(SERVICE_WORKER_URL);
      const subscription = registration ? await registration.pushManager.getSubscription() : null;
      if (subscription === null) {
        return null;
      }
      endpoint = subscription.endpoint;
      return endpoint;
    }

    async function beat(): Promise<void> {
      // Guard again at fire time: a beat scheduled while visible may run just
      // after the tab hid, and a hidden tab must send nothing.
      if (document.visibilityState !== 'visible') {
        return;
      }
      const target = await resolveEndpoint();
      if (target === null || cancelled) {
        return;
      }
      // Fire-and-forget: a failed heartbeat is fine (the lease just goes stale →
      // send), so we never surface an error or block on it.
      void postPresence(target, true);
    }

    function startHeartbeat(): void {
      if (timer !== null) {
        return;
      }
      void beat(); // immediate beat on becoming visible, then on a cadence.
      timer = setInterval(() => void beat(), HEARTBEAT_MS);
    }

    function stopHeartbeat(): void {
      if (timer !== null) {
        clearInterval(timer);
        timer = null;
      }
    }

    function leave(): void {
      // Only clear a lease we could have set. `beaconPresenceHidden` is the one
      // request that survives page teardown; if unsupported or it never fires,
      // the server TTL still ages the lease out.
      if (endpoint !== null) {
        beaconPresenceHidden(endpoint);
      }
    }

    function handleVisibility(): void {
      if (document.visibilityState === 'visible') {
        startHeartbeat();
      } else {
        stopHeartbeat();
        leave();
      }
    }

    function handlePageHide(): void {
      stopHeartbeat();
      leave();
    }

    if (document.visibilityState === 'visible') {
      startHeartbeat();
    }
    document.addEventListener('visibilitychange', handleVisibility);
    // pagehide is the most reliable teardown hook on mobile Safari (beforeunload
    // is not fired for a backgrounded PWA); still best-effort per R3.
    window.addEventListener('pagehide', handlePageHide);

    return () => {
      cancelled = true;
      stopHeartbeat();
      document.removeEventListener('visibilitychange', handleVisibility);
      window.removeEventListener('pagehide', handlePageHide);
    };
  }, []);
}
