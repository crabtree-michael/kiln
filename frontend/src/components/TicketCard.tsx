// Image-snapshot target (07 §9): all five board states, and Blocked cards
// with the full `blocked_reason` (07 §7 — with push deferred, the Blocked
// zone is the notification surface, so this card must read as the loudest
// thing on the page). Render is a placeholder; the solution phase supplies
// the real per-state styling.
import type { JSX, KeyboardEvent } from 'react';
import type { components } from '@/schema/generated';

export type Ticket = components['schemas']['Ticket'];

export interface TicketCardProps {
  ticket: Ticket;
  /** When supplied, the card becomes a keyboard-operable button that opens the
   * ticket's read-only detail (click or Enter/Space). Board stays read-only —
   * this is inspection, not mutation (D5). Omit it and the card is inert and
   * DOM-identical, so existing snapshots are unchanged. */
  onSelect?: (ticket: Ticket) => void;
}

export function TicketCard({ ticket, onSelect }: TicketCardProps): JSX.Element {
  const interactive =
    onSelect != null
      ? {
          role: 'button' as const,
          tabIndex: 0,
          'aria-label': `Open ticket: ${ticket.title}`,
          onClick: () => {
            onSelect(ticket);
          },
          onKeyDown: (event: KeyboardEvent<HTMLElement>) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              onSelect(ticket);
            }
          },
        }
      : {};

  return (
    <article data-role="ticket-card" data-state={ticket.state} {...interactive}>
      <h3>{ticket.title}</h3>
      <p data-role="body-preview">{ticket.body}</p>
      {ticket.state === 'blocked' && ticket.blocked_reason != null && (
        <p data-role="blocked-reason">{ticket.blocked_reason}</p>
      )}
    </article>
  );
}
