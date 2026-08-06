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
import type { Board } from '@/transport/transport';
import type { StreamState } from '@/components/feed-format';

/** One ticket an agent is currently bound to, as the strip renders it. */
export interface WorkingTicket {
  /** The ticket id — a stable render key, and what opening the row passes on. */
  id: string;
  /** The ticket title, shown verbatim: this is the "what" the strip exists for. */
  title: string;
  /** The bound worker's REAL session state from `board.agents`, not an assumption
   * from the ticket's column. A ticket can sit in Working with a stopped or
   * errored session behind it, and a strip that reported those as "working" would
   * be lying in exactly the place the user is looking for the truth. */
  status: StreamState;
  /** When the ticket entered Working (`state_changed_at`) — the time-in-status
   * clock. Deliberately not `updated_at`, which a same-state nudge bumps. */
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
 * in motion, which it can also NAME underneath. Blocked is what it says when
 * there is nothing in motion to report: a stuck project reads as stuck rather
 * than as idle, which is the difference between "nothing is happening" and
 * "nothing is happening and that is your move".
 *
 * Deliberately NOT keyed on the liveness signal (`active` — the brain mid-pass).
 * That is the breathing dot's job, and the two are different questions: a brain
 * pass over an empty board is something happening, but it is not a ticket being
 * worked, and a head that called it "working now" would be naming work no row
 * under it can show.
 */
export function workingPanelState(working: number, blocked: number): WorkingPanelState {
  if (working > 0) {
    return 'working';
  }
  if (blocked > 0) {
    return 'blocked';
  }
  return 'idle';
}

export function workingPanelLabel(state: WorkingPanelState): string {
  return PANEL_LABEL[state];
}

/** How many tickets are waiting on the user, for `workingPanelState`. Null board
 * (before the first snapshot) counts as none: an unknown board must not raise an
 * alarm the user would have to go check. */
export function blockedCount(board: Board | null): number {
  return board?.blocked.length ?? 0;
}

/**
 * The Working tickets of one board, oldest-started first.
 *
 * **The ordering is load-bearing.** Sorted ascending by `state_changed_at`, a
 * ticket that starts being worked is appended at the BOTTOM and no row already
 * on screen moves. Newest-first — the feed's rule — would push every existing
 * row down each time an agent picked something up, which is the reflow the rail
 * is explicitly forbidden (13 §5) and would be just as wrong here: this strip is
 * read at a glance, and a list that reorders under the eye cannot be.
 *
 * Returns [] for a null board (before the first snapshot), so the caller falls
 * back to the bare liveness indication rather than claiming nothing is running.
 */
export function workingTickets(board: Board | null): WorkingTicket[] {
  if (board === null) {
    return [];
  }
  const byTicket = new Map<string, StreamState>(
    board.agents.map((agent) => [agent.ticket_id, agent.status]),
  );
  return [...board.working]
    .sort((a, b) => new Date(a.state_changed_at).getTime() - new Date(b.state_changed_at).getTime())
    .map((ticket) => ({
      id: ticket.id,
      title: ticket.title,
      // `building` is the fallback for a ticket whose first status join is still
      // in flight — it is what the board's Working bucket already asserts, so the
      // row is never blank on the first paint after a pull.
      status: byTicket.get(ticket.id) ?? 'building',
      since: ticket.state_changed_at,
    }));
}
