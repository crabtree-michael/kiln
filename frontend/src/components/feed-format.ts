// Pure formatting helpers for the primary screen (08 §2, §4). Kept in a plain
// `.ts` module (no components) so the presentational `.tsx` files stay clean of
// react-refresh/only-export-components warnings, and so the header-status /
// relative-age logic is unit-testable on its own.
import type { FeedCard, FeedSummary } from '@/transport/transport';
import type { ToastVerb } from '@/stores/activity-context';

function plural(count: number, word: string): string {
  return count === 1 ? `${count.toString()} ${word}` : `${count.toString()} ${word}s`;
}

/** The one-line header status derived from the feed summary (08 §2 / §F):
 * blockers first, then live streams, then "all clear" for an empty feed. */
export function feedStatus(summary: FeedSummary): string {
  if (summary.blocker_count > 0) {
    return `${plural(summary.blocker_count, 'blocker')} · ${plural(summary.update_count, 'update')}`;
  }
  if (summary.stream_count > 0) {
    return `${plural(summary.stream_count, 'stream')} · nothing needs you`;
  }
  return 'all clear';
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

/** The all-clear detail line (08 §2 / 4d): "3 building · 2 idle · last word 6m ago". */
export function streamDetail(summary: FeedSummary, now: number = Date.now()): string {
  const base = `${summary.building.toString()} building · ${summary.idle.toString()} idle`;
  if (summary.last_word_at == null) {
    return base;
  }
  return `${base} · last word ${relativeAge(summary.last_word_at, now)} ago`;
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
