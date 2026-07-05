// Ticket detail overlay (debug view). Opening a board card shows the ticket's
// full record — everything the card elides: the complete body, priority,
// timestamps, id, and (when blocked) the full blocked reason. This is read-only
// inspection layered over the read-only board (D5); it never mutates state, so
// there is no edit affordance here. Dismiss is deliberately low-friction —
// backdrop click, the close button, or Escape — never a trap the user cannot
// get out of (07 §7–§8).
import { useEffect, type JSX } from 'react';
import type { Ticket } from '@/components/TicketCard';
import '@/components/TicketDetail.css';

export interface TicketDetailProps {
  ticket: Ticket;
  onClose: () => void;
  /** When provided, the detail is a proposal reached via click-through and shows
   * an Accept action (08 §5) — accept after reading the full ticket. Omitted →
   * the overlay stays strictly read-only (the debug board's inspection use, D5). */
  onAccept?: (ticketId: string) => void;
}

/** A labelled row in the metadata list, omitted entirely when the value is null. */
function MetaRow({ label, value }: { label: string; value: string | null }): JSX.Element | null {
  if (value === null) {
    return null;
  }
  return (
    <div data-role="detail-row">
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

export function TicketDetail({ ticket, onClose, onAccept }: TicketDetailProps): JSX.Element {
  // Escape closes the overlay from anywhere (07 §8 — never trap the user).
  useEffect(() => {
    function handleKey(event: KeyboardEvent): void {
      if (event.key === 'Escape') {
        onClose();
      }
    }
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('keydown', handleKey);
    };
  }, [onClose]);

  return (
    <div data-role="ticket-detail-backdrop" onClick={onClose}>
      <section
        role="dialog"
        aria-modal="true"
        aria-label={`Ticket: ${ticket.title}`}
        data-role="ticket-detail"
        data-state={ticket.state}
        // Clicks inside the panel must not fall through to the backdrop's close.
        onClick={(event) => {
          event.stopPropagation();
        }}
      >
        <header data-role="ticket-detail-header">
          <h2 data-role="ticket-detail-title">{ticket.title}</h2>
          <button
            type="button"
            data-role="ticket-detail-close"
            aria-label="Close"
            onClick={onClose}
          >
            ×
          </button>
        </header>

        <dl data-role="ticket-detail-meta">
          <MetaRow label="State" value={ticket.state} />
          <MetaRow label="Priority" value={String(ticket.priority)} />
          <MetaRow label="ID" value={ticket.id} />
          <MetaRow label="Created" value={ticket.created_at} />
          <MetaRow label="Updated" value={ticket.updated_at} />
          <MetaRow label="Ready" value={ticket.ready_at ?? null} />
        </dl>

        {ticket.state === 'blocked' && ticket.blocked_reason != null && (
          <p data-role="detail-blocked-reason">{ticket.blocked_reason}</p>
        )}

        <div data-role="ticket-detail-body">{ticket.body}</div>

        {onAccept !== undefined && (
          <div data-role="ticket-detail-actions">
            <button
              type="button"
              data-role="detail-accept"
              onClick={() => {
                onAccept(ticket.id);
              }}
            >
              Accept
            </button>
          </div>
        )}
      </section>
    </div>
  );
}
