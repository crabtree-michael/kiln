// Pure formatting helpers for the primary screen (08 §2, §4). Kept in a plain
// `.ts` module (no components) so the presentational `.tsx` files stay clean of
// react-refresh/only-export-components warnings, and so the header-status /
// relative-age logic is unit-testable on its own.
import type { Board, FeedCard, FeedSummary } from '@/transport/transport';
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

/** One active stream, broken out for the header dropdown (08 §2). The collapsed
 * header only carries the aggregate counts (`building`/`idle`); this expands
 * them per-stream from the same board state the counts are derived from —
 * `building` = working tickets, `idle` = blocked tickets (see the backend's
 * FeedSummary: Building = WorkingCount, Idle = BlockedCount). */
export interface StreamStatus {
  /** The ticket id — a stable render key. */
  id: string;
  /** The ticket title shown as the stream label. */
  label: string;
  /** `building` (worker actively building) or `idle` (blocked, awaiting you). */
  status: 'building' | 'idle';
  /** The blocker reason for an idle/blocked stream, when one is set. */
  reason: string | null;
}

/** Break the board's active streams out per-stream, building first then idle —
 * the same order the header counts them (08 §2). Returns [] before the first
 * board snapshot lands. */
export function streamStatuses(board: Board | null): StreamStatus[] {
  if (board === null) {
    return [];
  }
  const building: StreamStatus[] = board.working.map((ticket) => ({
    id: ticket.id,
    label: ticket.title,
    status: 'building',
    reason: null,
  }));
  const idle: StreamStatus[] = board.blocked.map((ticket) => ({
    id: ticket.id,
    label: ticket.title,
    status: 'idle',
    reason: ticket.blocked_reason ?? null,
  }));
  return [...building, ...idle];
}

/** The uppercase state chip shown on a stream row in the header dropdown. */
export function streamStatusLabel(status: StreamStatus['status']): string {
  return status === 'building' ? 'Building' : 'Idle';
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
