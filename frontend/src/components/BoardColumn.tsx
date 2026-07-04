// Image-snapshot target (07 §9): zone stacking within a column — Backlog
// stacks Shaping over Ready; Developing stacks Blocked above Working (01 §5,
// 07 §7). `zones` is ordered top-to-bottom by the caller (`Board`); this
// component only renders the stack, it does not decide the order.
import type { JSX } from 'react';
import { TicketCard, type Ticket } from '@/components/TicketCard';

export interface BoardColumnZone {
  /** e.g. "Shaping", "Ready", "Blocked", "Working", "Done". */
  label: string;
  tickets: Ticket[];
  /** Blocked is the loudest surface on the page in v1 (07 §7). */
  emphasis?: 'default' | 'loud';
}

export interface BoardColumnProps {
  /** e.g. "Backlog", "Developing", "Done". */
  title: string;
  zones: BoardColumnZone[];
  /** Forwarded to each card's `onSelect` to open its read-only detail. When
   * omitted, cards render inert (see `TicketCard`). */
  onSelectTicket?: (ticket: Ticket) => void;
}

export function BoardColumn({ title, zones, onSelectTicket }: BoardColumnProps): JSX.Element {
  return (
    <section aria-label={title} data-role="board-column">
      <h2>{title}</h2>
      {zones.map((zone) => (
        <div key={zone.label} data-role="board-zone" data-emphasis={zone.emphasis ?? 'default'}>
          <h3>{zone.label}</h3>
          <div data-role="board-zone-tickets">
            {zone.tickets.map((ticket) => (
              <TicketCard
                key={ticket.id}
                ticket={ticket}
                {...(onSelectTicket ? { onSelect: onSelectTicket } : {})}
              />
            ))}
          </div>
        </div>
      ))}
    </section>
  );
}
