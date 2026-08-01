// Deep-link → open-ticket bridge for the primary screen (02 §10 tap-to-open).
// The link itself (`/app?project=<id>&ticket=<id>`) and both of its arrival
// paths are described in `@/stores/deep-link`; this hook is only the ticket
// half. Three ways a ticket id reaches us:
//   • cold / backgrounded open: read `?ticket=` once on mount. The query param
//     is stripped after the first read so a later manual reload doesn't reopen a
//     ticket the user already dismissed.
//   • already-open tab, same project: the service worker's `kiln:navigate`
//     message, handled live.
//   • already-open tab, *different* project: the current-project store switches
//     projects, which remounts this screen; the tap's ticket was stashed for us
//     and we take it in the mount effect (12 §6.3).
// All three funnel to `onOpen(ticketId)`, which the screen uses to open the
// ticket's detail overlay.
import { useEffect } from 'react';
import { parseDeepLink, subscribeDeepLink, takeDeepLinkTicket } from '@/stores/deep-link';

/** Open the deep-linked ticket, if any, from any arrival path. `onOpen` should
 * be a stable reference (e.g. a `useState` setter) so the effect wires up once. */
export function useDeepLinkTicket(onOpen: (ticketId: string) => void): void {
  useEffect(() => {
    // A ticket stashed by a cross-project tap wins: we are the screen that
    // mounted *because* of that tap, and the URL is stale (it belongs to
    // whatever this tab was showing before).
    const stashed = takeDeepLinkTicket();
    if (stashed !== null) {
      onOpen(stashed);
    } else {
      // Cold-open path: the window was launched at the deep link.
      const initial = parseDeepLink(window.location.search).ticketId;
      if (initial !== null) {
        onOpen(initial);
        // Drop `?ticket=` so a later manual reload doesn't reopen a dismissed
        // ticket. The whole query goes — `?project=` has already been read by
        // the current-project store during render, before any effect runs.
        window.history.replaceState(null, '', window.location.pathname);
      }
    }

    // Already-open path: the worker forwards the tap rather than reloading us.
    // We open the named ticket unconditionally; when the tap belongs to another
    // project the store's switch remounts us a beat later and the stashed id
    // reopens it against the right board.
    return subscribeDeepLink((link) => {
      if (link.ticketId !== null) {
        onOpen(link.ticketId);
      }
    });
  }, [onOpen]);
}
