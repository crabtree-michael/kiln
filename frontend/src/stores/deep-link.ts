// Push deep-link plumbing (02 §10 tap-to-open, 12 §6.3 tap→project). A tapped
// notification's deep link is `/app?project=<id>[&ticket=<id>]` (built by
// `notifyURL` in cmd/kiln/adapters.go), and it arrives by one of two paths:
//   • cold / backgrounded: the service worker opens a fresh window at that URL,
//     so the link is `window.location.search` at mount;
//   • already-open tab: the worker can't reload it without dropping the live
//     session (the attached voice channel, 02 §10), so it postMessages
//     `{ type: 'kiln:navigate', url }` to the focused client instead.
//
// Two consumers split the link: the current-project store takes `project` and
// switches the app to the firing project (12 §6.3), and the primary screen takes
// `ticket` and opens its detail overlay (`use-deep-link-ticket`). Both parse
// through here so the two halves can never drift apart.
//
// This module is deliberately free of React and of the transport — it is pure
// URL parsing plus one service-worker subscription — so both a store and a
// component hook can depend on it without a layering inversion.

/** postMessage type the service worker sends a focused tab on notification tap. */
export const DEEP_LINK_MESSAGE = 'kiln:navigate';

/** The two ids a notification deep link can carry. Either may be absent: a
 * ticketless notify lands the app on the project alone, and a link with no
 * project (an older payload) just opens the ticket wherever the user is. */
export interface DeepLink {
  projectId: string | null;
  ticketId: string | null;
}

/** Read one query param out of a deep link — a full URL or a bare query string
 * both work. Returns null when the param is absent or empty. */
function param(url: string, name: string): string | null {
  const start = url.indexOf('?');
  if (start === -1) {
    return null;
  }
  const value = new URLSearchParams(url.slice(start)).get(name);
  return value !== null && value !== '' ? value : null;
}

/** Pull the project + ticket ids out of a `/app?project=<id>&ticket=<id>` deep
 * link (a full URL or a bare query string). */
export function parseDeepLink(url: string): DeepLink {
  return { projectId: param(url, 'project'), ticketId: param(url, 'ticket') };
}

/** Listen for taps forwarded by the service worker to this already-open tab.
 * Returns an unsubscribe; a no-op where service workers are unavailable. */
export function subscribeDeepLink(onLink: (link: DeepLink) => void): () => void {
  if (!('serviceWorker' in navigator)) {
    return () => {
      // Nothing subscribed — no service worker in this environment.
    };
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
      onLink(parseDeepLink(data.url));
    }
  };
  sw.addEventListener('message', onMessage);
  return () => {
    sw.removeEventListener('message', onMessage);
  };
}

// --- ticket handoff across a project switch -------------------------------
//
// A cross-project tap has to do two things at once, and they fight: switching
// the current project remounts the whole data subtree (it is keyed by project
// id, 12 §4.1), which throws away the primary screen's just-opened-ticket state.
// So the switching side parks the tap's ticket id here and the *remounted*
// screen picks it up in its mount effect — the same hand-off the cold-open path
// gets for free from the URL. Module-level (not context) precisely because it
// must outlive the subtree that is being torn down.
let pendingTicketId: string | null = null;

/** Park a tapped ticket id for the screen that mounts after a project switch. */
export function stashDeepLinkTicket(ticketId: string): void {
  pendingTicketId = ticketId;
}

/** Take the parked ticket id, clearing it — it opens exactly once. */
export function takeDeepLinkTicket(): string | null {
  const ticketId = pendingTicketId;
  pendingTicketId = null;
  return ticketId;
}
