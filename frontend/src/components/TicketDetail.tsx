// Ticket detail sheet. Opening a board card slides a bottom sheet up into view
// from the bottom edge (a classic mobile sheet) showing the ticket's full
// record — everything the card elides: the complete body, priority, timestamps,
// id, and (when blocked) the full blocked reason. This is read-only inspection
// layered over the read-only board (D5); it never mutates state, so there is no
// edit affordance here.
//
// The slide-up entrance + native rubber-band overscroll and drag-to-dismiss come
// from `vaul` (direction="bottom") — the standard React drawer/sheet, adopted
// with explicit user sign-off waiving the former blanket no-library rule
// (07 D4). Vaul owns dismissal entirely: dragging the sheet back down past the
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
   * the overlay stays strictly read-only (the debug board's inspection use, D5).
   * Accept only appears while the ticket is still shaping: accepting is what
   * moves a shaped proposal into the pull, so every later state (ready, working,
   * blocked, done) has already passed that point and shows no button regardless. */
  onAccept?: (ticketId: string) => void;
  /** When provided on a *blocked* ticket, the Accept action is replaced by a Talk
   * button — the blocked work can't be accepted, only discussed. Tapping it hands
   * off to the voice pipeline so the user can tell the brain how to unblock (the
   * caller closes the sheet and turns the mic on). Omitted → no Talk affordance
   * (the debug board's read-only inspection, and non-blocked states). */
  onTalk?: () => void;
  /** When provided on a *working* or *blocked* ticket, a "Poke to continue" button
   * appears — a manual nudge for a stalled agent, mirroring the steward's own
   * mechanical poke. Tapping it expresses the user's "continue" intent for this
   * ticket; the caller routes that through the brain (which decides to
   * send_to_agent(id, "continue")) — the client never commands an agent directly
   * (D5). Gated on working|blocked because those are the only board states where an
   * agent exists and can be stalled; there is no per-ticket idle signal on the wire,
   * so working stands in for "agent alive but possibly idle". Omitted → no Poke
   * affordance (the debug board's read-only inspection). */
  onPoke?: ((ticketId: string) => void) | undefined;
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

/** The header status badge — a dot + word pinned to the header's top-right that
 * names the ticket's lifecycle state at a glance, so it's always obvious what's
 * happening with the work (07 §7). Only the three states carrying a clear signal
 * get one, each in its own semantic colour:
 *   • working → "In progress" (ember, pulsing — the eye-drawing live state)
 *   • blocked → "Blocked" (fire — the loudest surface; the full reason renders
 *               below the header)
 *   • done    → "Done" (glaze/all-clear)
 * shaping/ready are the neutral "awaiting action" states and wear no badge —
 * shaping instead offers the Accept button. */
const STATUS_LABELS: Partial<Record<Ticket['state'], string>> = {
  working: 'In progress',
  blocked: 'Blocked',
  done: 'Done',
};

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
  onTalk,
  onPoke,
  showInternalMeta = false,
  surface = 'debug',
}: TicketDetailProps): JSX.Element {
  // Which affordances the sheet's footer carries is decided purely by lifecycle
  // state, so the caller can't wire a nonsensical one:
  //  • shaping         → Accept (when wired): the proposal click-through (08 §5) —
  //                      accepting is what moves a shaped proposal into the pull,
  //                      so it only makes sense here. Every later state has already
  //                      been accepted, so the button is gone.
  //  • blocked         → Talk (when wired): the work can't be accepted, only
  //                      unblocked through a conversation with the brain.
  //  • working|blocked → Poke (when wired): a manual nudge to continue for a
  //                      stalled agent, routed through the brain (never a direct
  //                      agent command, D5). Coexists with Talk on a blocked ticket.
  //  • done            → no action; the header badge already says "Done".
  // The footer branches below narrow on the callbacks directly (not derived
  // booleans) so TypeScript knows they're defined inside the handler — no
  // optional chain, which the lint gate rejects (mirrors FeedCardItem).
  const isShaping = ticket.state === 'shaping';
  const isBlocked = ticket.state === 'blocked';
  const isWorking = ticket.state === 'working';
  const statusLabel = STATUS_LABELS[ticket.state];
  // Whether each footer action can appear: the ticket's lifecycle state plus
  // whether the caller wired the callback. These decide only if the actions row
  // renders at all — each button below re-checks its own callback directly so
  // TypeScript narrows it to defined (a derived boolean wouldn't narrow, and the
  // lint gate rejects the optional chain the alternative would need).
  const canPoke = (isWorking || isBlocked) && onPoke !== undefined;
  const canTalk = isBlocked && onTalk !== undefined;
  const canAccept = isShaping && onAccept !== undefined;
  return (
    // `open` is fixed true: this component only mounts while a ticket is
    // selected, so Vaul's own open/closed state just mirrors that. Every dismiss
    // path (drag past threshold, scrim click, Escape) fires onOpenChange(false),
    // which we forward to onClose — the caller then unmounts us.
    <Drawer.Root
      // Bottom-anchored: slides up into view from the bottom edge (07 §7 — a
      // classic mobile sheet).
      direction="bottom"
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
          {/* A bottom sheet's drag affordance sits on its upper edge — the
              grabber is the first child, above the header. */}
          <Drawer.Handle data-role="ticket-detail-grabber" />

          <header data-role="ticket-detail-header">
            {/* Title and its lifecycle badge stack in a left-aligned column so the
                title gets the full header width instead of ceding room to a badge
                on its right. */}
            <div data-role="ticket-detail-heading">
              <Drawer.Title data-role="ticket-detail-title">{ticket.title}</Drawer.Title>
              {/* The lifecycle badge: a dot + word directly under the title that
                  names the ticket's state at a glance (In progress / Blocked /
                  Done), each in its own colour. Only the states that carry a
                  signal show one; shaping/ready wear none. Keyed on data-state
                  (not Radix's own data-state, which lives on the panel) for its
                  per-state colour. */}
              {statusLabel !== undefined && (
                <span data-role="ticket-detail-status" data-state={ticket.state}>
                  <span data-role="ticket-detail-status-dot" aria-hidden="true" />
                  {statusLabel}
                </span>
              )}
            </div>
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

          {/* Footer actions. Which affordances appear is decided purely by the
              ticket's lifecycle state and which callbacks the caller wired, so a
              nonsensical action can't be shown:
               • Poke   → working|blocked: nudge a stalled agent to continue. Only
                          expresses intent — the caller routes it through the brain
                          (D5), never a direct agent command.
               • Talk   → blocked: hand off to voice to discuss the unblock.
               • Accept → the proposal click-through (08 §5), shaping-only (every
                          later state has already been accepted).
              Poke sits first (left); the state's primary action (Talk/Accept) stays
              rightmost, where flex-end makes it the most prominent. Each button
              narrows on its callback directly inside the guard so TypeScript knows
              it's defined in the handler — no optional chain (the lint gate). */}
          {(canPoke || canTalk || canAccept) && (
            <div data-role="ticket-detail-actions">
              {(isWorking || isBlocked) && onPoke !== undefined && (
                <button
                  type="button"
                  data-role="detail-poke"
                  onClick={() => {
                    onPoke(ticket.id);
                  }}
                >
                  <svg
                    viewBox="0 0 24 24"
                    width="16"
                    height="16"
                    aria-hidden="true"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M6 5l10 7-10 7z" />
                    <path d="M20 5v14" />
                  </svg>
                  Poke to continue
                </button>
              )}
              {isBlocked && onTalk !== undefined && (
                <button
                  type="button"
                  data-role="detail-talk"
                  onClick={() => {
                    onTalk();
                  }}
                >
                  <svg
                    viewBox="0 0 24 24"
                    width="18"
                    height="18"
                    aria-hidden="true"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <rect x="9" y="3" width="6" height="11" rx="3" />
                    <path d="M5 11a7 7 0 0 0 14 0" />
                    <path d="M12 18v3" />
                  </svg>
                  Talk to unblock
                </button>
              )}
              {isShaping && onAccept !== undefined && (
                <button
                  type="button"
                  data-role="detail-accept"
                  onClick={() => {
                    onAccept(ticket.id);
                  }}
                >
                  Accept
                </button>
              )}
            </div>
          )}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}
