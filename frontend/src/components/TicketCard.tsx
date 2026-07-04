// Image-snapshot target (07 §9): all five board states, and Blocked cards
// with the full `blocked_reason` (07 §7 — with push deferred, the Blocked
// zone is the notification surface, so this card must read as the loudest
// thing on the page). Render is a placeholder; the solution phase supplies
// the real per-state styling.
//
// Opt-in interactivity (debug view): when `onSelect` is supplied the card
// becomes a keyboard-operable button that opens the ticket detail overlay. This
// is read-only inspection, not mutation — the board stays drag-free and every
// state change still flows through the brain (D5). Without `onSelect` the card
// renders exactly as before, so the existing DOM snapshots are unchanged.
import type { HTMLAttributes, JSX } from 'react';
import type { components } from '@/schema/generated';

export type Ticket = components['schemas']['Ticket'];

export interface TicketCardProps {
  ticket: Ticket;
  /** When provided, clicking or pressing Enter/Space selects the ticket. */
  onSelect?: ((ticket: Ticket) => void) | undefined;
}

export function TicketCard({ ticket, onSelect }: TicketCardProps): JSX.Element {
  // Narrow on `onSelect` directly (not a derived boolean) so TypeScript knows
  // it is defined inside the handlers — no optional chain, which the lint gate
  // rejects as unnecessary here.
  const interactive = onSelect !== undefined;
  const interactiveProps: HTMLAttributes<HTMLElement> =
    onSelect === undefined
      ? {}
      : {
          role: 'button',
          tabIndex: 0,
          'aria-label': `Open ticket: ${ticket.title}`,
          onClick: () => {
            onSelect(ticket);
          },
          onKeyDown: (event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              onSelect(ticket);
            }
          },
        };

  return (
    <article
      data-role="ticket-card"
      data-state={ticket.state}
      data-interactive={interactive ? 'true' : undefined}
      {...interactiveProps}
    >
      <h3>{ticket.title}</h3>
      <p data-role="body-preview">{ticket.body}</p>
      {ticket.state === 'blocked' && ticket.blocked_reason != null && (
        <p data-role="blocked-reason">{ticket.blocked_reason}</p>
      )}
    </article>
  );
}
