// Image-snapshot target (07 §9): all five board states, and Blocked cards
// with the full `blocked_reason` (07 §7 — with push deferred, the Blocked
// zone is the notification surface, so this card must read as the loudest
// thing on the page). Render is a placeholder; the solution phase supplies
// the real per-state styling.
import type { JSX } from 'react';
import type { components } from '@/schema/generated';

export type Ticket = components['schemas']['Ticket'];

export interface TicketCardProps {
  ticket: Ticket;
}

export function TicketCard({ ticket }: TicketCardProps): JSX.Element {
  return (
    <article data-role="ticket-card" data-state={ticket.state}>
      <h3>{ticket.title}</h3>
      <p data-role="body-preview">{ticket.body}</p>
      {ticket.state === 'blocked' && ticket.blocked_reason != null && (
        <p data-role="blocked-reason">{ticket.blocked_reason}</p>
      )}
    </article>
  );
}
