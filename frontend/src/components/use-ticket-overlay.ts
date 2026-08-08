// What a shell REMEMBERS about the open ticket (shell-architecture plan, layer
// L3). One hook, mounted once per shell, holding the state cluster that was
// previously copied identically into all three: which ticket is open, how it
// closes, the deep-link subscription, and whether a voice session is live inside
// the sheet.
//
// Two properties of this hook are load-bearing rather than incidental:
//
//   * **The state is per-mount, and that is correct.** The voice-active flag in
//     particular is per-shell by design (the sheet's footer rearranges around
//     it), and only one shell is ever mounted — the mobile/desktop switch in
//     `PrimaryScreen` is exclusive. That exclusivity is also what keeps
//     `useDeepLinkTicket` to exactly one subscription; call this hook once per
//     shell and never twice in one tree.
//   * **`board` may be null**, in every shell, and everything here tolerates it:
//     before the first snapshot lands there is simply no ticket to resolve.
//
// The open ticket is re-resolved against the live board on every render rather
// than captured, so the overlay drains on its own when the ticket leaves the
// board (after an Accept, say) instead of holding a stale copy.
import { useCallback, useState } from 'react';
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';
import { findTicket } from '@/components/feed-model';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
import { ticketDependencies, type TicketDependency } from '@/components/ticket-dependencies';

export interface TicketOverlay {
  /** The id the shell is trying to show, whether or not it resolves. */
  openTicketId: string | null;
  /** The resolved ticket, or null — which is also "no sheet is open". */
  openTicket: Ticket | null;
  /** The open ticket's bound session, from the board's `agents` join. It feeds
   * the sheet's gear-menu status line and nothing else — Poke is deliberately
   * NOT gated on it (see TicketDetail's `onPoke`). */
  openAgentStatus: string | undefined;
  /** Whether there is a free sandbox to move the open ticket to, from the board
   * snapshot's `worker_free` — so the user is never walked into the server's
   * 409. */
  canReassign: boolean;
  /** The tickets the open one waits for (0013), resolved to titles against the
   * same snapshot. Derived here for the same reason as `openAgentStatus`: it is
   * a fact about the open ticket that only the board can answer, and deriving it
   * once means all three shells render the sheet from one wiring. */
  openDependencies: TicketDependency[];
  /** Open a ticket by id. Handed straight to every affordance that opens one: a
   * feed card, the header dropdown, a board card, an activity pill. */
  setOpenTicketId: (ticketId: string) => void;
  /** Close the sheet. Stable, because the sheet and several in-sheet actions all
   * close through it. */
  closeTicket: () => void;
  /** Whether a voice session is live in the open sheet. A boolean on purpose: it
   * changes twice an utterance rather than once a word, so the shell re-renders
   * when the footer's shape changes and not on transcript churn. */
  voiceActive: boolean;
  setVoiceActive: (active: boolean) => void;
}

export function useTicketOverlay(board: Board | null): TicketOverlay {
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  const closeTicket = useCallback((): void => {
    setOpenTicketId(null);
  }, []);
  // A tapped push notification deep-links here (02 §10): open the ticket it
  // names, whether the app was opened fresh at `/app?ticket=<id>` or handed the
  // tap live by the service worker. The id resolves against the board below like
  // any other open.
  useDeepLinkTicket(setOpenTicketId);
  const [voiceActive, setVoiceActive] = useState(false);

  const openTicket = findTicket(board, openTicketId);
  const openAgentStatus =
    openTicket === null
      ? undefined
      : board?.agents.find((agent) => agent.ticket_id === openTicket.id)?.status;

  return {
    openTicketId,
    openTicket,
    openAgentStatus,
    canReassign: (board?.worker_free ?? 0) > 0,
    openDependencies: openTicket === null ? [] : ticketDependencies(board, openTicket),
    setOpenTicketId,
    closeTicket,
    voiceActive,
    setVoiceActive,
  };
}
