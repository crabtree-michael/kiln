// What is queued up but not started, derived from the board — the second half of
// the desktop shell's left panel (13 §8.2, extending the in-progress column).
//
// The panel answered one standing question ("what is running right now") and
// stopped there, so everything *waiting* was invisible at a desk: an accepted
// ticket sits in Ready until a worker frees up, and a proposal sits in Shaping
// until the user accepts it, and neither earns a feed card for the whole of that
// wait. The phone has always answered this — the header's tickets dropdown lists
// the ready backlog under the active tickets (`ticketStatuses` in feed-format) —
// and the desk had nothing equivalent.
//
// Board, not feed, for exactly the reason the working strip reads the board: the
// feed is the brain's curated narration (08 D1), and a ticket can wait a long
// time without the brain having anything to say about it.
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';

/** The two states that make up the backlog. This is the board's own vocabulary
 * — the same pair `shape_ticket`'s precondition names, and the same pair the
 * detail sheet lets a user edit the text of ("still in the backlog"). */
export type BacklogState = 'ready' | 'shaping';

/** One ticket waiting to be worked, as the panel renders it. */
export interface BacklogTicket {
  /** The ticket id — a stable render key, and what opening the row passes on. */
  id: string;
  /** The ticket title, shown verbatim: the "what" the row exists for. */
  title: string;
  /** Ready or shaping. Carried through rather than flattened away because the
   * two are waiting on different things — a ready ticket waits on a free worker,
   * a shaping one waits on the user — and the row states which. */
  state: BacklogState;
  /** When the ticket entered its current state (`state_changed_at`) — the
   * time-in-status clock, so a row can say how long it has been waiting.
   * Deliberately not `updated_at`, which a same-state nudge bumps. */
  since: string;
}

/** The word shown beside a title when the ticket is NOT simply queued. `ready`
 * is the expected case in a backlog and carries no note at all — a row reading
 * "ready · ready" is noise, and every extra word here is spent from the same
 * quiet budget the rest of the shell is written against (13 §1). A shaping
 * ticket is a proposal that has not been accepted yet, which is a different kind
 * of wait, so that one is named. */
const STATE_NOTE: Record<BacklogState, string> = {
  ready: '',
  shaping: 'proposal',
};

export function backlogStateNote(state: BacklogState): string {
  return STATE_NOTE[state];
}

function toBacklogTicket(state: BacklogState) {
  return (ticket: Ticket): BacklogTicket => ({
    id: ticket.id,
    title: ticket.title,
    state,
    since: ticket.state_changed_at,
  });
}

/**
 * The waiting tickets of one board: the ready queue first, then shaping.
 *
 * **The server's order is kept, and that is load-bearing.** Ready arrives in
 * exact pull order (03 §5 / D9 — priority DESC, ready_at ASC, id ASC), so the
 * top row is genuinely the next ticket a freed worker will take; shaping arrives
 * priority-first then oldest-first (03 §4). Re-sorting either here — by
 * `created_at`, say, the way the phone's dropdown approximates it — would throw
 * away the one thing this list's order actually carries. The phone's rule is a
 * lossy stand-in for this one, not a different intent.
 *
 * Ready leads because it is nearer: those tickets are decided and queued, and
 * the question "what happens next" is answered at the top of the list. Shaping
 * follows — still a proposal, still waiting on a person.
 *
 * Returns [] for a null board (before the first snapshot), so the caller shows
 * its resting line rather than claiming an empty backlog.
 */
export function backlogTickets(board: Board | null): BacklogTicket[] {
  if (board === null) {
    return [];
  }
  return [
    ...board.ready.map(toBacklogTicket('ready')),
    ...board.shaping.map(toBacklogTicket('shaping')),
  ];
}
