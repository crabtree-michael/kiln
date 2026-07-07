// Kiln's purpose-built service worker (02 §10 notification transport). It does
// exactly two things — show a notification when a `push` arrives, and focus/open
// the app when one is tapped — and PRECACHES NOTHING, so it can never serve a
// stale app shell (the outage that retired the old vite-plugin-pwa worker).
//
// This is a hand-written static asset served verbatim from `public/`, not a
// bundled/typed module: a service worker's global scope (`self`, `clients`,
// `registration`, push/notification events) doesn't fit the app's DOM typings or
// lint program, so it is plain JS and excluded from the eslint run. Keep it
// small and dependency-free. Registered on opt-in by src/stores/use-web-push.ts.

// Parse the JSON payload the backend sends (push.Notification in Go), falling
// back to a generic notification for a missing or malformed body.
function parsePush(data) {
  const fallback = { title: 'Kiln', body: 'You have a new notification.', url: '/' };
  if (!data) return fallback;
  try {
    const parsed = data.json();
    if (parsed && typeof parsed === 'object') {
      return {
        title: typeof parsed.title === 'string' && parsed.title ? parsed.title : fallback.title,
        body: typeof parsed.body === 'string' ? parsed.body : fallback.body,
        url: typeof parsed.url === 'string' && parsed.url ? parsed.url : fallback.url,
      };
    }
  } catch (err) {
    // Non-JSON payload — fall through to the generic notification.
  }
  return fallback;
}

self.addEventListener('install', function () {
  // A push-only worker has nothing to precache; activate immediately so a newly
  // opted-in client starts receiving pushes without a reload.
  self.skipWaiting();
});

self.addEventListener('activate', function (event) {
  event.waitUntil(
    (async function () {
      // Belt-and-suspenders: drop any caches left by the pre-notifications
      // (precaching) service-worker era so no stale shell can survive.
      const keys = await caches.keys();
      await Promise.all(keys.map((key) => caches.delete(key)));
      await self.clients.claim();
    })(),
  );
});

// True when a Kiln tab is open AND in the foreground (focused or merely
// visible). Web Push's userVisibleOnly contract requires a notification for
// every push only while the page is hidden — when a client is visible the
// browser imposes no penalty for staying silent, so this is the supported way
// to skip notifications the user would already see in the live app.
async function appIsInForeground() {
  const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
  return all.some((client) => client.focused || client.visibilityState === 'visible');
}

self.addEventListener('push', function (event) {
  const message = parsePush(event.data);
  event.waitUntil(
    (async function () {
      // Don't interrupt the user with a push for something they're already
      // looking at — the open board (and its Blocked zone, 07 §6) is the surface.
      if (await appIsInForeground()) return;
      await self.registration.showNotification(message.title, {
        body: message.body,
        icon: '/kiln-mark.svg',
        badge: '/kiln-mark.svg',
        data: { url: message.url },
      });
    })(),
  );
});

self.addEventListener('notificationclick', function (event) {
  event.notification.close();
  const data = event.notification.data;
  const target = data && typeof data.url === 'string' ? data.url : '/';
  event.waitUntil(
    (async function () {
      // Focus an already-open Kiln tab if there is one, and hand it the deep link
      // over postMessage so it opens the ticket in place — reloading/navigating a
      // live tab would drop its session (the attached voice channel, 02 §10). A
      // fresh window instead carries the target in its URL and reads it on load.
      const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
      for (const client of all) {
        if ('focus' in client) {
          await client.focus();
          if ('postMessage' in client) {
            client.postMessage({ type: 'kiln:navigate', url: target });
          }
          return;
        }
      }
      await self.clients.openWindow(target);
    })(),
  );
});
