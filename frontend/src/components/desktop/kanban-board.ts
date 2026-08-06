// The board as columns — the pure half of the `/kanban` view (13 §3 shell, 03
// §2.1 states).
//
// Kept out of the component for the same reason `working-now.ts` is: the column
// set, their order, and what a card knows about the session behind it are all
// decisions worth being able to read and test on their own, without a DOM.
//
// **Board, not feed.** The feed is the brain's curated narration (08 D1) and
// says nothing about tickets it hasn't chosen to mention; the board snapshot is
// the mechanical truth about where every ticket stands (03 §4). A kanban view
// whose columns came off the feed would be missing exactly the quiet tickets a
// board view exists to show.
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';
import type { StreamState } from '@/components/feed-format';

/** A column's key IS the ticket state it holds (03 §2.1) — the board snapshot
 * is already grouped by these, so there is no mapping step and no way for a
 * ticket to land in a column its `state` disagrees with. */
export type KanbanColumnKey = Ticket['state'];

/** The columns, left to right.
 *
 * This is the ticket's own lifecycle read as a pipeline: shaped, queued, being
 * worked, stuck, finished. Blocked sits AFTER working rather than beside it
 * because that is the order a ticket meets them in — a ticket is only ever
 * blocked out of work that had already started — and it puts the column that
 * wants a person next to the one that is running, where the eye already is.
 *
 * Note this deliberately differs from the mobile board's zone grouping
 * (shaping+ready → Backlog, blocked stacked above working → Developing, done),
 * which compresses five states into three for a phone's width. A desk has room
 * to show the states themselves, and a kanban board that hid two of them behind
 * a zone would be the mobile-stretched reading the desktop shell exists to
 * replace (13 §4). */
export const KANBAN_COLUMNS: readonly { key: KanbanColumnKey; label: string }[] = [
  { key: 'shaping', label: 'Shaping' },
  { key: 'ready', label: 'Ready' },
  { key: 'working', label: 'Working' },
  { key: 'blocked', label: 'Blocked' },
  { key: 'done', label: 'Done' },
];

/** One card on the board. */
export interface KanbanCard {
  ticket: Ticket;
  /** The bound worker's REAL session status from `board.agents`, or null when no
   * worker is bound to this ticket at all. Same reasoning as the working strip's:
   * a ticket can sit in Working with a stopped or errored session behind it, and
   * a card that reported that as "working" would be lying in the one place the
   * user is looking for the truth. Null for every backlog and done ticket, which
   * have no session — the absence is why this is nullable rather than defaulted
   * to `building`. */
  status: StreamState | null;
}

export interface KanbanColumn {
  key: KanbanColumnKey;
  label: string;
  cards: KanbanCard[];
}

/**
 * The five columns of one board snapshot, in `KANBAN_COLUMNS` order.
 *
 * **Ticket order inside a column is the server's, untouched.** The snapshot is
 * "grouped in render order" (03 §4) and `ready` in particular is in exact pull
 * order, top-to-bottom, so the user can see what gets pulled next — re-sorting
 * it here by age or priority would quietly destroy the one column whose order
 * carries information. The others are left alone for consistency: if an order is
 * worth changing it is worth changing server-side, where every client gets it.
 *
 * A null board (before the first snapshot) yields the five columns, all empty —
 * the shell renders its own loading line above them, so the geometry is up and
 * still from the first paint rather than assembling itself around the user.
 */
export function kanbanColumns(board: Board | null): KanbanColumn[] {
  const byTicket = new Map<string, StreamState>(
    (board?.agents ?? []).map((agent) => [agent.ticket_id, agent.status]),
  );
  return KANBAN_COLUMNS.map(({ key, label }) => ({
    key,
    label,
    cards: (board?.[key] ?? []).map((ticket) => ({
      ticket,
      status: byTicket.get(ticket.id) ?? null,
    })),
  }));
}
