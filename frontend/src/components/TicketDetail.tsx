// Read-only detail overlay for a board ticket (07 §7): clicking a card opens
// this to inspect the ticket's full record — complete body, state, priority,
// id, timestamps, and the full blocked reason when present. Inspection only:
// the board stays read-only, all mutation flows through the brain via chat
// (D5). Never traps the user — dismiss via backdrop click, the close button,
// or Escape. Holds no state of its own (02 §11); selection lives in `Board`.
import { useEffect, type JSX } from 'react';
import type { Ticket } from '@/components/TicketCard';

export interface TicketDetailProps {
  ticket: Ticket;
  onClose: () => void;
}

export function TicketDetail({ ticket, onClose }: TicketDetailProps): JSX.Element {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        onClose();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [onClose]);

  return (
    <div data-role="ticket-detail-backdrop" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`Ticket: ${ticket.title}`}
        data-role="ticket-detail"
        data-state={ticket.state}
        onClick={(event) => {
          event.stopPropagation();
        }}
      >
        <header data-role="ticket-detail-header">
          <h2>{ticket.title}</h2>
          <button type="button" data-role="ticket-detail-close" aria-label="Close" onClick={onClose}>
            ×
          </button>
        </header>

        <dl data-role="ticket-detail-meta">
          <div>
            <dt>State</dt>
            <dd data-role="detail-state">{ticket.state}</dd>
          </div>
          <div>
            <dt>Priority</dt>
            <dd data-role="detail-priority">{ticket.priority}</dd>
          </div>
          <div>
            <dt>ID</dt>
            <dd data-role="detail-id">{ticket.id}</dd>
          </div>
          <div>
            <dt>Created</dt>
            <dd data-role="detail-created">{ticket.created_at}</dd>
          </div>
          <div>
            <dt>Updated</dt>
            <dd data-role="detail-updated">{ticket.updated_at}</dd>
          </div>
        </dl>

        {ticket.state === 'blocked' && ticket.blocked_reason != null && (
          <p data-role="detail-blocked-reason">{ticket.blocked_reason}</p>
        )}

        <div data-role="ticket-detail-body">{ticket.body}</div>
      </div>
    </div>
  );
}
