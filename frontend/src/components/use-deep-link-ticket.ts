// Deep-link → open-ticket bridge for the primary screen (02 §10 tap-to-open).
// A push notification's `notificationclick` deep link is `/?ticket=<id>` (built
// by webPushNotifier). Two arrival paths converge here:
//   • cold / backgrounded: the service worker opens a fresh window at that URL,
//     and we read the `ticket` query param once on mount;
//   • already-open tab: the worker can't reload it without dropping the live
//     session (the attached voice channel, 02 §10), so it postMessages
//     `{ type, url }` to the focused client instead, and we handle it live.
// Both funnel to `onOpen(ticketId)`, which the screen uses to open the ticket's
// detail overlay. The query param is stripped after the first read so a later
// manual reload doesn't reopen a ticket the user already dismissed.
import { useEffect } from 'react';

/** postMessage type the service worker sends a focused tab on notification tap. */
export const DEEP_LINK_MESSAGE = 'kiln:navigate';

/** Pull the `ticket` id out of a `/?ticket=<id>` deep link — a full URL or a bare
 * query string both work. Returns null when the param is absent or empty. */
export function ticketIdFromUrl(url: string): string | null {
  const start = url.indexOf('?');
  if (start === -1) {
    return null;
  }
  const id = new URLSearchParams(url.slice(start)).get('ticket');
  return id !== null && id !== '' ? id : null;
}

/** Open the deep-linked ticket, if any, from either arrival path. `onOpen` should
 * be a stable reference (e.g. a `useState` setter) so the effect wires up once. */
export function useDeepLinkTicket(onOpen: (ticketId: string) => void): void {
  useEffect(() => {
    // Cold-open path: the window was launched at the deep link.
    const initial = ticketIdFromUrl(window.location.search);
    if (initial !== null) {
      onOpen(initial);
      // Drop `?ticket=` so a later manual reload doesn't reopen a dismissed ticket.
      window.history.replaceState(null, '', window.location.pathname);
    }

    // Already-open path: the worker forwards the tap rather than reloading us.
    if (!('serviceWorker' in navigator)) {
      return;
    }
    // Capture the container once so add/remove pair up on the same object even if
    // navigator.serviceWorker is later swapped out from under us.
    const sw = navigator.serviceWorker;
    const onMessage = (event: MessageEvent): void => {
      const data: unknown = event.data;
      if (
        typeof data === 'object' &&
        data !== null &&
        'type' in data &&
        data.type === DEEP_LINK_MESSAGE &&
        'url' in data &&
        typeof data.url === 'string'
      ) {
        const id = ticketIdFromUrl(data.url);
        if (id !== null) {
          onOpen(id);
        }
      }
    };
    sw.addEventListener('message', onMessage);
    return () => {
      sw.removeEventListener('message', onMessage);
    };
  }, [onOpen]);
}
