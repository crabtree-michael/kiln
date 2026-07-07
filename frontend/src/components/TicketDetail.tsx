// Ticket detail sheet. Opening a board card slides a top-anchored sheet down
// into view from the top edge (a classic notification/popover sheet) showing the
// ticket's full record — everything the card elides: the complete body,
// priority, timestamps, id, and (when blocked) the full blocked reason. This is
// read-only inspection layered over the read-only board (D5); it never mutates
// state, so there is no edit affordance here.
//
// The slide-down entrance + native rubber-band overscroll and drag-to-dismiss
// come from `vaul` (direction="top") — the standard React drawer/sheet, adopted
// with explicit user sign-off waiving the former blanket no-library rule
// (07 D4). Vaul owns dismissal entirely: dragging the sheet back up past the
// threshold, clicking the scrim, and pressing Escape all route through
// `onOpenChange(false)` → `onClose`, so this component adds none of that by
// hand — dismiss stays low-friction, never a trap (07 §7–§8).
import { type JSX } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Drawer } from 'vaul';
import type { Ticket } from '@/components/TicketCard';
import '@/components/TicketDetail.css';

export interface TicketDetailProps {
  ticket: Ticket;
  onClose: () => void;
  /** When provided, the detail is a proposal reached via click-through and shows
   * an Accept action (08 §5) — accept after reading the full ticket. Omitted →
   * the overlay stays strictly read-only (the debug board's inspection use, D5). */
  onAccept?: (ticketId: string) => void;
  /** Show the internal bookkeeping rows (state, priority, id, timestamps). Off by
   * default: the main app view shows only the title and description. The /debug
   * board opts in to inspect a ticket's full record (D5). */
  showInternalMeta?: boolean;
  /** Which surface's skin to wear. The sheet portals to `document.body` (so its
   * fixed positioning escapes any transformed/clipping ancestor), which lifts it
   * out of the `[data-role='primary-screen']` subtree the skin CSS used to key
   * off — so the surface is now carried explicitly on the panel as
   * `data-surface`. Defaults to the /debug board's denser register; the primary
   * screen passes `'primary'` for the app's first-class card skin (08 §5). */
  surface?: 'debug' | 'primary';
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

export function TicketDetail({
  ticket,
  onClose,
  onAccept,
  showInternalMeta = false,
  surface = 'debug',
}: TicketDetailProps): JSX.Element {
  return (
    // `open` is fixed true: this component only mounts while a ticket is
    // selected, so Vaul's own open/closed state just mirrors that. Every dismiss
    // path (drag past threshold, scrim click, Escape) fires onOpenChange(false),
    // which we forward to onClose — the caller then unmounts us.
    <Drawer.Root
      // Top-anchored: slides down into view from the top edge (07 §7 — a
      // notification/popover sheet, not a bottom drawer).
      direction="top"
      open
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      <Drawer.Portal>
        <Drawer.Overlay data-role="ticket-detail-backdrop" />
        <Drawer.Content
          // Radix (Vaul's base) owns role="dialog"/aria-modal and writes its own
          // data-state=open|closed for the slide animation — so the ticket's
          // lifecycle state rides on data-ticket-state to avoid clobbering it,
          // and the surface skin on data-surface (see the prop's doc). The dialog
          // is named by its <Drawer.Title> (the visible ticket title) via the
          // aria-labelledby Radix wires up, so no aria-label is needed here.
          // No description element; tell Radix so on purpose rather than warn.
          aria-describedby={undefined}
          data-role="ticket-detail"
          data-ticket-state={ticket.state}
          data-surface={surface}
        >
          <header data-role="ticket-detail-header">
            <Drawer.Title data-role="ticket-detail-title">{ticket.title}</Drawer.Title>
            <button
              type="button"
              data-role="ticket-detail-close"
              aria-label="Close"
              onClick={onClose}
            >
              ×
            </button>
          </header>

          {showInternalMeta && (
            <dl data-role="ticket-detail-meta">
              <MetaRow label="State" value={ticket.state} />
              <MetaRow label="Priority" value={String(ticket.priority)} />
              <MetaRow label="ID" value={ticket.id} />
              <MetaRow label="Created" value={ticket.created_at} />
              <MetaRow label="Updated" value={ticket.updated_at} />
              <MetaRow label="Ready" value={ticket.ready_at ?? null} />
            </dl>
          )}

          {ticket.state === 'blocked' && ticket.blocked_reason != null && (
            <p data-role="detail-blocked-reason">{ticket.blocked_reason}</p>
          )}

          <div data-role="ticket-detail-body">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{ticket.body}</ReactMarkdown>
          </div>

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

          {/* A top sheet's drag affordance sits on its lower edge — the grabber
              is the last child, pinned below the scrolling body. */}
          <Drawer.Handle data-role="ticket-detail-grabber" />
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}
