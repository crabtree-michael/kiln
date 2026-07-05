// Pure formatting helpers for the primary screen (08 §2, §4). Kept in a plain
// `.ts` module (no components) so the presentational `.tsx` files stay clean of
// react-refresh/only-export-components warnings, and so the header-status /
// relative-age logic is unit-testable on its own.
import type { AgentStatus, Board, FeedCard, FeedSummary } from '@/transport/transport';
import type { ToastVerb } from '@/stores/activity-context';

function plural(count: number, word: string): string {
  return count === 1 ? `${count.toString()} ${word}` : `${count.toString()} ${word}s`;
}

/** The one-line header status derived from the feed summary (08 §2 / §F):
 * blockers first, then the live active-stream count, then "Nothing active" for
 * an empty feed. */
export function feedStatus(summary: FeedSummary): string {
  if (summary.blocker_count > 0) {
    return `${plural(summary.blocker_count, 'blocker')} · ${plural(summary.update_count, 'update')}`;
  }
  if (summary.stream_count > 0) {
    return plural(summary.stream_count, 'stream');
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

/** The real session running-state of a stream (amended 2026-07-05): the actual
 * status of the underlying agent session, joined from `board.agents` — not a
 * hardcoded guess from the ticket's board column. A stopped/errored session is
 * now visibly distinct from one actively building. */
export type StreamState = AgentStatus['status'];

/** One active stream, broken out for the header dropdown (08 §2). The collapsed
 * header carries only the aggregate counts (`building`/`idle`); this expands
 * them per-stream, using each worker's real session status where the board
 * reports one and falling back to the ticket's column while a first status is
 * still in flight. */
export interface StreamStatus {
  /** The ticket id — a stable render key. */
  id: string;
  /** The ticket title shown as the stream label. */
  label: string;
  /** The real underlying session state (building/idle/stopped/errored/starting). */
  status: StreamState;
  /** The blocker reason for a blocked stream, when one is set. */
  reason: string | null;
}

/** Break the board's active streams out per-stream, working first then blocked —
 * the same order the header counts them (08 §2). Each stream's status is its
 * bound worker's real session state from `board.agents`, keyed by ticket id;
 * before a status has arrived it falls back to the board-column default
 * (`building` for working, `idle` for blocked) so the row is never blank.
 * Returns [] before the first board snapshot lands. */
export function streamStatuses(board: Board | null): StreamStatus[] {
  if (board === null) {
    return [];
  }
  const byTicket = new Map<string, StreamState>(
    board.agents.map((agent) => [agent.ticket_id, agent.status]),
  );
  const working: StreamStatus[] = board.working.map((ticket) => ({
    id: ticket.id,
    label: ticket.title,
    status: byTicket.get(ticket.id) ?? 'building',
    reason: null,
  }));
  const blocked: StreamStatus[] = board.blocked.map((ticket) => ({
    id: ticket.id,
    label: ticket.title,
    status: byTicket.get(ticket.id) ?? 'idle',
    reason: ticket.blocked_reason ?? null,
  }));
  return [...working, ...blocked];
}

/** The uppercase state chip shown on a stream row in the header dropdown. */
const STREAM_STATE_LABEL: Record<StreamState, string> = {
  building: 'Building',
  idle: 'Idle',
  stopped: 'Stopped',
  errored: 'Errored',
  starting: 'Starting',
};

/** The uppercase state chip shown on a stream row in the header dropdown. */
export function streamStatusLabel(status: StreamState): string {
  return STREAM_STATE_LABEL[status];
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
