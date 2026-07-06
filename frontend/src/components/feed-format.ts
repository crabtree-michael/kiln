// Pure formatting helpers for the primary screen (08 §2, §4). Kept in a plain
// `.ts` module (no components) so the presentational `.tsx` files stay clean of
// react-refresh/only-export-components warnings, and so the header-status /
// relative-age logic is unit-testable on its own.
import type { AgentStatus, Board, FeedCard, FeedSummary, Ticket } from '@/transport/transport';
import type { ToastVerb } from '@/stores/activity-context';

function plural(count: number, word: string): string {
  return count === 1 ? `${count.toString()} ${word}` : `${count.toString()} ${word}s`;
}

/** The one-line header status derived from the feed summary (08 §2 / §F): the
 * live active-ticket count, then "Nothing active" for an empty feed. Blockers
 * are surfaced in the dropdown list, not in this collapsed label. */
export function feedStatus(summary: FeedSummary): string {
  if (summary.stream_count > 0) {
    return plural(summary.stream_count, 'ticket');
  }
  return 'Nothing active';
}

/** Compact relative age used on cards and the all-clear line ("now", "2m",
 * "1h", "3d"). `now` is injectable so snapshots stay deterministic. */
export function relativeAge(iso: string, now: number = Date.now()): string {
  const deltaMs = now - new Date(iso).getTime();
  const seconds = Math.max(0, Math.floor(deltaMs / 1000));
  if (seconds < 60) {
    return 'now';
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes.toString()}m`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours.toString()}h`;
  }
  const days = Math.floor(hours / 24);
  return `${days.toString()}d`;
}

/** The all-clear detail line (08 §2 / 4d): "3 building · last word 6m ago". Only
 * the building count is shown — the idle count is deliberately omitted so the
 * all-clear state stays focused on what's actively moving. */
export function streamDetail(summary: FeedSummary, now: number = Date.now()): string {
  const base = `${summary.building.toString()} building`;
  if (summary.last_word_at == null) {
    return base;
  }
  return `${base} · last word ${relativeAge(summary.last_word_at, now)} ago`;
}

/** The real session running-state of a worker (amended 2026-07-05): the actual
 * status of the underlying agent session, joined from `board.agents` — not a
 * hardcoded guess from the ticket's board column. A stopped/errored session is
 * now visibly distinct from one actively building. */
export type StreamState = AgentStatus['status'];

/** The status chip shown on a ticket row in the header dropdown. For a ticket
 * with a live worker (working/blocked) it's that worker's real session state;
 * for one without (ready/shaping/done) it's the ticket's own lifecycle state. */
export type TicketRowStatus = StreamState | 'ready' | 'shaping' | 'done';

/** One ticket, broken out for the header dropdown (08 §2, amended 2026-07-06:
 * every ticket, not just the active ones). Each row's status is its bound
 * worker's real session state from `board.agents` where one exists, falling
 * back to the ticket's own state otherwise. */
export interface TicketStatus {
  /** The ticket id — a stable render key. */
  id: string;
  /** The ticket title shown as the row label. */
  label: string;
  /** The session state where a worker is bound, else the lifecycle state. */
  status: TicketRowStatus;
  /** The blocker reason for a blocked ticket, when one is set. */
  reason: string | null;
  /** ISO time of the ticket's last change (`updated_at`) — the row renders its
   * compact relative age as subtext ("time in status" since the last move). */
  updatedAt: string;
}

/** Ordering rank per board state (08 §2, amended 2026-07-06): active tickets
 * (working then blocked) first, then the ready backlog at the bottom. Done and
 * shaping tickets are excluded from the dropdown entirely (see `ticketStatuses`),
 * so their ranks are only placeholders. Within a rank rows sort by decreasing
 * activity (most-recently-updated first). */
const STATE_RANK: Record<Ticket['state'], number> = {
  working: 0,
  blocked: 1,
  ready: 2,
  done: 3,
  shaping: 4,
};

/** The row status for one ticket: its worker's real session state where the
 * board reports one, else the ticket's own lifecycle state — with the column
 * defaults (`building` for working, `idle` for blocked) preserved for an active
 * ticket whose first status is still in flight, so the row is never blank. */
function ticketRowStatus(ticket: Ticket, byTicket: Map<string, StreamState>): TicketRowStatus {
  const session = byTicket.get(ticket.id);
  if (session !== undefined) {
    return session;
  }
  switch (ticket.state) {
    case 'working':
      return 'building';
    case 'blocked':
      return 'idle';
    default:
      return ticket.state;
  }
}

/** Break out the header dropdown's per-ticket list: only the working, blocked,
 * and ready tickets, active first then the ready backlog, each in
 * decreasing-activity order (08 §2, amended 2026-07-06). Done and shaping
 * tickets are excluded entirely, not just sorted last. Returns [] before the
 * first board snapshot. */
export function ticketStatuses(board: Board | null): TicketStatus[] {
  if (board === null) {
    return [];
  }
  const byTicket = new Map<string, StreamState>(
    board.agents.map((agent) => [agent.ticket_id, agent.status]),
  );
  const all = [...board.working, ...board.blocked, ...board.ready];
  return all
    .sort((a, b) => {
      const rank = STATE_RANK[a.state] - STATE_RANK[b.state];
      if (rank !== 0) {
        return rank;
      }
      return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime();
    })
    .map((ticket) => ({
      id: ticket.id,
      label: ticket.title,
      status: ticketRowStatus(ticket, byTicket),
      reason: ticket.blocked_reason ?? null,
      updatedAt: ticket.updated_at,
    }));
}

/** The short uppercase tag shown on each card kind. */
export function cardTag(kind: FeedCard['kind']): string {
  switch (kind) {
    case 'blocker':
      return 'Blocker';
    case 'proposal':
      return 'Proposal';
    case 'preview':
      return 'Preview';
    case 'poke':
      return 'Poke';
    case 'done':
      return 'Done';
    default:
      return 'Update';
  }
}

const VERB_LABEL: Record<ToastVerb, string> = {
  started: 'Started',
  nudged: 'Nudged',
  finished: 'Finished',
  queued: 'Queued',
};

const VERB_EMOJI: Record<ToastVerb, string> = {
  started: '🚀',
  nudged: '👉',
  finished: '✅',
  queued: '📋',
};

export function verbLabel(verb: ToastVerb): string {
  return VERB_LABEL[verb];
}

export function verbEmoji(verb: ToastVerb): string {
  return VERB_EMOJI[verb];
}
