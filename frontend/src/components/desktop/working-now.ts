// What is being worked on right now, derived from the board (13 §8.2, §10).
//
// The desktop shell already had a *liveness* signal — one breathing dot that
// said "something is happening" — but not a *subject*: nothing on the screen
// named the tickets an agent was mid-turn on. The working strip is that answer,
// and this module is its pure half, kept out of the component so the ordering
// and the status vocabulary can be reasoned about (and tested) on their own.
//
// Board, not feed. The feed is the brain's curated narration (08 D1) and a
// ticket can be worked for a long time without earning a card; the board's
// Working bucket is the mechanical truth about which tickets hold a worker
// (03 §2.1). Reading it here is the same choice `project-status` makes for the
// rail, for the same reason.
//
// **Blocked tickets are listed here too (amended 2026-08-09).** The panel used
// to take the blocked bucket as a COUNT — enough to colour the head and to add
// half a sentence to the resting line, never enough to say *which* ticket. So
// the one state that exists to be answered by a person was the one state this
// column could not name, and on a desk the column is where a user looks for the
// list of things standing open: a blocker was reachable only through a feed card
// that scrolls away, or by opening tickets one at a time. The phone has always
// listed them (`ticketStatuses` — active means working *and* blocked) and
// `/kanban` gives them a column of their own; this is the desk's ticket panel
// brought level with both.
import type { Board } from '@/transport/transport';
import type { Ticket } from '@/components/TicketCard';
import type { StreamState } from '@/components/feed-format';

/** The two states a ticket the board has STARTED and not finished can be in —
 * being worked, or stuck waiting on the user. The board's own vocabulary, and
 * the same pair the phone's dropdown calls "active" and the detail sheet gates
 * its sandbox controls on. */
export type ActiveState = 'working' | 'blocked';

/** One started-and-unfinished ticket, as the panel renders it. */
export interface ActiveTicket {
  /** The ticket id — a stable render key, and what opening the row passes on. */
  id: string;
  /** The ticket title, shown verbatim: this is the "what" the strip exists for. */
  title: string;
  /** Working or blocked. Carried on the ROW rather than implied by which list it
   * is in, because both share one list: it is what picks the row's ink (the same
   * value the detail sheet's badge is keyed on) and its word. */
  state: ActiveState;
  /** The bound worker's REAL session state from `board.agents`, not an assumption
   * from the ticket's column. A ticket can sit in Working with a stopped or
   * errored session behind it, and a strip that reported those as "working" would
   * be lying in exactly the place the user is looking for the truth.
   *
   * Null for a blocked ticket, and deliberately so: a session state is how the
   * WORK is faring, and a blocked ticket's work has stopped by definition — the
   * agent asked and is waiting. Carrying one through would texture the mark with
   * a session nobody is watching, and a `building` reading would set the alarm
   * ink breathing, which is more than "one ticket needs a decision" is worth
   * (13 §4). The head has been rendered this way since it learned the blocked
   * state; the rows now match it. */
  status: StreamState | null;
  /** When the ticket entered its current state (`state_changed_at`) — the
   * time-in-status clock. Deliberately not `updated_at`, which a same-state
   * nudge bumps. */
  since: string;
}

/** The word shown beside a title when the session behind it is NOT plainly
 * building. `building` is the expected case and carries no note at all: a row
 * that says "working · working" is noise, and every extra word here is spent
 * from the same quiet budget the rest of the shell is written against. */
const STATUS_NOTE: Record<StreamState, string> = {
  building: '',
  starting: 'starting up',
  idle: 'idle',
  stopped: 'stopped',
  errored: 'failing',
};

export function workingStatusNote(status: StreamState): string {
  return STATUS_NOTE[status];
}

/**
 * The note for one row of the panel.
 *
 * A blocked ticket says so in a WORD, not only in fire. The mark carries the
 * alarm ink, and ink alone is not a reading: it fails anyone who cannot pick the
 * hue out, and on a two-state list it fails everyone at a glance, since the only
 * other thing distinguishing the two rows is a colour and a stopped breath.
 * "needs you" is the rail's phrase for exactly this state, kept verbatim so the
 * two surfaces say the same thing about the same board — and it is stated on
 * every blocked row rather than only when the head is free to say it, because
 * with one ticket building and another stuck the head says "working now" and the
 * row is on its own.
 *
 * It outranks the session vocabulary for the same reason `backlogStateNote`'s
 * dependency label outranks the state word: what the row needs to say is why it
 * is not moving, and for a blocked ticket that is never the worker.
 */
export function activeStatusNote(ticket: Pick<ActiveTicket, 'state' | 'status'>): string {
  if (ticket.state === 'blocked') {
    return 'needs you';
  }
  return ticket.status === null ? '' : STATUS_NOTE[ticket.status];
}

/** What the panel's head reports about the project as a whole. */
export type WorkingPanelState = 'working' | 'blocked' | 'idle';

/** The head's word per state. Lower case because the head is set in small caps
 * by CSS (`text-transform: uppercase`) — spelling it upper case here would
 * double-shout it anywhere the text is read rather than rendered, including the
 * accessible name. */
const PANEL_LABEL: Record<WorkingPanelState, string> = {
  working: 'working now',
  blocked: 'blocked',
  idle: 'idle',
};

/**
 * What the head says, from the board's own buckets.
 *
 * The head used to be the fixed word "working now", which made it a label for
 * the column rather than a reading of it: a project with nothing running still
 * announced work in progress, and the only thing that actually said otherwise
 * was the faint line underneath. So the word follows the board.
 *
 * Working wins over blocked. Both can be true at once — an agent building one
 * ticket while another waits on the user — and this panel's subject is the work
 * in motion. Blocked is what it says when there is nothing in motion to report:
 * a stuck project reads as stuck rather than as idle, which is the difference
 * between "nothing is happening" and "nothing is happening and that is your
 * move".
 *
 * That precedence survives the blocked rows arriving, and is worth restating
 * now that the head no longer has to carry the whole news of a blocker on its
 * own. The head is the PROJECT's reading; each row states its own state, in its
 * own ink, with its own word. It is the same division the rows' session statuses
 * already live under — a failing session under a head that says "working now" is
 * not a contradiction, it is one fact stated at the level it belongs to. Making
 * blocked outrank working here would have flipped it: one stuck ticket would
 * have retitled a column of live work, with the rows underneath saying
 * otherwise.
 *
 * Deliberately NOT keyed on the liveness signal (`active` — the brain mid-pass).
 * That is the breathing dot's job, and the two are different questions: a brain
 * pass over an empty board is something happening, but it is not a ticket being
 * worked, and a head that called it "working now" would be naming work no row
 * under it can show.
 */
export function workingPanelState(tickets: ActiveTicket[]): WorkingPanelState {
  if (tickets.some((ticket) => ticket.state === 'working')) {
    return 'working';
  }
  if (tickets.some((ticket) => ticket.state === 'blocked')) {
    return 'blocked';
  }
  return 'idle';
}

export function workingPanelLabel(state: WorkingPanelState): string {
  return PANEL_LABEL[state];
}

/** Oldest-first by time in state, on a copy — the board snapshot belongs to the
 * store and a sort in place would reorder it for every other reader. */
function oldestFirst(tickets: Ticket[]): Ticket[] {
  return [...tickets].sort(
    (a, b) => new Date(a.state_changed_at).getTime() - new Date(b.state_changed_at).getTime(),
  );
}

/**
 * The started-and-unfinished tickets of one board: blocked first, then working,
 * each group oldest-first.
 *
 * **Blocked leads.** It is the one state on this list that is waiting on the
 * person reading it, and a row that wants a decision has no business sitting
 * below rows that want nothing — on a column read top-down at a glance, last is
 * the same as absent. It is also the stabler half: a blocked ticket sits still
 * until it is answered, while the working group turns over as agents pick things
 * up, so the rows nearest the top are the ones least likely to move.
 *
 * **Within a group the ordering is load-bearing.** Sorted ascending by
 * `state_changed_at`, a ticket entering the group is appended at the BOTTOM of
 * it and no row already on screen moves. Newest-first — the feed's rule — would
 * push every existing row down each time an agent picked something up, which is
 * the reflow the rail is explicitly forbidden (13 §5) and would be just as wrong
 * here: this strip is read at a glance, and a list that reorders under the eye
 * cannot be.
 *
 * The two are one list rather than two sections on purpose. They are both
 * answers to "what is open right now", they carry the same row (mark, title,
 * word, age), and a ticket that blocks moves between them — a second head over a
 * second list would have made that ordinary transition look like the ticket had
 * left the panel and a different one arrived, and would have spent a third
 * heading in a column two headings wide.
 *
 * Returns [] for a null board (before the first snapshot), so the caller falls
 * back to the bare liveness indication rather than claiming nothing is running.
 */
export function activeTickets(board: Board | null): ActiveTicket[] {
  if (board === null) {
    return [];
  }
  const byTicket = new Map<string, StreamState>(
    board.agents.map((agent) => [agent.ticket_id, agent.status]),
  );
  const toBlocked = (ticket: Ticket): ActiveTicket => ({
    id: ticket.id,
    title: ticket.title,
    state: 'blocked',
    status: null,
    since: ticket.state_changed_at,
  });
  const toWorking = (ticket: Ticket): ActiveTicket => ({
    id: ticket.id,
    title: ticket.title,
    state: 'working',
    // `building` is the fallback for a ticket whose first status join is still
    // in flight — it is what the board's Working bucket already asserts, so the
    // row is never blank on the first paint after a pull.
    status: byTicket.get(ticket.id) ?? 'building',
    since: ticket.state_changed_at,
  });
  return [
    ...oldestFirst(board.blocked).map(toBlocked),
    ...oldestFirst(board.working).map(toWorking),
  ];
}
