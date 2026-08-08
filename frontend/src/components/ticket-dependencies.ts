// Resolving a ticket's dependency ids into something a person can read (0013).
//
// The wire carries `depends_on` as bare ids, which is right for the contract and
// useless on screen: "waiting on 2" answers *whether* a ticket is held, and the
// user's next question is always *by what*. The board snapshot already has every
// live ticket, so the titles are a lookup away and no extra request is needed.
//
// A dependency that is not in the snapshot is dropped rather than rendered as a
// bare id. The server only lists LIVE dependencies, so the two cases where that
// happens are a snapshot that has not caught up yet and a Done ticket aged out
// of the retained tail — in both, a row saying nothing is better than a row
// saying `a3f1c9e2-…`, which the user cannot act on either way.
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';

/** One resolved dependency, as the detail sheet renders it. */
export interface TicketDependency {
  id: string;
  title: string;
  /** Whether this one has landed. The list is worth showing in full — not just
   * the outstanding entries — because "2 of 3 done" is the progress a held
   * ticket has, and it is otherwise invisible. */
  done: boolean;
}

/** Every ticket in a snapshot, by id, across all five groups. */
function ticketsById(board: Board): Map<string, Ticket> {
  const all = [...board.shaping, ...board.ready, ...board.blocked, ...board.working, ...board.done];
  return new Map(all.map((ticket) => [ticket.id, ticket]));
}

/**
 * The tickets `ticket` waits for, in the server's order (the order they were
 * added), resolved against the board snapshot.
 *
 * Returns [] when there is no board yet or the ticket waits for nothing, so the
 * caller renders no section rather than an empty one.
 */
export function ticketDependencies(board: Board | null, ticket: Ticket): TicketDependency[] {
  if (board === null || ticket.depends_on.length === 0) {
    return [];
  }
  const byId = ticketsById(board);
  const out: TicketDependency[] = [];
  for (const id of ticket.depends_on) {
    const dep = byId.get(id);
    if (dep !== undefined) {
      out.push({ id, title: dep.title, done: dep.state === 'done' });
    }
  }
  return out;
}
